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
  callRing,
  callAccept,
  callReject,
  callJoin,
  callLeave,
  callMuteToggle,
  callVideoToggle,
  callScreenShareStart,
  callScreenShareStop,
  callHandRaise,
  callHandLower,
  callParticipantJoined,
  callParticipantLeft,
  callParticipantMuted,
  callParticipantUnmuted,
  callParticipantRemoved,
  callStateChange,
  callQualityReport,
  callUpgradeRequest,
  callUpgradeAccept,
  callUpgradeReject,
  callRecordingStarted,
  callRecordingStopped,
}

/// A signaling message received from or sent through the WebSocket.
class CallSignal {
  final SignalType type;
  final String senderId;
  final String targetUserId;
  final String? callId;
  final String? callType; // 'audio' | 'video'
  final String? sdp;
  final Map<String, dynamic>? candidate;
  final String? inviteId;

  const CallSignal({
    required this.type,
    required this.senderId,
    required this.targetUserId,
    this.callId,
    this.callType,
    this.sdp,
    this.candidate,
    this.inviteId,
  });

  factory CallSignal.fromJson(Map<String, dynamic> json) {
    return CallSignal(
      type: _parseType(json['type'] as String),
      senderId: json['sender_id'] as String? ?? '',
      targetUserId: json['target_user_id'] as String? ?? '',
      callId: json['call_id'] as String?,
      callType: json['call_type'] as String?,
      sdp: json['sdp'] as String?,
      candidate: json['candidate'] as Map<String, dynamic>?,
      inviteId: json['invite_id'] as String?,
    );
  }

  Map<String, dynamic> toJson() {
    final map = <String, dynamic>{
      'type': _typeToString(type),
      'target_user_id': targetUserId,
    };
    if (callId != null) map['call_id'] = callId;
    if (callType != null) map['call_type'] = callType;
    if (sdp != null) map['sdp'] = sdp;
    if (candidate != null) map['candidate'] = candidate;
    return map;
  }

  static const _typeMap = <String, SignalType>{
    'call_offer': SignalType.callOffer,
    'call_answer': SignalType.callAnswer,
    'ice_candidate': SignalType.iceCandidate,
    'call_end': SignalType.callEnd,
    'call_decline': SignalType.callDecline,
    'call_busy': SignalType.callBusy,
    'call_ring': SignalType.callRing,
    'call_accept': SignalType.callAccept,
    'call_reject': SignalType.callReject,
    'call_join': SignalType.callJoin,
    'call_leave': SignalType.callLeave,
    'call_mute_toggle': SignalType.callMuteToggle,
    'call_video_toggle': SignalType.callVideoToggle,
    'call_screen_share_start': SignalType.callScreenShareStart,
    'call_screen_share_stop': SignalType.callScreenShareStop,
    'call_hand_raise': SignalType.callHandRaise,
    'call_hand_lower': SignalType.callHandLower,
    'call_participant_joined': SignalType.callParticipantJoined,
    'call_participant_left': SignalType.callParticipantLeft,
    'call_participant_muted': SignalType.callParticipantMuted,
    'call_participant_unmuted': SignalType.callParticipantUnmuted,
    'call_participant_removed': SignalType.callParticipantRemoved,
    'call_state_change': SignalType.callStateChange,
    'call_quality_report': SignalType.callQualityReport,
    'call_upgrade_request': SignalType.callUpgradeRequest,
    'call_upgrade_accept': SignalType.callUpgradeAccept,
    'call_upgrade_reject': SignalType.callUpgradeReject,
    'call_recording_started': SignalType.callRecordingStarted,
    'call_recording_stopped': SignalType.callRecordingStopped,
  };

  static final _reverseTypeMap = {
    for (final e in _typeMap.entries) e.value: e.key,
  };

  static SignalType _parseType(String raw) => _typeMap[raw] ?? SignalType.callEnd;

  static String _typeToString(SignalType t) => _reverseTypeMap[t] ?? 'call_end';
}

bool _isCallSignalType(String type) =>
    type.startsWith('call_') || type == 'ice_candidate';

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
      if (type != null && _isCallSignalType(type)) {
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

  /// Send a raw JSON message (for subscribe/unsubscribe commands).
  void sendRaw(Map<String, dynamic> data) {
    if (_channel == null) return;
    _channel!.sink.add(jsonEncode(data));
  }

  /// Subscribe to a call room for room-wide broadcast signals.
  void subscribeToCallRoom(String callId) {
    sendRaw({'type': 'subscribe_call', 'call_id': callId});
  }

  /// Unsubscribe from a call room.
  void unsubscribeFromCallRoom(String callId) {
    sendRaw({'type': 'unsubscribe_call', 'call_id': callId});
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
