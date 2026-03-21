import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

enum ConnectionState {
  disconnected,
  connecting,
  connected,
  reconnecting,
}

/// Shared websocket connection used by chat, feed, presence, and call signaling.
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
  static const _tag = 'RealtimeService';

  RealtimeService(this._auth) {
    _stateController.add(_state);
  }

  Stream<RealtimeEvent> get events => _eventController.stream;
  Stream<ConnectionState> get stateStream => _stateController.stream;
  ConnectionState get state => _state;
  bool get isConnected => _state == ConnectionState.connected;

  Future<void> connect() async {
    if (_state == ConnectionState.connected ||
        _state == ConnectionState.connecting) {
      return;
    }

    final token = _auth.token;
    if (token == null || token.isEmpty) {
      AppLogger.warn('Skipping websocket connect without access token', tag: _tag);
      _updateState(ConnectionState.disconnected);
      return;
    }

    _updateState(ConnectionState.connecting);
    AppLogger.info('Establishing websocket connection', tag: _tag);

    try {
      final wsUri = Environment.buildWsGatewayUri({
        'access_token': token,
      });
      _channel = WebSocketChannel.connect(wsUri);

      _subscription = _channel!.stream.listen(
        _onRawMessage,
        onError: (error) => _handleDisconnect(error: error),
        onDone: () => _handleDisconnect(),
      );

      _retryCount = 0;
      _updateState(ConnectionState.connected);
      AppLogger.info('Websocket connection established', tag: _tag);
    } catch (error, stackTrace) {
      AppLogger.error(
        'Failed to connect websocket',
        tag: _tag,
        error: error,
        stackTrace: stackTrace,
      );
      _handleDisconnect(error: error);
    }
  }

  void _onRawMessage(dynamic raw) {
    try {
      final json = jsonDecode(raw as String) as Map<String, dynamic>;
      final event = RealtimeEvent.fromJson(json);
      _eventController.add(event);
      AppLogger.debug(
        'Received realtime event: ${event.eventType}',
        tag: _tag,
      );
    } catch (_) {
      AppLogger.warn('Failed to parse realtime message: $raw', tag: _tag);
    }
  }

  void _handleDisconnect({Object? error}) {
    _closeChannel();

    if (_auth.isAuthenticated && _retryCount < _maxRetries) {
      _updateState(ConnectionState.reconnecting);
      final multiplier = pow(2, _retryCount).toInt();
      final delay = Duration(seconds: _baseDelay.inSeconds * multiplier);
      _retryCount++;

      AppLogger.warn(
        'Websocket disconnected. Retrying in ${delay.inSeconds}s ($_retryCount/$_maxRetries)',
        tag: _tag,
      );
      Timer(delay, connect);
      return;
    }

    _updateState(ConnectionState.disconnected);
    if (_retryCount >= _maxRetries) {
      AppLogger.error('Max websocket retries reached', tag: _tag, error: error);
    }
  }

  void send(Map<String, dynamic> data) {
    if (_channel == null || _state != ConnectionState.connected) {
      AppLogger.warn('Attempted websocket send while disconnected', tag: _tag);
      return;
    }
    _channel!.sink.add(jsonEncode(data));
  }

  void subscribeToPost(String postId) {
    send({'type': 'subscribe_post', 'post_id': postId});
  }

  void unsubscribeFromPost(String postId) {
    send({'type': 'unsubscribe_post', 'post_id': postId});
  }

  void subscribeToCall(String callId) {
    send({'type': 'subscribe_call', 'call_id': callId});
  }

  void unsubscribeFromCall(String callId) {
    send({'type': 'unsubscribe_call', 'call_id': callId});
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
    AppLogger.info('Disconnecting websocket', tag: _tag);
    _retryCount = _maxRetries;
    _closeChannel();
    _updateState(ConnectionState.disconnected);
  }

  void dispose() {
    disconnect();
    _eventController.close();
    _stateController.close();
  }
}

final realtimeServiceProvider = Provider<RealtimeService>((ref) {
  final auth = ref.watch(authServiceProvider);
  final service = RealtimeService(auth);

  void syncAuth(AuthState state) {
    if (state.isAuthenticated && (state.token?.isNotEmpty ?? false)) {
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
