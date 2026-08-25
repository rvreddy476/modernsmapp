import 'dart:async';
import 'dart:io';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:path_provider/path_provider.dart';
import 'package:record/record.dart';

/// Module 1 P0-6 — voice post recorder.
///
/// Covers every state Codex called out: permission denied, interruption
/// (call/route change), cancel, retry, and returning from background.
/// The 180 s ceiling is mirrored here for a good UX, but the SERVER is
/// authoritative — media-service re-measures with ffprobe at confirm and
/// rejects anything longer, so a patched client cannot bypass it.
const int kMaxVoiceSeconds = 180;

enum _RecorderPhase { idle, recording, paused, finished, denied, error }

/// Returns the recorded file path, or null if cancelled/dismissed.
Future<String?> showVoiceRecorderSheet(BuildContext context) {
  return showModalBottomSheet<String>(
    context: context,
    isScrollControlled: true,
    isDismissible: false,
    enableDrag: false,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
    ),
    builder: (_) => const _VoiceRecorderSheet(),
  );
}

class _VoiceRecorderSheet extends StatefulWidget {
  const _VoiceRecorderSheet();

  @override
  State<_VoiceRecorderSheet> createState() => _VoiceRecorderSheetState();
}

class _VoiceRecorderSheetState extends State<_VoiceRecorderSheet>
    with WidgetsBindingObserver {
  final AudioRecorder _recorder = AudioRecorder();
  _RecorderPhase _phase = _RecorderPhase.idle;
  Timer? _ticker;
  int _elapsed = 0;
  String? _path;
  String? _error;
  // Rolling amplitude samples drive the live waveform.
  final List<double> _levels = [];
  StreamSubscription<Amplitude>? _amplitudeSub;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _ticker?.cancel();
    _amplitudeSub?.cancel();
    _recorder.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Backgrounding mid-recording: pause rather than silently continuing
    // (the OS may cut the mic anyway) so the user returns to an explicit,
    // resumable state instead of a corrupt clip.
    if (state == AppLifecycleState.paused &&
        _phase == _RecorderPhase.recording) {
      _pause();
    }
  }

  Future<void> _start() async {
    setState(() => _error = null);
    try {
      if (!await _recorder.hasPermission()) {
        setState(() => _phase = _RecorderPhase.denied);
        return;
      }
      final dir = await getTemporaryDirectory();
      final path =
          '${dir.path}/voice_${DateTime.now().millisecondsSinceEpoch}.m4a';
      await _recorder.start(
        const RecordConfig(encoder: AudioEncoder.aacLc, bitRate: 96000),
        path: path,
      );
      _amplitudeSub = _recorder
          .onAmplitudeChanged(const Duration(milliseconds: 200))
          .listen((amp) {
        if (!mounted) return;
        // dBFS (negative) → 0..1 for the bars.
        final normalized = ((amp.current + 45) / 45).clamp(0.05, 1.0);
        setState(() {
          _levels.add(normalized.toDouble());
          if (_levels.length > 60) _levels.removeAt(0);
        });
      });
      _startTicker();
      setState(() {
        _path = path;
        _phase = _RecorderPhase.recording;
        _elapsed = 0;
        _levels.clear();
      });
    } catch (e) {
      setState(() {
        _phase = _RecorderPhase.error;
        _error = 'Could not start recording. $e';
      });
    }
  }

  void _startTicker() {
    _ticker?.cancel();
    _ticker = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!mounted) return;
      setState(() => _elapsed++);
      if (_elapsed >= kMaxVoiceSeconds) _stop();
    });
  }

  Future<void> _pause() async {
    try {
      await _recorder.pause();
      _ticker?.cancel();
      if (mounted) setState(() => _phase = _RecorderPhase.paused);
    } catch (_) {
      // An interruption may have already torn the session down; treat it
      // as a finished recording so the user keeps what was captured.
      if (mounted) setState(() => _phase = _RecorderPhase.finished);
    }
  }

  Future<void> _resume() async {
    try {
      await _recorder.resume();
      _startTicker();
      if (mounted) setState(() => _phase = _RecorderPhase.recording);
    } catch (e) {
      if (mounted) {
        setState(() {
          _phase = _RecorderPhase.error;
          _error = 'Could not resume. Your recording so far was kept.';
        });
      }
    }
  }

  Future<void> _stop() async {
    _ticker?.cancel();
    await _amplitudeSub?.cancel();
    try {
      final path = await _recorder.stop();
      if (!mounted) return;
      setState(() {
        _path = path ?? _path;
        _phase = _RecorderPhase.finished;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _phase = _RecorderPhase.error;
        _error = 'Recording failed. Please try again.';
      });
    }
  }

  /// Cancel discards the capture and deletes the temp file — a cancelled
  /// recording must never linger on disk or reach the server.
  Future<void> _cancel() async {
    _ticker?.cancel();
    await _amplitudeSub?.cancel();
    try {
      if (await _recorder.isRecording()) await _recorder.stop();
    } catch (_) {/* already stopped */}
    final path = _path;
    if (path != null) {
      try {
        final file = File(path);
        if (file.existsSync()) await file.delete();
      } catch (_) {/* best effort */}
    }
    if (mounted) Navigator.of(context).pop();
  }

  Future<void> _retry() async {
    final path = _path;
    if (path != null) {
      try {
        final file = File(path);
        if (file.existsSync()) await file.delete();
      } catch (_) {}
    }
    setState(() {
      _phase = _RecorderPhase.idle;
      _elapsed = 0;
      _path = null;
      _error = null;
      _levels.clear();
    });
  }

  String get _timeLabel {
    final m = (_elapsed ~/ 60).toString().padLeft(2, '0');
    final s = (_elapsed % 60).toString().padLeft(2, '0');
    return '$m:$s';
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 16,
        right: 16,
        top: 16,
        bottom: MediaQuery.of(context).viewInsets.bottom + 16,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text('Voice post', style: AppTextStyles.h3),
          const SizedBox(height: 4),
          Text(
            'Up to ${kMaxVoiceSeconds ~/ 60} minutes.',
            style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
          ),
          const SizedBox(height: 20),
          if (_phase == _RecorderPhase.denied)
            _message(
              icon: Icons.mic_off,
              title: 'Microphone access needed',
              body: 'Enable microphone permission in Settings to record a '
                  'voice post.',
            )
          else if (_phase == _RecorderPhase.error)
            _message(
              icon: Icons.error_outline,
              title: 'Something went wrong',
              body: _error ?? 'Please try again.',
            )
          else ...[
            Semantics(
              liveRegion: true,
              label: 'Recording time $_timeLabel',
              child: Center(
                child: Text(_timeLabel, style: AppTextStyles.h1),
              ),
            ),
            const SizedBox(height: 12),
            SizedBox(height: 48, child: _Waveform(levels: _levels)),
          ],
          const SizedBox(height: 20),
          _controls(),
          const SizedBox(height: 8),
          TextButton(onPressed: _cancel, child: const Text('Cancel')),
        ],
      ),
    );
  }

  Widget _message({
    required IconData icon,
    required String title,
    required String body,
  }) {
    return Column(
      children: [
        Icon(icon, size: 36, color: AppColors.textDim),
        const SizedBox(height: 8),
        Text(title, style: AppTextStyles.bodyMedium),
        const SizedBox(height: 4),
        Text(
          body,
          textAlign: TextAlign.center,
          style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
        ),
      ],
    );
  }

  Widget _controls() {
    switch (_phase) {
      case _RecorderPhase.idle:
        return FilledButton.icon(
          onPressed: _start,
          icon: const Icon(Icons.mic),
          label: const Text('Start recording'),
        );
      case _RecorderPhase.recording:
        return Row(
          children: [
            Expanded(
              child: OutlinedButton.icon(
                onPressed: _pause,
                icon: const Icon(Icons.pause),
                label: const Text('Pause'),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: FilledButton.icon(
                onPressed: _stop,
                icon: const Icon(Icons.stop),
                label: const Text('Done'),
              ),
            ),
          ],
        );
      case _RecorderPhase.paused:
        return Row(
          children: [
            Expanded(
              child: OutlinedButton.icon(
                onPressed: _resume,
                icon: const Icon(Icons.mic),
                label: const Text('Resume'),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: FilledButton.icon(
                onPressed: _stop,
                icon: const Icon(Icons.stop),
                label: const Text('Done'),
              ),
            ),
          ],
        );
      case _RecorderPhase.finished:
        return Row(
          children: [
            Expanded(
              child: OutlinedButton.icon(
                onPressed: _retry,
                icon: const Icon(Icons.refresh),
                label: const Text('Re-record'),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: FilledButton(
                onPressed: _path == null
                    ? null
                    : () => Navigator.of(context).pop(_path),
                child: const Text('Use recording'),
              ),
            ),
          ],
        );
      case _RecorderPhase.denied:
      case _RecorderPhase.error:
        return FilledButton.icon(
          onPressed: _retry,
          icon: const Icon(Icons.refresh),
          label: const Text('Try again'),
        );
    }
  }
}

/// Live amplitude bars. Purely decorative — hidden from screen readers,
/// which get the spoken timer instead.
class _Waveform extends StatelessWidget {
  const _Waveform({required this.levels});
  final List<double> levels;

  @override
  Widget build(BuildContext context) {
    if (levels.isEmpty) {
      return Center(
        child: Text(
          'Waveform appears while recording',
          style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDimmest),
        ),
      );
    }
    return ExcludeSemantics(
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        mainAxisAlignment: MainAxisAlignment.center,
        children: levels
            .map((l) => Expanded(
                  child: Container(
                    margin: const EdgeInsets.symmetric(horizontal: 1),
                    height: 48 * l,
                    decoration: BoxDecoration(
                      color: AppColors.posttubePrimary,
                      borderRadius: BorderRadius.circular(2),
                    ),
                  ),
                ))
            .toList(),
      ),
    );
  }
}
