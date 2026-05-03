import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/data/repositories/stories_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Renders a story poll (2-4 options, single-select) with optimistic feedback.
class PollWidget extends ConsumerStatefulWidget {
  const PollWidget({
    super.key,
    required this.storyId,
    required this.interactive,
    this.onSubmitted,
  });

  final String storyId;
  final StoryInteractive interactive;
  final VoidCallback? onSubmitted;

  @override
  ConsumerState<PollWidget> createState() => _PollWidgetState();
}

class _PollWidgetState extends ConsumerState<PollWidget> {
  String? _selected;
  bool _submitting = false;

  Future<void> _vote(String optionId) async {
    if (_submitting || _selected != null) return;
    setState(() {
      _selected = optionId;
      _submitting = true;
    });
    try {
      await ref.read(storiesRepositoryProvider).submitInteractiveResponse(
            storyId: widget.storyId,
            interactiveId: widget.interactive.id,
            optionId: optionId,
          );
      widget.onSubmitted?.call();
    } catch (_) {
      if (mounted) {
        setState(() => _selected = null);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not submit vote.')),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: Colors.black.withAlpha(140),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: Colors.white.withAlpha(40)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            widget.interactive.question,
            textAlign: TextAlign.center,
            style: AppTextStyles.h3.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 12),
          for (final option in widget.interactive.options)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: _PollOption(
                text: option.text,
                selected: _selected == option.id,
                disabled: _submitting && _selected != option.id,
                onTap: () => _vote(option.id),
              ),
            ),
        ],
      ),
    );
  }
}

class _PollOption extends StatelessWidget {
  const _PollOption({
    required this.text,
    required this.selected,
    required this.disabled,
    required this.onTap,
  });

  final String text;
  final bool selected;
  final bool disabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Opacity(
      opacity: disabled ? 0.6 : 1,
      child: Material(
        color: selected
            ? AppColors.postbookPrimary.withAlpha(220)
            : Colors.white.withAlpha(30),
        borderRadius: BorderRadius.circular(12),
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: disabled ? null : onTap,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
            child: Center(
              child: Text(
                text,
                style: AppTextStyles.bodyMedium.copyWith(color: Colors.white),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
