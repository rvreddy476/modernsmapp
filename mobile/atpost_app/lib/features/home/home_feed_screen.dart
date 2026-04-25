import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/providers/notification_provider.dart';
import 'package:atpost_app/providers/stories_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/shared/widgets/badge_icon_button.dart';
import 'package:atpost_app/shared/widgets/content_cards.dart';
import 'package:atpost_app/shared/widgets/filter_pills.dart';
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
  int feedTab = 0;
  final ScrollController _scrollController = ScrollController();

  @override
  void initState() {
    super.initState();
    _scrollController.addListener(_maybeLoadMore);
  }

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
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
      child: RefreshIndicator(
        color: AppColors.postbookPrimary,
        backgroundColor: AppColors.bgSecondary,
        onRefresh: _refreshHome,
        child: CustomScrollView(
          controller: _scrollController,
          physics: const AlwaysScrollableScrollPhysics(
            parent: BouncingScrollPhysics(),
          ),
          slivers: [
            SliverToBoxAdapter(
              child: Padding(
                padding: AppSpacing.pagePadding.copyWith(top: 10),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        ShaderMask(
                          blendMode: BlendMode.srcIn,
                          shaderCallback: (rect) => const LinearGradient(
                            colors: [
                              AppColors.postbookPrimary,
                              AppColors.posttubePrimary,
                            ],
                          ).createShader(rect),
                          child: Text('atpost', style: AppTextStyles.logo),
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
                          onPressed: () => context.push('/discover'),
                        ),
                        const SizedBox(width: 8),
                        BadgeIconButton(
                          icon: Icons.chat_bubble_rounded,
                          tooltip: 'Messages',
                          badgeCount:
                              ref.watch(unreadChatCountProvider).valueOrNull ??
                              0,
                          onPressed: () => context.push('/chat'),
                        ),
                        const SizedBox(width: 8),
                        BadgeIconButton(
                          icon: Icons.storefront_rounded,
                          tooltip: 'Shop',
                          onPressed: () => context.push('/shop'),
                        ),
                        const SizedBox(width: 8),
                        BadgeIconButton(
                          icon: Icons.favorite_rounded,
                          tooltip: 'Dating app',
                          onPressed: () => context.push('/postmatch'),
                        ),
                        const SizedBox(width: 8),
                        BadgeIconButton(
                          icon: Icons.notifications_rounded,
                          tooltip: 'Notifications',
                          badgeCount:
                              ref
                                  .watch(unreadNotificationCountProvider)
                                  .valueOrNull ??
                              0,
                          onPressed: () => context.push('/notifications'),
                        ),
                        const SizedBox(width: 8),
                        GestureDetector(
                          onTap: () => context.push('/profile'),
                          child: Builder(
                            builder: (_) {
                              final me = ref.watch(currentUserProvider).valueOrNull;
                              final avatar = me?.hasAvatar == true ? me!.avatarUrl : null;
                              return CircleAvatar(
                                radius: 18,
                                backgroundColor: AppColors.bgTertiary,
                                backgroundImage: avatar != null ? NetworkImage(avatar) : null,
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
                    ),
                    const SizedBox(height: 16),
                    FilterPills(
                      labels: const ['For You', 'Following', '#HashTag'],
                      activeIndex: feedTab,
                      onChanged: (v) {
                        setState(() => feedTab = v);
                        ref.read(feedFilterProvider.notifier).state = [
                          'For You',
                          'Following',
                          'Hashtag',
                        ][v];
                      },
                    ),
                    const SizedBox(height: 14),
                  ],
                ),
              ),
            ),
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
                  padding: AppSpacing.pagePadding.copyWith(bottom: 130),
                  sliver: SliverList(
                    delegate: SliverChildBuilderDelegate(
                      (context, index) {
                        if (index >= feedState.posts.length) {
                          return const Padding(
                            padding: EdgeInsets.symmetric(vertical: 18),
                            child: Center(
                              child: CircularProgressIndicator(
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                          );
                        }
                        final post = feedState.posts[index];
                        return ConstrainedBox(
                          constraints: BoxConstraints(
                            minHeight: MediaQuery.of(context).size.height * 0.7,
                          ),
                          child: Padding(
                            padding: const EdgeInsets.only(bottom: 16),
                            child: post.isReel
                                ? ReelCard(
                                  title: post.content,
                                  creator: 'By ${post.authorName ?? 'unknown'}',
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
                                      onTap: () => context.push('/posttube'),
                                    )
                                    : PostCard(post: post),
                          ),
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
    );
  }
}

String _initialsFor(String name) {
  final parts = name
      .trim()
      .split(RegExp(r'\s+'))
      .where((part) => part.isNotEmpty)
      .toList();
  if (parts.isEmpty) return '?';
  if (parts.length == 1) return parts.first[0].toUpperCase();
  return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
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
