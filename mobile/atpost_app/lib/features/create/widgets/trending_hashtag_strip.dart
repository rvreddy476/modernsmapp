// Reusable trending-hashtag chip strip. Fetched once on mount via
// HashtagRepository.getTrending, then kept live by subscribing to the
// /v1/hashtags/trending/stream SSE endpoint — post-service's
// trending.Publisher pushes a debounced snapshot whenever the top-N
// changes (typically every 30 s when activity is high, never when
// idle). Used by both the post composer (create_post_screen) and the
// reels caption composer to anchor canonical tag choices instead of
// letting users invent fresh near-duplicates.
//
// Callers supply an `onTagSelected` callback receiving the chip's
// display string (with leading `#`) so each composer decides how to
// splice it into its own input shape — text-field insert vs. chip
// list vs. dropdown reuse.

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/hashtag_feed/data/hashtag_live_stream.dart';
import 'package:atpost_app/features/hashtag_feed/data/hashtag_repository.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:atpost_app/features/hashtag_feed/state/hashtag_feed_notifier.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class TrendingHashtagStrip extends ConsumerStatefulWidget {
  const TrendingHashtagStrip({
    super.key,
    required this.onTagSelected,
    this.limit = 8,
    this.excluded = const <String>{},
    this.label = 'Trending',
  });

  /// Invoked with the formatted chip text (e.g. `#football`) so the
  /// caller can splice it into its input. The string is always
  /// lowercased and prefixed with `#`.
  final ValueChanged<String> onTagSelected;

  /// Hard cap on how many chips to render. The endpoint typically
  /// returns ~15 but rendering more crowds the composer footer.
  final int limit;

  /// Tags (lowercased, with or without leading `#`) the caller has
  /// already used; we hide them so users can't pick the same one
  /// twice.
  final Set<String> excluded;

  /// Section label shown above the chip strip. Pass empty to hide.
  final String label;

  @override
  ConsumerState<TrendingHashtagStrip> createState() => _TrendingHashtagStripState();
}

class _TrendingHashtagStripState extends ConsumerState<TrendingHashtagStrip> {
  List<HashtagModel> _trending = const <HashtagModel>[];
  bool _loading = true;
  bool _failed = false;
  StreamSubscription<HashtagSnapshot>? _liveSub;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _load();
      _openLiveStream();
    });
  }

  @override
  void dispose() {
    _liveSub?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final tags = await ref
          .read(hashtagRepositoryProvider)
          .getTrending(limit: widget.limit + widget.excluded.length);
      if (!mounted) return;
      setState(() {
        _trending = tags;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _failed = true;
        _loading = false;
      });
    }
  }

  /// Open the SSE subscription so the chip strip refreshes whenever
  /// the post-service publisher emits a new top-N snapshot. Cancelled
  /// in dispose. Errors are logged at the stream layer and fall back
  /// to the initial REST load.
  void _openLiveStream() {
    _liveSub?.cancel();
    final stream = ref.read(hashtagLiveStreamProvider).subscribeTrending();
    _liveSub = stream.listen(
      (snapshot) {
        if (!mounted || snapshot.tags.isEmpty) return;
        setState(() {
          _trending = snapshot.tags;
          // Successful push implicitly clears any prior failure state.
          _failed = false;
          _loading = false;
        });
      },
      onError: (_) {
        // Push channel down — keep showing whatever we last loaded.
      },
      cancelOnError: false,
    );
  }

  bool _isExcluded(String name) {
    final lower = name.toLowerCase();
    return widget.excluded.contains(lower) ||
        widget.excluded.contains('#$lower');
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 6),
        child: SizedBox(
          height: 14,
          width: 14,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    if (_failed || _trending.isEmpty) return const SizedBox.shrink();
    final visible = _trending
        .where((t) {
          final name = t.displayName.isNotEmpty ? t.displayName : t.normalizedName;
          return name.isNotEmpty && !_isExcluded(name);
        })
        .take(widget.limit)
        .toList();
    if (visible.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (widget.label.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 6),
            child: Text(
              '🔥 ${widget.label}',
              style: AppTextStyles.labelSmall.copyWith(color: Colors.white54),
            ),
          ),
        Wrap(
          spacing: 6,
          runSpacing: 6,
          children: visible.map((tag) {
            final name = tag.displayName.isNotEmpty
                ? tag.displayName
                : tag.normalizedName;
            return GestureDetector(
              onTap: () => widget.onTagSelected('#${name.toLowerCase()}'),
              child: Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 10,
                  vertical: 5,
                ),
                decoration: BoxDecoration(
                  color: AppColors.postbookPrimary.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(14),
                  border: Border.all(
                    color: AppColors.postbookPrimary.withValues(alpha: 0.35),
                  ),
                ),
                child: Text(
                  '#$name',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.postbookPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            );
          }).toList(),
        ),
      ],
    );
  }
}
