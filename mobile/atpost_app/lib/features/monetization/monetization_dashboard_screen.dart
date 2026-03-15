import 'dart:math';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/monetization_provider.dart';
import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class MonetizationDashboardScreen extends ConsumerWidget {
  const MonetizationDashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final earningsAsync = ref.watch(earningsSummaryProvider);
    final payoutsAsync = ref.watch(payoutsProvider);
    final earningsHistoryAsync = ref.watch(earningsHistoryProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new,
              color: AppColors.textPrimary, size: 20),
          onPressed: () => context.pop(),
        ),
        title: Text('Creator Studio', style: AppTextStyles.h2),
        centerTitle: false,
      ),
      body: SingleChildScrollView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 32),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Earnings Summary Cards
            earningsAsync.when(
              loading: () => const Center(
                child: Padding(
                  padding: EdgeInsets.all(32),
                  child: CircularProgressIndicator(),
                ),
              ),
              error: (_, _) => _ErrorCard(
                message: 'Could not load earnings. Tap to retry.',
                onRetry: () => ref.invalidate(earningsSummaryProvider),
              ),
              data: (earnings) => Row(
                children: [
                  Expanded(
                    child: _StatCard(
                      label: 'This Month',
                      value: earnings['earnings_this_month']?.toString() ??
                          '\u20b90',
                      gradient: AppColors.postbookGradient,
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: _StatCard(
                      label: 'Subscribers',
                      value:
                          earnings['total_subscribers']?.toString() ?? '0',
                      gradient: const LinearGradient(
                        colors: [AppColors.accentPurple, Color(0xFF5B4FCF)],
                      ),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: _StatCard(
                      label: 'Pending',
                      value:
                          earnings['pending_payout']?.toString() ?? '\u20b90',
                      gradient: AppColors.posttubeGradient,
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 24),

            // Earnings Trend Chart
            Text('Earnings Trend (30 days)', style: AppTextStyles.h3),
            const SizedBox(height: 12),
            earningsHistoryAsync.when(
              loading: () => Container(
                height: 200,
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusLarge),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: const Center(child: CircularProgressIndicator()),
              ),
              error: (_, _) => Container(
                height: 200,
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusLarge),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Center(
                  child: Text('Could not load chart data',
                      style: AppTextStyles.bodySmall),
                ),
              ),
              data: (history) => _EarningsLineChart(history: history),
            ),
            const SizedBox(height: 24),

            // Quick Actions
            Text('Quick Actions', style: AppTextStyles.h3),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: _ActionCard(
                    icon: Icons.layers_outlined,
                    label: 'Manage Tiers',
                    onTap: () => context.push('/monetization/tiers'),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: _ActionCard(
                    icon: Icons.bar_chart_outlined,
                    label: 'Analytics',
                    onTap: () => context.push('/monetization/analytics'),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: _ActionCard(
                    icon: Icons.account_balance_wallet_outlined,
                    label: 'Withdraw',
                    onTap: () => context.push('/monetization/payouts'),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 24),

            // Recent Activity
            Text('Recent Activity', style: AppTextStyles.h3),
            const SizedBox(height: 12),
            payoutsAsync.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (_, _) => _ErrorCard(
                message: 'Could not load data. Tap to retry.',
                onRetry: () => ref.invalidate(payoutsProvider),
              ),
              data: (payouts) {
                if (payouts.isEmpty) {
                  return Container(
                    padding: const EdgeInsets.all(24),
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusLarge),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: Center(
                      child: Text(
                        'No recent activity',
                        style: AppTextStyles.bodySmall,
                      ),
                    ),
                  );
                }
                final recent = payouts.take(5).toList();
                return Container(
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusLarge),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Column(
                    children: recent.asMap().entries.map((entry) {
                      final i = entry.key;
                      final payout = entry.value;
                      final isLast = i == recent.length - 1;
                      return Column(
                        children: [
                          _PayoutTile(payout: payout),
                          if (!isLast)
                            Divider(
                                color: AppColors.borderSubtle,
                                height: 1),
                        ],
                      );
                    }).toList(),
                  ),
                );
              },
            ),
          ],
        ),
      ),
    );
  }
}

// ---------- Earnings Line Chart ----------

class _EarningsLineChart extends StatelessWidget {
  final List<Map<String, dynamic>> history;

  const _EarningsLineChart({required this.history});

  @override
  Widget build(BuildContext context) {
    if (history.isEmpty) {
      return Container(
        height: 200,
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Center(
          child: Text('No earnings data yet', style: AppTextStyles.bodySmall),
        ),
      );
    }

    final spots = history.asMap().entries.map((entry) {
      final views = (entry.value['views'] as int? ?? 0).toDouble();
      return FlSpot(entry.key.toDouble(), views);
    }).toList();

    final maxY = spots.map((s) => s.y).reduce(max);
    final yMax = maxY == 0 ? 100.0 : maxY * 1.2;

    return Container(
      height: 200,
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
                interval: (history.length / 5).ceilToDouble(),
                getTitlesWidget: (value, meta) {
                  final idx = value.toInt();
                  if (idx < 0 || idx >= history.length) {
                    return const SizedBox.shrink();
                  }
                  final date = history[idx]['date']?.toString() ?? '';
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
              color: AppColors.postbookPrimary,
              barWidth: 2.5,
              isStrokeCapRound: true,
              dotData: FlDotData(
                show: spots.length <= 14,
                getDotPainter: (spot, percent, barData, index) =>
                    FlDotCirclePainter(
                  radius: 3,
                  color: AppColors.postbookPrimary,
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
                    AppColors.postbookPrimary.withValues(alpha: 0.25),
                    AppColors.postbookPrimary.withValues(alpha: 0.0),
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
                  final dateLabel = idx < history.length
                      ? (history[idx]['date']?.toString() ?? '')
                      : '';
                  return LineTooltipItem(
                    '${spot.y.toInt()} views\n$dateLabel',
                    AppTextStyles.labelSmall.copyWith(
                      color: AppColors.postbookPrimary,
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
}

// ---------- Supporting widgets (unchanged) ----------

class _StatCard extends StatelessWidget {
  final String label;
  final String value;
  final LinearGradient gradient;

  const _StatCard({
    required this.label,
    required this.value,
    required this.gradient,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
      decoration: BoxDecoration(
        gradient: gradient,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(
              color: Colors.white.withValues(alpha: 0.8),
            ),
          ),
          const SizedBox(height: 6),
          Text(
            value,
            style: AppTextStyles.h2.copyWith(color: Colors.white),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ],
      ),
    );
  }
}

class _ActionCard extends StatelessWidget {
  final IconData icon;
  final String label;
  final VoidCallback onTap;

  const _ActionCard({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(vertical: 16),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          children: [
            Icon(icon, color: AppColors.posttubePrimary, size: 24),
            const SizedBox(height: 6),
            Text(label, style: AppTextStyles.labelSmall, textAlign: TextAlign.center),
          ],
        ),
      ),
    );
  }
}

class _PayoutTile extends StatelessWidget {
  final Map<String, dynamic> payout;

  const _PayoutTile({required this.payout});

  @override
  Widget build(BuildContext context) {
    final amount = payout['amount']?.toString() ?? '0';
    final status = payout['status']?.toString() ?? 'pending';
    final date = payout['created_at']?.toString() ?? '';

    Color statusColor;
    switch (status.toLowerCase()) {
      case 'completed':
        statusColor = AppColors.onlineGreen;
        break;
      case 'failed':
        statusColor = AppColors.liveRed;
        break;
      default:
        statusColor = const Color(0xFFFFB347);
    }

    return ListTile(
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      leading: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        ),
        child: const Icon(
          Icons.account_balance_wallet_outlined,
          color: AppColors.posttubePrimary,
          size: 18,
        ),
      ),
      title: Text(
        'Payout \u20b9$amount',
        style: AppTextStyles.label,
      ),
      subtitle: date.isNotEmpty
          ? Text(date, style: AppTextStyles.labelTiny)
          : null,
      trailing: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: statusColor.withValues(alpha: 0.15),
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        ),
        child: Text(
          status,
          style: AppTextStyles.labelTiny.copyWith(color: statusColor),
        ),
      ),
    );
  }
}

class _ErrorCard extends StatelessWidget {
  final String message;
  final VoidCallback onRetry;

  const _ErrorCard({required this.message, required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Text(message, style: AppTextStyles.bodySmall),
          const SizedBox(height: 12),
          GestureDetector(
            onTap: onRetry,
            child: Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              decoration: BoxDecoration(
                gradient: AppColors.posttubeGradient,
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusLarge),
              ),
              child: Text(
                'Retry',
                style:
                    AppTextStyles.label.copyWith(color: Colors.white),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
