// Bill-pay receipt — Phase 2.
//
// Receipt-style card for a single payment. Identifier rendered masked
// (last 4 chars). "Download PDF" / "Share receipt" stubs surface a
// snackbar. "Pay again" returns to the account detail. "Report issue"
// links into the existing trust-safety report flow.
//
// PRIVACY: identifier appears only as `••••1234`. Full identifier never
// renders. BBPS RRN is shown — that's the user's payment proof.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BillPayReceiptScreen extends ConsumerWidget {
  const BillPayReceiptScreen({super.key, required this.paymentId});

  final String paymentId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final paymentAsync = ref.watch(billPaymentDetailProvider(paymentId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Receipt', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/billpay'),
        ),
      ),
      body: paymentAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Text(
            'Could not load receipt.',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.statusError,
            ),
          ),
        ),
        data: (p) => _ReceiptBody(payment: p),
      ),
    );
  }
}

class _ReceiptBody extends StatelessWidget {
  const _ReceiptBody({required this.payment});

  final BillPayment payment;

  Color _bannerColor() {
    if (payment.isSucceeded) return AppColors.statusSuccess;
    if (payment.isFailed) return AppColors.statusError;
    if (payment.isRefunded) return AppColors.accentPurple;
    return AppColors.statusWarning;
  }

  IconData _bannerIcon() {
    if (payment.isSucceeded) return Icons.check_circle_rounded;
    if (payment.isFailed) return Icons.error_outline_rounded;
    if (payment.isRefunded) return Icons.replay_rounded;
    return Icons.hourglass_top_rounded;
  }

  String _statusLabel() {
    if (payment.isSucceeded) return 'Payment successful';
    if (payment.isFailed) return 'Payment failed';
    if (payment.isRefunded) return 'Refunded';
    return 'Payment pending';
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      children: [
        Container(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          decoration: BoxDecoration(
            color: _bannerColor().withAlpha(30),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: _bannerColor().withAlpha(80)),
          ),
          child: Column(
            children: [
              Icon(_bannerIcon(), color: _bannerColor(), size: 36),
              const SizedBox(height: AppSpacing.s),
              Text(
                _statusLabel(),
                style: AppTextStyles.h2.copyWith(color: _bannerColor()),
              ),
              const SizedBox(height: AppSpacing.s),
              Text(
                formatRupees(payment.amountPaise),
                style: AppTextStyles.h1.copyWith(fontSize: 32),
              ),
              if (payment.failureReason != null) ...[
                const SizedBox(height: AppSpacing.s),
                Text(
                  payment.failureReason!,
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.statusError,
                  ),
                  textAlign: TextAlign.center,
                ),
              ],
            ],
          ),
        ),
        const SizedBox(height: AppSpacing.xxl),
        Container(
          padding: const EdgeInsets.all(AppSpacing.l),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            children: [
              _Row(label: 'Provider', value: payment.providerName),
              if (payment.maskedIdentifier != null)
                _Row(
                  label: 'Account',
                  value: payment.maskedIdentifier!,
                  monospace: true,
                ),
              _Row(
                label: 'Amount',
                value: formatRupees(payment.amountPaise),
              ),
              if (payment.feePaise > 0)
                _Row(
                  label: 'Convenience fee',
                  value: formatRupees(payment.feePaise),
                ),
              const Divider(color: AppColors.borderSubtle, height: 24),
              _Row(
                label: 'Total',
                value: formatRupees(payment.totalPaise),
                emphasize: true,
              ),
              _Row(
                label: 'Method',
                value: _methodLabel(payment.paymentMethod),
              ),
              _Row(label: 'Status', value: _statusLabel()),
              if (payment.receiptNumber != null)
                _Row(
                  label: 'BBPS RRN',
                  value: payment.receiptNumber!,
                  monospace: true,
                ),
              _Row(
                label: 'Initiated',
                value: _fmtDateTime(payment.createdAt),
              ),
              if (payment.settledAt != null)
                _Row(
                  label: 'Settled',
                  value: _fmtDateTime(payment.settledAt!),
                ),
            ],
          ),
        ),
        const SizedBox(height: AppSpacing.xxl),
        Row(
          children: [
            Expanded(
              child: OutlinedButton.icon(
                onPressed: () => _showStub(
                  context,
                  'Receipt PDF will be available in a future build.',
                ),
                icon: const Icon(
                  Icons.download_rounded,
                  color: AppColors.textPrimary,
                  size: 18,
                ),
                label: Text(
                  'Download',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textPrimary,
                  ),
                ),
                style: OutlinedButton.styleFrom(
                  side: const BorderSide(color: AppColors.borderSubtle),
                  padding: const EdgeInsets.symmetric(vertical: 12),
                ),
              ),
            ),
            const SizedBox(width: AppSpacing.s),
            Expanded(
              child: OutlinedButton.icon(
                onPressed: () => _showStub(
                  context,
                  'Share will use share_plus once added to pubspec.',
                ),
                icon: const Icon(
                  Icons.share_outlined,
                  color: AppColors.textPrimary,
                  size: 18,
                ),
                label: Text(
                  'Share',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textPrimary,
                  ),
                ),
                style: OutlinedButton.styleFrom(
                  side: const BorderSide(color: AppColors.borderSubtle),
                  padding: const EdgeInsets.symmetric(vertical: 12),
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: AppSpacing.l),
        ElevatedButton(
          onPressed: () {
            if (payment.accountId != null) {
              context.go('/billpay/account/${payment.accountId}');
            } else {
              context.go('/billpay/recharge');
            }
          },
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.postbookPrimary,
            padding: const EdgeInsets.symmetric(vertical: 14),
          ),
          child: const Text('Pay again'),
        ),
        const SizedBox(height: AppSpacing.s),
        TextButton(
          onPressed: () =>
              context.push('/pulse/safety/reports?ref=billpay-$paymentId'),
          child: Text(
            'Report issue',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.statusError,
            ),
          ),
        ),
      ],
    );
  }

  String get paymentId => payment.id;

  void _showStub(BuildContext context, String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }
}

String _methodLabel(String method) {
  switch (method) {
    case 'wallet':
      return 'VChat Wallet';
    case 'upi':
      return 'UPI';
    case 'card':
      return 'Card';
    default:
      return method;
  }
}

String _fmtDateTime(DateTime d) {
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  final hh = d.hour.toString().padLeft(2, '0');
  final mm = d.minute.toString().padLeft(2, '0');
  return '${d.day} ${months[d.month - 1]} ${d.year} · $hh:$mm';
}

class _Row extends StatelessWidget {
  const _Row({
    required this.label,
    required this.value,
    this.monospace = false,
    this.emphasize = false,
  });

  final String label;
  final String value;
  final bool monospace;
  final bool emphasize;

  @override
  Widget build(BuildContext context) {
    final valueStyle = monospace
        ? AppTextStyles.mono.copyWith(color: AppColors.textPrimary)
        : (emphasize
            ? AppTextStyles.h3
            : AppTextStyles.bodyMedium.copyWith(
                color: AppColors.textPrimary,
              ));
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.s),
      child: Row(
        children: [
          Expanded(
            flex: 4,
            child: Text(label, style: AppTextStyles.bodySmall),
          ),
          Expanded(
            flex: 6,
            child: Text(
              value,
              style: valueStyle,
              textAlign: TextAlign.right,
            ),
          ),
        ],
      ),
    );
  }
}
