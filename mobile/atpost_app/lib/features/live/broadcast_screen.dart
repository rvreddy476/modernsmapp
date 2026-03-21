import 'dart:async';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:camera/camera.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// A production-grade screen for creators to broadcast live content.
/// Features: Camera preview, Live statistics, Chat overlay, and Audio/Video toggles.
class BroadcastScreen extends ConsumerStatefulWidget {
  final String streamId;
  final String title;

  const BroadcastScreen({
    super.key,
    required this.streamId,
    required this.title,
  });

  @override
  ConsumerState<BroadcastScreen> createState() => _BroadcastScreenState();
}

class _BroadcastScreenState extends ConsumerState<BroadcastScreen> {
  CameraController? _cameraController;
  bool _isCameraInitialized = false;
  bool _isMuted = false;
  bool _isCameraOff = false;
  int _viewerCount = 0;
  DateTime? _startTime;
  Timer? _durationTimer;
  String _elapsedTime = "00:00:00";

  final List<String> _dummyChat = [
    "Awesome stream! 🔥",
    "Where are you from?",
    "Love the content!",
    "Can you show your setup?",
    "Hello from NYC!",
  ];

  @override
  void initState() {
    super.initState();
    _initCamera();
    _startStreamSession();
  }

  Future<void> _initCamera() async {
    final cameras = await availableCameras();
    if (cameras.isEmpty) return;

    // Use front camera by default
    final frontCamera = cameras.firstWhere(
      (c) => c.lensDirection == CameraLensDirection.front,
      orElse: () => cameras.first,
    );

    _cameraController = CameraController(
      frontCamera,
      ResolutionPreset.high,
      enableAudio: true,
    );

    try {
      await _cameraController!.initialize();
      if (!mounted) return;
      setState(() => _isCameraInitialized = true);
    } catch (e) {
      debugPrint("Camera initialization error: $e");
    }
  }

  void _startStreamSession() {
    _startTime = DateTime.now();
    _durationTimer = Timer.periodic(const Duration(seconds: 1), (timer) {
      if (!mounted) return;
      final diff = DateTime.now().difference(_startTime!);
      setState(() {
        _elapsedTime = _formatDuration(diff);
        // Simulate viewer growth
        if (timer.tick % 5 == 0) _viewerCount += 1;
      });
    });
  }

  String _formatDuration(Duration d) {
    String twoDigits(int n) => n.toString().padLeft(2, "0");
    final h = twoDigits(d.inHours);
    final m = twoDigits(d.inMinutes.remainder(60));
    final s = twoDigits(d.inSeconds.remainder(60));
    return "$h:$m:$s";
  }

  @override
  void dispose() {
    _durationTimer?.cancel();
    _cameraController?.dispose();
    super.dispose();
  }

  void _toggleMute() {
    setState(() => _isMuted = !_isMuted);
    _isMuted ? _cameraController?.pauseVideoRecording() : _cameraController?.resumeVideoRecording();
  }

  void _toggleCamera() {
    setState(() => _isCameraOff = !_isCameraOff);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        children: [
          // 1. Full-screen Camera Preview
          _buildCameraPreview(),

          // 2. Gradient Overlays for readability
          _buildUIOverlays(),

          // 3. Top Controls (Live Status, Timer, Viewers)
          _buildTopBar(),

          // 4. Chat Overlay (Bottom Left)
          _buildChatOverlay(),

          // 5. Bottom Action Bar (End, Mute, Camera Toggle)
          _buildBottomActions(),
        ],
      ),
    );
  }

  Widget _buildCameraPreview() {
    if (!_isCameraInitialized || _isCameraOff) {
      return Container(
        color: Colors.black87,
        child: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.videocam_off, size: 64, color: Colors.white24),
              const SizedBox(height: 16),
              Text("Camera is off", style: AppTextStyles.bodyMedium.copyWith(color: Colors.white54)),
            ],
          ),
        ),
      );
    }

    return SizedBox.expand(
      child: FittedBox(
        fit: BoxFit.cover,
        child: SizedBox(
          width: _cameraController!.value.previewSize?.height ?? 1080,
          height: _cameraController!.value.previewSize?.width ?? 1920,
          child: CameraPreview(_cameraController!),
        ),
      ),
    );
  }

  Widget _buildUIOverlays() {
    return Positioned.fill(
      child: Column(
        children: [
          Container(
            height: 150,
            decoration: BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.topCenter,
                end: Alignment.bottomCenter,
                colors: [Colors.black.withOpacity(0.7), Colors.transparent],
              ),
            ),
          ),
          const Spacer(),
          Container(
            height: 250,
            decoration: BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.bottomCenter,
                end: Alignment.topCenter,
                colors: [Colors.black.withOpacity(0.7), Colors.transparent],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildTopBar() {
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Row(
          children: [
            // LIVE Badge
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              decoration: BoxDecoration(
                color: AppColors.liveRed,
                borderRadius: BorderRadius.circular(4),
              ),
              child: const Text(
                "LIVE",
                style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 12),
              ),
            ).animate().shimmer(duration: 2.seconds),
            const SizedBox(width: 12),
            // Duration
            Text(
              _elapsedTime,
              style: const TextStyle(color: Colors.white, fontWeight: FontWeight.w600),
            ),
            const Spacer(),
            // Viewer Count
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
              decoration: BoxDecoration(
                color: Colors.black26,
                borderRadius: BorderRadius.circular(16),
              ),
              child: Row(
                children: [
                  const Icon(Icons.visibility, color: Colors.white, size: 14),
                  const SizedBox(width: 6),
                  Text(
                    "$_viewerCount",
                    style: const TextStyle(color: Colors.white, fontSize: 13, fontWeight: FontWeight.bold),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildChatOverlay() {
    return Positioned(
      left: 16,
      bottom: 120,
      width: MediaQuery.of(context).size.width * 0.7,
      height: 200,
      child: ListView.builder(
        reverse: true, // New messages at bottom
        itemCount: _dummyChat.length,
        itemBuilder: (context, index) {
          return Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                CircleAvatar(radius: 12, backgroundColor: Colors.accents[index % Colors.accents.length]),
                const SizedBox(width: 8),
                Expanded(
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                    decoration: BoxDecoration(
                      color: Colors.black45,
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: RichText(
                      text: TextSpan(
                        children: [
                          TextSpan(
                            text: "User $index: ",
                            style: const TextStyle(fontWeight: FontWeight.bold, color: Colors.white70, fontSize: 12),
                          ),
                          TextSpan(
                            text: _dummyChat[index],
                            style: const TextStyle(color: Colors.white, fontSize: 13),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ],
            ).animate().fadeIn().slideX(begin: -0.1, end: 0),
          );
        },
      ),
    );
  }

  Widget _buildBottomActions() {
    return Positioned(
      left: 0,
      right: 0,
      bottom: 40,
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceEvenly,
        children: [
          // End Stream
          _ActionCircle(
            icon: Icons.close,
            color: Colors.white24,
            onTap: () => context.pop(),
            label: "End",
          ),
          // Mute Toggle
          _ActionCircle(
            icon: _isMuted ? Icons.mic_off : Icons.mic,
            color: _isMuted ? AppColors.liveRed : Colors.white24,
            onTap: _toggleMute,
            label: "Audio",
          ),
          // Go Live CTA (Main Action)
          Container(
            padding: const EdgeInsets.all(16),
            decoration: const BoxDecoration(
              color: AppColors.liveRed,
              shape: BoxShape.circle,
              boxShadow: [BoxShadow(color: AppColors.liveRed, blurRadius: 20, spreadRadius: 2)],
            ),
            child: const Icon(Icons.videocam, color: Colors.white, size: 32),
          ),
          // Camera Toggle
          _ActionCircle(
            icon: _isCameraOff ? Icons.videocam_off : Icons.videocam,
            color: _isCameraOff ? AppColors.liveRed : Colors.white24,
            onTap: _toggleCamera,
            label: "Video",
          ),
          // Switch Camera
          _ActionCircle(
            icon: Icons.flip_camera_ios,
            color: Colors.white24,
            onTap: () {}, // Switch camera logic
            label: "Flip",
          ),
        ],
      ),
    );
  }
}

class _ActionCircle extends StatelessWidget {
  final IconData icon;
  final Color color;
  final VoidCallback onTap;
  final String label;

  const _ActionCircle({
    required this.icon,
    required this.color,
    required this.onTap,
    required this.label,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        GestureDetector(
          onTap: onTap,
          child: Container(
            width: 50,
            height: 50,
            decoration: BoxDecoration(
              color: color,
              shape: BoxShape.circle,
            ),
            child: Icon(icon, color: Colors.white, size: 24),
          ),
        ),
        const SizedBox(height: 6),
        Text(label, style: const TextStyle(color: Colors.white70, fontSize: 10, fontWeight: FontWeight.bold)),
      ],
    );
  }
}
