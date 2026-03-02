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

  @override
  Widget build(BuildContext context) {
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
          SliverPadding(
            padding: AppSpacing.pagePadding.copyWith(bottom: 130),
            sliver: SliverList(
              delegate: SliverChildListDelegate(
                [
                  const PostCard(
                    name: 'Aarav Singh',
                    handle: '@aarav',
                    content:
                        'Launching atpost design alpha tonight. Blending social feed, reels, and long-form video in one app shell.',
                    tags: ['#design', '#flutter', '#atpost'],
                  ),
                  ReelCard(
                    title: 'Quick UI motion pass for the new feed transitions',
                    creator: 'By neha.motion',
                    duration: '00:35',
                    onTap: () => context.push('/reels'),
                  ),
                  VideoCard(
                    title: 'Building scalable feed ranking with event-driven architecture',
                    stats: '45.2K views  -  3h ago  -  4.9 rating',
                    onTap: () => context.push('/posttube'),
                  ),
                  const PostCard(
                    name: 'Meera Das',
                    handle: '@meera',
                    content:
                        'Prototype feels much smoother after tuning spacing, corner radii, and subtle glass surfaces.',
                    tags: ['#product', '#ux'],
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

