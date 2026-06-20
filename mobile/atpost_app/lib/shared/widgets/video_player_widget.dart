import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:video_player/video_player.dart';
import 'package:chewie/chewie.dart';

/// Reusable video player widget that wraps video_player + chewie.
///
/// Modes:
///   - Reels: autoPlay=true, looping=true, showControls=false (tap to pause)
///   - PostTube: autoPlay=true, showControls=true
///   - Live: autoPlay=true, showControls=false, looping=false
class VideoPlayerWidget extends StatefulWidget {
  const VideoPlayerWidget({
    super.key,
    required this.videoUrl,
    this.autoPlay = true,
    this.looping = false,
    this.showControls = true,
    this.muted = false,
    this.aspectRatio,
    this.placeholder,
    this.onTogglePlay,
    this.onPositionUpdate,
  });

  final String videoUrl;
  final bool autoPlay;
  final bool looping;
  final bool showControls;
  final bool muted;
  final double? aspectRatio;

  /// Optional widget to show behind the video (e.g. gradient background).
  final Widget? placeholder;

  /// Called when the user taps to toggle play/pause (for reel-style tap).
  final VoidCallback? onTogglePlay;

  /// Absolute playhead in milliseconds, emitted on the controller's
  /// value listener. Feeds ProductTagOverlay so it knows which tags
  /// are currently in-window. Throttled to ~10Hz in the listener to
  /// avoid setState pressure inside whoever consumes it.
  final void Function(int positionMs)? onPositionUpdate;

  @override
  State<VideoPlayerWidget> createState() => VideoPlayerWidgetState();
}

class VideoPlayerWidgetState extends State<VideoPlayerWidget> {
  VideoPlayerController? _videoController;
  ChewieController? _chewieController;

  bool _initialized = false;
  bool _hasError = false;
  bool _showPauseIcon = false;

  @override
  void initState() {
    super.initState();
    _initializePlayer();
  }

  @override
  void didUpdateWidget(VideoPlayerWidget oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.videoUrl != widget.videoUrl) {
      _disposeControllers();
      _initializePlayer();
    } else if (oldWidget.muted != widget.muted) {
      _videoController?.setVolume(widget.muted ? 0 : 1.0);
    }
  }

  Future<void> _initializePlayer() async {
    if (widget.videoUrl.isEmpty) {
      setState(() => _hasError = true);
      return;
    }

    setState(() {
      _initialized = false;
      _hasError = false;
    });

    try {
      final controller = VideoPlayerController.networkUrl(
        Uri.parse(widget.videoUrl),
      );
      _videoController = controller;

      await controller.initialize();
      if (!mounted) return;

      controller.setLooping(widget.looping);
      controller.setVolume(widget.muted ? 0 : 1.0);

      // Throttled position emitter — fires onPositionUpdate at most
      // every 100ms (10 Hz). Fine-grained enough that a 1-second tag
      // window is hit ~10 times; cheap enough that the consumer's
      // setState doesn't thrash on every frame.
      if (widget.onPositionUpdate != null) {
        var lastEmittedMs = -1000;
        controller.addListener(() {
          final pos = controller.value.position.inMilliseconds;
          if ((pos - lastEmittedMs).abs() >= 100) {
            lastEmittedMs = pos;
            widget.onPositionUpdate?.call(pos);
          }
        });
      }

      _chewieController = ChewieController(
        videoPlayerController: controller,
        autoPlay: widget.autoPlay,
        looping: widget.looping,
        showControls: widget.showControls,
        aspectRatio: widget.aspectRatio ?? controller.value.aspectRatio,
        allowFullScreen: widget.showControls,
        allowMuting: widget.showControls,
        errorBuilder: (context, errorMessage) {
          return _ErrorOverlay(message: errorMessage);
        },
        materialProgressColors: ChewieProgressColors(
          playedColor: AppColors.posttubePrimary,
          handleColor: AppColors.posttubePrimary,
          bufferedColor: Colors.white.withValues(alpha: 0.3),
          backgroundColor: Colors.white.withValues(alpha: 0.1),
        ),
      );

      setState(() => _initialized = true);
    } catch (_) {
      if (!mounted) return;
      setState(() => _hasError = true);
    }
  }

  void _disposeControllers() {
    _chewieController?.dispose();
    _chewieController = null;
    _videoController?.dispose();
    _videoController = null;
    _initialized = false;
  }

  /// Pause the video (useful when the reel goes off-screen).
  void pause() {
    _videoController?.pause();
  }

  /// Resume the video.
  void play() {
    _videoController?.play();
  }

  /// Toggle play/pause and show a brief icon overlay for reel-style UX.
  void togglePlayPause() {
    final controller = _videoController;
    if (controller == null || !controller.value.isInitialized) return;

    if (controller.value.isPlaying) {
      controller.pause();
    } else {
      controller.play();
    }

    setState(() => _showPauseIcon = true);
    Future.delayed(const Duration(milliseconds: 600), () {
      if (mounted) setState(() => _showPauseIcon = false);
    });

    widget.onTogglePlay?.call();
  }

  bool get isPlaying => _videoController?.value.isPlaying ?? false;

  @override
  void dispose() {
    _disposeControllers();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (_hasError) {
      return widget.placeholder ?? const _ErrorOverlay();
    }

    if (!_initialized) {
      return widget.placeholder ??
          const Center(
            child: CircularProgressIndicator(
              color: AppColors.posttubePrimary,
              strokeWidth: 2,
            ),
          );
    }

    final chewie = _chewieController;
    if (chewie == null) {
      return widget.placeholder ?? const _ErrorOverlay();
    }

    // For reel/live modes (no chewie controls), we render the raw video
    // and handle tap-to-pause ourselves.
    if (!widget.showControls) {
      return GestureDetector(
        onTap: togglePlayPause,
        child: Stack(
          alignment: Alignment.center,
          children: [
            SizedBox.expand(
              child: FittedBox(
                fit: BoxFit.cover,
                clipBehavior: Clip.hardEdge,
                child: SizedBox(
                  width: _videoController!.value.size.width,
                  height: _videoController!.value.size.height,
                  child: VideoPlayer(_videoController!),
                ),
              ),
            ),
            if (_showPauseIcon)
              AnimatedOpacity(
                opacity: _showPauseIcon ? 1.0 : 0.0,
                duration: const Duration(milliseconds: 200),
                child: Container(
                  width: 64,
                  height: 64,
                  decoration: BoxDecoration(
                    color: Colors.black.withValues(alpha: 0.5),
                    shape: BoxShape.circle,
                  ),
                  child: Icon(
                    _videoController!.value.isPlaying
                        ? Icons.play_arrow_rounded
                        : Icons.pause_rounded,
                    color: Colors.white,
                    size: 36,
                  ),
                ),
              ),
          ],
        ),
      );
    }

    return Chewie(controller: chewie);
  }
}

class _ErrorOverlay extends StatelessWidget {
  const _ErrorOverlay({this.message});

  final String? message;

  @override
  Widget build(BuildContext context) {
    return Container(
      color: AppColors.bgTertiary,
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(
              Icons.error_outline_rounded,
              size: 40,
              color: AppColors.textMuted,
            ),
            const SizedBox(height: 8),
            Text(
              message ?? 'Video unavailable',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textMuted,
              ),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}
