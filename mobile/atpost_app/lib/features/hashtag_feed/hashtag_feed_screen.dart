import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:atpost_app/features/hashtag_feed/state/hashtag_feed_notifier.dart';
import 'package:atpost_app/features/hashtag_feed/state/hashtag_feed_state.dart';
import 'package:atpost_app/shared/widgets/content_cards.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Inline body for the home feed's #Hashtag tab.
/// Renders: search bar → trending chips OR selected header → sort toggle → posts.
class HashtagFeedScreen extends ConsumerStatefulWidget {
  const HashtagFeedScreen({super.key});

  @override
  ConsumerState<HashtagFeedScreen> createState() => _HashtagFeedScreenState();
}

class _HashtagFeedScreenState extends ConsumerState<HashtagFeedScreen>
    with AutomaticKeepAliveClientMixin {
  final _scrollController = ScrollController();
  final _searchController = TextEditingController();

  @override
  bool get wantKeepAlive => true;

  @override
  void initState() {
    super.initState();
    _scrollController.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scrollController
      ..removeListener(_onScroll)
      ..dispose();
    _searchController.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (!_scrollController.hasClients) return;
    final pos = _scrollController.position;
    if (pos.pixels >= pos.maxScrollExtent - 400) {
      ref.read(hashtagFeedProvider.notifier).loadMore();
    }
  }

  @override
  Widget build(BuildContext context) {
    super.build(context);
    final state = ref.watch(hashtagFeedProvider);
    final notifier = ref.read(hashtagFeedProvider.notifier);

    return RefreshIndicator(
      color: AppColors.postbookPrimary,
      backgroundColor: AppColors.bgSecondary,
      onRefresh: notifier.refresh,
      child: ListView(
        controller: _scrollController,
        physics: const AlwaysScrollableScrollPhysics(
            parent: BouncingScrollPhysics()),
        padding: const EdgeInsets.only(bottom: 130),
        children: [
          Padding(
            padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 8),
            child: _SearchBar(
              controller: _searchController,
              query: state.query,
              onChanged: notifier.onSearchChanged,
              onClear: () {
                _searchController.clear();
                notifier.clearSearch();
              },
            ),
          ),
          if (state.query.replaceAll('#', '').length >= 2)
            _SearchSuggestions(
              suggestions: state.searchSuggestions,
              isSearching: state.isSearching,
              onTap: (h) {
                _searchController.clear();
                notifier.selectHashtag(h);
              },
            )
          else if (state.selectedHashtag != null)
            _SelectedHashtagHeader(
              hashtag: state.selectedHashtag!,
              onClear: notifier.clearSelectedHashtag,
            )
          else
            _TrendingChips(
              hashtags: state.trendingHashtags,
              selected: state.selectedHashtag,
              onTap: notifier.selectHashtag,
            ),
          if (state.selectedHashtag != null ||
              state.status == HashtagFeedStatus.loaded)
            Padding(
              padding: AppSpacing.pagePadding.copyWith(top: 4, bottom: 4),
              child: _SortToggle(
                sort: state.sort,
                onChanged: notifier.setSort,
              ),
            ),
          ..._buildBody(state, notifier),
        ],
      ),
    );
  }

  List<Widget> _buildBody(HashtagFeedState state, HashtagFeedNotifier notifier) {
    if (state.status == HashtagFeedStatus.loading && state.posts.isEmpty) {
      return const [_FeedSkeleton()];
    }
    if (state.status == HashtagFeedStatus.error && state.posts.isEmpty) {
      return [_ErrorState(onRetry: notifier.refresh)];
    }
    if (state.status == HashtagFeedStatus.loaded && state.posts.isEmpty) {
      return [
        _EmptyState(selected: state.selectedHashtag, query: state.query),
      ];
    }
    return [
      Padding(
        padding: AppSpacing.pagePadding.copyWith(top: 8),
        child: Column(
          children: [
            for (final post in state.posts)
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: PostCard(post: post),
              ),
          ],
        ),
      ),
      if (state.status == HashtagFeedStatus.loadingMore)
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 24),
          child: Center(
            child: SizedBox(
              width: 22,
              height: 22,
              child: CircularProgressIndicator(
                strokeWidth: 2,
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
        )
      else if (!state.hasMore && state.posts.isNotEmpty)
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 24),
          child: Center(
            child: Text(
              "You're all caught up.",
              style: TextStyle(color: AppColors.textMuted, fontSize: 12),
            ),
          ),
        ),
    ];
  }
}

// ---------------- Search bar ----------------

class _SearchBar extends StatelessWidget {
  const _SearchBar({
    required this.controller,
    required this.query,
    required this.onChanged,
    required this.onClear,
  });

  final TextEditingController controller;
  final String query;
  final ValueChanged<String> onChanged;
  final VoidCallback onClear;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: TextField(
        controller: controller,
        onChanged: onChanged,
        style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
        decoration: InputDecoration(
          hintText: 'Search #hashtags, topics…',
          hintStyle:
              AppTextStyles.body.copyWith(color: AppColors.textMuted),
          prefixIcon: const Icon(Icons.search_rounded,
              color: AppColors.textMuted, size: 20),
          suffixIcon: query.isEmpty
              ? null
              : IconButton(
                  icon: const Icon(Icons.close_rounded,
                      color: AppColors.textMuted, size: 18),
                  onPressed: onClear,
                ),
          border: InputBorder.none,
          contentPadding:
              const EdgeInsets.symmetric(vertical: 12, horizontal: 4),
        ),
      ),
    );
  }
}

class _SearchSuggestions extends StatelessWidget {
  const _SearchSuggestions({
    required this.suggestions,
    required this.isSearching,
    required this.onTap,
  });

  final List<HashtagModel> suggestions;
  final bool isSearching;
  final ValueChanged<HashtagModel> onTap;

  @override
  Widget build(BuildContext context) {
    if (isSearching && suggestions.isEmpty) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 24),
        child: Center(
          child: SizedBox(
            width: 18,
            height: 18,
            child: CircularProgressIndicator(
              strokeWidth: 2,
              color: AppColors.postbookPrimary,
            ),
          ),
        ),
      );
    }
    if (suggestions.isEmpty) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 28, horizontal: 18),
        child: Center(
          child: Text(
            'No matching hashtags. Try a different topic.',
            style: TextStyle(color: AppColors.textMuted, fontSize: 13),
          ),
        ),
      );
    }
    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 4, bottom: 8),
      child: Column(
        children: [
          for (final h in suggestions)
            InkWell(
              onTap: () => onTap(h),
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              child: Padding(
                padding: const EdgeInsets.symmetric(
                    horizontal: 8, vertical: 10),
                child: Row(
                  children: [
                    const Icon(Icons.tag_rounded,
                        size: 18, color: AppColors.accentPurple),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        h.displayName,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: AppTextStyles.label.copyWith(
                          color: AppColors.textPrimary,
                        ),
                      ),
                    ),
                    Text(
                      _formatCount(h.postCount),
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}

// ---------------- Trending chips ----------------

class _TrendingChips extends StatelessWidget {
  const _TrendingChips({
    required this.hashtags,
    required this.selected,
    required this.onTap,
  });

  final List<HashtagModel> hashtags;
  final HashtagModel? selected;
  final ValueChanged<HashtagModel> onTap;

  @override
  Widget build(BuildContext context) {
    if (hashtags.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 6, bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 8, left: 2),
            child: Text(
              'Trending Now',
              style: AppTextStyles.labelSmall.copyWith(
                color: AppColors.textTertiary,
                letterSpacing: 0.6,
                fontWeight: FontWeight.w700,
              ),
            ),
          ),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final h in hashtags)
                _Chip(
                  label: h.displayName,
                  active: selected?.normalizedName == h.normalizedName,
                  onTap: () => onTap(h),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    required this.label,
    required this.active,
    required this.onTap,
  });

  final String label;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 160),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
        decoration: BoxDecoration(
          color: active ? AppColors.accentPurple : AppColors.bgCard,
          border: Border.all(
            color: active ? AppColors.accentPurple : AppColors.borderSubtle,
          ),
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: active ? Colors.white : AppColors.textSecondary,
            fontWeight: active ? FontWeight.w700 : FontWeight.w500,
          ),
        ),
      ),
    );
  }
}

// ---------------- Selected header ----------------

class _SelectedHashtagHeader extends StatelessWidget {
  const _SelectedHashtagHeader({
    required this.hashtag,
    required this.onClear,
  });

  final HashtagModel hashtag;
  final VoidCallback onClear;

  @override
  Widget build(BuildContext context) {
    final count = hashtag.postCount;
    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 4, bottom: 8),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            const Icon(Icons.tag_rounded,
                size: 20, color: AppColors.accentPurple),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    hashtag.displayName,
                    style: AppTextStyles.h3.copyWith(
                      color: AppColors.textPrimary,
                    ),
                  ),
                  if (count > 0)
                    Text(
                      '${_formatCount(count)} posts',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                ],
              ),
            ),
            IconButton(
              onPressed: onClear,
              icon: const Icon(Icons.close_rounded,
                  color: AppColors.textSecondary, size: 18),
              tooltip: 'Clear hashtag',
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------- Sort toggle ----------------

class _SortToggle extends StatelessWidget {
  const _SortToggle({required this.sort, required this.onChanged});

  final HashtagSort sort;
  final ValueChanged<HashtagSort> onChanged;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        for (final option in HashtagSort.values)
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: GestureDetector(
              onTap: () => onChanged(option),
              child: AnimatedContainer(
                duration: const Duration(milliseconds: 160),
                padding: const EdgeInsets.symmetric(
                    horizontal: 14, vertical: 6),
                decoration: BoxDecoration(
                  color: option == sort
                      ? AppColors.postbookPrimary
                      : AppColors.bgCard,
                  border: Border.all(
                    color: option == sort
                        ? AppColors.postbookPrimary
                        : AppColors.borderSubtle,
                  ),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                ),
                child: Text(
                  option.label,
                  style: AppTextStyles.label.copyWith(
                    color:
                        option == sort ? Colors.white : AppColors.textSecondary,
                    fontWeight:
                        option == sort ? FontWeight.w700 : FontWeight.w500,
                  ),
                ),
              ),
            ),
          ),
      ],
    );
  }
}

// ---------------- Skeleton / empty / error ----------------

class _FeedSkeleton extends StatelessWidget {
  const _FeedSkeleton();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 16),
      child: Column(
        children: [
          for (var i = 0; i < 3; i++)
            Padding(
              padding: const EdgeInsets.only(bottom: 14),
              child: Container(
                height: 220,
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusLarge),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.selected, required this.query});

  final HashtagModel? selected;
  final String query;

  @override
  Widget build(BuildContext context) {
    final cleanedQ = query.replaceAll('#', '').trim();
    final String title;
    final String subtitle;

    if (selected != null) {
      title = 'No posts for ${selected!.displayName}';
      subtitle = 'Be the first to post with this hashtag.';
    } else if (cleanedQ.isNotEmpty) {
      title = 'No results for "$cleanedQ"';
      subtitle = 'Try a different topic or create a post using #$cleanedQ.';
    } else {
      title = 'Nothing trending yet';
      subtitle =
          'Start a post with a hashtag like #Cricket or #AI to get things going.';
    }

    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 60, bottom: 60),
      child: Column(
        children: [
          const Icon(Icons.tag_rounded,
              size: 48, color: AppColors.textDimmest),
          const SizedBox(height: 14),
          Text(
            title,
            textAlign: TextAlign.center,
            style: AppTextStyles.h3.copyWith(color: AppColors.textPrimary),
          ),
          const SizedBox(height: 6),
          Text(
            subtitle,
            textAlign: TextAlign.center,
            style: AppTextStyles.body.copyWith(color: AppColors.textMuted),
          ),
        ],
      ),
    );
  }
}

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.onRetry});

  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 60, bottom: 60),
      child: Column(
        children: [
          const Icon(Icons.cloud_off_rounded,
              size: 48, color: AppColors.statusError),
          const SizedBox(height: 14),
          Text(
            'Could not load hashtag feed',
            style: AppTextStyles.h3.copyWith(color: AppColors.textPrimary),
          ),
          const SizedBox(height: 6),
          Text(
            'Check your connection and try again.',
            textAlign: TextAlign.center,
            style: AppTextStyles.body.copyWith(color: AppColors.textMuted),
          ),
          const SizedBox(height: 18),
          ElevatedButton(
            onPressed: onRetry,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              foregroundColor: Colors.white,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              ),
              padding:
                  const EdgeInsets.symmetric(horizontal: 22, vertical: 12),
              elevation: 0,
            ),
            child: const Text('Try again'),
          ),
        ],
      ),
    );
  }
}

String _formatCount(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}k';
  return n.toString();
}
