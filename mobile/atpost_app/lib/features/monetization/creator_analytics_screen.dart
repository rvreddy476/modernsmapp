import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/monetization_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CreatorAnalyticsScreen extends ConsumerStatefulWidget {
  const CreatorAnalyticsScreen({super.key});

  @override
  ConsumerState<CreatorAnalyticsScreen> createState() =>
      _CreatorAnalyticsScreenState();
}

class _CreatorAnalyticsScreenState
    extends ConsumerState<CreatorAnalyticsScreen> {
  String _period = '30d';

  static const List<String> _periods = ['7d', '30d', '90d'];

  @override
  Widget build(BuildContext context) {
    final statsAsync = ref.watch(creatorStatsProvider(_period));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new,
              color: AppColors.textPrimary, size: 20),
          onPressed: () => context.pop(),
        ),
        title: Text('Creator Analytics', style: AppTextStyles.h2),
        centerTitle: false,
      ),
      body: CustomScrollView(
        slivers: [
          // Period selector
          SliverToBoxAdapter(
            child: Padding(
              padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 0),
              child: Row(
                children: _periods.map((period) {
                  final selected = _period == period;
                  return Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: ChoiceChip(
                      label: Text(period),
                      selected: selected,
                      onSelected: (_) =>
                          setState(() => _period = period),
                      selectedColor: AppColors.posttubePrimary,
                      backgroundColor: AppColors.bgCard,
                      labelStyle: selected
                          ? AppTextStyles.label
                              .copyWith(color: Colors.white)
                          : AppTextStyles.label,
                      side: BorderSide(
                        color: selected
                            ? AppColors.posttubePrimary
                            : AppColors.borderSubtle,
                      ),
                    ),
                  );
                }).toList(),
              ),
            ),
          ),
          const SliverToBoxAdapter(child: SizedBox(height: 20)),

          // Stats content
          statsAsync.when(
            loading: () => const SliverToBoxAdapter(
              child: Center(
                child: Padding(
                  padding: EdgeInsets.all(48),
                  child: CircularProgressIndicator(),
                ),
              ),
            ),
            error: (_, _) => SliverToBoxAdapter(
              child: Padding(
                padding: AppSpacing.pagePadding,
                child: Column(
                  children: [
                    const Icon(Icons.error_outline,
                        color: AppColors.textMuted, size: 40),
                    const SizedBox(height: 12),
                    Text('Could not load analytics.',
                        style: AppTextStyles.bodySmall),
                    const SizedBox(height: 12),
                    GestureDetector(
                      onTap: () =>
                          ref.invalidate(creatorStatsProvider(_period)),
                      child: Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 16, vertical: 8),
                        decoration: BoxDecoration(
                          gradient: AppColors.posttubeGradient,
                          borderRadius: BorderRadius.circular(
                              AppSpacing.radiusLarge),
                        ),
                        child: Text('Retry',
                            style: AppTextStyles.label
                                .copyWith(color: Colors.white)),
                      ),
                    ),
                  ],
                ),
              ),
            ),
            data: (stats) {
              final views =
                  stats['total_views']?.toString() ?? '0';
              final likes =
                  stats['total_likes']?.toString() ?? '0';
              final comments =
                  stats['total_comments']?.toString() ?? '0';
              final growth =
                  stats['follower_growth']?.toString() ?? '0';
              final reach =
                  stats['reach_estimate']?.toString() ?? '0';
              final topPosts =
                  (stats['top_posts'] as List<dynamic>?) ?? [];

              return SliverPadding(
                padding: AppSpacing.pagePadding
                    .copyWith(top: 0, bottom: 32),
                sliver: SliverList(
                  delegate: SliverChildListDelegate([
                    // Row 1: Views + Likes
                    Row(
                      children: [
                        Expanded(
                          child: _MetricCard(
                            label: 'Total Views',
                            value: views,
                            icon: Icons.visibility_outlined,
                            color: AppColors.posttubePrimary,
                          ),
                        ),
                        const SizedBox(width: 10),
                        Expanded(
                          child: _MetricCard(
                            label: 'Total Likes',
                            value: likes,
                            icon: Icons.favorite_outline,
                            color: AppColors.liveRed,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 10),

                    // Row 2: Comments + Follower Growth
                    Row(
                      children: [
                        Expanded(
                          child: _MetricCard(
                            label: 'Comments',
                            value: comments,
                            icon: Icons.chat_bubble_outline,
                            color: AppColors.accentPurple,
                          ),
                        ),
                        const SizedBox(width: 10),
                        Expanded(
                          child: _MetricCard(
                            label: 'Follower Growth',
                            value: growth,
                            icon: Icons.trending_up,
                            color: AppColors.postbookPrimary,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 10),

                    // Row 3: Reach (full width)
                    _MetricCard(
                      label: 'Reach Estimate',
                      value: reach,
                      icon: Icons.radar,
                      color: AppColors.postgramPrimary,
                      fullWidth: true,
                    ),
                    const SizedBox(height: 24),

                    // Top Posts section
                    Text('Top Posts', style: AppTextStyles.h2),
                    const SizedBox(height: 12),

                    if (topPosts.isEmpty)
                      Container(
                        padding: const EdgeInsets.all(24),
                        decoration: BoxDecoration(
                          color: AppColors.bgCard,
                          borderRadius: BorderRadius.circular(
                              AppSpacing.radiusLarge),
                          border:
                              Border.all(color: AppColors.borderSubtle),
                        ),
                        child: Center(
                          child: Text(
                            'No post data available for this period.',
                            style: AppTextStyles.bodySmall,
                          ),
                        ),
                      )
                    else
                      ...topPosts.asMap().entries.map((entry) {
                        final i = entry.key;
                        final post = entry.value is Map
                            ? Map<String, dynamic>.from(
                                entry.value as Map)
                            : <String, dynamic>{};
                        return _TopPostItem(index: i + 1, post: post);
                      }),
                  ]),
                ),
              );
            },
          ),
        ],
      ),
    );
  }
}

class _MetricCard extends StatelessWidget {
  final String label;
  final String value;
  final IconData icon;
  final Color color;
  final bool fullWidth;

  const _MetricCard({
    required this.label,
    required this.value,
    required this.icon,
    required this.color,
    this.fullWidth = false,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 36,
            height: 36,
            decoration: BoxDecoration(
              color: color.withValues(alpha: 0.15),
              borderRadius:
                  BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            child: Icon(icon, color: color, size: 18),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(label, style: AppTextStyles.labelSmall),
                const SizedBox(height: 2),
                Text(value, style: AppTextStyles.h3),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _TopPostItem extends StatelessWidget {
  final int index;
  final Map<String, dynamic> post;

  const _TopPostItem({required this.index, required this.post});

  @override
  Widget build(BuildContext context) {
    final title = post['title']?.toString() ??
        post['content']?.toString() ??
        'Post #$index';
    final views = post['views']?.toString() ?? '0';
    final likes = post['likes']?.toString() ?? '0';

    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 28,
            height: 28,
            decoration: BoxDecoration(
              gradient: AppColors.posttubeGradient,
              borderRadius:
                  BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            child: Center(
              child: Text(
                '$index',
                style: AppTextStyles.labelSmall
                    .copyWith(color: Colors.white),
              ),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              title,
              style: AppTextStyles.label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const SizedBox(width: 8),
          Column(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Row(
                children: [
                  const Icon(Icons.visibility_outlined,
                      color: AppColors.textMuted, size: 12),
                  const SizedBox(width: 3),
                  Text(views, style: AppTextStyles.labelTiny),
                ],
              ),
              const SizedBox(height: 2),
              Row(
                children: [
                  const Icon(Icons.favorite_outline,
                      color: AppColors.textMuted, size: 12),
                  const SizedBox(width: 3),
                  Text(likes, style: AppTextStyles.labelTiny),
                ],
              ),
            ],
          ),
        ],
      ),
    );
  }
}
