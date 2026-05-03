import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/data/repositories/stories_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Renders an emoji slider (0..100). Viewer drags, releases, value persists.
class SliderInteractiveWidget extends ConsumerStatefulWidget {
  const SliderInteractiveWidget({
    super.key,
    required this.storyId,
    required this.interactive,
    this.onSubmitted,
  });

  final String storyId;
  final StoryInteractive interactive;
  final VoidCallback? onSubmitted;

  @override
  ConsumerState<SliderInteractiveWidget> createState() =>
      _SliderInteractiveWidgetState();
}

class _SliderInteractiveWidgetState
    extends ConsumerState<SliderInteractiveWidget> {
  double _value = 50;
  bool _submitting = false;
  bool _sent = false;

  Future<void> _submit() async {
    if (_submitting || _sent) return;
    setState(() => _submitting = true);
    try {
      await ref.read(storiesRepositoryProvider).submitInteractiveResponse(
            storyId: widget.storyId,
            interactiveId: widget.interactive.id,
            sliderValue: _value.round(),
          );
      if (mounted) setState(() => _sent = true);
      widget.onSubmitted?.call();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not submit slider.')),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final emoji = widget.interactive.emoji ?? '😍';
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
        children: [
          Text(
            widget.interactive.question,
            textAlign: TextAlign.center,
            style: AppTextStyles.h3.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 12),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Transform.scale(
                scale: 0.8 + (_value / 100) * 1.2,
                child: Text(emoji, style: const TextStyle(fontSize: 40)),
              ),
            ],
          ),
          SliderTheme(
            data: SliderTheme.of(context).copyWith(
              activeTrackColor: AppColors.postbookPrimary,
              inactiveTrackColor: Colors.white24,
              thumbColor: Colors.white,
              overlayColor: AppColors.postbookPrimary.withAlpha(60),
            ),
            child: Slider(
              value: _value,
              min: 0,
              max: 100,
              onChanged: _sent
                  ? null
                  : (v) {
                      setState(() => _value = v);
                    },
              onChangeEnd: _sent ? null : (_) => _submit(),
            ),
          ),
          if (_sent)
            Text(
              'Submitted: ${_value.round()}',
              style:
                  AppTextStyles.bodyMedium.copyWith(color: Colors.white70),
            )
          else if (_submitting)
            Text('Saving…',
                style:
                    AppTextStyles.bodyMedium.copyWith(color: Colors.white70)),
        ],
      ),
    );
  }
}
