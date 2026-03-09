import 'dart:async';
import 'dart:convert';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

/// Signaling message types matching the web frontend protocol.
enum SignalType {
  callOffer,
  callAnswer,
  iceCandidate,
  callEnd,
  callDecline,
  callBusy,
}

/// A signaling message received from or sent through the WebSocket.
class CallSignal {
  final SignalType type;
  final String senderId;
  final String targetUserId;
  final String? callType; // 'audio' | 'video'
  final String? sdp;
  final Map<String, dynamic>? candidate;

  const CallSignal({
    required this.type,
    required this.senderId,
    required this.targetUserId,
    this.callType,
    this.sdp,
    this.candidate,
  });

  factory CallSignal.fromJson(Map<String, dynamic> json) {
    return CallSignal(
      type: _parseType(json['type'] as String),
      senderId: json['sender_id'] as String? ?? '',
      targetUserId: json['target_user_id'] as String? ?? '',
      callType: json['call_type'] as String?,
      sdp: json['sdp'] as String?,
      candidate: json['candidate'] as Map<String, dynamic>?,
    );
  }

  Map<String, dynamic> toJson() {
    final map = <String, dynamic>{
      'type': _typeToString(type),
      'target_user_id': targetUserId,
    };
    if (callType != null) map['call_type'] = callType;
    if (sdp != null) map['sdp'] = sdp;
    if (candidate != null) map['candidate'] = candidate;
    return map;
  }

  static SignalType _parseType(String raw) {
    switch (raw) {
      case 'call_offer':
        return SignalType.callOffer;
      case 'call_answer':
        return SignalType.callAnswer;
      case 'ice_candidate':
        return SignalType.iceCandidate;
      case 'call_end':
        return SignalType.callEnd;
      case 'call_decline':
        return SignalType.callDecline;
      case 'call_busy':
        return SignalType.callBusy;
      default:
        return SignalType.callEnd;
    }
  }

  static String _typeToString(SignalType t) {
    switch (t) {
      case SignalType.callOffer:
        return 'call_offer';
      case SignalType.callAnswer:
        return 'call_answer';
      case SignalType.iceCandidate:
        return 'ice_candidate';
      case SignalType.callEnd:
        return 'call_end';
      case SignalType.callDecline:
        return 'call_decline';
      case SignalType.callBusy:
        return 'call_busy';
    }
  }
}

const _signalingTypes = {
  'call_offer',
  'call_answer',
  'ice_candidate',
  'call_end',
  'call_decline',
  'call_busy',
};

/// Manages the WebSocket connection for call signaling.
class SignalingService {
  WebSocketChannel? _channel;
  final _signalController = StreamController<CallSignal>.broadcast();
  StreamSubscription? _subscription;
  Timer? _reconnectTimer;
  String? _wsUrl;
  String? _userId;
  bool _shouldReconnect = false;

  Stream<CallSignal> get signals => _signalController.stream;
  bool get isConnected => _channel != null;

  /// Connect to the ws-gateway with user authentication.
  void connect(String wsUrl, String userId) {
    if (_channel != null && _wsUrl == wsUrl && _userId == userId) {
      return;
    }
    _wsUrl = wsUrl;
    _userId = userId;
    _shouldReconnect = true;
    _reconnectTimer?.cancel();
    _closeChannel();
    _doConnect();
  }

  void _doConnect() {
    if (!_shouldReconnect || _wsUrl == null || _userId == null) return;
    if (_channel != null) return;
    try {
      final uri = Uri.parse('$_wsUrl?user_id=$_userId');
      _channel = WebSocketChannel.connect(uri);
      _subscription = _channel!.stream.listen(
        _onMessage,
        onError: (_) => _reconnect(),
        onDone: _reconnect,
      );
    } catch (_) {
      _reconnect();
    }
  }

  void _reconnect() {
    if (!_shouldReconnect) return;
    _closeChannel();
    _reconnectTimer?.cancel();
    // Retry after 3 seconds
    _reconnectTimer = Timer(const Duration(seconds: 3), _doConnect);
  }

  /// Disconnect from signaling and stop reconnect attempts.
  void disconnect() {
    _shouldReconnect = false;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _wsUrl = null;
    _userId = null;
    _closeChannel();
  }

  void _onMessage(dynamic raw) {
    try {
      final json = jsonDecode(raw as String) as Map<String, dynamic>;
      final type = json['type'] as String?;
      if (type != null && _signalingTypes.contains(type)) {
        _signalController.add(CallSignal.fromJson(json));
      }
    } catch (_) {
      // Ignore non-signaling messages
    }
  }

  /// Send a signaling message to the ws-gateway.
  void send(CallSignal signal) {
    if (_channel == null) return;
    _channel!.sink.add(jsonEncode(signal.toJson()));
  }

  void _closeChannel() {
    _subscription?.cancel();
    _subscription = null;
    _channel?.sink.close();
    _channel = null;
  }

  void dispose() {
    disconnect();
    _signalController.close();
  }
}

/// Global signaling service provider.
final signalingServiceProvider = Provider<SignalingService>((ref) {
  final auth = ref.watch(authServiceProvider);
  final service = SignalingService();

  void syncAuth(AuthState state) {
    final userId = state.userId;
    if (state.isAuthenticated && userId != null && userId.isNotEmpty) {
      service.connect(Environment.wsGatewayUrl, userId);
      return;
    }
    service.disconnect();
  }

  syncAuth(auth.state);
  final authSub = auth.stateStream.listen(syncAuth);
  ref.onDispose(() {
    authSub.cancel();
    service.dispose();
  });
  return service;
});
