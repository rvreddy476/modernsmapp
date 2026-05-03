import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/data/repositories/stories_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Renders a story countdown: a label + ticking digits to a target time.
/// Tap "Remind me" to mark a reminder server-side.
class CountdownWidget extends ConsumerStatefulWidget {
  const CountdownWidget({
    super.key,
    required this.storyId,
    required this.interactive,
    this.onSubmitted,
  });

  final String storyId;
  final StoryInteractive interactive;
  final VoidCallback? onSubmitted;

  @override
  ConsumerState<CountdownWidget> createState() => _CountdownWidgetState();
}

class _CountdownWidgetState extends ConsumerState<CountdownWidget> {
  Timer? _ticker;
  bool _reminderSet = false;
  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    _ticker = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _ticker?.cancel();
    super.dispose();
  }

  Future<void> _setReminder() async {
    if (_submitting || _reminderSet) return;
    setState(() => _submitting = true);
    try {
      await ref.read(storiesRepositoryProvider).submitInteractiveResponse(
            storyId: widget.storyId,
            interactiveId: widget.interactive.id,
            reminder: true,
          );
      if (mounted) setState(() => _reminderSet = true);
      widget.onSubmitted?.call();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not set reminder.')),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  String _twoDigits(int n) => n.toString().padLeft(2, '0');

  @override
  Widget build(BuildContext context) {
    final target = widget.interactive.endTime ?? DateTime.now();
    final diff = target.difference(DateTime.now());
    final isOver = diff.isNegative;

    final days = diff.inDays;
    final hours = diff.inHours.remainder(24);
    final minutes = diff.inMinutes.remainder(60);
    final seconds = diff.inSeconds.remainder(60);

    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        gradient: AppColors.postbookGradient,
        borderRadius: BorderRadius.circular(16),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            widget.interactive.question,
            textAlign: TextAlign.center,
            style: AppTextStyles.h3.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 12),
          if (isOver)
            Text(
              "It's time!",
              style: AppTextStyles.h2.copyWith(color: Colors.white),
            )
          else
            Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                _CountdownCell(label: 'd', value: _twoDigits(days)),
                _CountdownCell(label: 'h', value: _twoDigits(hours.abs())),
                _CountdownCell(label: 'm', value: _twoDigits(minutes.abs())),
                _CountdownCell(label: 's', value: _twoDigits(seconds.abs())),
              ],
            ),
          const SizedBox(height: 12),
          ElevatedButton.icon(
            style: ElevatedButton.styleFrom(
              backgroundColor: Colors.white,
              foregroundColor: AppColors.postbookPrimary,
              padding:
                  const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            ),
            onPressed: (_reminderSet || _submitting) ? null : _setReminder,
            icon: Icon(_reminderSet
                ? Icons.notifications_active
                : Icons.notifications_outlined),
            label: Text(
              _reminderSet ? 'Reminder set' : 'Remind me',
              style: AppTextStyles.label
                  .copyWith(color: AppColors.postbookPrimary),
            ),
          ),
        ],
      ),
    );
  }
}

class _CountdownCell extends StatelessWidget {
  const _CountdownCell({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: Colors.black.withAlpha(80),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Column(
          children: [
            Text(value,
                style: AppTextStyles.h2.copyWith(color: Colors.white)),
            Text(label,
                style: AppTextStyles.labelTiny
                    .copyWith(color: Colors.white70)),
          ],
        ),
      ),
    );
  }
}
