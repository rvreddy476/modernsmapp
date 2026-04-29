import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:livekit_client/livekit_client.dart' as lk;

/// Full-screen call overlay rendered above the app.
class CallOverlay extends ConsumerStatefulWidget {
  const CallOverlay({super.key});

  @override
  ConsumerState<CallOverlay> createState() => _CallOverlayState();
}

class _CallOverlayState extends ConsumerState<CallOverlay> {
  bool _isMuted = false;
  bool _isCameraOff = false;
  Timer? _timer;
  int _elapsedSeconds = 0;

  final RTCVideoRenderer _localRenderer = RTCVideoRenderer();
  final RTCVideoRenderer _remoteRenderer = RTCVideoRenderer();

  @override
  void initState() {
    super.initState();
    _localRenderer.initialize();
    _remoteRenderer.initialize();
  }

  @override
  void dispose() {
    _timer?.cancel();
    _localRenderer.dispose();
    _remoteRenderer.dispose();
    super.dispose();
  }

  void _startTimer() {
    _timer?.cancel();
    _elapsedSeconds = 0;
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() => _elapsedSeconds++);
    });
  }

  String _formatDuration(int seconds) {
    final m = (seconds ~/ 60).toString().padLeft(2, '0');
    final s = (seconds % 60).toString().padLeft(2, '0');
    return '$m:$s';
  }

  @override
  Widget build(BuildContext context) {
    final callInfo = ref.watch(callProvider);
    if (callInfo == null) return const SizedBox.shrink();

    // Attach streams to renderers
    if (callInfo.localStream != null) {
      _localRenderer.srcObject = callInfo.localStream;
    }
    if (callInfo.remoteStream != null) {
      _remoteRenderer.srcObject = callInfo.remoteStream;
    }

    // Start timer when active
    if (callInfo.state == CallState.active && _timer == null) {
      _startTimer();
    }

    return Material(
      color: Colors.transparent,
      child: Container(
        color: AppColors.bgPrimary.withValues(alpha: 0.95),
        child: SafeArea(child: _buildContent(callInfo)),
      ),
    );
  }

  Widget _buildContent(CallInfo info) {
    switch (info.state) {
      case CallState.incoming:
        return _buildIncoming(info);
      case CallState.outgoing:
        return _buildOutgoing(info);
      case CallState.connecting:
        return _buildConnecting(info);
      case CallState.active:
        return _buildActive(info);
      case CallState.idle:
      case CallState.failed:
        return const SizedBox.shrink();
    }
  }

  // --- Incoming Call ---
  Widget _buildIncoming(CallInfo info) {
    final isVideo = info.type == CallType.video;
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        const Spacer(flex: 2),
        _buildAvatar(info, pulse: true),
        const SizedBox(height: 24),
        Text(
          info.peerName.isNotEmpty ? info.peerName : 'Unknown',
          style: AppTextStyles.h1.copyWith(color: AppColors.textPrimary),
        ),
        const SizedBox(height: 8),
        Text(
          isVideo ? 'Incoming video call...' : 'Incoming audio call...',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textTertiary,
          ),
        ),
        const Spacer(flex: 3),
        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            _callActionButton(
              icon: Icons.call_end,
              color: Colors.red,
              label: 'Decline',
              onPressed: () => ref.read(callProvider.notifier).declineCall(),
            ),
            const SizedBox(width: 60),
            _callActionButton(
              icon: Icons.call,
              color: Colors.green,
              label: 'Accept',
              onPressed: () => ref.read(callProvider.notifier).acceptCall(),
            ),
          ],
        ),
        const SizedBox(height: 48),
      ],
    );
  }

  // --- Outgoing Call ---
  Widget _buildOutgoing(CallInfo info) {
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        const Spacer(flex: 2),
        _buildAvatar(info),
        const SizedBox(height: 24),
        if (info.joinResponse?.usesStubSfu ?? false) ...[
          _buildSfuWarning(info.joinResponse?.hasTurnRelay ?? false),
          const SizedBox(height: 16),
        ],
        Text(
          info.peerName.isNotEmpty ? info.peerName : 'Unknown',
          style: AppTextStyles.h1.copyWith(color: AppColors.textPrimary),
        ),
        const SizedBox(height: 8),
        Text(
          'Calling...',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textTertiary,
          ),
        ),
        const Spacer(flex: 3),
        _callActionButton(
          icon: Icons.call_end,
          color: Colors.red,
          label: 'Cancel',
          onPressed: () => ref.read(callProvider.notifier).endCall(),
        ),
        const SizedBox(height: 48),
      ],
    );
  }

  // --- Connecting ---
  Widget _buildConnecting(CallInfo info) {
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        const Spacer(flex: 2),
        _buildAvatar(info),
        const SizedBox(height: 24),
        if (info.joinResponse?.usesStubSfu ?? false) ...[
          _buildSfuWarning(info.joinResponse?.hasTurnRelay ?? false),
          const SizedBox(height: 16),
        ],
        Text(
          info.peerName.isNotEmpty ? info.peerName : 'Unknown',
          style: AppTextStyles.h1.copyWith(color: AppColors.textPrimary),
        ),
        const SizedBox(height: 8),
        Text(
          'Connecting...',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textTertiary,
          ),
        ),
        const SizedBox(height: 16),
        const SizedBox(
          width: 24,
          height: 24,
          child: CircularProgressIndicator(
            strokeWidth: 2,
            color: AppColors.postbookPrimary,
          ),
        ),
        const Spacer(flex: 3),
        _callActionButton(
          icon: Icons.call_end,
          color: Colors.red,
          label: 'Cancel',
          onPressed: () => ref.read(callProvider.notifier).endCall(),
        ),
        const SizedBox(height: 48),
      ],
    );
  }

  // --- Active Call ---
  Widget _buildActive(CallInfo info) {
    final isVideo = info.type == CallType.video;

    return Stack(
      children: [
        // Remote video (full screen)
        if (isVideo &&
            (info.remoteVideoTrack != null || info.remoteStream != null))
          Positioned.fill(child: _buildRemoteVideo(info)),

        // Audio call: show centered avatar
        if (!isVideo)
          Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                _buildAvatar(info),
                const SizedBox(height: 24),
                Text(
                  info.peerName.isNotEmpty ? info.peerName : 'Unknown',
                  style: AppTextStyles.h1.copyWith(
                    color: AppColors.textPrimary,
                  ),
                ),
                const SizedBox(height: 8),
                Text(
                  _formatDuration(_elapsedSeconds),
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textTertiary,
                  ),
                ),
              ],
            ),
          ),

        // Video call: timer + name overlay at top
        if (isVideo && (info.joinResponse?.usesStubSfu ?? false))
          Positioned(
            top: 64,
            left: 16,
            right: 16,
            child: _buildSfuWarning(info.joinResponse?.hasTurnRelay ?? false),
          ),

        if (isVideo)
          Positioned(
            top: 16,
            left: 0,
            right: 0,
            child: Column(
              children: [
                Text(
                  info.peerName.isNotEmpty ? info.peerName : 'Unknown',
                  style: AppTextStyles.h2.copyWith(
                    color: Colors.white,
                    shadows: [
                      const Shadow(blurRadius: 8, color: Colors.black54),
                    ],
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  _formatDuration(_elapsedSeconds),
                  style: AppTextStyles.bodySmall.copyWith(
                    color: Colors.white70,
                    shadows: [
                      const Shadow(blurRadius: 8, color: Colors.black54),
                    ],
                  ),
                ),
              ],
            ),
          ),

        // Local video PiP (top right)
        if (isVideo &&
            (info.localVideoTrack != null || info.localStream != null))
          Positioned(
            top: 80,
            right: 16,
            child: ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: SizedBox(
                width: 100,
                height: 140,
                child: _buildLocalVideo(info),
              ),
            ),
          ),

        // Controls at bottom
        Positioned(
          bottom: 48,
          left: 0,
          right: 0,
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              _controlButton(
                icon: _isMuted ? Icons.mic_off : Icons.mic,
                label: _isMuted ? 'Unmute' : 'Mute',
                isActive: _isMuted,
                onPressed: () {
                  final muted = ref.read(callProvider.notifier).toggleMute();
                  setState(() => _isMuted = muted);
                },
              ),
              const SizedBox(width: 24),
              _callActionButton(
                icon: Icons.call_end,
                color: Colors.red,
                label: 'End',
                onPressed: () {
                  _timer?.cancel();
                  _timer = null;
                  ref.read(callProvider.notifier).endCall();
                },
              ),
              const SizedBox(width: 24),
              if (isVideo)
                _controlButton(
                  icon: _isCameraOff ? Icons.videocam_off : Icons.videocam,
                  label: _isCameraOff ? 'Camera On' : 'Camera Off',
                  isActive: _isCameraOff,
                  onPressed: () {
                    final off = ref.read(callProvider.notifier).toggleCamera();
                    setState(() => _isCameraOff = off);
                  },
                ),
            ],
          ),
        ),
      ],
    );
  }

  // --- Shared Widgets ---

  Widget _buildAvatar(CallInfo info, {bool pulse = false}) {
    Widget avatar = Container(
      width: 120,
      height: 120,
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        gradient: AppColors.postbookGradient,
      ),
      child: Center(
        child: Text(
          _initials(info.peerName),
          style: AppTextStyles.h1.copyWith(color: Colors.white, fontSize: 40),
        ),
      ),
    );

    if (pulse) {
      avatar = _PulsingAvatar(child: avatar);
    }
    return avatar;
  }

  Widget _buildRemoteVideo(CallInfo info) {
    if (info.remoteVideoTrack != null) {
      return lk.VideoTrackRenderer(
        info.remoteVideoTrack!,
        fit: RTCVideoViewObjectFit.RTCVideoViewObjectFitCover,
      );
    }

    return RTCVideoView(
      _remoteRenderer,
      objectFit: RTCVideoViewObjectFit.RTCVideoViewObjectFitCover,
    );
  }

  Widget _buildLocalVideo(CallInfo info) {
    if (info.localVideoTrack != null) {
      return lk.VideoTrackRenderer(
        info.localVideoTrack!,
        fit: RTCVideoViewObjectFit.RTCVideoViewObjectFitCover,
        mirrorMode: lk.VideoViewMirrorMode.mirror,
      );
    }

    return RTCVideoView(
      _localRenderer,
      mirror: true,
      objectFit: RTCVideoViewObjectFit.RTCVideoViewObjectFitCover,
    );
  }

  Widget _callActionButton({
    required IconData icon,
    required Color color,
    required String label,
    required VoidCallback onPressed,
  }) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        GestureDetector(
          onTap: onPressed,
          child: Container(
            width: 64,
            height: 64,
            decoration: BoxDecoration(shape: BoxShape.circle, color: color),
            child: Icon(icon, color: Colors.white, size: 32),
          ),
        ),
        const SizedBox(height: 8),
        Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: AppColors.textTertiary,
          ),
        ),
      ],
    );
  }

  Widget _controlButton({
    required IconData icon,
    required String label,
    required bool isActive,
    required VoidCallback onPressed,
  }) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        GestureDetector(
          onTap: onPressed,
          child: Container(
            width: 56,
            height: 56,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: isActive
                  ? AppColors.textPrimary.withValues(alpha: 0.3)
                  : AppColors.bgCard,
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Icon(icon, color: AppColors.textPrimary, size: 24),
          ),
        ),
        const SizedBox(height: 8),
        Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: AppColors.textTertiary,
          ),
        ),
      ],
    );
  }

  Widget _buildSfuWarning(bool hasTurnRelay) {
    final message = hasTurnRelay
        ? 'Fallback WebRTC media path is active. TURN relay is configured for direct calls, but scalable group calling still needs a real SFU such as LiveKit.'
        : 'Fallback WebRTC media path is active. Configure TURN for reliable NAT traversal and LiveKit for scalable group calling.';
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 24),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: Colors.amber.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.amber.withValues(alpha: 0.35)),
      ),
      child: Text(
        message,
        textAlign: TextAlign.center,
        style: AppTextStyles.bodySmall.copyWith(color: Colors.amber),
      ),
    );
  }

  String _initials(String name) {
    if (name.isEmpty) return '?';
    final parts = name.split(' ');
    if (parts.length >= 2) {
      return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
    }
    return name[0].toUpperCase();
  }
}

/// Animated pulsing ring around the avatar for incoming calls.
class _PulsingAvatar extends StatefulWidget {
  final Widget child;
  const _PulsingAvatar({required this.child});

  @override
  State<_PulsingAvatar> createState() => _PulsingAvatarState();
}

class _PulsingAvatarState extends State<_PulsingAvatar>
    with SingleTickerProviderStateMixin {
  late AnimationController _controller;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1500),
    )..repeat();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _controller,
      builder: (context, child) {
        final scale = 1.0 + (_controller.value * 0.15);
        final opacity = 1.0 - _controller.value;
        return Stack(
          alignment: Alignment.center,
          children: [
            Transform.scale(
              scale: scale,
              child: Container(
                width: 130,
                height: 130,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  border: Border.all(
                    color: AppColors.postbookPrimary.withValues(
                      alpha: opacity * 0.5,
                    ),
                    width: 3,
                  ),
                ),
              ),
            ),
            child!,
          ],
        );
      },
      child: widget.child,
    );
  }
}
