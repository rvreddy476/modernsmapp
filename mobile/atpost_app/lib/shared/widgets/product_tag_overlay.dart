// In-video affiliate product overlay (mobile).
//
// Mirrors postbook-ui/src/features/posttube/components/ProductTagOverlay.tsx.
// Renders absolute-positioned cards over the video player, keyed by the
// current playhead. The video player feeds positionMs in via a parent
// state field (see VideoPlayerWidget's onPositionUpdate callback).
//
// Time window
//   timeStartMs null = "from the beginning"
//   timeEndMs   null = "until the end"
//   both null = "the whole video" (image posts behave this way)
//
// Impressions
//   We fire ONE impression per (tag, mount). Re-mounting on next
//   playthrough is the new-playthrough signal — the parent decides
//   when to do that (usually a Key change on the video URL change).

import 'package:atpost_app/data/models/product_tag.dart';
import 'package:atpost_app/data/repositories/product_tags_repository.dart';
import 'package:atpost_app/providers/product_tags_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ProductTagOverlay extends ConsumerWidget {
  const ProductTagOverlay({
    super.key,
    required this.postId,
    required this.positionMs,
    this.onCardTap,
  });

  final String postId;
  final int positionMs;

  /// Optional override for the tap target. Defaults to a no-op routing
  /// stub so the host app can wire navigation via GoRouter later.
  final void Function(PostProductTag tag)? onCardTap;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tagsAsync = ref.watch(productTagsByPostProvider(postId));
    return tagsAsync.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (tags) {
        final visible = filterActiveTags(tags, positionMs);
        if (visible.isEmpty) return const SizedBox.shrink();
        return IgnorePointer(
          // The overlay sits on top of the player; let touches fall
          // through the empty space to the playback controls. The
          // _ProductCard itself opts back in via its own GestureDetector.
          ignoring: false,
          child: Stack(
            children: [
              for (final tag in visible)
                _ProductCard(
                  key: ValueKey(tag.id),
                  postId: postId,
                  tag: tag,
                  onTap: onCardTap,
                ),
            ],
          ),
        );
      },
    );
  }
}

class _ProductCard extends ConsumerStatefulWidget {
  const _ProductCard({
    super.key,
    required this.postId,
    required this.tag,
    this.onTap,
  });

  final String postId;
  final PostProductTag tag;
  final void Function(PostProductTag tag)? onTap;

  @override
  ConsumerState<_ProductCard> createState() => _ProductCardState();
}

class _ProductCardState extends ConsumerState<_ProductCard> {
  bool _impressionFired = false;

  @override
  void initState() {
    super.initState();
    // Fire impression on first mount per playthrough. Best-effort —
    // repository swallows errors so a transient blip doesn't surface.
    if (!_impressionFired) {
      _impressionFired = true;
      // unawaited
      ref.read(productTagsRepositoryProvider).emitImpression(
            postId: widget.postId,
            tagId: widget.tag.id,
          );
    }
  }

  Future<void> _handleTap() async {
    // Increment click BEFORE navigating so we don't lose the event on
    // route push / app backgrounding.
    await ref.read(productTagsRepositoryProvider).emitClick(
          postId: widget.postId,
          tagId: widget.tag.id,
        );
    widget.onTap?.call(widget.tag);
  }

  @override
  Widget build(BuildContext context) {
    final tag = widget.tag;

    // Default position: dead-centre horizontally, lower-third vertically.
    // Leaves the upper portion clear for player chrome (mute button,
    // back arrow, etc.).
    final x = (tag.positionX ?? 50.0) / 100.0;
    final y = (tag.positionY ?? 80.0) / 100.0;

    return Align(
      // FractionalOffset goes 0..1 from top-left to bottom-right and
      // maps directly to the percentage stored on the tag.
      alignment: FractionalOffset(x, y),
      child: GestureDetector(
        onTap: _handleTap,
        behavior: HitTestBehavior.opaque,
        child: Container(
          margin: const EdgeInsets.symmetric(horizontal: 12),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: 0.95),
            borderRadius: BorderRadius.circular(16),
            boxShadow: const [
              BoxShadow(
                color: Color(0x33000000),
                blurRadius: 12,
                offset: Offset(0, 4),
              ),
            ],
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (tag.imageUrl.isNotEmpty)
                ClipRRect(
                  borderRadius: BorderRadius.circular(8),
                  child: Image.network(
                    tag.imageUrl,
                    width: 36,
                    height: 36,
                    fit: BoxFit.cover,
                    errorBuilder: (_, _, _) => _placeholderThumb(),
                  ),
                )
              else
                _placeholderThumb(),
              const SizedBox(width: 8),
              Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (tag.label.isNotEmpty)
                    Text(
                      tag.label,
                      style: const TextStyle(
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                        color: Colors.black87,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  const Text(
                    'Affiliate ↗',
                    style: TextStyle(
                      fontSize: 10,
                      fontWeight: FontWeight.w500,
                      letterSpacing: 1.0,
                      color: Color(0xFF7C3AED), // violet-600
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

Widget _placeholderThumb() {
  return Container(
    width: 36,
    height: 36,
    decoration: BoxDecoration(
      color: const Color(0xFFEDE9FE), // violet-100
      borderRadius: BorderRadius.circular(8),
    ),
  );
}

/// Pure filter — extracted so a Dart test can pin the time-window
/// contract without spinning up the widget tree.
List<PostProductTag> filterActiveTags(
  List<PostProductTag> tags,
  int positionMs,
) {
  return tags.where((t) {
    if (!t.isActive) return false;
    final startOk = t.timeStartMs == null || positionMs >= t.timeStartMs!;
    final endOk = t.timeEndMs == null || positionMs <= t.timeEndMs!;
    return startOk && endOk;
  }).toList();
}
