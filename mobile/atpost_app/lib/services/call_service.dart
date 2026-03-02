import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

import 'signaling_service.dart';

/// Call states matching the web frontend protocol.
enum CallState { idle, outgoing, incoming, connecting, active }

/// Audio or video call.
enum CallType { audio, video }

/// Immutable snapshot of current call state.
class CallInfo {
  final CallState state;
  final CallType type;
  final String peerId;
  final String peerName;
  final String peerAvatar;
  final DateTime? startedAt;
  final MediaStream? localStream;
  final MediaStream? remoteStream;

  const CallInfo({
    required this.state,
    required this.type,
    required this.peerId,
    this.peerName = '',
    this.peerAvatar = '',
    this.startedAt,
    this.localStream,
    this.remoteStream,
  });

  CallInfo copyWith({
    CallState? state,
    CallType? type,
    String? peerId,
    String? peerName,
    String? peerAvatar,
    DateTime? startedAt,
    MediaStream? localStream,
    MediaStream? remoteStream,
  }) {
    return CallInfo(
      state: state ?? this.state,
      type: type ?? this.type,
      peerId: peerId ?? this.peerId,
      peerName: peerName ?? this.peerName,
      peerAvatar: peerAvatar ?? this.peerAvatar,
      startedAt: startedAt ?? this.startedAt,
      localStream: localStream ?? this.localStream,
      remoteStream: remoteStream ?? this.remoteStream,
    );
  }
}

const _iceServers = <Map<String, dynamic>>[
  {
    'urls': ['stun:stun.l.google.com:19302', 'stun:stun1.l.google.com:19302'],
  },
];

/// Manages WebRTC peer connections and call state.
class CallNotifier extends StateNotifier<CallInfo?> {
  final SignalingService _signaling;
  StreamSubscription? _signalSub;
  RTCPeerConnection? _pc;
  final List<RTCIceCandidate> _pendingCandidates = [];
  String? _offerSdp; // stored SDP for incoming calls

  CallNotifier(this._signaling) : super(null) {
    _signalSub = _signaling.signals.listen(_handleSignal);
  }

  @override
  void dispose() {
    _signalSub?.cancel();
    _cleanup();
    super.dispose();
  }

  // --- Public API ---

  /// Initiate an outgoing call.
  Future<void> initiateCall({
    required String contactId,
    required String contactName,
    required String contactAvatar,
    required CallType type,
  }) async {
    if (state != null) return; // already in a call

    state = CallInfo(
      state: CallState.outgoing,
      type: type,
      peerId: contactId,
      peerName: contactName,
      peerAvatar: contactAvatar,
    );

    try {
      final stream = await _getMedia(type);
      if (state == null || state!.state != CallState.outgoing) {
        _disposeStream(stream);
        return;
      }
      state = state!.copyWith(localStream: stream);

      _pc = await _createPeerConnection();
      for (final track in stream.getTracks()) {
        await _pc!.addTrack(track, stream);
      }

      final offer = await _pc!.createOffer();
      await _pc!.setLocalDescription(offer);

      _signaling.send(CallSignal(
        type: SignalType.callOffer,
        senderId: '',
        targetUserId: contactId,
        callType: type == CallType.video ? 'video' : 'audio',
        sdp: offer.sdp,
      ));
    } catch (_) {
      _cleanup();
    }
  }

  /// Accept an incoming call.
  Future<void> acceptCall() async {
    if (state == null || state!.state != CallState.incoming) return;

    state = state!.copyWith(state: CallState.connecting);

    try {
      final stream = await _getMedia(state!.type);
      if (state == null) {
        _disposeStream(stream);
        return;
      }
      state = state!.copyWith(localStream: stream);

      _pc = await _createPeerConnection();
      for (final track in stream.getTracks()) {
        await _pc!.addTrack(track, stream);
      }

      // Set remote offer
      await _pc!.setRemoteDescription(
        RTCSessionDescription(_offerSdp, 'offer'),
      );
      await _flushPendingCandidates();

      final answer = await _pc!.createAnswer();
      await _pc!.setLocalDescription(answer);

      _signaling.send(CallSignal(
        type: SignalType.callAnswer,
        senderId: '',
        targetUserId: state!.peerId,
        sdp: answer.sdp,
      ));
    } catch (_) {
      _cleanup();
    }
  }

  /// Decline an incoming call.
  void declineCall() {
    if (state == null) return;
    _signaling.send(CallSignal(
      type: SignalType.callDecline,
      senderId: '',
      targetUserId: state!.peerId,
    ));
    _cleanup();
  }

  /// End an active call.
  void endCall() {
    if (state == null) return;
    _signaling.send(CallSignal(
      type: SignalType.callEnd,
      senderId: '',
      targetUserId: state!.peerId,
    ));
    _cleanup();
  }

  /// Toggle microphone mute. Returns true if now muted.
  bool toggleMute() {
    final stream = state?.localStream;
    if (stream == null) return false;
    final tracks = stream.getAudioTracks();
    if (tracks.isEmpty) return false;
    tracks.first.enabled = !tracks.first.enabled;
    return !tracks.first.enabled;
  }

  /// Toggle camera. Returns true if camera is now off.
  bool toggleCamera() {
    final stream = state?.localStream;
    if (stream == null) return false;
    final tracks = stream.getVideoTracks();
    if (tracks.isEmpty) return false;
    tracks.first.enabled = !tracks.first.enabled;
    return !tracks.first.enabled;
  }

  // --- Private ---

  Future<RTCPeerConnection> _createPeerConnection() async {
    final config = <String, dynamic>{
      'iceServers': _iceServers,
    };
    final conn = await createPeerConnection(config);

    conn.onIceCandidate = (candidate) {
      if (state != null) {
        _signaling.send(CallSignal(
          type: SignalType.iceCandidate,
          senderId: '',
          targetUserId: state!.peerId,
          candidate: candidate.toMap(),
        ));
      }
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

  Future<MediaStream> _getMedia(CallType type) async {
    final constraints = <String, dynamic>{
      'audio': true,
      'video': type == CallType.video,
    };
    return await navigator.mediaDevices.getUserMedia(constraints);
  }

  Future<void> _flushPendingCandidates() async {
    if (_pc == null) return;
    for (final c in _pendingCandidates) {
      await _pc!.addCandidate(c);
    }
    _pendingCandidates.clear();
  }

  void _handleSignal(CallSignal signal) {
    switch (signal.type) {
      case SignalType.callOffer:
        _handleOffer(signal);
      case SignalType.callAnswer:
        _handleAnswer(signal);
      case SignalType.iceCandidate:
        _handleIceCandidate(signal);
      case SignalType.callEnd:
      case SignalType.callDecline:
      case SignalType.callBusy:
        _cleanup();
    }
  }

  void _handleOffer(CallSignal signal) {
    if (state != null) {
      // Already in a call, send busy
      _signaling.send(CallSignal(
        type: SignalType.callBusy,
        senderId: '',
        targetUserId: signal.senderId,
      ));
      return;
    }
    _offerSdp = signal.sdp;
    state = CallInfo(
      state: CallState.incoming,
      type: signal.callType == 'video' ? CallType.video : CallType.audio,
      peerId: signal.senderId,
      peerName: signal.senderId, // UI can resolve display name
    );
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
      _pc!.addCandidate(candidate);
    } else {
      _pendingCandidates.add(candidate);
    }
  }

  void _cleanup() {
    _pc?.close();
    _pc = null;
    _disposeStream(state?.localStream);
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
}

/// Riverpod provider for call state.
final callProvider = StateNotifierProvider<CallNotifier, CallInfo?>((ref) {
  final signaling = ref.watch(signalingServiceProvider);
  return CallNotifier(signaling);
});
