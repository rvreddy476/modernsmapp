import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/providers/notification_provider.dart';
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
      child: CustomScrollView(
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
                          colors: [AppColors.postbookPrimary, AppColors.posttubePrimary],
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
                        icon: Icons.search,
                        onPressed: () {},
                      ),
                      const SizedBox(width: 8),
                      BadgeIconButton(
                        icon: Icons.notifications_none,
                        badgeCount: ref.watch(unreadNotificationCountProvider).valueOrNull ?? 0,
                      ),
                      const SizedBox(width: 8),
                      BadgeIconButton(
                        icon: Icons.chat_bubble_outline,
                        badgeCount: ref.watch(unreadChatCountProvider),
                        onPressed: () => context.push('/chat'),
                      ),
                    ],
                  ),
                  const SizedBox(height: 16),
                  SizedBox(
                    height: 98,
                    child: ListView.separated(
                      scrollDirection: Axis.horizontal,
                      itemCount: 7,
                      separatorBuilder: (context, index) => const SizedBox(width: 10),
                      itemBuilder: (context, index) {
                        if (index == 0) {
                          return const StoryRing(
                            initials: 'Y',
                            label: 'Your Story',
                            isOwn: true,
                          );
                        }
                        if (index == 1) {
                          return const StoryRing(
                            initials: 'L',
                            label: 'Live',
                            isLive: true,
                          );
                        }
                        return StoryRing(
                          initials: String.fromCharCode(65 + index),
                          label: 'user_$index',
                        );
                      },
                    ),
                  ),
                  const SizedBox(height: 14),
                  FilterPills(
                    labels: const ['For You', 'Following', 'Trending'],
                    activeIndex: feedTab,
                    onChanged: (v) {
                      setState(() => feedTab = v);
                      ref.read(feedFilterProvider.notifier).state =
                          ['For You', 'Following', 'Trending'][v];
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
            data: (posts) => [
              SliverPadding(
                padding: AppSpacing.pagePadding.copyWith(bottom: 130),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) {
                      final post = posts[index];
                      if (post.isReel) {
                        return ReelCard(
                          title: post.content,
                          creator: 'By ${post.authorName ?? 'unknown'}',
                          duration: _formatDuration(post.durationSeconds ?? 0),
                          onTap: () => context.push('/reels'),
                        );
                      }
                      if (post.isVideo) {
                        return VideoCard(
                          title: post.content,
                          stats: '${_formatCount(post.likeCount)} views  -  ${_timeAgo(post.createdAt)}',
                          onTap: () => context.push('/posttube'),
                        );
                      }
                      return PostCard(
                        name: post.authorName ?? 'Anonymous',
                        handle: '@${(post.authorName ?? 'user').toLowerCase().replaceAll(' ', '_')}',
                        content: post.content,
                        tags: post.tags,
                        liked: post.isLiked,
                      );
                    },
                    childCount: posts.length,
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

