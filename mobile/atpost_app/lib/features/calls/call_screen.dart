import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:go_router/go_router.dart';

class CallScreen extends ConsumerStatefulWidget {
  const CallScreen({super.key});

  @override
  ConsumerState<CallScreen> createState() => _CallScreenState();
}

class _CallScreenState extends ConsumerState<CallScreen> {
  final RTCVideoRenderer _localRenderer = RTCVideoRenderer();
  final RTCVideoRenderer _remoteRenderer = RTCVideoRenderer();

  @override
  void initState() {
    super.initState();
    _initRenderers();
  }

  Future<void> _initRenderers() async {
    await _localRenderer.initialize();
    await _remoteRenderer.initialize();
  }

  @override
  void dispose() {
    _localRenderer.dispose();
    _remoteRenderer.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final callInfo = ref.watch(callProvider);

    // If call ended or idle, auto-close the screen
    if (callInfo == null || callInfo.state == CallState.idle) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (context.mounted && GoRouter.of(context).canPop()) {
          context.pop();
        }
      });
      return const Scaffold(backgroundColor: Colors.black);
    }

    _updateStreams(callInfo);

    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        children: [
          // 1. Remote Video (Full Screen)
          if (callInfo.type == CallType.video && callInfo.remoteStream != null)
            Positioned.fill(child: RTCVideoView(_remoteRenderer, objectFit: RTCVideoViewObjectFit.RTCVideoViewObjectFitCover))
          else
            _buildAudioCallBackground(callInfo),

          // 2. Local Video (Picture-in-Picture)
          if (callInfo.type == CallType.video && callInfo.localStream != null)
            Positioned(
              right: 20,
              top: 60,
              width: 120,
              height: 180,
              child: ClipRRect(
                borderRadius: BorderRadius.circular(16),
                child: Container(
                  color: Colors.black54,
                  child: RTCVideoView(_localRenderer, mirror: true, objectFit: RTCVideoViewObjectFit.RTCVideoViewObjectFitCover),
                ),
              ),
            ),

          // 3. Status Overlays
          _buildStatusOverlay(callInfo),
          if (callInfo.joinResponse?.usesStubSfu ?? false)
            const Positioned(
              top: 112,
              left: 20,
              right: 20,
              child: _CallWarningBanner(),
            ),

          // 4. Call Controls
          _buildCallControls(callInfo),
        ],
      ),
    );
  }

  void _updateStreams(CallInfo info) {
    if (_localRenderer.srcObject != info.localStream) {
      _localRenderer.srcObject = info.localStream;
    }
    if (_remoteRenderer.srcObject != info.remoteStream) {
      _remoteRenderer.srcObject = info.remoteStream;
    }
  }

  Widget _buildAudioCallBackground(CallInfo info) {
    return Container(
      width: double.infinity,
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [Color(0xFF1A1A2E), Color(0xFF16213E)],
        ),
      ),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          CircleAvatar(
            radius: 60,
            backgroundImage: info.peerAvatar.isNotEmpty ? NetworkImage(info.peerAvatar) : null,
            child: info.peerAvatar.isEmpty ? Text(info.peerName.isNotEmpty ? info.peerName[0] : '?', style: const TextStyle(fontSize: 40)) : null,
          ),
          const SizedBox(height: 24),
          Text(info.peerName, style: AppTextStyles.h1.copyWith(color: Colors.white)),
          const SizedBox(height: 8),
          Text(
            info.state == CallState.outgoing ? 'Calling...' :
            info.state == CallState.incoming ? 'Incoming Call' :
            info.state == CallState.active ? 'On Call' : 'Connecting...',
            style: AppTextStyles.bodyMedium.copyWith(color: Colors.white70),
          ),
        ],
      ),
    );
  }

  Widget _buildStatusOverlay(CallInfo info) {
    return Positioned(
      top: 60,
      left: 20,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (info.state == CallState.active && info.startedAt != null)
            _CallTimer(startTime: info.startedAt!),
        ],
      ),
    );
  }

  Widget _buildCallControls(CallInfo info) {
    final notifier = ref.read(callProvider.notifier);

    return Positioned(
      bottom: 50,
      left: 0,
      right: 0,
      child: Column(
        children: [
          if (info.state == CallState.incoming)
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceEvenly,
              children: [
                _ControlButton(icon: Icons.close, color: Colors.red, label: 'Decline', onTap: notifier.declineCall),
                _ControlButton(icon: Icons.check, color: Colors.green, label: 'Accept', onTap: notifier.acceptCall),
              ],
            )
          else
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceEvenly,
              children: [
                _ControlButton(icon: Icons.mic_off, color: Colors.white24, label: 'Mute', onTap: notifier.toggleMute),
                _ControlButton(icon: Icons.call_end, color: Colors.red, label: 'End', onTap: notifier.endCall),
                if (info.type == CallType.video)
                  _ControlButton(icon: Icons.videocam_off, color: Colors.white24, label: 'Video', onTap: notifier.toggleCamera),
              ],
            ),
        ],
      ),
    );
  }
}

class _ControlButton extends StatelessWidget {
  final IconData icon;
  final Color color;
  final String label;
  final VoidCallback onTap;

  const _ControlButton({required this.icon, required this.color, required this.label, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        GestureDetector(
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(color: color, shape: BoxShape.circle),
            child: Icon(icon, color: Colors.white, size: 28),
          ),
        ),
        const SizedBox(height: 8),
        Text(label, style: const TextStyle(color: Colors.white70, fontSize: 12)),
      ],
    );
  }
}

class _CallWarningBanner extends StatelessWidget {
  const _CallWarningBanner();

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Colors.amber.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.amber.withValues(alpha: 0.42)),
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          children: [
            const Icon(
              Icons.warning_amber_rounded,
              color: Colors.amber,
              size: 18,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                'Calls are using the development SFU. Media relay is limited until LiveKit is configured.',
                style: AppTextStyles.bodySmall.copyWith(color: Colors.amber),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _CallTimer extends StatefulWidget {
  final DateTime startTime;
  const _CallTimer({required this.startTime});

  @override
  State<_CallTimer> createState() => _CallTimerState();
}

class _CallTimerState extends State<_CallTimer> {
  late Duration _duration;
  late final Stream<Duration> _timerStream;

  @override
  void initState() {
    super.initState();
    _duration = DateTime.now().difference(widget.startTime);
    _timerStream = Stream.periodic(const Duration(seconds: 1), (_) => DateTime.now().difference(widget.startTime));
  }

  @override
  Widget build(BuildContext context) {
    return StreamBuilder<Duration>(
      stream: _timerStream,
      initialData: _duration,
      builder: (context, snapshot) {
        final d = snapshot.data ?? _duration;
        String twoDigits(int n) => n.toString().padLeft(2, '0');
        final minutes = twoDigits(d.inMinutes.remainder(60));
        final seconds = twoDigits(d.inSeconds.remainder(60));
        return Text('$minutes:$seconds', style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 18));
      },
    );
  }
}
