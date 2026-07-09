import 'dart:async';

import 'package:atpost_app/data/models/live_stream.dart';
import 'package:atpost_app/data/repositories/live_repository.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

enum LivePublishState {
  idle,
  preparingPreview,
  previewReady,
  starting,
  publishing,
  stopping,
  error,
}

class LiveWhipPublisher extends ChangeNotifier {
  static const _defaultIceServers = <Map<String, dynamic>>[
    {
      'urls': ['stun:stun.l.google.com:19302', 'stun:stun1.l.google.com:19302'],
    },
  ];

  final RTCVideoRenderer previewRenderer = RTCVideoRenderer();

  LivePublishState _state = LivePublishState.idle;
  MediaStream? _localStream;
  RTCPeerConnection? _peerConnection;
  String? _sessionUrl;
  String? _errorMessage;
  bool _rendererInitialized = false;
  bool _audioEnabled = true;
  bool _videoEnabled = true;
  bool _disposed = false;

  LivePublishState get state => _state;
  String? get errorMessage => _errorMessage;
  bool get hasPreview => _localStream != null;
  bool get isPreparingPreview => _state == LivePublishState.preparingPreview;
  bool get isStarting => _state == LivePublishState.starting;
  bool get isPublishing => _state == LivePublishState.publishing;
  bool get isStopping => _state == LivePublishState.stopping;
  bool get isBusy => isPreparingPreview || isStarting || isStopping;
  bool get isAudioEnabled => _audioEnabled;
  bool get isVideoEnabled => _videoEnabled;
  bool get hasSession => (_sessionUrl ?? '').isNotEmpty;

  Future<void> ensurePreview(LiveStream stream) async {
    if (_disposed || _localStream != null || !stream.canPublishFromMobile) {
      return;
    }

    _setState(LivePublishState.preparingPreview, clearError: true);
    try {
      await _ensureRendererInitialized();
      final localStream = await navigator.mediaDevices.getUserMedia(_constraints);
      if (_disposed) {
        await _disposeStream(localStream);
        return;
      }

      _localStream = localStream;
      previewRenderer.srcObject = localStream;
      _audioEnabled = _firstTrackEnabled(localStream.getAudioTracks());
      _videoEnabled = _firstTrackEnabled(localStream.getVideoTracks());
      _setState(LivePublishState.previewReady, clearError: true);
    } catch (error) {
      _errorMessage = _normalizeError(error);
      _setState(LivePublishState.error);
      rethrow;
    }
  }

  Future<void> startPublishing({
    required LiveStream stream,
    required LiveRepository repository,
  }) async {
    if (_disposed || isBusy || isPublishing) return;
    if (!stream.canPublishFromMobile) {
      throw StateError('This live stream does not expose a WHIP publish URL.');
    }

    await ensurePreview(stream);
    final localStream = _localStream;
    if (localStream == null) {
      throw StateError('Camera preview is not ready.');
    }

    _setState(LivePublishState.starting, clearError: true);
    try {
      final peerConnection = await createPeerConnection(<String, dynamic>{
        'iceServers': _iceServersFor(stream),
      });
      _peerConnection = peerConnection;
      _bindConnectionCallbacks(peerConnection);

      for (final track in localStream.getTracks()) {
        await peerConnection.addTrack(track, localStream);
      }

      final offer = await peerConnection.createOffer(<String, dynamic>{
        'offerToReceiveAudio': false,
        'offerToReceiveVideo': false,
      });
      await peerConnection.setLocalDescription(offer);

      final localDescription = await _awaitLocalDescription(peerConnection);
      final publishSession = await repository.createPublishSession(
        publishUrl: stream.publishUrl!,
        sdpOffer: localDescription.sdp ?? '',
        headers: stream.publishHeaders,
      );

      if ((publishSession.answerSdp).trim().isEmpty) {
        throw StateError('WHIP publish endpoint returned an empty SDP answer.');
      }

      await peerConnection.setRemoteDescription(
        RTCSessionDescription(publishSession.answerSdp, 'answer'),
      );

      _sessionUrl = publishSession.sessionUrl;
      _setState(LivePublishState.publishing, clearError: true);
    } catch (error) {
      _errorMessage = _normalizeError(error);
      await _closePeerConnection();
      _setState(
        _localStream != null ? LivePublishState.previewReady : LivePublishState.error,
      );
      rethrow;
    }
  }

  Future<void> stopPublishing({
    required LiveStream stream,
    required LiveRepository repository,
    bool preservePreview = true,
  }) async {
    if (_disposed || (_peerConnection == null && (_sessionUrl ?? '').isEmpty)) {
      if (!preservePreview) {
        await disposeAsync();
      }
      return;
    }

    _setState(LivePublishState.stopping, clearError: true);
    Object? deleteError;
    final sessionUrl = _sessionUrl;
    _sessionUrl = null;

    try {
      if (sessionUrl != null && sessionUrl.isNotEmpty) {
        await repository.deletePublishSession(
          sessionUrl,
          headers: stream.publishHeaders,
        );
      }
    } catch (error) {
      deleteError = error;
      _errorMessage = _normalizeError(error);
    } finally {
      await _closePeerConnection();
    }

    if (!preservePreview) {
      await _disposePreviewOnly();
    }

    _setState(
      _localStream != null ? LivePublishState.previewReady : LivePublishState.idle,
      clearError: deleteError == null,
    );

    if (deleteError != null) {
      throw deleteError;
    }
  }

  void toggleAudioEnabled() {
    final tracks = _localStream?.getAudioTracks() ?? const <MediaStreamTrack>[];
    if (tracks.isEmpty) return;
    final next = !_firstTrackEnabled(tracks);
    for (final track in tracks) {
      track.enabled = next;
    }
    _audioEnabled = next;
    notifyListeners();
  }

  void toggleVideoEnabled() {
    final tracks = _localStream?.getVideoTracks() ?? const <MediaStreamTrack>[];
    if (tracks.isEmpty) return;
    final next = !_firstTrackEnabled(tracks);
    for (final track in tracks) {
      track.enabled = next;
    }
    _videoEnabled = next;
    notifyListeners();
  }

  Future<void> disposeAsync() async {
    if (_disposed) return;
    _disposed = true;
    await _closePeerConnection();
    await _disposePreviewOnly();
    if (_rendererInitialized) {
      await previewRenderer.dispose();
      _rendererInitialized = false;
    }
    super.dispose();
  }

  Future<void> _ensureRendererInitialized() async {
    if (_rendererInitialized) return;
    await previewRenderer.initialize();
    _rendererInitialized = true;
  }

  void _bindConnectionCallbacks(RTCPeerConnection peerConnection) {
    peerConnection.onIceConnectionState = (state) {
      if (_disposed) return;
      if (state == RTCIceConnectionState.RTCIceConnectionStateConnected ||
          state == RTCIceConnectionState.RTCIceConnectionStateCompleted) {
        if (_state == LivePublishState.starting) {
          _setState(LivePublishState.publishing, clearError: true);
        }
        return;
      }
      if (state == RTCIceConnectionState.RTCIceConnectionStateFailed ||
          state == RTCIceConnectionState.RTCIceConnectionStateDisconnected ||
          state == RTCIceConnectionState.RTCIceConnectionStateClosed) {
        _errorMessage = 'The live publishing connection was interrupted.';
        _setState(
          _localStream != null ? LivePublishState.previewReady : LivePublishState.error,
        );
      }
    };
  }

  Future<RTCSessionDescription> _awaitLocalDescription(
    RTCPeerConnection peerConnection,
  ) async {
    final complete = Completer<void>();
    peerConnection.onIceGatheringState = (state) {
      if (!complete.isCompleted &&
          state == RTCIceGatheringState.RTCIceGatheringStateComplete) {
        complete.complete();
      }
    };

    await Future.any<void>([
      complete.future,
      Future<void>.delayed(const Duration(seconds: 5)),
    ]);

    final description = await peerConnection.getLocalDescription();
    if (description == null || (description.sdp ?? '').trim().isEmpty) {
      throw StateError('Could not build a local SDP offer for live publishing.');
    }
    return description;
  }

  List<Map<String, dynamic>> _iceServersFor(LiveStream stream) {
    if (stream.publishIceServers.isEmpty) {
      return _defaultIceServers;
    }
    return stream.publishIceServers
        .map((server) => server.toRtcConfiguration())
        .toList(growable: false);
  }

  Future<void> _closePeerConnection() async {
    final peerConnection = _peerConnection;
    _peerConnection = null;
    if (peerConnection == null) return;
    try {
      await peerConnection.close();
    } catch (_) {}
    await peerConnection.dispose();
  }

  Future<void> _disposePreviewOnly() async {
    final localStream = _localStream;
    _localStream = null;
    previewRenderer.srcObject = null;
    if (localStream != null) {
      await _disposeStream(localStream);
    }
    _audioEnabled = true;
    _videoEnabled = true;
  }

  Future<void> _disposeStream(MediaStream stream) async {
    for (final track in stream.getTracks()) {
      try {
        await track.stop();
      } catch (_) {}
    }
  }

  bool _firstTrackEnabled(List<MediaStreamTrack> tracks) {
    if (tracks.isEmpty) return false;
    return tracks.first.enabled;
  }

  void _setState(LivePublishState next, {bool clearError = false}) {
    _state = next;
    if (clearError) {
      _errorMessage = null;
    }
    notifyListeners();
  }

  String _normalizeError(Object error) {
    final text = error.toString().trim();
    if (text.isEmpty) return 'Live publishing failed.';
    if (text.startsWith('Exception: ')) {
      return text.substring('Exception: '.length);
    }
    if (text.startsWith('StateError: ')) {
      return text.substring('StateError: '.length);
    }
    return text;
  }

  Map<String, dynamic> get _constraints {
    return <String, dynamic>{
      'audio': true,
      'video': <String, dynamic>{
        'facingMode': 'user',
        'width': <String, dynamic>{'ideal': 1280},
        'height': <String, dynamic>{'ideal': 720},
        'frameRate': <String, dynamic>{'ideal': 30},
      },
    };
  }
}
