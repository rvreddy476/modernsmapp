import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

/// Connection states for the RealtimeService.
enum ConnectionState {
  disconnected,
  connecting,
  connected,
  reconnecting,
}

/// A unified real-time service that handles chat, notifications, feed updates, and call signaling.
/// Replaces the old SignalingService.
class RealtimeService {
  WebSocketChannel? _channel;
  final _eventController = StreamController<RealtimeEvent>.broadcast();
  final _stateController = StreamController<ConnectionState>.broadcast();
  StreamSubscription? _subscription;

  ConnectionState _state = ConnectionState.disconnected;
  int _retryCount = 0;
  static const int _maxRetries = 5;
  static const Duration _baseDelay = Duration(seconds: 3);

  final AuthService _auth;
  final ApiClient _api;
  static const _tag = 'RealtimeService';

  RealtimeService(this._auth, this._api) {
    _stateController.add(_state);
  }

  Stream<RealtimeEvent> get events => _eventController.stream;
  Stream<ConnectionState> get stateStream => _stateController.stream;
  ConnectionState get state => _state;

  /// Connect to the WebSocket hub.
  /// First fetches a signed chat token from the backend.
  Future<void> connect() async {
    if (_state == ConnectionState.connected || _state == ConnectionState.connecting) return;

    _updateState(ConnectionState.connecting);
    AppLogger.info('Establishing real-time connection...', tag: _tag);

    try {
      // 1. Fetch chat token (matches web's /api/chat/token)
      final response = await _api.get('${Environment.chatPath}/token');
      final data = response.data['data'] as Map<String, dynamic>? ?? response.data;
      final token = data['token'] as String?;

      if (token == null) {
        throw Exception('Failed to acquire real-time token');
      }

      // 2. Connect to WS (matches web's /v1/ws/connect?access_token=...)
      final wsUrl = '${Environment.apiBaseUrl.replaceFirst('http', 'ws')}/v1/ws/connect?access_token=${Uri.encodeComponent(token)}';
      _channel = WebSocketChannel.connect(Uri.parse(wsUrl));

      _subscription = _channel!.stream.listen(
        _onRawMessage,
        onError: (e) => _handleDisconnect(error: e),
        onDone: () => _handleDisconnect(),
      );

      _retryCount = 0;
      _updateState(ConnectionState.connected);
      AppLogger.info('Real-time connection established', tag: _tag);
    } catch (e, stack) {
      AppLogger.error('Failed to connect to real-time hub', tag: _tag, error: e, stackTrace: stack);
      _handleDisconnect(error: e);
    }
  }

  void _onRawMessage(dynamic raw) {
    try {
      final json = jsonDecode(raw as String) as Map<String, dynamic>;
      final event = RealtimeEvent.fromJson(json);
      _eventController.add(event);
      AppLogger.debug('Received real-time event: ${event.type}', tag: _tag);
    } catch (e) {
      AppLogger.warn('Failed to parse real-time message: $raw', tag: _tag);
    }
  }

  void _handleDisconnect({Object? error}) {
    _closeChannel();

    if (_auth.isAuthenticated && _retryCount < _maxRetries) {
      _updateState(ConnectionState.reconnecting);
      final delay = _baseDelay * pow(2, _retryCount);
      _retryCount++;

      AppLogger.warn('Connection lost. Retrying in ${delay.inSeconds}s ($_retryCount/$_maxRetries)...', tag: _tag);
      Timer(delay, () => connect());
    } else {
      _updateState(ConnectionState.disconnected);
      if (_retryCount >= _maxRetries) {
        AppLogger.error('Max reconnection retries reached. Real-time features disabled.', tag: _tag);
      }
    }
  }

  /// Send a message or signal through the WebSocket.
  void send(Map<String, dynamic> data) {
    if (_state != ConnectionState.connected || _channel == null) {
      AppLogger.warn('Attempted to send message while disconnected', tag: _tag);
      return;
    }
    _channel!.sink.add(jsonEncode(data));
  }

  /// Subscribe to a specific post's real-time updates.
  void subscribeToPost(String postId) {
    send({'type': 'subscribe_post', 'post_id': postId});
  }

  /// Unsubscribe from a specific post's real-time updates.
  void unsubscribeFromPost(String postId) {
    send({'type': 'unsubscribe_post', 'post_id': postId});
  }

  void _updateState(ConnectionState newState) {
    _state = newState;
    _stateController.add(_state);
  }

  void _closeChannel() {
    _subscription?.cancel();
    _subscription = null;
    _channel?.sink.close();
    _channel = null;
  }

  void disconnect() {
    AppLogger.info('Disconnecting real-time service', tag: _tag);
    _retryCount = _maxRetries; // Prevent auto-reconnect
    _closeChannel();
    _updateState(ConnectionState.disconnected);
  }

  void dispose() {
    disconnect();
    _eventController.close();
    _stateController.close();
  }
}

/// Global real-time service provider.
final realtimeServiceProvider = Provider<RealtimeService>((ref) {
  final auth = ref.watch(authServiceProvider);
  final api = ref.watch(apiClientProvider);
  final service = RealtimeService(auth, api);

  void syncAuth(AuthState state) {
    if (state.isAuthenticated) {
      service.connect();
    } else {
      service.disconnect();
    }
  }

  syncAuth(auth.state);
  final authSub = auth.stateStream.listen(syncAuth);

  ref.onDispose(() {
    authSub.cancel();
    service.dispose();
  });
  return service;
});

// NOTE: signalingServiceProvider has been removed from here to resolve
// duplicate provider conflict. The auth-wired version is defined in
// signaling_service.dart and is used by CallService.

