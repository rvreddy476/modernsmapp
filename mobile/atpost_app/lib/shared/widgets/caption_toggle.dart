import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/captions_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// CC toggle button + selected-language pill. The widget queries
/// `captionsForMediaProvider(mediaId)` on first build; if the media
/// has no caption tracks the button hides itself entirely so we
/// don't suggest captions where none exist.
///
/// Caller controls the on/off state so the player can reuse the
/// same toggle across multiple videos in a playlist without losing
/// the user's preference.
class CaptionToggle extends ConsumerWidget {
  const CaptionToggle({
    super.key,
    required this.mediaId,
    required this.enabled,
    required this.onToggle,
    this.compact = false,
  });

  /// Media id (the post-service media row's UUID). When [mediaId] is
  /// empty the toggle hides itself silently.
  final String mediaId;

  /// Whether captions are currently rendered on the player surface.
  final bool enabled;

  /// Fires when the user taps the CC button. Caller toggles its own
  /// state; the widget is dumb on purpose.
  final VoidCallback onToggle;

  /// Compact mode renders the button as a 32-dp icon-only pill (used
  /// in the reels player rail). Full mode renders a 40-dp pill with a
  /// "CC" label (used on the PostTube watch screen).
  final bool compact;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (mediaId.isEmpty) return const SizedBox.shrink();
    final tracks = ref.watch(captionsForMediaProvider(mediaId));
    return tracks.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (list) {
        if (list.isEmpty) return const SizedBox.shrink();
        final color = enabled ? AppColors.postbookPrimary : Colors.white70;
        final size = compact ? 32.0 : 40.0;
        return Semantics(
          button: true,
          toggled: enabled,
          label: enabled ? 'Hide captions' : 'Show captions',
          child: GestureDetector(
            onTap: onToggle,
            child: Container(
              height: size,
              padding: EdgeInsets.symmetric(
                horizontal: compact ? 8 : 12,
                vertical: 4,
              ),
              decoration: BoxDecoration(
                color: enabled
                    ? AppColors.postbookPrimary.withValues(alpha: 0.16)
                    : Colors.black.withValues(alpha: 0.45),
                borderRadius: BorderRadius.circular(999),
                border: Border.all(
                  color: enabled
                      ? AppColors.postbookPrimary.withValues(alpha: 0.6)
                      : Colors.white24,
                ),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(Icons.closed_caption_rounded, color: color, size: 18),
                  if (!compact) ...[
                    const SizedBox(width: 6),
                    Text(
                      'CC',
                      style: AppTextStyles.labelSmall.copyWith(color: color),
                    ),
                    if (enabled) ...[
                      const SizedBox(width: 6),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 6,
                          vertical: 2,
                        ),
                        decoration: BoxDecoration(
                          color: AppColors.postbookPrimary
                              .withValues(alpha: 0.25),
                          borderRadius: BorderRadius.circular(8),
                        ),
                        child: Text(
                          list.first.language.toUpperCase(),
                          style: AppTextStyles.labelTiny.copyWith(color: color),
                        ),
                      ),
                    ],
                  ],
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}
