import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/hashtag_feed/hashtag_feed_screen.dart';
import 'package:atpost_app/features/shell/shell_providers.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/providers/notification_provider.dart';
import 'package:atpost_app/providers/stories_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/shared/widgets/badge_icon_button.dart';
import 'package:atpost_app/shared/widgets/content_cards.dart';
import 'package:atpost_app/shared/widgets/story_ring.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class HomeFeedScreen extends ConsumerStatefulWidget {
  const HomeFeedScreen({super.key});

  @override
  ConsumerState<HomeFeedScreen> createState() => _HomeFeedScreenState();
}

class _HomeFeedScreenState extends ConsumerState<HomeFeedScreen> {
  final ScrollController _scrollController = ScrollController();

  // Inline search state.
  bool _searchMode = false;
  final TextEditingController _searchCtrl = TextEditingController();
  final FocusNode _searchFocus = FocusNode();
  Timer? _searchDebounce;
  String _searchQuery = '';
  List<User> _searchResults = const [];
  bool _searchLoading = false;
  String? _searchError;

  @override
  void initState() {
    super.initState();
    _scrollController.addListener(_maybeLoadMore);
  }

  @override
  void dispose() {
    _scrollController.dispose();
    _searchCtrl.dispose();
    _searchFocus.dispose();
    _searchDebounce?.cancel();
    super.dispose();
  }

  void _enterSearchMode() {
    setState(() => _searchMode = true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _searchFocus.requestFocus();
    });
  }

  void _exitSearchMode() {
    setState(() {
      _searchMode = false;
      _searchCtrl.clear();
      _searchQuery = '';
      _searchResults = const [];
      _searchError = null;
      _searchLoading = false;
    });
    _searchDebounce?.cancel();
    _searchFocus.unfocus();
  }

  void _onSearchChanged(String value) {
    _searchDebounce?.cancel();
    final q = value.trim();
    setState(() {
      _searchQuery = q;
      _searchError = null;
    });
    if (q.length < 2) {
      setState(() {
        _searchResults = const [];
        _searchLoading = false;
      });
      return;
    }
    setState(() => _searchLoading = true);
    _searchDebounce = Timer(const Duration(milliseconds: 300), () {
      _runSearch(q);
    });
  }

  Future<void> _runSearch(String query) async {
    final repo = ref.read(userRepositoryProvider);
    try {
      final result = await repo.searchUsers(query, limit: 20);
      if (!mounted || _searchQuery != query) return;
      setState(() {
        _searchResults = result.users;
        _searchLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _searchError = 'Could not search right now.';
        _searchLoading = false;
      });
    }
  }

  void _maybeLoadMore() {
    if (!_scrollController.hasClients) return;
    if (_scrollController.position.extentAfter > 700) return;

    final state = ref.read(homeFeedProvider).valueOrNull;
    if (state == null || state.posts.isEmpty || state.hasReachedEnd) return;

    ref
        .read(homeFeedProvider.notifier)
        .onListItemVisible(state.posts.length - 1);
  }

  Future<void> _refreshHome() async {
    ref.invalidate(feedStoriesProvider);
    ref.invalidate(unreadNotificationCountProvider);
    ref.invalidate(unreadChatCountProvider);
    await ref.read(homeFeedProvider.notifier).fetchFirstPage();
  }

  Widget _buildBrandHeader() {
    return Row(
      children: [
        ShaderMask(
          blendMode: BlendMode.srcIn,
          shaderCallback: (rect) => const LinearGradient(
            colors: [AppColors.postbookPrimary, AppColors.posttubePrimary],
          ).createShader(rect),
          child: Text('VChat', style: AppTextStyles.logo),
        ),
        const SizedBox(width: 8),
        Container(
          width: 8,
          height: 8,
          decoration: const BoxDecoration(
            color: AppColors.posttubePrimary,
            shape: BoxShape.circle,
          ),
        ),
        const Spacer(),
        BadgeIconButton(
          icon: Icons.search_rounded,
          tooltip: 'Search',
          tintColor: AppColors.accentPurple,
          onPressed: _enterSearchMode,
        ),
        const SizedBox(width: 8),
        BadgeIconButton(
          icon: Icons.storefront_rounded,
          tooltip: 'Commerce',
          tintColor: AppColors.statusWarning,
          onPressed: () => context.push('/shop'),
        ),
        const SizedBox(width: 8),
        BadgeIconButton(
          icon: Icons.live_tv_rounded,
          tooltip: 'PostTube',
          tintColor: AppColors.posttubePrimary,
          onPressed: () => context.push('/posttube'),
        ),
        const SizedBox(width: 8),
        BadgeIconButton(
          icon: Icons.notifications_rounded,
          tooltip: 'Notifications',
          tintColor: AppColors.postbookPrimary,
          badgeCount:
              ref.watch(unreadNotificationCountProvider).valueOrNull ?? 0,
          onPressed: () => context.push('/notifications'),
        ),
        const SizedBox(width: 8),
        GestureDetector(
          onTap: () =>
              ref.read(shellTabProvider.notifier).state = 4,
          child: Builder(
            builder: (_) {
              final me = ref.watch(currentUserProvider).valueOrNull;
              final avatar = me?.hasAvatar == true ? me!.avatarUrl : null;
              return CircleAvatar(
                radius: 18,
                backgroundColor: AppColors.bgTertiary,
                backgroundImage:
                    avatar != null ? NetworkImage(avatar) : null,
                child: avatar == null
                    ? const Icon(
                        Icons.person_rounded,
                        size: 20,
                        color: AppColors.textDim,
                      )
                    : null,
              );
            },
          ),
        ),
      ],
    );
  }

  Widget _buildSearchHeader() {
    return Row(
      children: [
        Expanded(
          child: Container(
            height: 44,
            padding: const EdgeInsets.symmetric(horizontal: 14),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(99),
              border: Border.all(
                color: AppColors.accentPurple.withValues(alpha: 0.4),
              ),
            ),
            child: Row(
              children: [
                const Icon(
                  Icons.search_rounded,
                  size: 18,
                  color: AppColors.accentPurple,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    controller: _searchCtrl,
                    focusNode: _searchFocus,
                    autofocus: true,
                    onChanged: _onSearchChanged,
                    textInputAction: TextInputAction.search,
                    style: AppTextStyles.body,
                    cursorColor: AppColors.accentPurple,
                    decoration: const InputDecoration(
                      hintText: 'Search people…',
                      isCollapsed: true,
                      border: InputBorder.none,
                      hintStyle: TextStyle(color: AppColors.textDim),
                    ),
                  ),
                ),
                if (_searchCtrl.text.isNotEmpty)
                  GestureDetector(
                    onTap: () {
                      _searchCtrl.clear();
                      _onSearchChanged('');
                    },
                    child: const Icon(
                      Icons.close_rounded,
                      size: 18,
                      color: AppColors.textMuted,
                    ),
                  ),
              ],
            ),
          ),
        ),
        const SizedBox(width: 8),
        TextButton(
          onPressed: _exitSearchMode,
          style: TextButton.styleFrom(
            foregroundColor: AppColors.textSecondary,
          ),
          child: const Text('Cancel'),
        ),
      ],
    );
  }

  Widget _buildSearchResultsSliver() {
    if (_searchQuery.length < 2) {
      return SliverToBoxAdapter(
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 40),
          child: Center(
            child: Text(
              'Type at least 2 characters',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textMuted,
              ),
            ),
          ),
        ),
      );
    }
    if (_searchLoading) {
      return const SliverToBoxAdapter(
        child: Padding(
          padding: EdgeInsets.symmetric(vertical: 40),
          child: Center(child: CircularProgressIndicator()),
        ),
      );
    }
    if (_searchError != null) {
      return SliverToBoxAdapter(
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 40),
          child: Center(
            child: Text(
              _searchError!,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.statusError,
              ),
            ),
          ),
        ),
      );
    }
    if (_searchResults.isEmpty) {
      return SliverToBoxAdapter(
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 40),
          child: Center(
            child: Text(
              'No people found for “$_searchQuery”',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textMuted,
              ),
            ),
          ),
        ),
      );
    }
    return SliverPadding(
      padding: AppSpacing.pagePadding.copyWith(bottom: 130),
      sliver: SliverList(
        delegate: SliverChildBuilderDelegate(
          (ctx, i) {
            final user = _searchResults[i];
            final hasAvatar = user.hasAvatar;
            return ListTile(
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 4,
                vertical: 4,
              ),
              leading: CircleAvatar(
                radius: 22,
                backgroundColor: AppColors.bgTertiary,
                backgroundImage: hasAvatar ? NetworkImage(user.avatarUrl) : null,
                child: hasAvatar
                    ? null
                    : Text(
                        user.displayName.isNotEmpty
                            ? user.displayName[0].toUpperCase()
                            : '?',
                        style: AppTextStyles.h3,
                      ),
              ),
              title: Text(
                user.displayName,
                style: AppTextStyles.h3,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: user.username.isNotEmpty
                  ? Text(
                      '@${user.username}',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.textDim,
                      ),
                    )
                  : null,
              onTap: () {
                _exitSearchMode();
                context.push('/profile/${user.id}');
              },
            );
          },
          childCount: _searchResults.length,
        ),
      ),
    );
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d ago';
    if (diff.inHours > 0) return '${diff.inHours}h ago';
    return '${diff.inMinutes}m ago';
  }

  String _formatDuration(int seconds) {
    final m = seconds ~/ 60;
    final s = seconds % 60;
    return '${m.toString().padLeft(2, '0')}:${s.toString().padLeft(2, '0')}';
  }

  @override
  Widget build(BuildContext context) {
    final feedAsync = ref.watch(homeFeedProvider);

    return SafeArea(
      child: Column(
        children: [
          // Pinned chrome — does not scroll.
          Padding(
            padding: AppSpacing.pagePadding.copyWith(top: 10, bottom: 0),
            child: _searchMode ? _buildSearchHeader() : _buildBrandHeader(),
          ),
          if (!_searchMode) ...[
            const SizedBox(height: 12),
            _FeedTabStrip(
              activeIndex: ref.watch(homeFeedTabProvider),
              onChanged: (v) {
                ref.read(homeFeedTabProvider.notifier).state = v;
                if (v != 2) {
                  ref.read(feedFilterProvider.notifier).state = [
                    'For You',
                    'Following',
                    'Hashtag',
                  ][v];
                }
              },
            ),
          ] else
            const SizedBox(height: 8),

          // Scrollable region — only this scrolls.
          Expanded(
            child: !_searchMode && ref.watch(homeFeedTabProvider) == 2
                ? const HashtagFeedScreen()
                : RefreshIndicator(
              color: AppColors.postbookPrimary,
              backgroundColor: AppColors.bgSecondary,
              onRefresh: _refreshHome,
              child: CustomScrollView(
                controller: _scrollController,
                physics: const AlwaysScrollableScrollPhysics(
                  parent: BouncingScrollPhysics(),
                ),
                slivers: [
                  if (_searchMode)
                    _buildSearchResultsSliver()
                  else
                    ...feedAsync.when(
                      loading: () => [
                        const SliverToBoxAdapter(
                          child: Padding(
                            padding: EdgeInsets.symmetric(vertical: 40),
                            child: Center(child: CircularProgressIndicator()),
                          ),
                        ),
                      ],
                      error: (_, _) => [
                        const SliverToBoxAdapter(
                          child: Padding(
                            padding: EdgeInsets.symmetric(vertical: 40),
                            child: Center(child: Text('Could not load feed')),
                          ),
                        ),
                      ],
                      data: (feedState) => [
                        if (feedState.posts.isEmpty)
                          const SliverToBoxAdapter(
                            child: Padding(
                              padding: EdgeInsets.symmetric(vertical: 56),
                              child: _EmptyFeedState(),
                            ),
                          )
                        else
                          SliverPadding(
                            padding: AppSpacing.pagePadding.copyWith(
                              top: 12,
                              bottom: 130,
                            ),
                            sliver: SliverList(
                              delegate: SliverChildBuilderDelegate(
                                (context, index) {
                                  if (index >= feedState.posts.length) {
                                    return const Padding(
                                      padding: EdgeInsets.symmetric(
                                        vertical: 18,
                                      ),
                                      child: Center(
                                        child: CircularProgressIndicator(
                                          color: AppColors.postbookPrimary,
                                        ),
                                      ),
                                    );
                                  }
                                  final post = feedState.posts[index];
                                  return Padding(
                                    padding: const EdgeInsets.only(bottom: 12),
                                    child: post.isReel
                                        ? ReelCard(
                                            title: post.content,
                                            creator:
                                                'By ${post.authorName ?? 'unknown'}',
                                            duration: _formatDuration(
                                              post.durationSeconds ?? 0,
                                            ),
                                            onTap: () => context.push('/reels'),
                                          )
                                        : post.isVideo
                                            ? VideoCard(
                                                title: post.content,
                                                stats:
                                                    '${_formatCount(post.likeCount)} views  -  ${_timeAgo(post.createdAt)}',
                                                onTap: () =>
                                                    context.push('/posttube'),
                                              )
                                            : PostCard(post: post),
                                  );
                                },
                                childCount:
                                    feedState.posts.length +
                                    (feedState.isLoadingMore ? 1 : 0),
                              ),
                            ),
                          ),
                      ],
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

/// A tabbed strip that visually anchors the feed region. Each tab takes equal
/// width; the active tab carries a coloured underline indicator and stronger
/// text so the strip reads as part of the feed list rather than as floating
/// buttons.
class _FeedTabStrip extends StatelessWidget {
  const _FeedTabStrip({required this.activeIndex, required this.onChanged});

  final int activeIndex;
  final ValueChanged<int> onChanged;

  static const _items = <_FeedTabSpec>[
    _FeedTabSpec(
      label: 'For You',
      icon: Icons.auto_awesome_rounded,
      colour: AppColors.postbookPrimary,
    ),
    _FeedTabSpec(
      label: 'Following',
      icon: Icons.people_alt_rounded,
      colour: AppColors.posttubePrimary,
    ),
    _FeedTabSpec(
      label: '#HashTag',
      icon: Icons.tag_rounded,
      colour: AppColors.accentPurple,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: const BoxDecoration(
        border: Border(
          bottom: BorderSide(color: AppColors.borderSubtle, width: 1),
        ),
      ),
      child: SizedBox(
        height: 46,
        child: Row(
          children: List.generate(_items.length, (i) {
            final spec = _items[i];
            final active = i == activeIndex;
            return Expanded(
              child: _FeedTabButton(
                spec: spec,
                isActive: active,
                onTap: () => onChanged(i),
              ),
            );
          }),
        ),
      ),
    );
  }
}

class _FeedTabSpec {
  const _FeedTabSpec({
    required this.label,
    required this.icon,
    required this.colour,
  });
  final String label;
  final IconData icon;
  final Color colour;
}

class _FeedTabButton extends StatelessWidget {
  const _FeedTabButton({
    required this.spec,
    required this.isActive,
    required this.onTap,
  });

  final _FeedTabSpec spec;
  final bool isActive;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final tone = isActive ? spec.colour : AppColors.textMuted;
    return Semantics(
      selected: isActive,
      button: true,
      label: spec.label,
      child: InkWell(
        onTap: onTap,
        child: Stack(
          children: [
            Center(
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(spec.icon, size: 16, color: tone),
                  const SizedBox(width: 6),
                  Text(
                    spec.label,
                    style: AppTextStyles.label.copyWith(
                      color: tone,
                      fontWeight:
                          isActive ? FontWeight.w700 : FontWeight.w500,
                    ),
                  ),
                ],
              ),
            ),
            if (isActive)
              Positioned(
                left: 24,
                right: 24,
                bottom: 0,
                child: Container(
                  height: 3,
                  decoration: BoxDecoration(
                    color: spec.colour,
                    borderRadius: const BorderRadius.vertical(
                      top: Radius.circular(2),
                    ),
                    boxShadow: [
                      BoxShadow(
                        color: spec.colour.withValues(alpha: 0.45),
                        blurRadius: 8,
                        offset: const Offset(0, -1),
                      ),
                    ],
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}


class _EmptyFeedState extends StatelessWidget {
  const _EmptyFeedState();

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        const Icon(
          Icons.dynamic_feed_outlined,
          size: 40,
          color: AppColors.textMuted,
        ),
        const SizedBox(height: 10),
        Text('No posts yet', style: AppTextStyles.h3),
        const SizedBox(height: 6),
        Text(
          'Follow people or refresh after someone posts.',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textSecondary,
          ),
          textAlign: TextAlign.center,
        ),
      ],
    );
  }
}
