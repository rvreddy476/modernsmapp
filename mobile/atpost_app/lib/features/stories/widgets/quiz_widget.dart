import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/data/repositories/stories_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Renders a story quiz: question + 2-4 options + a single correct answer.
/// After tap, the right option flashes green / wrong flashes red.
class QuizWidget extends ConsumerStatefulWidget {
  const QuizWidget({
    super.key,
    required this.storyId,
    required this.interactive,
    this.onSubmitted,
  });

  final String storyId;
  final StoryInteractive interactive;
  final VoidCallback? onSubmitted;

  @override
  ConsumerState<QuizWidget> createState() => _QuizWidgetState();
}

class _QuizWidgetState extends ConsumerState<QuizWidget> {
  int? _selectedIdx;
  bool _submitting = false;

  Future<void> _answer(int idx, String optionId) async {
    if (_submitting || _selectedIdx != null) return;
    setState(() {
      _selectedIdx = idx;
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
        setState(() => _selectedIdx = null);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not submit answer.')),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  Color _colorFor(int idx) {
    if (_selectedIdx == null) return Colors.white.withAlpha(30);
    final correct = widget.interactive.correctIdx;
    if (correct == null) return Colors.white.withAlpha(30);
    if (idx == correct) return AppColors.statusSuccess.withAlpha(220);
    if (idx == _selectedIdx) return AppColors.statusError.withAlpha(220);
    return Colors.white.withAlpha(30);
  }

  @override
  Widget build(BuildContext context) {
    final options = widget.interactive.options;
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
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              const Icon(Icons.psychology_outlined,
                  color: Colors.white70, size: 16),
              const SizedBox(width: 6),
              Text('QUIZ',
                  style: AppTextStyles.labelTiny.copyWith(color: Colors.white70)),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            widget.interactive.question,
            textAlign: TextAlign.center,
            style: AppTextStyles.h3.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 12),
          for (var i = 0; i < options.length; i++)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Material(
                color: _colorFor(i),
                borderRadius: BorderRadius.circular(12),
                child: InkWell(
                  borderRadius: BorderRadius.circular(12),
                  onTap: _selectedIdx != null
                      ? null
                      : () => _answer(i, options[i].id),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 14, vertical: 12),
                    child: Center(
                      child: Text(
                        options[i].text,
                        style: AppTextStyles.bodyMedium
                            .copyWith(color: Colors.white),
                      ),
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
