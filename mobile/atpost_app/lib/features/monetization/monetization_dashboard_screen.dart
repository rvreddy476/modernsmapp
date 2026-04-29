import 'dart:math';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/monetization.dart';
import 'package:atpost_app/providers/monetization_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_animate/flutter_animate.dart';

/// A modern, elegant Monetization Dashboard for creators.
/// Features: High-performance charting, optimistic UI, and glassmorphism styling.
class MonetizationDashboardScreen extends ConsumerWidget {
  const MonetizationDashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(monetizationProvider);

    return Scaffold(
      backgroundColor: Colors.black,
      body: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [Color(0xFF0F111A), Color(0xFF090A11), Color(0xFF1A1D2E)],
          ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              _buildHeader(context),
              Expanded(
                child: RefreshIndicator(
                  onRefresh: () =>
                      ref.read(monetizationProvider.notifier).refresh(),
                  color: AppColors.postbookPrimary,
                  child: state.when(
                    loading: () => const Center(
                      child: CircularProgressIndicator(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                    error: (e, _) => _buildErrorState(ref),
                    data: (data) => _buildDashboard(context, data),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back_ios_new,
            tooltip: 'Back',
            onPressed: () => context.pop(),
          ),
          const SizedBox(width: 12),
          Text('Creator Studio', style: AppTextStyles.h1),
          const Spacer(),
          const GlassIconButton(icon: Icons.help_outline, tooltip: 'Help'),
        ],
      ),
    );
  }

  Widget _buildDashboard(BuildContext context, MonetizationState data) {
    return SingleChildScrollView(
      physics: const BouncingScrollPhysics(),
      padding: const EdgeInsets.symmetric(horizontal: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _buildEarningsSummary(data.earnings),
          const SizedBox(height: 24),
          _buildChartSection(data.history),
          const SizedBox(height: 24),
          _buildQuickActions(context),
          const SizedBox(height: 24),
          _buildRecentActivity(data.payouts),
          const SizedBox(height: 100), // Space for bottom scroll
        ],
      ),
    );
  }

  Widget _buildEarningsSummary(EarningsSummary earnings) {
    return Row(
      children: [
        Expanded(
          child: _GlassStatCard(
            label: 'Earnings',
            value: earnings.formattedThisMonth,
            color: AppColors.postbookPrimary,
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: _GlassStatCard(
            label: 'Pending',
            value: earnings.formattedPending,
            color: AppColors.posttubePrimary,
          ),
        ),
      ],
    ).animate().fadeIn().slideY(begin: 0.1, end: 0);
  }

  Widget _buildChartSection(List<Map<String, dynamic>> history) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Revenue Growth', style: AppTextStyles.h2),
        const SizedBox(height: 16),
        RepaintBoundary(
          child: Container(
            height: 220,
            padding: const EdgeInsets.all(20),
            decoration: BoxDecoration(
              color: Colors.white.withValues(alpha: 0.03),
              borderRadius: BorderRadius.circular(24),
              border: Border.all(color: Colors.white.withValues(alpha: 0.05)),
            ),
            child: _EarningsLineChart(history: history),
          ),
        ),
      ],
    ).animate().fadeIn(delay: 200.ms);
  }

  Widget _buildQuickActions(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Quick Actions', style: AppTextStyles.h2),
        const SizedBox(height: 16),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            _ActionPill(
              icon: Icons.layers_outlined,
              label: 'Tiers',
              onTap: () => context.push('/monetization/tiers'),
            ),
            _ActionPill(
              icon: Icons.insights,
              label: 'Analytics',
              onTap: () => context.push('/monetization/analytics'),
            ),
            _ActionPill(
              icon: Icons.account_balance_wallet,
              label: 'Withdraw',
              onTap: () => context.push('/monetization/payouts'),
            ),
          ],
        ),
      ],
    );
  }

  Widget _buildRecentActivity(List<PayoutRecord> payouts) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Recent Payouts', style: AppTextStyles.h2),
        const SizedBox(height: 16),
        if (payouts.isEmpty)
          Center(
            child: Text(
              'No recent payouts',
              style: AppTextStyles.bodySmall.copyWith(color: Colors.white24),
            ),
          )
        else
          ...payouts.take(3).map((p) => _PayoutGlassTile(payout: p)),
      ],
    );
  }

  Widget _buildErrorState(WidgetRef ref) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, color: Colors.redAccent, size: 40),
          const SizedBox(height: 16),
          Text('Failed to load dashboard', style: AppTextStyles.body),
          TextButton(
            onPressed: () => ref.read(monetizationProvider.notifier).refresh(),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}

class _GlassStatCard extends StatelessWidget {
  final String label;
  final String value;
  final Color color;

  const _GlassStatCard({
    required this.label,
    required this.value,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.03),
        borderRadius: BorderRadius.circular(24),
        border: Border.all(color: Colors.white.withValues(alpha: 0.05)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(color: Colors.white38),
          ),
          const SizedBox(height: 8),
          Text(value, style: AppTextStyles.h1.copyWith(color: Colors.white)),
          const SizedBox(height: 12),
          Container(
            height: 2,
            width: 30,
            decoration: BoxDecoration(
              color: color,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        ],
      ),
    );
  }
}

class _ActionPill extends StatelessWidget {
  final IconData icon;
  final String label;
  final VoidCallback onTap;

  const _ActionPill({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Column(
        children: [
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: Colors.white.withValues(alpha: 0.05),
              shape: BoxShape.circle,
              border: Border.all(color: Colors.white.withValues(alpha: 0.08)),
            ),
            child: Icon(icon, color: Colors.white70, size: 24),
          ),
          const SizedBox(height: 8),
          Text(
            label,
            style: AppTextStyles.labelTiny.copyWith(color: Colors.white38),
          ),
        ],
      ),
    );
  }
}

class _PayoutGlassTile extends StatelessWidget {
  final PayoutRecord payout;
  const _PayoutGlassTile({required this.payout});

  @override
  Widget build(BuildContext context) {
    final isCompleted = payout.status == 'completed';
    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.02),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: Colors.white.withValues(alpha: 0.05)),
      ),
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: (isCompleted ? Colors.green : Colors.amber).withValues(alpha: 
                0.1,
              ),
              borderRadius: BorderRadius.circular(12),
            ),
            child: Icon(
              isCompleted ? Icons.check : Icons.access_time,
              color: isCompleted ? Colors.green : Colors.amber,
              size: 18,
            ),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Payout ID: ${payout.id.substring(0, min(8, payout.id.length))}',
                  style: AppTextStyles.label.copyWith(color: Colors.white70),
                ),
                Text(
                  payout.status.toUpperCase(),
                  style: AppTextStyles.labelTiny.copyWith(
                    color: Colors.white24,
                  ),
                ),
              ],
            ),
          ),
          Text(
            '₹${payout.amount.toStringAsFixed(0)}',
            style: AppTextStyles.h3.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _EarningsLineChart extends StatelessWidget {
  final List<Map<String, dynamic>> history;
  const _EarningsLineChart({required this.history});

  @override
  Widget build(BuildContext context) {
    if (history.isEmpty) {
      return const Center(
        child: Text('No data yet', style: TextStyle(color: Colors.white24)),
      );
    }

    final spots = history.asMap().entries.map((e) {
      final views = (e.value['views'] as num? ?? 0).toDouble();
      return FlSpot(e.key.toDouble(), views);
    }).toList();

    return LineChart(
      LineChartData(
        gridData: const FlGridData(show: false),
        titlesData: const FlTitlesData(show: false),
        borderData: FlBorderData(show: false),
        lineBarsData: [
          LineChartBarData(
            spots: spots,
            isCurved: true,
            color: AppColors.postbookPrimary,
            barWidth: 3,
            isStrokeCapRound: true,
            dotData: const FlDotData(show: false),
            belowBarData: BarAreaData(
              show: true,
              gradient: LinearGradient(
                begin: Alignment.topCenter,
                end: Alignment.bottomCenter,
                colors: [
                  AppColors.postbookPrimary.withValues(alpha: 0.2),
                  AppColors.postbookPrimary.withValues(alpha: 0),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
