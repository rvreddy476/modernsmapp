// Live-streaming v2: publisher screen for the broadcaster. Hits
// POST /v1/live/streams/:id/start to mint a publisher token, opens a
// LiveKit Room, attaches the device camera + mic, and renders the local
// camera feed via VideoTrackRenderer.

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/live_streams_repository.dart';
import 'package:atpost_app/providers/live_streams_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:livekit_client/livekit_client.dart' as lk;

class LiveBroadcasterScreen extends ConsumerStatefulWidget {
  final String streamId;
  const LiveBroadcasterScreen({super.key, required this.streamId});

  @override
  ConsumerState<LiveBroadcasterScreen> createState() =>
      _LiveBroadcasterScreenState();
}

enum _BroadcastPhase { idle, starting, publishing, ending, ended, error }

class _LiveBroadcasterScreenState
    extends ConsumerState<LiveBroadcasterScreen> {
  lk.Room? _room;
  lk.LocalVideoTrack? _videoTrack;
  lk.LocalAudioTrack? _audioTrack;
  _BroadcastPhase _phase = _BroadcastPhase.idle;
  String? _errorMessage;
  int _participantCount = 0;
  bool _started = false;

  @override
  void initState() {
    super.initState();
    // StrictMode-equivalent guard: ensure we don't double-start when the
    // widget rebuilds while we're already in the connect handshake.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_started) return;
      _started = true;
      unawaited(_start());
    });
  }

  @override
  void dispose() {
    _teardown();
    super.dispose();
  }

  Future<void> _teardown() async {
    try {
      await _room?.disconnect();
    } catch (_) {
      // ignore
    }
    _room = null;
    await _videoTrack?.stop();
    await _audioTrack?.stop();
    _videoTrack = null;
    _audioTrack = null;
  }

  Future<void> _start() async {
    if (!mounted) return;
    setState(() {
      _phase = _BroadcastPhase.starting;
      _errorMessage = null;
    });
    try {
      final controller = ref.read(liveBroadcasterControllerProvider);
      final result = await controller.start(widget.streamId);

      final room = lk.Room();
      room.events.listen((event) {
        if (!mounted) return;
        if (event is lk.ParticipantConnectedEvent ||
            event is lk.ParticipantDisconnectedEvent) {
          setState(() {
            _participantCount = room.remoteParticipants.length;
          });
        } else if (event is lk.RoomDisconnectedEvent) {
          if (_phase != _BroadcastPhase.ending &&
              _phase != _BroadcastPhase.ended) {
            setState(() => _phase = _BroadcastPhase.ended);
          }
        }
      });

      await room.connect(result.serverUrl, result.publisherToken);

      final video = await lk.LocalVideoTrack.createCameraTrack();
      final audio = await lk.LocalAudioTrack.create();
      _videoTrack = video;
      _audioTrack = audio;

      await room.localParticipant?.publishVideoTrack(video);
      await room.localParticipant?.publishAudioTrack(audio);

      if (!mounted) {
        // We may have been disposed during the async chain — clean up.
        await room.disconnect();
        await video.stop();
        await audio.stop();
        return;
      }
      setState(() {
        _room = room;
        _participantCount = room.remoteParticipants.length;
        _phase = _BroadcastPhase.publishing;
      });
    } catch (err) {
      if (!mounted) return;
      final access = LiveAccessException.maybeFrom(err);
      setState(() {
        _phase = _BroadcastPhase.error;
        _errorMessage = access?.message ?? 'Couldn\'t start broadcast.';
      });
    }
  }

  Future<void> _endStream() async {
    if (!mounted) return;
    setState(() => _phase = _BroadcastPhase.ending);
    try {
      final controller = ref.read(liveBroadcasterControllerProvider);
      await controller.end(widget.streamId);
    } catch (_) {
      // Even if the server call fails we still want to release the
      // camera. Reconciliation worker on the backend handles cleanup.
    }
    await _teardown();
    if (!mounted) return;
    setState(() => _phase = _BroadcastPhase.ended);
    if (context.canPop()) {
      context.pop();
    } else {
      context.go('/live/v2/${widget.streamId}');
    }
  }

  @override
  Widget build(BuildContext context) {
    final isLive = _phase == _BroadcastPhase.publishing;
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        elevation: 0,
        title: Row(
          children: [
            if (isLive)
              Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: AppColors.liveRed,
                  borderRadius: BorderRadius.circular(10),
                ),
                child: Text(
                  'LIVE',
                  style: AppTextStyles.labelTiny.copyWith(
                    color: Colors.white,
                    fontWeight: FontWeight.w900,
                    letterSpacing: 1.5,
                  ),
                ),
              ),
            if (isLive) const SizedBox(width: 8),
            Text('You\'re broadcasting',
                style: AppTextStyles.h3.copyWith(color: Colors.white)),
          ],
        ),
      ),
      body: SafeArea(
        child: Column(
          children: [
            Expanded(
              child: Stack(
                children: [
                  Positioned.fill(child: _buildPreview()),
                  Positioned(
                    left: 12,
                    top: 12,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 10, vertical: 4),
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: 0.55),
                        borderRadius: BorderRadius.circular(20),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.remove_red_eye,
                              color: Colors.white, size: 12),
                          const SizedBox(width: 4),
                          Text(
                            '$_participantCount',
                            style: AppTextStyles.labelTiny.copyWith(
                              color: Colors.white,
                              fontWeight: FontWeight.bold,
                            ),
                          ),
                          const SizedBox(width: 6),
                          Text(
                            'in-room',
                            style: AppTextStyles.labelTiny.copyWith(
                              color: Colors.white.withValues(alpha: 0.7),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
            Container(
              padding: const EdgeInsets.symmetric(
                  horizontal: 16, vertical: 12),
              color: AppColors.bgSecondary,
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      _statusLabel(),
                      style: AppTextStyles.labelSmall
                          .copyWith(color: AppColors.textSecondary),
                    ),
                  ),
                  FilledButton.icon(
                    onPressed: (_phase == _BroadcastPhase.publishing ||
                            _phase == _BroadcastPhase.error)
                        ? _endStream
                        : null,
                    icon: _phase == _BroadcastPhase.ending
                        ? const SizedBox(
                            width: 14,
                            height: 14,
                            child: CircularProgressIndicator(
                                strokeWidth: 2, color: Colors.white),
                          )
                        : const Icon(Icons.stop_circle_outlined),
                    label: Text(
                      _phase == _BroadcastPhase.ending
                          ? 'Ending…'
                          : 'End stream',
                      style: AppTextStyles.label
                          .copyWith(color: Colors.white),
                    ),
                    style: FilledButton.styleFrom(
                      backgroundColor: AppColors.liveRed,
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(10),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildPreview() {
    if (_phase == _BroadcastPhase.starting) {
      return const Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            CircularProgressIndicator(color: AppColors.liveRed),
            SizedBox(height: 12),
            Text(
              'Connecting to the broadcast server…',
              style: TextStyle(color: Colors.white),
            ),
          ],
        ),
      );
    }
    if (_phase == _BroadcastPhase.error) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.error_outline,
                  color: AppColors.statusError, size: 32),
              const SizedBox(height: 8),
              Text(
                _errorMessage ?? 'Couldn\'t start broadcast.',
                style: const TextStyle(color: Colors.white),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 16),
              FilledButton(
                onPressed: () => _start(),
                style: FilledButton.styleFrom(
                    backgroundColor: AppColors.liveRed),
                child: const Text('Try again'),
              ),
            ],
          ),
        ),
      );
    }
    final track = _videoTrack;
    if (track == null) {
      return const Center(
        child: Text('Camera preview', style: TextStyle(color: Colors.white60)),
      );
    }
    return lk.VideoTrackRenderer(track);
  }

  String _statusLabel() {
    switch (_phase) {
      case _BroadcastPhase.idle:
      case _BroadcastPhase.starting:
        return 'Setting things up…';
      case _BroadcastPhase.publishing:
        return 'You\'re live — your camera + mic are publishing.';
      case _BroadcastPhase.ending:
        return 'Ending stream…';
      case _BroadcastPhase.ended:
        return 'Stream ended.';
      case _BroadcastPhase.error:
        return _errorMessage ?? 'Something went wrong.';
    }
  }
}
