import 'dart:math';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/monetization.dart';
import 'package:atpost_app/providers/monetization_provider.dart';
import 'package:fl_chart/fl_chart.dart';
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
  String _period = '7d';

  static const List<String> _periods = ['7d', '30d', '90d'];

  @override
  Widget build(BuildContext context) {
    final analyticsAsync = ref.watch(creatorAnalyticsProvider(_period));

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
          analyticsAsync.when(
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
                          ref.invalidate(creatorAnalyticsProvider(_period)),
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
            data: (analytics) {
              return SliverPadding(
                padding: AppSpacing.pagePadding
                    .copyWith(top: 0, bottom: 32),
                sliver: SliverList(
                  delegate: SliverChildListDelegate([
                    // Summary cards row 1
                    Row(
                      children: [
                        Expanded(
                          child: _SummaryCard(
                            label: 'Total Views',
                            value: _formatNumber(analytics.views),
                            icon: Icons.visibility_outlined,
                            color: AppColors.posttubePrimary,
                          ),
                        ),
                        const SizedBox(width: 10),
                        Expanded(
                          child: _SummaryCard(
                            label: 'Total Likes',
                            value: _formatNumber(analytics.likes),
                            icon: Icons.favorite_outline,
                            color: AppColors.liveRed,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 10),

                    // Summary cards row 2
                    Row(
                      children: [
                        Expanded(
                          child: _SummaryCard(
                            label: 'Comments',
                            value: _formatNumber(analytics.comments),
                            icon: Icons.chat_bubble_outline,
                            color: AppColors.accentPurple,
                          ),
                        ),
                        const SizedBox(width: 10),
                        Expanded(
                          child: _SummaryCard(
                            label: 'Followers Gained',
                            value: _formatNumber(analytics.followersGained),
                            icon: Icons.person_add_outlined,
                            color: AppColors.postbookPrimary,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 24),

                    // Views over time - Line Chart
                    Text('Views Over Time', style: AppTextStyles.h3),
                    const SizedBox(height: 12),
                    _ViewsLineChart(dailyStats: analytics.dailyStats),
                    const SizedBox(height: 24),

                    // Engagement breakdown - Bar Chart
                    Text('Engagement Breakdown', style: AppTextStyles.h3),
                    const SizedBox(height: 12),
                    _EngagementBarChart(
                      likes: analytics.likes,
                      comments: analytics.comments,
                      shares: analytics.shares,
                    ),
                    const SizedBox(height: 24),

                    // Top Posts section
                    Text('Top Posts', style: AppTextStyles.h3),
                    const SizedBox(height: 12),

                    if (analytics.topPosts.isEmpty)
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
                      ...analytics.topPosts.asMap().entries.map((entry) {
                        final i = entry.key;
                        final post = entry.value;
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

  String _formatNumber(int value) {
    if (value >= 1000000) {
      return '${(value / 1000000).toStringAsFixed(1)}M';
    } else if (value >= 1000) {
      return '${(value / 1000).toStringAsFixed(1)}K';
    }
    return value.toString();
  }
}

// ---------- Summary Card ----------

class _SummaryCard extends StatelessWidget {
  final String label;
  final String value;
  final IconData icon;
  final Color color;

  const _SummaryCard({
    required this.label,
    required this.value,
    required this.icon,
    required this.color,
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

// ---------- Views Line Chart ----------

class _ViewsLineChart extends StatelessWidget {
  final List<DailyStat> dailyStats;

  const _ViewsLineChart({required this.dailyStats});

  @override
  Widget build(BuildContext context) {
    if (dailyStats.isEmpty) {
      return Container(
        height: 200,
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Center(
          child: Text('No daily data available', style: AppTextStyles.bodySmall),
        ),
      );
    }

    final spots = dailyStats.asMap().entries.map((entry) {
      return FlSpot(entry.key.toDouble(), entry.value.views.toDouble());
    }).toList();

    final maxY = dailyStats.isEmpty
        ? 0.0
        : dailyStats.map((s) => s.views).reduce(max).toDouble();
    final yMax = maxY == 0 ? 100.0 : maxY * 1.2;

    return Container(
      height: 220,
      padding: const EdgeInsets.fromLTRB(8, 16, 16, 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: LineChart(
        LineChartData(
          minY: 0,
          maxY: yMax,
          gridData: FlGridData(
            show: true,
            drawVerticalLine: false,
            horizontalInterval: yMax / 4,
            getDrawingHorizontalLine: (value) => FlLine(
              color: AppColors.borderSubtle,
              strokeWidth: 0.5,
            ),
          ),
          titlesData: FlTitlesData(
            topTitles: const AxisTitles(
              sideTitles: SideTitles(showTitles: false),
            ),
            rightTitles: const AxisTitles(
              sideTitles: SideTitles(showTitles: false),
            ),
            leftTitles: AxisTitles(
              sideTitles: SideTitles(
                showTitles: true,
                reservedSize: 40,
                getTitlesWidget: (value, meta) {
                  if (value == meta.min || value == meta.max) {
                    return const SizedBox.shrink();
                  }
                  return Padding(
                    padding: const EdgeInsets.only(right: 4),
                    child: Text(
                      _shortNumber(value.toInt()),
                      style: AppTextStyles.labelTiny,
                    ),
                  );
                },
              ),
            ),
            bottomTitles: AxisTitles(
              sideTitles: SideTitles(
                showTitles: true,
                reservedSize: 28,
                interval: _bottomInterval(),
                getTitlesWidget: (value, meta) {
                  final idx = value.toInt();
                  if (idx < 0 || idx >= dailyStats.length) {
                    return const SizedBox.shrink();
                  }
                  final date = dailyStats[idx].date;
                  final label = date.length >= 10
                      ? '${date.substring(8, 10)}/${date.substring(5, 7)}'
                      : date;
                  return Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text(label, style: AppTextStyles.labelTiny),
                  );
                },
              ),
            ),
          ),
          borderData: FlBorderData(show: false),
          lineBarsData: [
            LineChartBarData(
              spots: spots,
              isCurved: true,
              curveSmoothness: 0.3,
              color: AppColors.posttubePrimary,
              barWidth: 2.5,
              isStrokeCapRound: true,
              dotData: FlDotData(
                show: spots.length <= 14,
                getDotPainter: (spot, percent, barData, index) =>
                    FlDotCirclePainter(
                  radius: 3,
                  color: AppColors.posttubePrimary,
                  strokeWidth: 1.5,
                  strokeColor: AppColors.bgPrimary,
                ),
              ),
              belowBarData: BarAreaData(
                show: true,
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [
                    AppColors.posttubePrimary.withValues(alpha: 0.25),
                    AppColors.posttubePrimary.withValues(alpha: 0.0),
                  ],
                ),
              ),
            ),
          ],
          lineTouchData: LineTouchData(
            touchTooltipData: LineTouchTooltipData(
              getTooltipColor: (_) => AppColors.bgSecondary,
              getTooltipItems: (touchedSpots) {
                return touchedSpots.map((spot) {
                  final idx = spot.x.toInt();
                  final dateLabel = idx < dailyStats.length
                      ? dailyStats[idx].date
                      : '';
                  return LineTooltipItem(
                    '${spot.y.toInt()} views\n$dateLabel',
                    AppTextStyles.labelSmall.copyWith(
                      color: AppColors.posttubePrimary,
                    ),
                  );
                }).toList();
              },
            ),
          ),
        ),
      ),
    );
  }

  double _bottomInterval() {
    if (dailyStats.length <= 7) return 1;
    if (dailyStats.length <= 14) return 2;
    return (dailyStats.length / 6).ceilToDouble();
  }

  String _shortNumber(int value) {
    if (value >= 1000) return '${(value / 1000).toStringAsFixed(1)}K';
    return value.toString();
  }
}

// ---------- Engagement Bar Chart ----------

class _EngagementBarChart extends StatelessWidget {
  final int likes;
  final int comments;
  final int shares;

  const _EngagementBarChart({
    required this.likes,
    required this.comments,
    required this.shares,
  });

  @override
  Widget build(BuildContext context) {
    final maxVal = [likes, comments, shares].reduce(max).toDouble();
    final yMax = maxVal == 0 ? 100.0 : maxVal * 1.3;

    return Container(
      height: 200,
      padding: const EdgeInsets.fromLTRB(8, 16, 16, 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: BarChart(
        BarChartData(
          maxY: yMax,
          barTouchData: BarTouchData(
            touchTooltipData: BarTouchTooltipData(
              getTooltipColor: (_) => AppColors.bgSecondary,
              getTooltipItem: (group, groupIndex, rod, rodIndex) {
                final labels = ['Likes', 'Comments', 'Shares'];
                return BarTooltipItem(
                  '${labels[groupIndex]}\n${rod.toY.toInt()}',
                  AppTextStyles.labelSmall.copyWith(
                    color: rod.color ?? AppColors.textPrimary,
                  ),
                );
              },
            ),
          ),
          gridData: FlGridData(
            show: true,
            drawVerticalLine: false,
            horizontalInterval: yMax / 4,
            getDrawingHorizontalLine: (value) => FlLine(
              color: AppColors.borderSubtle,
              strokeWidth: 0.5,
            ),
          ),
          borderData: FlBorderData(show: false),
          titlesData: FlTitlesData(
            topTitles: const AxisTitles(
              sideTitles: SideTitles(showTitles: false),
            ),
            rightTitles: const AxisTitles(
              sideTitles: SideTitles(showTitles: false),
            ),
            leftTitles: AxisTitles(
              sideTitles: SideTitles(
                showTitles: true,
                reservedSize: 40,
                getTitlesWidget: (value, meta) {
                  if (value == meta.min || value == meta.max) {
                    return const SizedBox.shrink();
                  }
                  return Padding(
                    padding: const EdgeInsets.only(right: 4),
                    child: Text(
                      value.toInt().toString(),
                      style: AppTextStyles.labelTiny,
                    ),
                  );
                },
              ),
            ),
            bottomTitles: AxisTitles(
              sideTitles: SideTitles(
                showTitles: true,
                reservedSize: 28,
                getTitlesWidget: (value, meta) {
                  const labels = ['Likes', 'Comments', 'Shares'];
                  final idx = value.toInt();
                  if (idx < 0 || idx >= labels.length) {
                    return const SizedBox.shrink();
                  }
                  return Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text(labels[idx], style: AppTextStyles.labelSmall),
                  );
                },
              ),
            ),
          ),
          barGroups: [
            _makeBarGroup(0, likes.toDouble(), AppColors.liveRed),
            _makeBarGroup(1, comments.toDouble(), AppColors.accentPurple),
            _makeBarGroup(2, shares.toDouble(), AppColors.posttubePrimary),
          ],
        ),
      ),
    );
  }

  BarChartGroupData _makeBarGroup(int x, double y, Color color) {
    return BarChartGroupData(
      x: x,
      barRods: [
        BarChartRodData(
          toY: y,
          color: color,
          width: 28,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(6),
            topRight: Radius.circular(6),
          ),
          backDrawRodData: BackgroundBarChartRodData(
            show: true,
            color: color.withValues(alpha: 0.08),
          ),
        ),
      ],
    );
  }
}

// ---------- Top Post Item ----------

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
