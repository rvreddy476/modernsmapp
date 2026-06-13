// Live-streaming v2: subscriber/viewer screen. Pulls the stream detail
// via liveStreamDetailProvider (with polling), and when status == 'live'
// fetches a LiveKit viewer token + connects as a subscriber. The first
// remote video track is rendered through VideoTrackRenderer. When the
// stream has ended and a recording_url is set, falls back to VOD
// playback via the existing video_player.

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/live_stream_v2.dart';
import 'package:atpost_app/data/repositories/live_streams_repository.dart';
import 'package:atpost_app/features/live/live_chat_panel.dart';
import 'package:atpost_app/providers/live_streams_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:livekit_client/livekit_client.dart' as lk;
import 'package:video_player/video_player.dart';

class LiveViewerScreen extends ConsumerStatefulWidget {
  final String streamId;
  const LiveViewerScreen({super.key, required this.streamId});

  @override
  ConsumerState<LiveViewerScreen> createState() => _LiveViewerScreenState();
}

class _LiveViewerScreenState extends ConsumerState<LiveViewerScreen> {
  lk.Room? _room;
  lk.VideoTrack? _videoTrack;
  Timer? _refreshTimer;
  VideoPlayerController? _vodController;
  bool _connecting = false;
  String? _accessMessage; // 403/402/404 user-facing fallback
  int _participantCount = 0;

  @override
  void initState() {
    super.initState();
    // Lightweight polling so the viewer-peak counter advances and we
    // pick up the status flip when the host hits Start.
    _refreshTimer = Timer.periodic(const Duration(seconds: 5), (_) {
      if (!mounted) return;
      ref.invalidate(liveStreamDetailProvider(widget.streamId));
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _refreshTimer = null;
    unawaited(_room?.disconnect());
    _room = null;
    _vodController?.dispose();
    _vodController = null;
    super.dispose();
  }

  Future<void> _connectViewer() async {
    if (_connecting || _room != null) return;
    _connecting = true;
    try {
      final repo = ref.read(liveStreamsRepositoryProvider);
      final result = await repo.getViewerToken(widget.streamId);
      final room = lk.Room();
      room.events.listen((event) {
        if (!mounted) return;
        if (event is lk.TrackSubscribedEvent &&
            event.track is lk.VideoTrack) {
          setState(() {
            _videoTrack = event.track as lk.VideoTrack;
            _participantCount = room.remoteParticipants.length;
          });
        } else if (event is lk.ParticipantConnectedEvent ||
            event is lk.ParticipantDisconnectedEvent) {
          setState(() {
            _participantCount = room.remoteParticipants.length;
          });
        }
      });
      await room.connect(result.serverUrl, result.token);
      // Pull in any already-published tracks.
      lk.VideoTrack? firstVideo;
      for (final participant in room.remoteParticipants.values) {
        for (final pub in participant.videoTrackPublications) {
          final track = pub.track;
          if (track is lk.VideoTrack) {
            firstVideo = track;
            break;
          }
        }
        if (firstVideo != null) break;
      }
      if (!mounted) {
        await room.disconnect();
        return;
      }
      setState(() {
        _room = room;
        _videoTrack = firstVideo;
        _participantCount = room.remoteParticipants.length;
      });
    } catch (err) {
      final access = LiveAccessException.maybeFrom(err);
      if (!mounted) return;
      setState(() {
        _accessMessage = access?.message ?? 'Couldn\'t connect to the stream.';
      });
    } finally {
      _connecting = false;
    }
  }

  Future<void> _initVod(String url) async {
    if (_vodController != null) return;
    try {
      final controller = VideoPlayerController.networkUrl(Uri.parse(url));
      await controller.initialize();
      if (!mounted) {
        await controller.dispose();
        return;
      }
      setState(() => _vodController = controller);
    } catch (_) {
      // best-effort; the UI falls back to a "stream ended" panel.
    }
  }

  @override
  Widget build(BuildContext context) {
    final detail = ref.watch(liveStreamDetailProvider(widget.streamId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: detail.when(
          loading: () => Text('Live', style: AppTextStyles.h2),
          error: (_, _) => Text('Live', style: AppTextStyles.h2),
          data: (s) => Text(
            s.title.isEmpty ? 'Live' : s.title,
            style: AppTextStyles.h2,
            overflow: TextOverflow.ellipsis,
          ),
        ),
      ),
      body: detail.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.liveRed),
        ),
        error: (err, _) {
          final access = LiveAccessException.maybeFrom(err);
          return _AccessFallback(
            message: access?.message ?? 'Couldn\'t load this stream.',
          );
        },
        data: (stream) {
          // Kick the LiveKit connect once we observe a live stream.
          if (stream.isLive && _room == null && _accessMessage == null) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (mounted) _connectViewer();
            });
          }
          if (stream.isEnded && stream.hasRecording && _vodController == null) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (mounted) _initVod(stream.recordingUrl!);
            });
          }
          if (_accessMessage != null) {
            return _AccessFallback(message: _accessMessage!);
          }
          if (stream.isScheduled) {
            return _ScheduledPanel(scheduledAt: stream.scheduledAt);
          }
          if (stream.isEnded) {
            if (stream.hasRecording) {
              return _VodView(controller: _vodController);
            }
            return _EndedPanel();
          }
          if (stream.status == LiveStreamStatus.failed) {
            return _AccessFallback(
              message: 'This stream couldn\'t be played.',
            );
          }
          return _LivePlayer(
            streamId: widget.streamId,
            videoTrack: _videoTrack,
            participantCount: _participantCount,
            viewerPeak: stream.viewerPeak,
          );
        },
      ),
    );
  }
}

class _LivePlayer extends StatelessWidget {
  final lk.VideoTrack? videoTrack;
  final int participantCount;
  final int viewerPeak;
  final String streamId;
  const _LivePlayer({
    required this.streamId,
    required this.videoTrack,
    required this.participantCount,
    required this.viewerPeak,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Expanded(
          child: Container(
            color: Colors.black,
            child: Stack(
              children: [
                Positioned.fill(
                  child: videoTrack != null
                      ? lk.VideoTrackRenderer(videoTrack!)
                      : const Center(
                          child: Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              CircularProgressIndicator(
                                  color: AppColors.liveRed),
                              SizedBox(height: 12),
                              Text(
                                'Connecting…',
                                style: TextStyle(color: Colors.white60),
                              ),
                            ],
                          ),
                        ),
                ),
                Positioned(
                  left: 12,
                  top: 12,
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 8, vertical: 3),
                    decoration: BoxDecoration(
                      color: AppColors.liveRed,
                      borderRadius: BorderRadius.circular(20),
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
                ),
                Positioned(
                  right: 12,
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
                          participantCount.toString(),
                          style: AppTextStyles.labelTiny.copyWith(
                            color: Colors.white,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
        SizedBox(
          height: 280,
          child: Padding(
            padding: const EdgeInsets.all(8),
            child: LiveChatPanel(streamId: streamId),
          ),
        ),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          color: AppColors.bgSecondary,
          child: Row(
            children: [
              Text(
                'Peak viewers: $viewerPeak',
                style: AppTextStyles.labelSmall
                    .copyWith(color: AppColors.textTertiary),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _VodView extends StatelessWidget {
  final VideoPlayerController? controller;
  const _VodView({required this.controller});

  @override
  Widget build(BuildContext context) {
    final c = controller;
    if (c == null || !c.value.isInitialized) {
      return const Center(
        child: CircularProgressIndicator(color: AppColors.liveRed),
      );
    }
    return Center(
      child: AspectRatio(
        aspectRatio: c.value.aspectRatio,
        child: Stack(
          children: [
            VideoPlayer(c),
            Positioned.fill(
              child: GestureDetector(
                onTap: () {
                  c.value.isPlaying ? c.pause() : c.play();
                },
                child: Container(color: Colors.transparent),
              ),
            ),
            VideoProgressIndicator(c, allowScrubbing: true),
          ],
        ),
      ),
    );
  }
}

class _ScheduledPanel extends StatelessWidget {
  final DateTime? scheduledAt;
  const _ScheduledPanel({required this.scheduledAt});

  @override
  Widget build(BuildContext context) {
    final when = scheduledAt == null ? 'soon' : scheduledAt!.toLocal().toString();
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.event, size: 48, color: AppColors.textTertiary),
            const SizedBox(height: 12),
            Text(
              'This stream hasn\'t started yet',
              style: AppTextStyles.h3,
            ),
            const SizedBox(height: 6),
            Text(
              'Starts at $when',
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}

class _EndedPanel extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.podcasts,
                size: 48, color: AppColors.textTertiary),
            const SizedBox(height: 12),
            Text('Stream ended', style: AppTextStyles.h3),
            const SizedBox(height: 6),
            Text(
              'A recording will be available soon.',
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}

class _AccessFallback extends StatelessWidget {
  final String message;
  const _AccessFallback({required this.message});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.lock_outline,
                size: 48, color: AppColors.textTertiary),
            const SizedBox(height: 12),
            Text(message,
                style: AppTextStyles.bodyMedium
                    .copyWith(color: AppColors.textPrimary),
                textAlign: TextAlign.center),
          ],
        ),
      ),
    );
  }
}
