// Wallet transaction detail — Phase 2 Sprint 1.
//
// Receipt-style card with amount, status, counterparty / merchant, timing,
// and the bank txn ref when available. CTAs depend on transaction type:
//   * `top_up`     → "Top up again"
//   * `send`       → "Send again"
//   * `receive`    → "Send back"
//   * `merchant_*` → "View order" (deferred — surfaces nothing yet)
//   * any         → "Report an issue" + "Download receipt (PDF)" placeholder.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class WalletTransactionDetailScreen extends ConsumerWidget {
  const WalletTransactionDetailScreen({super.key, required this.transactionId});

  final String transactionId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(walletTransactionProvider(transactionId));
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Receipt', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
          onPressed: () => context.canPop() ? context.pop() : context.go('/wallet'),
        ),
      ),
      body: async.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(Icons.error_outline,
                    color: AppColors.statusError, size: 36),
                const SizedBox(height: AppSpacing.l),
                Text('Could not load receipt.', style: AppTextStyles.h3),
                const SizedBox(height: AppSpacing.s),
                Text('$e', style: AppTextStyles.bodySmall),
                const SizedBox(height: AppSpacing.l),
                ElevatedButton(
                  onPressed: () => ref.invalidate(
                    walletTransactionProvider(transactionId),
                  ),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                  ),
                  child: const Text('Retry'),
                ),
              ],
            ),
          ),
        ),
        data: (t) => _Body(txn: t),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.txn});

  final WalletTransaction txn;

  @override
  Widget build(BuildContext context) {
    final credit = txn.isCredit;
    return SingleChildScrollView(
      padding: const EdgeInsets.all(AppSpacing.l),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // ─── Receipt card ──────────────────────────────────────────
          Container(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              children: [
                _StatusPill(status: txn.status),
                const SizedBox(height: AppSpacing.l),
                Text(
                  '${credit ? '+' : '-'}${formatRupees(txn.amountPaise)}',
                  style: AppTextStyles.h1.copyWith(
                    fontSize: 36,
                    color: credit
                        ? AppColors.statusSuccess
                        : AppColors.textPrimary,
                  ),
                ),
                const SizedBox(height: AppSpacing.s),
                Text(_label(txn), style: AppTextStyles.h3),
                const SizedBox(height: AppSpacing.l),
                const Divider(color: AppColors.borderSubtle, height: 1),
                const SizedBox(height: AppSpacing.l),
                _DetailRow(label: 'Type', value: _typeLabel(txn.type)),
                _DetailRow(
                  label: 'Direction',
                  value: credit ? 'Credit' : 'Debit',
                ),
                if (txn.counterpartyLabel != null)
                  _DetailRow(
                    label: 'Recipient',
                    value: txn.counterpartyLabel!,
                  ),
                if (txn.counterpartyPhone != null)
                  _DetailRow(
                    label: 'Phone',
                    value: _maskPhone(txn.counterpartyPhone!),
                  ),
                if (txn.merchantService != null)
                  _DetailRow(
                    label: 'Merchant',
                    value: txn.merchantService!,
                  ),
                if (txn.merchantRef != null)
                  _DetailRow(label: 'Merchant ref', value: txn.merchantRef!),
                _DetailRow(
                  label: 'Created',
                  value: _formatDate(txn.createdAt),
                ),
                if (txn.settledAt != null)
                  _DetailRow(
                    label: 'Settled',
                    value: _formatDate(txn.settledAt!),
                  ),
                if (txn.bankTxnRef != null)
                  _DetailRow(label: 'Bank ref', value: txn.bankTxnRef!),
                _DetailRow(label: 'AtPost ID', value: txn.id),
                if (txn.failureReason != null)
                  _DetailRow(
                    label: 'Failure',
                    value: txn.failureReason!,
                    valueColor: AppColors.statusError,
                  ),
              ],
            ),
          ),
          const SizedBox(height: AppSpacing.l),
          // ─── CTAs ───────────────────────────────────────────────────
          if (txn.type == 'top_up')
            _CtaButton(
              icon: Icons.account_balance_wallet_outlined,
              label: 'Top up again',
              onTap: () => GoRouter.of(context).push('/wallet/top-up'),
            ),
          if (txn.type == 'send' || txn.type == 'receive')
            _CtaButton(
              icon: txn.type == 'send' ? Icons.send_outlined : Icons.reply,
              label: txn.type == 'send' ? 'Send again' : 'Send back',
              onTap: () {
                GoRouter.of(context).push(
                  '/wallet/send',
                  extra: {
                    'recipient_user_id': txn.counterpartyUserId,
                    'recipient_phone': txn.counterpartyPhone,
                    'label': txn.counterpartyLabel,
                    'source': 'receipt',
                  },
                );
              },
            ),
          const SizedBox(height: AppSpacing.s),
          _CtaButton(
            icon: Icons.picture_as_pdf_outlined,
            label: 'Download receipt (PDF)',
            onTap: () {
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(
                  content: Text('PDF receipts arrive in the next sprint.'),
                ),
              );
            },
            outlined: true,
          ),
          const SizedBox(height: AppSpacing.s),
          _CtaButton(
            icon: Icons.flag_outlined,
            label: 'Report an issue',
            onTap: () {
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(
                  content: Text(
                    'Reports are routed to AtPost Support. Logging this one.',
                  ),
                ),
              );
            },
            outlined: true,
          ),
        ],
      ),
    );
  }

  String _label(WalletTransaction t) {
    switch (t.type) {
      case 'top_up':
        return 'Top-up to wallet';
      case 'send':
        return 'Sent to ${t.counterpartyLabel ?? t.counterpartyPhone ?? 'recipient'}';
      case 'receive':
        return 'Received from ${t.counterpartyLabel ?? t.counterpartyPhone ?? 'sender'}';
      case 'merchant_pay':
        return 'Paid to ${t.merchantService ?? 'merchant'}';
      case 'refund':
        return 'Refund';
      case 'reversal':
        return 'Reversal';
      default:
        return t.type;
    }
  }

  String _typeLabel(String t) {
    switch (t) {
      case 'top_up':
        return 'Top-up';
      case 'send':
        return 'P2P send';
      case 'receive':
        return 'P2P receive';
      case 'merchant_pay':
        return 'Merchant pay';
      case 'refund':
        return 'Refund';
      case 'reversal':
        return 'Reversal';
      default:
        return t;
    }
  }

  String _maskPhone(String p) {
    if (p.length <= 4) return p;
    return '${'•' * (p.length - 4)}${p.substring(p.length - 4)}';
  }

  String _formatDate(DateTime d) {
    final l = d.toLocal();
    final m = l.month.toString().padLeft(2, '0');
    final day = l.day.toString().padLeft(2, '0');
    final hh = l.hour.toString().padLeft(2, '0');
    final mm = l.minute.toString().padLeft(2, '0');
    return '$day-$m-${l.year} · $hh:$mm';
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      'succeeded' => ('Success', AppColors.statusSuccess),
      'pending' => ('Pending', AppColors.statusWarning),
      'failed' => ('Failed', AppColors.statusError),
      'reversed' => ('Reversed', AppColors.statusError),
      _ => (status, AppColors.textTertiary),
    };
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.l,
        vertical: AppSpacing.xs,
      ),
      decoration: BoxDecoration(
        color: color.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        border: Border.all(color: color.withAlpha(80)),
      ),
      child: Text(
        label,
        style: AppTextStyles.label.copyWith(color: color),
      ),
    );
  }
}

class _DetailRow extends StatelessWidget {
  const _DetailRow({
    required this.label,
    required this.value,
    this.valueColor,
  });

  final String label;
  final String value;
  final Color? valueColor;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.xs),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 110,
            child: Text(
              label,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textTertiary,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: AppTextStyles.body.copyWith(
                color: valueColor ?? AppColors.textPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _CtaButton extends StatelessWidget {
  const _CtaButton({
    required this.icon,
    required this.label,
    required this.onTap,
    this.outlined = false,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final bool outlined;

  @override
  Widget build(BuildContext context) {
    if (outlined) {
      return OutlinedButton.icon(
        onPressed: onTap,
        icon: Icon(icon, color: AppColors.textSecondary),
        label: Text(label,
            style: AppTextStyles.label
                .copyWith(color: AppColors.textSecondary)),
        style: OutlinedButton.styleFrom(
          padding: const EdgeInsets.symmetric(vertical: 14),
          side: const BorderSide(color: AppColors.borderMedium),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          ),
        ),
      );
    }
    return ElevatedButton.icon(
      onPressed: onTap,
      icon: Icon(icon, color: Colors.white),
      label:
          Text(label, style: AppTextStyles.label.copyWith(color: Colors.white)),
      style: ElevatedButton.styleFrom(
        backgroundColor: AppColors.postbookPrimary,
        padding: const EdgeInsets.symmetric(vertical: 14),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        ),
      ),
    );
  }
}
