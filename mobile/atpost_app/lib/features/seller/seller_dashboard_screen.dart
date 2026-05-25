// Seller dashboard home — entry point for the mobile seller surface.
//
// Mirrors the postbook-ui /seller/dashboard page in shape. Loads the
// seller's profile and stats; renders an onboarding-required state
// when the user hasn't completed the seller signup flow yet.
//
// Stats are read-only here — the action surfaces (products, variants)
// live on dedicated pages reachable via the cards in the Manage
// section.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SellerDashboardScreen extends ConsumerWidget {
  const SellerDashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final profileAsync = ref.watch(mySellerProfileProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Seller dashboard', style: AppTextStyles.h2),
      ),
      body: profileAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => _errorState(context, '$e'),
        data: (profile) {
          if (profile == null) {
            return const _OnboardingRequired();
          }
          return _DashboardBody(profile: profile);
        },
      ),
    );
  }

  Widget _errorState(BuildContext context, String message) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(AppSpacing.xxl),
        child: Text(
          'Could not load your seller profile.\n$message',
          textAlign: TextAlign.center,
          style: AppTextStyles.body,
        ),
      ),
    );
  }
}

class _OnboardingRequired extends StatelessWidget {
  const _OnboardingRequired();

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      children: [
        const SizedBox(height: 80),
        const Icon(Icons.storefront_outlined,
            size: 56, color: AppColors.textGhost),
        const SizedBox(height: AppSpacing.l),
        Center(
          child: Text('Become a seller', style: AppTextStyles.h2),
        ),
        const SizedBox(height: AppSpacing.s),
        Center(
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
            child: Text(
              'You need to complete seller onboarding before you can manage products on mobile. Onboarding is currently web-only.',
              textAlign: TextAlign.center,
              style: AppTextStyles.body,
            ),
          ),
        ),
      ],
    );
  }
}

class _DashboardBody extends ConsumerWidget {
  const _DashboardBody({required this.profile});

  final SellerProfile profile;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final statsAsync = ref.watch(sellerDashboardProvider);
    return RefreshIndicator(
      onRefresh: () async {
        ref.invalidate(sellerDashboardProvider);
        ref.invalidate(mySellerProductsProvider);
        await ref.read(sellerDashboardProvider.future);
      },
      color: AppColors.postbookPrimary,
      child: ListView(
        padding: const EdgeInsets.all(AppSpacing.l),
        children: [
          _StoreCard(profile: profile),
          const SizedBox(height: AppSpacing.l),
          Text('Today', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.s),
          statsAsync.when(
            loading: () => const _StatsSkeleton(),
            error: (_, _) => Container(
              padding: const EdgeInsets.all(AppSpacing.l),
              decoration: _cardDeco(),
              child: Text(
                'Stats unavailable. Pull to retry.',
                style: AppTextStyles.body,
              ),
            ),
            data: (stats) => _StatsGrid(stats: stats),
          ),
          const SizedBox(height: AppSpacing.xxl),
          Text('Manage', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.s),
          _NavTile(
            icon: Icons.inventory_2_outlined,
            label: 'Products',
            subtitle: 'View + edit your catalog',
            onTap: () => GoRouter.of(context).push('/seller/products'),
          ),
          const SizedBox(height: AppSpacing.s),
          _NavTile(
            icon: Icons.receipt_long_outlined,
            label: 'Orders',
            subtitle: 'Fulfillment queue with status filters',
            onTap: () => GoRouter.of(context).push('/seller/orders'),
          ),
          const SizedBox(height: AppSpacing.s),
          _NavTile(
            icon: Icons.assignment_return_outlined,
            label: 'Returns',
            subtitle: 'Pending returns + refund status',
            onTap: () => GoRouter.of(context).push('/seller/returns'),
          ),
          const SizedBox(height: AppSpacing.s),
          _NavTile(
            icon: Icons.account_balance_wallet_outlined,
            label: 'Earnings',
            subtitle: 'Prepaid + COD payout ledger',
            onTap: () => GoRouter.of(context).push('/seller/earnings'),
          ),
          const SizedBox(height: AppSpacing.s),
          _NavTile(
            icon: Icons.upload_file_outlined,
            label: 'Bulk import',
            subtitle: 'Monitor + execute CSV upload jobs',
            onTap: () => GoRouter.of(context).push('/seller/bulk-import'),
          ),
        ],
      ),
    );
  }
}

class _StoreCard extends StatelessWidget {
  const _StoreCard({required this.profile});

  final SellerProfile profile;

  @override
  Widget build(BuildContext context) {
    final approved = profile.status == 'approved';
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: _cardDeco(),
      child: Row(
        children: [
          Container(
            width: 48,
            height: 48,
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            child: const Icon(Icons.storefront,
                color: AppColors.postbookPrimary, size: 24),
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(profile.storeName, style: AppTextStyles.h3),
                const SizedBox(height: 2),
                Row(
                  children: [
                    _StatusChip(status: profile.status),
                    const SizedBox(width: AppSpacing.s),
                    if (!approved)
                      Text(
                        'Restrictions apply',
                        style: AppTextStyles.bodySmall.copyWith(
                          color: const Color(0xFF92400E),
                        ),
                      ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    Color bg;
    Color fg;
    switch (status) {
      case 'approved':
        bg = const Color(0xFFD1FAE5);
        fg = const Color(0xFF047857);
        break;
      case 'suspended':
      case 'rejected':
        bg = const Color(0xFFFFE4E6);
        fg = const Color(0xFFB91C1C);
        break;
      default:
        bg = const Color(0xFFFEF3C7);
        fg = const Color(0xFF92400E);
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        status.toUpperCase(),
        style: AppTextStyles.labelTiny.copyWith(
          color: fg,
          fontWeight: FontWeight.w800,
          letterSpacing: 0.5,
        ),
      ),
    );
  }
}

class _StatsGrid extends StatelessWidget {
  const _StatsGrid({required this.stats});

  final SellerDashboardStats stats;

  @override
  Widget build(BuildContext context) {
    final cells = <_StatCell>[
      _StatCell(label: 'Orders today', value: '${stats.ordersToday}'),
      _StatCell(label: 'Revenue', value: 'Rs. ${stats.revenueTotal.toStringAsFixed(0)}'),
      _StatCell(label: 'Live', value: '${stats.liveProducts}'),
      _StatCell(label: 'Drafts', value: '${stats.draftProducts}'),
      _StatCell(label: 'Pending', value: '${stats.pendingProducts}'),
      _StatCell(label: 'Low stock', value: '${stats.lowStockItems}'),
    ];
    return GridView.count(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      crossAxisCount: 2,
      mainAxisSpacing: AppSpacing.s,
      crossAxisSpacing: AppSpacing.s,
      childAspectRatio: 2.6,
      children: cells
          .map((c) => Container(
                padding: const EdgeInsets.all(AppSpacing.m),
                decoration: _cardDeco(),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Text(c.label, style: AppTextStyles.bodySmall),
                    const SizedBox(height: 2),
                    Text(c.value, style: AppTextStyles.h3),
                  ],
                ),
              ))
          .toList(),
    );
  }
}

class _StatCell {
  const _StatCell({required this.label, required this.value});
  final String label;
  final String value;
}

class _StatsSkeleton extends StatelessWidget {
  const _StatsSkeleton();

  @override
  Widget build(BuildContext context) {
    return GridView.count(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      crossAxisCount: 2,
      mainAxisSpacing: AppSpacing.s,
      crossAxisSpacing: AppSpacing.s,
      childAspectRatio: 2.6,
      children: List.generate(
        6,
        (_) => Container(
          decoration: _cardDeco(),
          child: const Center(
            child: SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(
                strokeWidth: 2,
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _NavTile extends StatelessWidget {
  const _NavTile({
    required this.icon,
    required this.label,
    required this.subtitle,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final String subtitle;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: _cardDeco(),
        child: Row(
          children: [
            Container(
              width: 40,
              height: 40,
              decoration: BoxDecoration(
                color: AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
              ),
              child: Icon(icon, color: AppColors.postbookPrimary, size: 20),
            ),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(label, style: AppTextStyles.label),
                  const SizedBox(height: 2),
                  Text(subtitle, style: AppTextStyles.bodySmall),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, color: AppColors.textTertiary),
          ],
        ),
      ),
    );
  }
}

BoxDecoration _cardDeco() => BoxDecoration(
      color: AppColors.bgCard,
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      border: Border.all(color: AppColors.borderSubtle),
    );
