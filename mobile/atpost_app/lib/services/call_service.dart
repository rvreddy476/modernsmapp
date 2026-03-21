import 'dart:async';
import 'dart:math';

import 'package:atpost_app/data/models/call.dart' as models;
import 'package:atpost_app/data/repositories/calls_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'signaling_service.dart';

enum CallState { idle, outgoing, incoming, connecting, active }

enum CallType { audio, video }

class CallInfo {
  final CallState state;
  final CallType type;
  final String peerId;
  final String peerName;
  final String peerAvatar;
  final bool isInitiator;
  final DateTime? startedAt;
  final MediaStream? localStream;
  final MediaStream? remoteStream;
  final String? callId;
  final String? inviteId;
  final models.JoinResponse? joinResponse;

  const CallInfo({
    required this.state,
    required this.type,
    required this.peerId,
    this.peerName = '',
    this.peerAvatar = '',
    this.isInitiator = false,
    this.startedAt,
    this.localStream,
    this.remoteStream,
    this.callId,
    this.inviteId,
    this.joinResponse,
  });

  CallInfo copyWith({
    CallState? state,
    CallType? type,
    String? peerId,
    String? peerName,
    String? peerAvatar,
    bool? isInitiator,
    DateTime? startedAt,
    MediaStream? localStream,
    MediaStream? remoteStream,
    String? callId,
    String? inviteId,
    models.JoinResponse? joinResponse,
  }) {
    return CallInfo(
      state: state ?? this.state,
      type: type ?? this.type,
      peerId: peerId ?? this.peerId,
      peerName: peerName ?? this.peerName,
      peerAvatar: peerAvatar ?? this.peerAvatar,
      isInitiator: isInitiator ?? this.isInitiator,
      startedAt: startedAt ?? this.startedAt,
      localStream: localStream ?? this.localStream,
      remoteStream: remoteStream ?? this.remoteStream,
      callId: callId ?? this.callId,
      inviteId: inviteId ?? this.inviteId,
      joinResponse: joinResponse ?? this.joinResponse,
    );
  }
}

const _fallbackIceServers = <Map<String, dynamic>>[
  {
    'urls': ['stun:stun.l.google.com:19302', 'stun:stun1.l.google.com:19302'],
  },
];

class CallNotifier extends StateNotifier<CallInfo?> {
  final SignalingService _signaling;
  final CallsRepository? _callsRepo;
  final Random _random = Random.secure();

  StreamSubscription? _signalSub;
  RTCPeerConnection? _pc;
  final List<RTCIceCandidate> _pendingCandidates = [];
  String? _offerSdp;

  CallNotifier(this._signaling, [this._callsRepo]) : super(null) {
    _signalSub = _signaling.signals.listen(_handleSignal);
  }

  @override
  void dispose() {
    _signalSub?.cancel();
    _cleanup();
    super.dispose();
  }

  Future<void> initiateCall({
    required String contactId,
    required String contactName,
    required String contactAvatar,
    required CallType type,
  }) async {
    if (state != null) return;

    state = CallInfo(
      state: CallState.outgoing,
      type: type,
      peerId: contactId,
      peerName: contactName,
      peerAvatar: contactAvatar,
      isInitiator: true,
    );

    try {
      models.CallSession? session;
      models.JoinResponse? joinResponse;
      if (_callsRepo != null) {
        session = await _callsRepo!.createCall(
          callType: _callTypeForRest(type),
          sourceType: 'direct',
          audioOnly: type == CallType.audio,
          targetUserIds: [contactId],
          maxParticipants: 2,
          idempotencyKey: _idempotencyKey(),
        );
        joinResponse = await _callsRepo!.joinCall(session.id);
      }

      if (state == null || state!.state != CallState.outgoing) return;
      state = state!.copyWith(
        callId: session?.id,
        joinResponse: joinResponse,
      );

      if (session?.id case final callId?) {
        _signaling.subscribeToCallRoom(callId);
      }

      final stream = await _getMedia(type);
      if (state == null || state!.state != CallState.outgoing) {
        _disposeStream(stream);
        return;
      }

      state = state!.copyWith(localStream: stream);
      _pc = await _createPeerConnection(joinResponse);
      for (final track in stream.getTracks()) {
        await _pc!.addTrack(track, stream);
      }

      final offer = await _pc!.createOffer();
      await _pc!.setLocalDescription(offer);

      _signaling.send(
        CallSignal(
          type: SignalType.callOffer,
          senderId: '',
          targetUserId: contactId,
          callId: session?.id,
          callType: _callTypeForSignal(type),
          sdp: offer.sdp,
        ),
      );
    } catch (_) {
      _cleanup();
    }
  }

  Future<void> acceptCall() async {
    if (state == null || state!.state != CallState.incoming) return;

    state = state!.copyWith(state: CallState.connecting);

    try {
      models.JoinResponse? joinResponse;
      final callId = state!.callId;
      final inviteId = state!.inviteId;
      if (_offerSdp == null || _offerSdp!.isEmpty) {
        _cleanup();
        return;
      }

      if (_callsRepo != null && callId != null) {
        if (inviteId != null && inviteId.isNotEmpty) {
          try {
            await _callsRepo!.acceptInvite(callId, inviteId);
          } catch (_) {
            // Joining the call can still succeed even if invite acceptance was not provided.
          }
        }
        joinResponse = await _callsRepo!.joinCall(callId);
      }

      final stream = await _getMedia(state!.type);
      if (state == null) {
        _disposeStream(stream);
        return;
      }

      state = state!.copyWith(localStream: stream, joinResponse: joinResponse);
      _pc = await _createPeerConnection(joinResponse);
      for (final track in stream.getTracks()) {
        await _pc!.addTrack(track, stream);
      }

      await _pc!.setRemoteDescription(
        RTCSessionDescription(_offerSdp, 'offer'),
      );
      await _flushPendingCandidates();

      final answer = await _pc!.createAnswer();
      await _pc!.setLocalDescription(answer);

      _signaling.send(
        CallSignal(
          type: SignalType.callAnswer,
          senderId: '',
          targetUserId: state!.peerId,
          callId: state!.callId,
          callType: _callTypeForSignal(state!.type),
          sdp: answer.sdp,
        ),
      );
    } catch (_) {
      _cleanup();
    }
  }

  void declineCall() {
    if (state == null) return;
    final callId = state!.callId;
    final inviteId = state!.inviteId;

    _signaling.send(
      CallSignal(
        type: SignalType.callDecline,
        senderId: '',
        targetUserId: state!.peerId,
        callId: callId,
      ),
    );

    if (_callsRepo != null && callId != null && inviteId != null) {
      unawaited(_callsRepo!.declineInvite(callId, inviteId));
    }
    _cleanup();
  }

  void endCall() {
    if (state == null) return;
    final info = state!;
    final callId = info.callId;

    _signaling.send(
      CallSignal(
        type: SignalType.callEnd,
        senderId: '',
        targetUserId: info.peerId,
        callId: callId,
      ),
    );

    if (_callsRepo != null && callId != null) {
      if (info.isInitiator) {
        unawaited(_callsRepo!.endCall(callId));
      } else {
        unawaited(_callsRepo!.leaveCall(callId));
      }
    }
    _cleanup();
  }

  bool toggleMute() {
    final stream = state?.localStream;
    if (stream == null) return false;
    final tracks = stream.getAudioTracks();
    if (tracks.isEmpty) return false;

    tracks.first.enabled = !tracks.first.enabled;
    final muted = !tracks.first.enabled;

    if (state?.callId case final callId?) {
      _signaling.send(
        CallSignal(
          type: SignalType.callMuteToggle,
          senderId: '',
          targetUserId: '',
          callId: callId,
        ),
      );
    }

    return muted;
  }

  bool toggleCamera() {
    final stream = state?.localStream;
    if (stream == null) return false;
    final tracks = stream.getVideoTracks();
    if (tracks.isEmpty) return false;

    tracks.first.enabled = !tracks.first.enabled;
    final off = !tracks.first.enabled;

    if (state?.callId case final callId?) {
      _signaling.send(
        CallSignal(
          type: SignalType.callVideoToggle,
          senderId: '',
          targetUserId: '',
          callId: callId,
        ),
      );
    }

    return off;
  }

  Future<RTCPeerConnection> _createPeerConnection(
    models.JoinResponse? joinResponse,
  ) async {
    final config = <String, dynamic>{
      'iceServers': _iceServersFor(joinResponse),
    };
    final conn = await createPeerConnection(config);

    conn.onIceCandidate = (candidate) {
      if (state == null) return;

      _signaling.send(
        CallSignal(
          type: SignalType.iceCandidate,
          senderId: '',
          targetUserId: state!.peerId,
          callId: state!.callId,
          candidate: candidate.toMap(),
        ),
      );
    };

    conn.onTrack = (event) {
      if (state != null && event.streams.isNotEmpty) {
        state = state!.copyWith(remoteStream: event.streams.first);
      }
    };

    conn.onIceConnectionState = (iceState) {
      if (iceState == RTCIceConnectionState.RTCIceConnectionStateConnected ||
          iceState == RTCIceConnectionState.RTCIceConnectionStateCompleted) {
        state = state?.copyWith(
          state: CallState.active,
          startedAt: DateTime.now(),
        );
      } else if (iceState ==
              RTCIceConnectionState.RTCIceConnectionStateFailed ||
          iceState ==
              RTCIceConnectionState.RTCIceConnectionStateDisconnected ||
          iceState == RTCIceConnectionState.RTCIceConnectionStateClosed) {
        _cleanup();
      }
    };

    return conn;
  }

  List<Map<String, dynamic>> _iceServersFor(models.JoinResponse? joinResponse) {
    final servers =
        joinResponse?.iceServers
            .where((server) => server.urls.isNotEmpty)
            .map((server) {
              return <String, dynamic>{
                'urls': server.urls,
                if (server.username != null && server.username!.isNotEmpty)
                  'username': server.username,
                if (server.credential != null && server.credential!.isNotEmpty)
                  'credential': server.credential,
              };
            })
            .toList() ??
        const <Map<String, dynamic>>[];

    if (servers.isNotEmpty) {
      return servers;
    }
    return _fallbackIceServers;
  }

  Future<MediaStream> _getMedia(CallType type) async {
    final constraints = <String, dynamic>{
      'audio': true,
      'video': type == CallType.video,
    };
    return navigator.mediaDevices.getUserMedia(constraints);
  }

  Future<void> _flushPendingCandidates() async {
    if (_pc == null) return;
    for (final candidate in _pendingCandidates) {
      await _pc!.addCandidate(candidate);
    }
    _pendingCandidates.clear();
  }

  void _handleSignal(CallSignal signal) {
    switch (signal.type) {
      case SignalType.callOffer:
        _handleOffer(signal);
        return;
      case SignalType.callRing:
        _handleRing(signal);
        return;
      case SignalType.callAnswer:
        _handleAnswer(signal);
        return;
      case SignalType.iceCandidate:
        _handleIceCandidate(signal);
        return;
      case SignalType.callEnd:
      case SignalType.callDecline:
      case SignalType.callReject:
      case SignalType.callBusy:
        _cleanup();
        return;
      case SignalType.callParticipantJoined:
      case SignalType.callParticipantLeft:
      case SignalType.callParticipantRemoved:
      case SignalType.callStateChange:
        if (state != null) {
          state = state!.copyWith();
        }
        return;
      case SignalType.callAccept:
      case SignalType.callJoin:
      case SignalType.callLeave:
      case SignalType.callMuteToggle:
      case SignalType.callVideoToggle:
      case SignalType.callScreenShareStart:
      case SignalType.callScreenShareStop:
      case SignalType.callHandRaise:
      case SignalType.callHandLower:
      case SignalType.callParticipantMuted:
      case SignalType.callParticipantUnmuted:
      case SignalType.callQualityReport:
      case SignalType.callUpgradeRequest:
      case SignalType.callUpgradeAccept:
      case SignalType.callUpgradeReject:
      case SignalType.callRecordingStarted:
      case SignalType.callRecordingStopped:
        return;
    }
  }

  void _handleOffer(CallSignal signal) {
    if (state != null) {
      _signaling.send(
        CallSignal(
          type: SignalType.callBusy,
          senderId: '',
          targetUserId: signal.senderId,
          callId: signal.callId,
        ),
      );
      return;
    }

    _offerSdp = signal.sdp;
    state = CallInfo(
      state: CallState.incoming,
      type: _signalTypeToCallType(signal.callType),
      peerId: signal.senderId,
      peerName: signal.senderId,
      isInitiator: false,
      callId: signal.callId,
      inviteId: signal.inviteId,
    );

    if (signal.callId case final callId?) {
      _signaling.subscribeToCallRoom(callId);
    }
  }

  void _handleRing(CallSignal signal) {
    if (state != null) {
      _signaling.send(
        CallSignal(
          type: SignalType.callBusy,
          senderId: '',
          targetUserId: signal.senderId,
          callId: signal.callId,
        ),
      );
      return;
    }

    state = CallInfo(
      state: CallState.incoming,
      type: _signalTypeToCallType(signal.callType),
      peerId: signal.senderId,
      peerName: signal.senderId,
      isInitiator: false,
      callId: signal.callId,
      inviteId: signal.inviteId,
    );

    if (signal.callId case final callId?) {
      _signaling.subscribeToCallRoom(callId);
    }
  }

  void _handleAnswer(CallSignal signal) async {
    if (state == null || state!.state != CallState.outgoing || _pc == null) {
      return;
    }

    state = state!.copyWith(state: CallState.connecting);
    try {
      await _pc!.setRemoteDescription(
        RTCSessionDescription(signal.sdp, 'answer'),
      );
      await _flushPendingCandidates();
    } catch (_) {
      _cleanup();
    }
  }

  void _handleIceCandidate(CallSignal signal) {
    if (signal.candidate == null) return;

    final candidate = RTCIceCandidate(
      signal.candidate!['candidate'] as String?,
      signal.candidate!['sdpMid'] as String?,
      signal.candidate!['sdpMLineIndex'] as int?,
    );

    if (_pc != null) {
      unawaited(_pc!.addCandidate(candidate));
    } else {
      _pendingCandidates.add(candidate);
    }
  }

  void _cleanup() {
    final callId = state?.callId;
    if (callId != null && callId.isNotEmpty) {
      _signaling.unsubscribeFromCallRoom(callId);
    }
    _pc?.close();
    _pc = null;
    _disposeStream(state?.localStream);
    _disposeStream(state?.remoteStream);
    _pendingCandidates.clear();
    _offerSdp = null;
    state = null;
  }

  void _disposeStream(MediaStream? stream) {
    if (stream == null) return;
    for (final track in stream.getTracks()) {
      track.stop();
    }
  }

  String _callTypeForRest(CallType type) {
    return type == CallType.video ? 'direct_video' : 'direct_audio';
  }

  String _callTypeForSignal(CallType type) {
    return type == CallType.video ? 'video' : 'audio';
  }

  CallType _signalTypeToCallType(String? callType) {
    switch (callType) {
      case 'video':
      case 'direct_video':
      case 'group_video':
        return CallType.video;
      default:
        return CallType.audio;
    }
  }

  String _idempotencyKey() {
    final timestamp = DateTime.now().microsecondsSinceEpoch.toRadixString(16);
    final randomPart = _random.nextInt(1 << 32).toRadixString(16);
    return 'call-$timestamp-$randomPart';
  }
}

final callProvider = StateNotifierProvider<CallNotifier, CallInfo?>((ref) {
  final signaling = ref.watch(signalingServiceProvider);
  CallsRepository? callsRepo;
  try {
    callsRepo = ref.watch(callsRepositoryProvider);
  } catch (_) {
    // Repository not available in tests.
  }
  return CallNotifier(signaling, callsRepo);
});
