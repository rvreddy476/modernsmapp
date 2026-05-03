import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/data/repositories/stories_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Renders a free-text "Ask me anything" prompt. Viewers reply with text;
/// only the creator sees the aggregate via the results sheet.
class QuestionWidget extends ConsumerStatefulWidget {
  const QuestionWidget({
    super.key,
    required this.storyId,
    required this.interactive,
    this.onSubmitted,
  });

  final String storyId;
  final StoryInteractive interactive;
  final VoidCallback? onSubmitted;

  @override
  ConsumerState<QuestionWidget> createState() => _QuestionWidgetState();
}

class _QuestionWidgetState extends ConsumerState<QuestionWidget> {
  final TextEditingController _controller = TextEditingController();
  bool _submitting = false;
  bool _sent = false;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final text = _controller.text.trim();
    if (text.isEmpty || _submitting) return;
    setState(() => _submitting = true);
    try {
      await ref.read(storiesRepositoryProvider).submitInteractiveResponse(
            storyId: widget.storyId,
            interactiveId: widget.interactive.id,
            text: text,
          );
      if (mounted) {
        setState(() {
          _sent = true;
          _controller.clear();
        });
      }
      widget.onSubmitted?.call();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not send reply.')),
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
          if (_sent)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 12),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  const Icon(Icons.check_circle,
                      color: AppColors.statusSuccess, size: 18),
                  const SizedBox(width: 6),
                  Text('Reply sent',
                      style:
                          AppTextStyles.bodyMedium.copyWith(color: Colors.white)),
                ],
              ),
            )
          else
            TextField(
              controller: _controller,
              style: AppTextStyles.body.copyWith(color: Colors.white),
              maxLength: 280,
              decoration: InputDecoration(
                hintText: 'Type a reply…',
                hintStyle:
                    AppTextStyles.body.copyWith(color: Colors.white54),
                filled: true,
                fillColor: Colors.white.withAlpha(30),
                counterStyle:
                    AppTextStyles.labelTiny.copyWith(color: Colors.white54),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(12),
                  borderSide: BorderSide.none,
                ),
                suffixIcon: IconButton(
                  icon: _submitting
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(
                              strokeWidth: 2, color: Colors.white))
                      : const Icon(Icons.send, color: Colors.white),
                  onPressed: _submitting ? null : _submit,
                ),
              ),
              onSubmitted: (_) => _submit(),
            ),
        ],
      ),
    );
  }
}
