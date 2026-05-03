// Home tab — AtPost super-app shell.
//
// Unified scrollable home: stories rail, quick-action chips, and a mixed
// feed (posts + reels-in-feed + sponsored product cards + Q&A teasers).
//
// The mix is composed client-side for v1: every 5th item is a Reels
// carousel, every 8th item a sponsored product card, every 12th a
// "Top question this week" teaser. Sprint 2 will move this to a unified
// `/v1/home/feed` endpoint so the shape comes pre-mixed from the server.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/providers/stories_provider.dart';
import 'package:atpost_app/shared/widgets/content_cards.dart';
import 'package:atpost_app/shared/widgets/story_ring.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class HomeTab extends ConsumerStatefulWidget {
  const HomeTab({super.key});

  @override
  ConsumerState<HomeTab> createState() => _HomeTabState();
}

class _HomeTabState extends ConsumerState<HomeTab> {
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
    final position = _scrollController.position;
    if (position.pixels >= position.maxScrollExtent - 600) {
      ref.read(homeFeedProvider.notifier).onListItemVisible(9999);
    }
  }

  Future<void> _refresh() async {
    ref.invalidate(feedStoriesProvider);
    ref.invalidate(productsProvider);
    await ref.read(homeFeedProvider.notifier).fetchFirstPage();
  }

  @override
  Widget build(BuildContext context) {
    final feed = ref.watch(homeFeedProvider);
    final stories = ref.watch(feedStoriesProvider);
    final sponsored = ref.watch(productsProvider(const ProductsQuery()));

    return RefreshIndicator(
      onRefresh: _refresh,
      child: CustomScrollView(
        controller: _scrollController,
        slivers: [
          SliverAppBar(
            pinned: false,
            floating: true,
            elevation: 0,
            backgroundColor: AppColors.bgPrimary,
            title: Text('AtPost', style: AppTextStyles.logo),
          ),
          SliverToBoxAdapter(child: _StoriesRail(asyncStories: stories)),
          const SliverToBoxAdapter(child: _QuickActionChips()),
          ...feed.when(
            data: (state) {
              if (state.posts.isEmpty) {
                return [
                  const SliverFillRemaining(
                    hasScrollBody: false,
                    child: _EmptyHome(),
                  ),
                ];
              }
              final products = sponsored.valueOrNull?.items ?? const <Product>[];
              return _buildMixedSlivers(state.posts, products);
            },
            loading: () => const [
              SliverFillRemaining(
                hasScrollBody: false,
                child: Center(child: CircularProgressIndicator()),
              ),
            ],
            error: (e, _) => [
              SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text(
                      'Could not load your feed.\n$e',
                      textAlign: TextAlign.center,
                      style: AppTextStyles.bodySmall,
                    ),
                  ),
                ),
              ),
            ],
          ),
          const SliverToBoxAdapter(child: SizedBox(height: 80)),
        ],
      ),
    );
  }

  /// Walks the post list and interleaves reels-carousel / product / Q&A
  /// teaser slivers. Pure function over the inputs — no side effects.
  List<Widget> _buildMixedSlivers(List<Post> posts, List<Product> products) {
    // Split out reels for the carousels and keep the rest as the main spine.
    final reels = posts.where((p) => p.contentType == 'reel').toList();
    final spine = posts.where((p) => p.contentType != 'reel').toList();
    final slivers = <Widget>[];

    var reelCursor = 0;
    var productCursor = 0;

    for (var i = 0; i < spine.length; i++) {
      slivers.add(
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.symmetric(
              horizontal: 12,
              vertical: 6,
            ),
            child: PostCard(post: spine[i]),
          ),
        ),
      );

      // Every 5th spine item — a horizontal Reels carousel.
      if ((i + 1) % 5 == 0 && reels.isNotEmpty) {
        final slice = <Post>[];
        for (var j = 0; j < 5 && reelCursor < reels.length; j++) {
          slice.add(reels[reelCursor]);
          reelCursor = (reelCursor + 1) % reels.length;
          if (slice.length >= 5) break;
        }
        slivers.add(SliverToBoxAdapter(child: _ReelsRow(reels: slice)));
      }

      // Every 8th spine item — a sponsored product card.
      if ((i + 1) % 8 == 0 && products.isNotEmpty) {
        final p = products[productCursor % products.length];
        productCursor++;
        slivers.add(SliverToBoxAdapter(child: _SponsoredProductCard(product: p)));
      }

      // Every 12th spine item — a Q&A teaser.
      if ((i + 1) % 12 == 0) {
        slivers.add(const SliverToBoxAdapter(child: _TopQuestionTeaser()));
      }
    }

    return slivers;
  }
}

// ─── Stories rail ──────────────────────────────────────────────────────

class _StoriesRail extends StatelessWidget {
  const _StoriesRail({required this.asyncStories});

  final AsyncValue<List<Story>> asyncStories;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 100,
      child: asyncStories.when(
        data: (list) {
          if (list.isEmpty) return const SizedBox.shrink();
          return ListView.separated(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            itemBuilder: (context, i) {
              final story = list[i];
              final initials = _initials(story.authorName);
              return GestureDetector(
                onTap: () => context.push('/stories/${story.authorId}'),
                child: StoryRing(
                  initials: initials,
                  label: story.authorName,
                ),
              );
            },
            separatorBuilder: (_, _) => const SizedBox(width: 10),
            itemCount: list.length,
          );
        },
        loading: () => const SizedBox.shrink(),
        error: (_, _) => const SizedBox.shrink(),
      ),
    );
  }

  String _initials(String name) {
    if (name.isEmpty) return '?';
    final parts = name.trim().split(RegExp(r'\s+'));
    if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
    return (parts.first.substring(0, 1) + parts.last.substring(0, 1))
        .toUpperCase();
  }
}

// ─── Quick action chips ────────────────────────────────────────────────

class _QuickActionChips extends StatelessWidget {
  const _QuickActionChips();

  static const _actions = <_QuickAction>[
    _QuickAction(
      label: 'Recharge',
      icon: Icons.smartphone,
      route: '/billpay/recharge',
      color: AppColors.posttubePrimary,
    ),
    _QuickAction(
      label: 'Pay bill',
      icon: Icons.receipt_long,
      route: '/billpay',
      color: AppColors.statusSuccess,
    ),
    _QuickAction(
      label: 'Send money',
      icon: Icons.send,
      route: '/wallet/send',
      color: AppColors.postbookPrimary,
    ),
    _QuickAction(
      label: 'Pulse',
      icon: Icons.favorite_border,
      route: '/pulse',
      color: AppColors.postgramPrimary,
    ),
    _QuickAction(
      label: 'Sell',
      icon: Icons.local_offer,
      route: '/seller/listings/new',
      color: AppColors.statusWarning,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 44,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        itemCount: _actions.length,
        separatorBuilder: (_, _) => const SizedBox(width: 8),
        itemBuilder: (context, i) {
          final a = _actions[i];
          return _ChipButton(action: a);
        },
      ),
    );
  }
}

class _QuickAction {
  const _QuickAction({
    required this.label,
    required this.icon,
    required this.route,
    required this.color,
  });

  final String label;
  final IconData icon;
  final String route;
  final Color color;
}

class _ChipButton extends StatelessWidget {
  const _ChipButton({required this.action});

  final _QuickAction action;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(99),
        onTap: () => context.push(action.route),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: action.color.withValues(alpha: 0.15),
            borderRadius: BorderRadius.circular(99),
            border: Border.all(color: action.color.withValues(alpha: 0.35)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(action.icon, color: action.color, size: 16),
              const SizedBox(width: 6),
              Text(
                action.label,
                style: AppTextStyles.label.copyWith(color: action.color),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Reels-in-feed carousel ────────────────────────────────────────────

class _ReelsRow extends StatelessWidget {
  const _ReelsRow({required this.reels});

  final List<Post> reels;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 8),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(
                Icons.movie_filter,
                color: AppColors.postgramPrimary,
                size: 18,
              ),
              const SizedBox(width: 8),
              Text('Reels for you', style: AppTextStyles.h3),
              const Spacer(),
              TextButton(
                onPressed: () => context.push('/reels'),
                style: TextButton.styleFrom(
                  padding: EdgeInsets.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(
                  'See all',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.postgramPrimary,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          SizedBox(
            height: 180,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              itemCount: reels.length,
              separatorBuilder: (_, _) => const SizedBox(width: 8),
              itemBuilder: (context, i) {
                return GestureDetector(
                  onTap: () => context.push('/reels'),
                  child: AspectRatio(
                    aspectRatio: 9 / 16,
                    child: Container(
                      decoration: BoxDecoration(
                        gradient: AppColors.postgramGradient,
                        borderRadius: BorderRadius.circular(
                          AppSpacing.radiusMedium,
                        ),
                      ),
                      child: const Center(
                        child: Icon(
                          Icons.play_circle_filled,
                          color: Colors.white,
                          size: 32,
                        ),
                      ),
                    ),
                  ),
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Sponsored product card ────────────────────────────────────────────

class _SponsoredProductCard extends StatelessWidget {
  const _SponsoredProductCard({required this.product});

  final Product product;

  @override
  Widget build(BuildContext context) {
    final price = product.basePrice.toStringAsFixed(0);
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusWarning.withValues(alpha: 0.3)),
      ),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          onTap: () => context.push('/commerce/product/${product.id}'),
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Row(
              children: [
                Container(
                  width: 64,
                  height: 64,
                  decoration: BoxDecoration(
                    color: AppColors.bgTertiary,
                    borderRadius: BorderRadius.circular(
                      AppSpacing.radiusMedium,
                    ),
                  ),
                  child: const Icon(
                    Icons.shopping_bag,
                    color: AppColors.statusWarning,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        'Sponsored',
                        style: AppTextStyles.labelSmall.copyWith(
                          color: AppColors.statusWarning,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        product.title,
                        style: AppTextStyles.h3,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                      const SizedBox(height: 4),
                      Text(
                        '${product.currency} $price',
                        style: AppTextStyles.label.copyWith(
                          color: AppColors.textPrimary,
                        ),
                      ),
                    ],
                  ),
                ),
                const Icon(Icons.chevron_right, color: AppColors.textDim),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

// ─── Q&A "Top question this week" teaser ───────────────────────────────

class _TopQuestionTeaser extends ConsumerWidget {
  const _TopQuestionTeaser();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncQuestions =
        ref.watch(qaSearchProvider(const QaSearchParams(query: 'top')));
    return asyncQuestions.when(
      data: (list) {
        if (list.isEmpty) return const SizedBox.shrink();
        final q = list.first;
        return _QuestionTeaserCard(question: q);
      },
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
    );
  }
}

class _QuestionTeaserCard extends StatelessWidget {
  const _QuestionTeaserCard({required this.question});

  final Question question;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(
          color: AppColors.posttubePrimary.withValues(alpha: 0.3),
        ),
      ),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          onTap: () => context.push('/qa/question/${question.id}'),
          child: Padding(
            padding: const EdgeInsets.all(14),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    const Icon(
                      Icons.help_outline,
                      color: AppColors.posttubePrimary,
                      size: 18,
                    ),
                    const SizedBox(width: 6),
                    Text(
                      'Top question this week',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.posttubePrimary,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(
                  question.title,
                  style: AppTextStyles.h3,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    const Icon(
                      Icons.chat_bubble_outline,
                      size: 14,
                      color: AppColors.textTertiary,
                    ),
                    const SizedBox(width: 4),
                    Text(
                      '${question.answerCount} answers',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

// ─── Empty state ───────────────────────────────────────────────────────

class _EmptyHome extends StatelessWidget {
  const _EmptyHome();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(36),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(
            Icons.feed_outlined,
            color: AppColors.textDim,
            size: 48,
          ),
          const SizedBox(height: 12),
          Text('Your feed is quiet', style: AppTextStyles.h2),
          const SizedBox(height: 4),
          Text(
            'Follow some people, or pull to refresh.',
            style: AppTextStyles.body,
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }
}
