// Seller earnings — two tabs: Prepaid (the SellerEarning ledger for
// delivered prepaid items) and COD (CODRemittance rows where the
// courier collected cash). Each row breaks out gross / commission /
// fee / TDS / net so the seller can reconcile against payouts.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SellerEarningsScreen extends ConsumerStatefulWidget {
  const SellerEarningsScreen({super.key});

  @override
  ConsumerState<SellerEarningsScreen> createState() =>
      _SellerEarningsScreenState();
}

class _SellerEarningsScreenState extends ConsumerState<SellerEarningsScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Earnings', style: AppTextStyles.h2),
        bottom: TabBar(
          controller: _tabs,
          labelColor: AppColors.postbookPrimary,
          unselectedLabelColor: AppColors.textSecondary,
          indicatorColor: AppColors.postbookPrimary,
          tabs: const [
            Tab(text: 'Prepaid'),
            Tab(text: 'COD'),
          ],
        ),
      ),
      body: TabBarView(
        controller: _tabs,
        children: const [
          _PrepaidEarningsTab(),
          _CODEarningsTab(),
        ],
      ),
    );
  }
}

class _PrepaidEarningsTab extends ConsumerWidget {
  const _PrepaidEarningsTab();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final earningsAsync = ref.watch(sellerEarningsProvider);
    return RefreshIndicator(
      color: AppColors.postbookPrimary,
      onRefresh: () async {
        ref.invalidate(sellerEarningsProvider);
        await ref.read(sellerEarningsProvider.future);
      },
      child: earningsAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => _errorList(context, e),
        data: (rows) {
          if (rows.isEmpty) {
            return _emptyList(
              context,
              'No earnings yet',
              "Delivered prepaid items will appear here with the payout split.",
            );
          }
          final total = rows.fold<double>(0, (s, r) => s + r.netAmount);
          return ListView(
            padding: const EdgeInsets.all(AppSpacing.l),
            children: [
              _SummaryCard(
                label: 'Net (this page)',
                value: 'Rs. ${total.toStringAsFixed(0)}',
              ),
              const SizedBox(height: AppSpacing.l),
              ...rows.map((r) => Padding(
                    padding: const EdgeInsets.only(bottom: AppSpacing.s),
                    child: _EarningRow(earning: r),
                  )),
            ],
          );
        },
      ),
    );
  }
}

class _CODEarningsTab extends ConsumerWidget {
  const _CODEarningsTab();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rowsAsync = ref.watch(sellerCODRemittancesProvider(''));
    return RefreshIndicator(
      color: AppColors.postbookPrimary,
      onRefresh: () async {
        ref.invalidate(sellerCODRemittancesProvider(''));
        await ref.read(sellerCODRemittancesProvider('').future);
      },
      child: rowsAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => _errorList(context, e),
        data: (rows) {
          if (rows.isEmpty) {
            return _emptyList(
              context,
              'No COD payouts yet',
              "Once a courier confirms a COD delivery, the remittance row appears here.",
            );
          }
          final pending = rows.where((r) => r.status == 'pending');
          final pendingTotal = pending.fold<double>(0, (s, r) => s + r.netAmount);
          return ListView(
            padding: const EdgeInsets.all(AppSpacing.l),
            children: [
              _SummaryCard(
                label: 'Pending payout',
                value: 'Rs. ${pendingTotal.toStringAsFixed(0)}',
              ),
              const SizedBox(height: AppSpacing.l),
              ...rows.map((r) => Padding(
                    padding: const EdgeInsets.only(bottom: AppSpacing.s),
                    child: _CODRow(remittance: r),
                  )),
            ],
          );
        },
      ),
    );
  }
}

class _SummaryCard extends StatelessWidget {
  const _SummaryCard({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: AppTextStyles.bodySmall),
          const SizedBox(height: 2),
          Text(value, style: AppTextStyles.h2),
        ],
      ),
    );
  }
}

class _EarningRow extends StatelessWidget {
  const _EarningRow({required this.earning});
  final SellerEarning earning;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  earning.productTitle,
                  style: AppTextStyles.label,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text(
                'Rs. ${earning.netAmount.toStringAsFixed(0)}',
                style: AppTextStyles.h3.copyWith(
                  color: const Color(0xFF047857),
                ),
              ),
            ],
          ),
          const SizedBox(height: 2),
          Text(
            '${earning.orderNumber} · ${earning.sku} × ${earning.quantity}',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: AppSpacing.s),
          _SplitRow(label: 'Gross', value: earning.grossAmount),
          _SplitRow(label: 'Commission', value: -earning.commissionAmount),
          _SplitRow(label: 'Platform fee', value: -earning.platformFee),
          _SplitRow(label: 'TDS', value: -earning.tdsAmount),
        ],
      ),
    );
  }
}

class _CODRow extends StatelessWidget {
  const _CODRow({required this.remittance});
  final CODRemittance remittance;

  @override
  Widget build(BuildContext context) {
    final settled = remittance.status == 'settled';
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  'Order ${remittance.orderId.substring(0, 8)}',
                  style: AppTextStyles.label,
                ),
              ),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: settled
                      ? const Color(0xFFD1FAE5)
                      : const Color(0xFFFEF3C7),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  remittance.status.toUpperCase(),
                  style: AppTextStyles.labelTiny.copyWith(
                    color: settled
                        ? const Color(0xFF047857)
                        : const Color(0xFF92400E),
                    fontWeight: FontWeight.w800,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: AppSpacing.s),
          _SplitRow(label: 'Gross', value: remittance.grossAmount),
          _SplitRow(label: 'Commission', value: -remittance.commissionAmount),
          _SplitRow(label: 'Platform fee', value: -remittance.platformFee),
          _SplitRow(label: 'TDS', value: -remittance.tdsAmount),
          const Divider(height: AppSpacing.l, color: AppColors.borderSubtle),
          _SplitRow(label: 'Net', value: remittance.netAmount, bold: true),
        ],
      ),
    );
  }
}

class _SplitRow extends StatelessWidget {
  const _SplitRow({
    required this.label,
    required this.value,
    this.bold = false,
  });
  final String label;
  final double value;
  final bool bold;

  @override
  Widget build(BuildContext context) {
    final style = bold ? AppTextStyles.label : AppTextStyles.bodySmall;
    final color = value >= 0
        ? AppColors.textPrimary
        : AppColors.textSecondary;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 1),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Text(label, style: style.copyWith(color: color)),
          Text('Rs. ${value.toStringAsFixed(0)}', style: style.copyWith(color: color)),
        ],
      ),
    );
  }
}

Widget _errorList(BuildContext context, Object e) {
  return ListView(
    children: [
      SizedBox(
        height: MediaQuery.of(context).size.height * 0.5,
        child: Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load earnings.\n$e',
              textAlign: TextAlign.center,
              style: AppTextStyles.body,
            ),
          ),
        ),
      ),
    ],
  );
}

Widget _emptyList(BuildContext context, String title, String body) {
  return ListView(
    children: [
      SizedBox(
        height: MediaQuery.of(context).size.height * 0.5,
        child: Center(
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: AppSpacing.xxl),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(Icons.account_balance_wallet_outlined,
                    size: 56, color: AppColors.textGhost),
                const SizedBox(height: AppSpacing.l),
                Text(title, style: AppTextStyles.h2),
                const SizedBox(height: AppSpacing.s),
                Text(
                  body,
                  textAlign: TextAlign.center,
                  style: AppTextStyles.body,
                ),
              ],
            ),
          ),
        ),
      ),
    ],
  );
}
