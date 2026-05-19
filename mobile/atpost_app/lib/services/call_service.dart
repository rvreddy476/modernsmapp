import 'dart:async';
import 'dart:math';

import 'package:atpost_app/core/errors/app_exception.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/call.dart' as models;
import 'package:atpost_app/data/repositories/calls_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:livekit_client/livekit_client.dart' as lk;

import 'signaling_service.dart';

enum CallState { idle, outgoing, incoming, connecting, active, failed }

enum CallType { audio, video }

const _copySentinel = Object();
const _fallbackIceServers = <Map<String, dynamic>>[
  {
    'urls': ['stun:stun.l.google.com:19302', 'stun:stun1.l.google.com:19302'],
  },
];

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
  final lk.VideoTrack? localVideoTrack;
  final lk.VideoTrack? remoteVideoTrack;
  final String? callId;
  final String? inviteId;
  final models.JoinResponse? joinResponse;
  final String? error;

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
    this.localVideoTrack,
    this.remoteVideoTrack,
    this.callId,
    this.inviteId,
    this.joinResponse,
    this.error,
  });

  CallInfo copyWith({
    CallState? state,
    CallType? type,
    String? peerId,
    String? peerName,
    String? peerAvatar,
    bool? isInitiator,
    Object? startedAt = _copySentinel,
    Object? localStream = _copySentinel,
    Object? remoteStream = _copySentinel,
    Object? localVideoTrack = _copySentinel,
    Object? remoteVideoTrack = _copySentinel,
    Object? callId = _copySentinel,
    Object? inviteId = _copySentinel,
    Object? joinResponse = _copySentinel,
    Object? error = _copySentinel,
  }) {
    return CallInfo(
      state: state ?? this.state,
      type: type ?? this.type,
      peerId: peerId ?? this.peerId,
      peerName: peerName ?? this.peerName,
      peerAvatar: peerAvatar ?? this.peerAvatar,
      isInitiator: isInitiator ?? this.isInitiator,
      startedAt: identical(startedAt, _copySentinel)
          ? this.startedAt
          : startedAt as DateTime?,
      localStream: identical(localStream, _copySentinel)
          ? this.localStream
          : localStream as MediaStream?,
      remoteStream: identical(remoteStream, _copySentinel)
          ? this.remoteStream
          : remoteStream as MediaStream?,
      localVideoTrack: identical(localVideoTrack, _copySentinel)
          ? this.localVideoTrack
          : localVideoTrack as lk.VideoTrack?,
      remoteVideoTrack: identical(remoteVideoTrack, _copySentinel)
          ? this.remoteVideoTrack
          : remoteVideoTrack as lk.VideoTrack?,
      callId: identical(callId, _copySentinel)
          ? this.callId
          : callId as String?,
      inviteId: identical(inviteId, _copySentinel)
          ? this.inviteId
          : inviteId as String?,
      joinResponse: identical(joinResponse, _copySentinel)
          ? this.joinResponse
          : joinResponse as models.JoinResponse?,
      error: identical(error, _copySentinel) ? this.error : error as String?,
    );
  }
}

class CallNotifier extends StateNotifier<CallInfo?> {
  final SignalingService _signaling;
  final CallsRepository? _callsRepo;
  final UserRepository? _userRepo;
  final Random _random = Random.secure();

  StreamSubscription? _signalSub;
  RTCPeerConnection? _pc;
  final List<RTCIceCandidate> _pendingCandidates = [];
  String? _offerSdp;

  lk.Room? _room;
  lk.EventsListener<lk.RoomEvent>? _roomEvents;
  void Function()? _roomChangeListener;
  bool _localAudioEnabled = true;
  bool _localVideoEnabled = false;

  CallNotifier(this._signaling, [this._callsRepo, this._userRepo]) : super(null) {
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
    String sourceType = 'chat',
    String? sourceId,
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

    models.CallSession? session;
    models.JoinResponse? joinResponse;

    try {
      AppLogger.info('Initiating call to $contactId', tag: 'CallService');
      if (_callsRepo != null) {
        session = await _callsRepo
            .createCall(
              callType: _callTypeForRest(type),
              sourceType: sourceType,
              sourceId: sourceId,
              audioOnly: type == CallType.audio,
              targetUserIds: [contactId],
              maxParticipants: 2,
              idempotencyKey: _idempotencyKey(),
            )
            .timeout(const Duration(seconds: 10));

        AppLogger.info(
          'Call session created: ${session.id}',
          tag: 'CallService',
        );
        joinResponse = await _callsRepo
            .joinCall(session.id)
            .timeout(const Duration(seconds: 10));
        AppLogger.info(
          'Join response received. LiveKit: ${joinResponse.usesLiveKit}',
          tag: 'CallService',
        );
      }

      if (state == null || state!.state != CallState.outgoing) {
        AppLogger.warn(
          'Call state changed or null during initiation',
          tag: 'CallService',
        );
        return;
      }
      state = state!.copyWith(callId: session?.id, joinResponse: joinResponse);

      if (session?.id case final callId?) {
        _signaling.subscribeToCallRoom(callId);
      }

      if (_shouldUseLiveKit(joinResponse)) {
        AppLogger.info(
          'Connecting to LiveKit: ${joinResponse?.sfuUrl}',
          tag: 'CallService',
        );
        await _connectLiveKitRoom(joinResponse: joinResponse!, type: type);
        if (state == null) return;

        _signaling.send(
          CallSignal(
            type: SignalType.callRing,
            senderId: '',
            targetUserId: contactId,
            callId: session?.id,
            callType: _callTypeForSignal(type),
          ),
        );
        return;
      }

      AppLogger.info('Connecting via P2P WebRTC', tag: 'CallService');
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
    } catch (e, st) {
      AppLogger.error(
        'Call Initiation Failed',
        tag: 'CallService',
        error: e,
        stackTrace: st,
      );
      final failedCallId = session?.id;
      final callsRepo = _callsRepo;
      if (callsRepo != null && failedCallId != null) {
        unawaited(callsRepo.endCall(failedCallId));
      }
      final errorMessage = switch (e) {
        AppException() => e.message,
        _ => e.toString(),
      };
      state = state?.copyWith(state: CallState.failed, error: errorMessage);
      // Wait a bit so the user can see the error before we cleanup to idle/null
      Future.delayed(const Duration(seconds: 3), () {
        if (state?.state == CallState.failed) {
          _cleanup();
        }
      });
    }
  }

  Future<void> acceptCall() async {
    if (state == null || state!.state != CallState.incoming) return;

    state = state!.copyWith(state: CallState.connecting);

    final callId = state!.callId;
    final inviteId = state!.inviteId;

    try {
      models.JoinResponse? joinResponse;
      if (_callsRepo != null && callId != null) {
        if (inviteId != null && inviteId.isNotEmpty) {
          try {
            await _callsRepo.acceptInvite(callId, inviteId);
          } catch (_) {
            // Joining can still succeed even if invite acceptance was not provided.
          }
        }
        joinResponse = await _callsRepo.joinCall(callId);
      }

      if (_shouldUseLiveKit(joinResponse)) {
        state = state!.copyWith(joinResponse: joinResponse);
        await _connectLiveKitRoom(
          joinResponse: joinResponse!,
          type: state!.type,
        );
        if (state == null) return;

        _signaling.send(
          CallSignal(
            type: SignalType.callAccept,
            senderId: '',
            targetUserId: state!.peerId,
            callId: callId,
            callType: _callTypeForSignal(state!.type),
            inviteId: inviteId,
          ),
        );
        return;
      }

      if (_offerSdp == null || _offerSdp!.isEmpty) {
        _cleanup();
        return;
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
    } catch (e, st) {
      AppLogger.error(
        'Failed to accept call',
        tag: 'CallService',
        error: e,
        stackTrace: st,
      );
      final callsRepo = _callsRepo;
      if (callsRepo != null && callId != null) {
        unawaited(callsRepo.leaveCall(callId));
      }
      final errorMessage = switch (e) {
        AppException() => e.message,
        _ => e.toString(),
      };
      state = state?.copyWith(state: CallState.failed, error: errorMessage);
      Future.delayed(const Duration(seconds: 3), () {
        if (state?.state == CallState.failed) {
          _cleanup();
        }
      });
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
      unawaited(_callsRepo.declineInvite(callId, inviteId));
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
        unawaited(_callsRepo.endCall(callId));
      } else {
        unawaited(_callsRepo.leaveCall(callId));
      }
    }
    _cleanup();
  }

  bool toggleMute() {
    if (_room != null) {
      return _toggleLiveKitMicrophone();
    }

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
    if (_room != null) {
      return _toggleLiveKitCamera();
    }

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
          iceState == RTCIceConnectionState.RTCIceConnectionStateDisconnected ||
          iceState == RTCIceConnectionState.RTCIceConnectionStateClosed) {
        _cleanup();
      }
    };

    return conn;
  }

  Future<void> _connectLiveKitRoom({
    required models.JoinResponse joinResponse,
    required CallType type,
  }) async {
    await _disposeLiveKitRoom();

    final room = lk.Room();
    _room = room;

    _roomEvents = room.createListener()
      ..on<lk.RoomDisconnectedEvent>((_) {
        _cleanup();
      })
      ..on<lk.ParticipantConnectedEvent>((_) {
        _syncLiveKitRoomState();
      })
      ..on<lk.ParticipantDisconnectedEvent>((_) {
        _syncLiveKitRoomState();
      })
      ..on<lk.TrackSubscribedEvent>((_) {
        _syncLiveKitRoomState();
      })
      ..on<lk.TrackUnsubscribedEvent>((_) {
        _syncLiveKitRoomState();
      });

    _roomChangeListener = _syncLiveKitRoomState;
    room.addListener(_roomChangeListener!);

    await room.connect(joinResponse.sfuUrl, joinResponse.sfuToken);

    final localParticipant = room.localParticipant;
    if (localParticipant == null) {
      throw StateError('LiveKit room connected without a local participant');
    }

    _localAudioEnabled = true;
    _localVideoEnabled = type == CallType.video;

    await localParticipant.setMicrophoneEnabled(true);
    if (type == CallType.video) {
      try {
        await localParticipant.setCameraEnabled(true);
      } catch (_) {
        _localVideoEnabled = false;
      }
    }

    _syncLiveKitRoomState();
  }

  void _syncLiveKitRoomState() {
    final current = state;
    final room = _room;
    if (current == null || room == null) return;

    final localParticipant = room.localParticipant;
    if (localParticipant == null) return;

    final localVideoTrack = _pickVideoTrack(
      localParticipant.videoTrackPublications,
    );

    lk.VideoTrack? remoteVideoTrack;
    for (final participant in room.remoteParticipants.values) {
      remoteVideoTrack = _pickVideoTrack(participant.videoTrackPublications);
      if (remoteVideoTrack != null) {
        break;
      }
    }

    final hasRemoteParticipant = room.remoteParticipants.isNotEmpty;
    final nextState = hasRemoteParticipant
        ? CallState.active
        : (current.isInitiator ? CallState.outgoing : CallState.connecting);

    state = current.copyWith(
      state: nextState,
      startedAt: hasRemoteParticipant
          ? (current.startedAt ?? DateTime.now())
          : current.startedAt,
      localVideoTrack: localVideoTrack,
      remoteVideoTrack: remoteVideoTrack,
      localStream: null,
      remoteStream: null,
    );
  }

  lk.VideoTrack? _pickVideoTrack(Iterable<dynamic> publications) {
    for (final publication in publications) {
      final track = publication.track;
      final muted = publication.muted == true;
      final isScreenShare = publication.isScreenShare == true;
      if (!muted && !isScreenShare && track is lk.VideoTrack) {
        return track;
      }
    }
    return null;
  }

  bool _toggleLiveKitMicrophone() {
    final room = _room;
    if (room == null) return false;
    final localParticipant = room.localParticipant;
    if (localParticipant == null) return false;

    _localAudioEnabled = !_localAudioEnabled;
    unawaited(localParticipant.setMicrophoneEnabled(_localAudioEnabled));

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

    return !_localAudioEnabled;
  }

  bool _toggleLiveKitCamera() {
    final room = _room;
    if (room == null) return false;
    final localParticipant = room.localParticipant;
    if (localParticipant == null) return false;

    _localVideoEnabled = !_localVideoEnabled;
    unawaited(localParticipant.setCameraEnabled(_localVideoEnabled));
    _syncLiveKitRoomState();

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

    return !_localVideoEnabled;
  }

  bool _shouldUseLiveKit(models.JoinResponse? joinResponse) {
    return joinResponse != null && joinResponse.usesLiveKit;
  }

  List<Map<String, dynamic>> _iceServersFor(models.JoinResponse? joinResponse) {
    final servers =
        joinResponse?.iceServers.where((server) => server.urls.isNotEmpty).map((
          server,
        ) {
          return <String, dynamic>{
            'urls': server.urls,
            if (server.username != null && server.username!.isNotEmpty)
              'username': server.username,
            if (server.credential != null && server.credential!.isNotEmpty)
              'credential': server.credential,
          };
        }).toList() ??
        const <Map<String, dynamic>>[];

    if (servers.isNotEmpty) {
      return servers;
    }
    return _fallbackIceServers;
  }

  Future<MediaStream> _getMedia(CallType type) async {
    final constraints = <String, dynamic>{
      'audio': true,
      'video': type == CallType.video
          ? {
              'facingMode': 'user',
              'width': {'ideal': 1280},
              'height': {'ideal': 720},
            }
          : false,
    };

    try {
      return await navigator.mediaDevices.getUserMedia(constraints);
    } catch (e) {
      // If requested resolution fails, try with default constraints
      if (type == CallType.video) {
        return await navigator.mediaDevices.getUserMedia({
          'audio': true,
          'video': true,
        });
      }
      rethrow;
    }
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
      case SignalType.callAccept:
        if (state != null && state!.state == CallState.outgoing) {
          state = state!.copyWith(state: CallState.connecting);
        }
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
        if (_room != null) {
          _syncLiveKitRoomState();
        } else if (state != null) {
          state = state!.copyWith();
        }
        return;
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
      peerName: '',
      isInitiator: false,
      callId: signal.callId,
      inviteId: signal.inviteId,
    );
    unawaited(_hydratePeer(signal.senderId));

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
      peerName: '',
      isInitiator: false,
      callId: signal.callId,
      inviteId: signal.inviteId,
    );
    unawaited(_hydratePeer(signal.senderId));

    if (signal.callId case final callId?) {
      _signaling.subscribeToCallRoom(callId);
    }
  }

  /// Resolve the peer's display name + avatar from their user ID. Incoming
  /// call signals carry only the sender's UUID, so without this the call UI
  /// would show a raw ID. Best-effort: keeps the fallback if the lookup fails.
  Future<void> _hydratePeer(String userId) async {
    final repo = _userRepo;
    if (repo == null || userId.isEmpty) return;
    try {
      final user = await repo.getUser(userId);
      final current = state;
      if (current == null || current.peerId != userId) return;
      state = current.copyWith(
        peerName: user.displayName,
        peerAvatar: user.hasAvatar ? user.avatarUrl : '',
      );
    } catch (_) {
      // Best-effort — the call still works without a resolved name.
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

    final room = _room;
    final roomEvents = _roomEvents;
    final roomChangeListener = _roomChangeListener;
    _room = null;
    _roomEvents = null;
    _roomChangeListener = null;

    if (room != null && roomChangeListener != null) {
      room.removeListener(roomChangeListener);
    }
    if (roomEvents != null) {
      unawaited(roomEvents.dispose());
    }
    if (room != null) {
      unawaited(_disposeRoom(room));
    }

    _pc?.close();
    _pc = null;
    _disposeStream(state?.localStream);
    _disposeStream(state?.remoteStream);
    _pendingCandidates.clear();
    _offerSdp = null;
    _localAudioEnabled = true;
    _localVideoEnabled = false;
    state = null;
  }

  Future<void> _disposeLiveKitRoom() async {
    final room = _room;
    final roomEvents = _roomEvents;
    final roomChangeListener = _roomChangeListener;
    _room = null;
    _roomEvents = null;
    _roomChangeListener = null;

    if (room != null && roomChangeListener != null) {
      room.removeListener(roomChangeListener);
    }
    if (roomEvents != null) {
      await roomEvents.dispose();
    }
    if (room != null) {
      await _disposeRoom(room);
    }
  }

  Future<void> _disposeRoom(lk.Room room) async {
    try {
      await room.disconnect();
    } catch (_) {
      // Ignore disconnect failures during teardown.
    }
    try {
      await room.dispose();
    } catch (_) {
      // Ignore disposal failures during teardown.
    }
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
  UserRepository? userRepo;
  try {
    userRepo = ref.watch(userRepositoryProvider);
  } catch (_) {
    // Repository not available in tests.
  }
  return CallNotifier(signaling, callsRepo, userRepo);
});
