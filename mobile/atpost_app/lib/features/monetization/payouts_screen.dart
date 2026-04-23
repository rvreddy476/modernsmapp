import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/monetization.dart';
import 'package:atpost_app/providers/monetization_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PayoutsScreen extends ConsumerStatefulWidget {
  const PayoutsScreen({super.key});

  @override
  ConsumerState<PayoutsScreen> createState() => _PayoutsScreenState();
}

class _PayoutsScreenState extends ConsumerState<PayoutsScreen> {
  @override
  Widget build(BuildContext context) {
    final earningsAsync = ref.watch(earningsSummaryProvider);
    final payoutsAsync = ref.watch(payoutsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new,
              color: AppColors.textPrimary, size: 20),
          onPressed: () => context.pop(),
        ),
        title: Text('Payouts', style: AppTextStyles.h2),
        centerTitle: false,
      ),
      body: RefreshIndicator(
        color: AppColors.posttubePrimary,
        onRefresh: () async => ref.invalidate(payoutsProvider),
        child: CustomScrollView(
          slivers: [
            SliverToBoxAdapter(
              child: Padding(
                padding: AppSpacing.pagePadding.copyWith(top: 16),
                child: earningsAsync.when(
                  loading: () => Container(
                    height: 100,
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusXL),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: const Center(child: CircularProgressIndicator()),
                  ),
                  error: (_, _) => Container(
                    padding: const EdgeInsets.all(20),
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusXL),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: Text('Could not load balance.',
                        style: AppTextStyles.bodySmall),
                  ),
                  data: (earnings) {
                    final balance = earnings.pendingPayout.toString();
                    return _BalanceCard(
                      balance: balance,
                      onWithdraw: () => _showWithdrawDialog(context),
                    );
                  },
                ),
              ),
            ),
            SliverToBoxAdapter(
              child: Padding(
                padding: AppSpacing.pagePadding.copyWith(top: 24, bottom: 8),
                child: Text('Payout History', style: AppTextStyles.h3),
              ),
            ),
            payoutsAsync.when(
              loading: () => const SliverToBoxAdapter(
                child: Center(
                  child: Padding(
                    padding: EdgeInsets.all(32),
                    child: CircularProgressIndicator(),
                  ),
                ),
              ),
              error: (_, _) => SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding,
                  child: Column(
                    children: [
                      Text('Could not load payouts.',
                          style: AppTextStyles.bodySmall),
                      const SizedBox(height: 12),
                      GestureDetector(
                        onTap: () => ref.invalidate(payoutsProvider),
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
              data: (payouts) {
                if (payouts.isEmpty) {
                  return SliverToBoxAdapter(
                    child: Padding(
                      padding: AppSpacing.pagePadding.copyWith(top: 40),
                      child: Column(
                        children: [
                          const Icon(Icons.receipt_long_outlined,
                              color: AppColors.textMuted, size: 48),
                          const SizedBox(height: 16),
                          Text('No payouts yet',
                              style: AppTextStyles.h3),
                          const SizedBox(height: 4),
                          Text(
                            'Your payout history will appear here.',
                            style: AppTextStyles.bodySmall,
                          ),
                        ],
                      ),
                    ),
                  );
                }
                return SliverPadding(
                  padding:
                      AppSpacing.pagePadding.copyWith(top: 0, bottom: 32),
                  sliver: SliverList(
                    delegate: SliverChildBuilderDelegate(
                      (context, index) =>
                          _PayoutRow(payout: payouts[index]),
                      childCount: payouts.length,
                    ),
                  ),
                );
              },
            ),
          ],
        ),
      ),
    );
  }

  void _showWithdrawDialog(BuildContext context) {
    final amountCtrl = TextEditingController();
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Withdraw Funds', style: AppTextStyles.h3),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Enter the amount you wish to withdraw.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 16),
            TextField(
              controller: amountCtrl,
              keyboardType: TextInputType.number,
              style: AppTextStyles.body
                  .copyWith(color: AppColors.textPrimary),
              decoration: InputDecoration(
                hintText: 'Amount (₹)',
                hintStyle: AppTextStyles.bodySmall,
                prefixText: '₹ ',
                prefixStyle: AppTextStyles.label
                    .copyWith(color: AppColors.posttubePrimary),
                filled: true,
                fillColor: AppColors.bgCard,
                contentPadding: const EdgeInsets.symmetric(
                    horizontal: 14, vertical: 12),
                border: OutlineInputBorder(
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide:
                      BorderSide(color: AppColors.borderSubtle),
                ),
                enabledBorder: OutlineInputBorder(
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide:
                      BorderSide(color: AppColors.borderSubtle),
                ),
                focusedBorder: OutlineInputBorder(
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide: const BorderSide(
                      color: AppColors.posttubePrimary),
                ),
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text('Cancel', style: AppTextStyles.label),
          ),
          TextButton(
            onPressed: () async {
              final amount = amountCtrl.text.trim();
              if (amount.isEmpty) return;
              Navigator.of(ctx).pop();
              final messenger = ScaffoldMessenger.of(context);
              try {
                final api = ref.read(apiClientProvider);
                await api.post(
                  '/v1/monetization/payouts/withdraw',
                  data: {'amount': amount},
                );
                ref.invalidate(payoutsProvider);
                ref.invalidate(earningsSummaryProvider);
                if (mounted) {
                  messenger.showSnackBar(
                    const SnackBar(
                        content: Text('Withdrawal request submitted.')),
                  );
                }
              } catch (_) {
                if (mounted) {
                  messenger.showSnackBar(
                    const SnackBar(
                        content: Text('Withdrawal failed. Please try again.')),
                  );
                }
              }
            },
            child: Text(
              'Withdraw',
              style: AppTextStyles.label
                  .copyWith(color: AppColors.posttubePrimary),
            ),
          ),
        ],
      ),
    );
  }
}

class _BalanceCard extends StatelessWidget {
  final String balance;
  final VoidCallback onWithdraw;

  const _BalanceCard({required this.balance, required this.onWithdraw});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        gradient: AppColors.posttubeGradient,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Available Balance',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: Colors.white.withValues(alpha: 0.8),
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  '\u20b9$balance',
                  style: AppTextStyles.h1.copyWith(color: Colors.white),
                ),
              ],
            ),
          ),
          GestureDetector(
            onTap: onWithdraw,
            child: Container(
              padding: const EdgeInsets.symmetric(
                  horizontal: 16, vertical: 10),
              decoration: BoxDecoration(
                color: Colors.white.withValues(alpha: 0.2),
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusLarge),
                border: Border.all(
                    color: Colors.white.withValues(alpha: 0.3)),
              ),
              child: Text(
                'Withdraw',
                style: AppTextStyles.label.copyWith(color: Colors.white),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _PayoutRow extends StatelessWidget {
  final PayoutRecord payout;

  const _PayoutRow({required this.payout});

  @override
  Widget build(BuildContext context) {
    final amount = payout.amount.toString();
    final status = payout.status;
    final date = payout.createdAt.toString();

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
            width: 40,
            height: 40,
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius:
                  BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            child: const Icon(
              Icons.account_balance_wallet_outlined,
              color: AppColors.posttubePrimary,
              size: 20,
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('\u20b9$amount', style: AppTextStyles.label),
                if (date.isNotEmpty)
                  Text(date, style: AppTextStyles.labelTiny),
              ],
            ),
          ),
          Container(
            padding:
                const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
            decoration: BoxDecoration(
              color: statusColor.withValues(alpha: 0.15),
              borderRadius:
                  BorderRadius.circular(AppSpacing.radiusFull),
            ),
            child: Text(
              status,
              style: AppTextStyles.labelTiny
                  .copyWith(color: statusColor),
            ),
          ),
        ],
      ),
    );
  }
}
