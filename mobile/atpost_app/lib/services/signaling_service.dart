import 'dart:async';

import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

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

class CallSignal {
  final SignalType type;
  final String senderId;
  final String targetUserId;
  final String? callId;
  final String? callType;
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
      type: _parseType(json['type'] as String? ?? ''),
      senderId: json['sender_id'] as String? ?? '',
      targetUserId: json['target_user_id'] as String? ?? '',
      callId: json['call_id'] as String?,
      callType: json['call_type'] as String?,
      sdp: json['sdp'] as String?,
      candidate: json['candidate'] as Map<String, dynamic>?,
      inviteId: json['invite_id'] as String?,
    );
  }

  factory CallSignal.fromRealtimeEvent(CallSignalEvent event) {
    return CallSignal(
      type: _parseType(event.eventType),
      senderId: event.senderId,
      targetUserId: event.targetUserId,
      callId: event.callId,
      callType: event.callType,
      sdp: event.sdp,
      candidate: event.candidate,
      inviteId: event.inviteId,
    );
  }

  Map<String, dynamic> toJson() {
    final map = <String, dynamic>{
      'type': _typeToString(type),
    };
    if (targetUserId.isNotEmpty) {
      map['target_user_id'] = targetUserId;
    }
    if (callId != null && callId!.isNotEmpty) {
      map['call_id'] = callId;
    }
    if (callType != null && callType!.isNotEmpty) {
      map['call_type'] = callType;
    }
    if (sdp != null && sdp!.isNotEmpty) {
      map['sdp'] = sdp;
    }
    if (candidate != null) {
      map['candidate'] = candidate;
    }
    if (inviteId != null && inviteId!.isNotEmpty) {
      map['invite_id'] = inviteId;
    }
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
    for (final entry in _typeMap.entries) entry.value: entry.key,
  };

  static SignalType _parseType(String raw) =>
      _typeMap[raw] ?? SignalType.callEnd;

  static String _typeToString(SignalType signalType) =>
      _reverseTypeMap[signalType] ?? 'call_end';
}

class SignalingService {
  final RealtimeService _realtime;
  final _signalController = StreamController<CallSignal>.broadcast();
  StreamSubscription? _subscription;

  SignalingService(this._realtime) {
    _subscription = _realtime.events.listen((event) {
      if (event is CallSignalEvent) {
        _signalController.add(CallSignal.fromRealtimeEvent(event));
      }
    });
  }

  Stream<CallSignal> get signals => _signalController.stream;
  bool get isConnected => _realtime.isConnected;

  void send(CallSignal signal) {
    _realtime.send(signal.toJson());
  }

  void sendRaw(Map<String, dynamic> data) {
    _realtime.send(data);
  }

  void subscribeToCallRoom(String callId) {
    _realtime.subscribeToCall(callId);
  }

  void unsubscribeFromCallRoom(String callId) {
    _realtime.unsubscribeFromCall(callId);
  }

  void dispose() {
    _subscription?.cancel();
    _signalController.close();
  }
}

final signalingServiceProvider = Provider<SignalingService>((ref) {
  final realtime = ref.watch(realtimeServiceProvider);
  final service = SignalingService(realtime);
  ref.onDispose(service.dispose);
  return service;
});
