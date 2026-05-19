// Onboarding step 7 — payment.
//
// Three rails:
//   1. Wallet (recommended) — server debits the AtPost wallet balance.
//   2. UPI Intent — surfaces a `upi://pay?...` URL via `launchUPIIntent`.
//   3. Manual proof — partner pays out-of-band, uploads a screenshot.
//
// IDEMPOTENCY: every `subscribe()` mints a fresh UUIDv4 inside
// `partnerOnboardingNotifier`. A failed attempt that retries gets a new
// key; the backend dedupes via the same key — and the UI doesn't try to
// reuse the same key twice.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:atpost_app/services/upi_intent_helper.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class OnboardingPaymentStep extends ConsumerStatefulWidget {
  const OnboardingPaymentStep({super.key});

  @override
  ConsumerState<OnboardingPaymentStep> createState() =>
      _OnboardingPaymentStepState();
}

class _OnboardingPaymentStepState
    extends ConsumerState<OnboardingPaymentStep> {
  String _method = 'wallet';
  bool _proofUploaded = false;

  @override
  Widget build(BuildContext context) {
    final st = ref.watch(partnerOnboardingNotifier);
    final plan = st.selectedPlan;
    if (plan == null) {
      return const Center(child: Text('No plan selected. Go back.'));
    }
    final balance = ref.watch(walletBalanceProvider);
    final balanceLabel = balance.maybeWhen(
      data: (b) => formatRupees(b.availablePaise),
      orElse: () => '—',
    );
    final hasBalance = balance.maybeWhen(
      data: (b) => b.availablePaise >= plan.priceInrPaise,
      orElse: () => false,
    );

    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              _PlanSummary(plan: plan),
              const SizedBox(height: 16),
              Text('Payment method', style: AppTextStyles.h2),
              const SizedBox(height: 8),
              _MethodTile(
                value: 'wallet',
                groupValue: _method,
                title: 'VChat Wallet (recommended)',
                subtitle: 'Balance: $balanceLabel',
                icon: Icons.account_balance_wallet,
                disabled: !plan.isTrial && !hasBalance,
                disabledReason: 'Insufficient balance. Top up first.',
                onChanged: (v) => setState(() => _method = v),
              ),
              const SizedBox(height: 8),
              _MethodTile(
                value: 'upi',
                groupValue: _method,
                title: 'UPI Intent',
                subtitle: 'Open in GPay / PhonePe / BHIM / Paytm.',
                icon: Icons.qr_code_2,
                onChanged: (v) => setState(() => _method = v),
              ),
              const SizedBox(height: 8),
              _MethodTile(
                value: 'manual_proof',
                groupValue: _method,
                title: 'Manual proof',
                subtitle: 'Pay to merchant ID, upload screenshot.',
                icon: Icons.upload_file,
                onChanged: (v) => setState(() => _method = v),
              ),
              if (_method == 'manual_proof' && st.payment != null) ...[
                const SizedBox(height: 12),
                _ManualProofPanel(
                  paymentId: st.payment!.id,
                  uploaded: _proofUploaded,
                  onUpload: _onUploadProof,
                ),
              ],
              if (st.error != null) ...[
                const SizedBox(height: 12),
                Text(
                  'Could not start subscription. Please retry.',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
              ],
            ],
          ),
        ),
        _PayBar(
          plan: plan,
          method: _method,
          busy: st.busy,
          onTap: _onPay,
        ),
      ],
    );
  }

  Future<void> _onPay() async {
    final st = ref.read(partnerOnboardingNotifier);
    final plan = st.selectedPlan;
    if (plan == null) return;
    final notifier = ref.read(partnerOnboardingNotifier.notifier);
    final payment = await notifier.subscribe(paymentMethod: _method);
    if (payment == null || !mounted) return;

    if (_method == 'upi' && (payment.upiIntentUrl?.isNotEmpty ?? false)) {
      await launchUPIIntent(context, payment.upiIntentUrl!);
    }

    // For wallet (success) and trial we advance immediately. For UPI and
    // manual proof we wait for the user to confirm — in this Sprint we
    // treat any non-error response as ready to advance to verification.
    if (_method == 'wallet' || plan.isTrial) {
      notifier.next();
    } else if (_method == 'manual_proof') {
      // Partner must upload proof before advancing.
      _toast('Upload your payment screenshot, then continue.');
    } else {
      notifier.next();
    }
  }

  Future<void> _onUploadProof() async {
    final st = ref.read(partnerOnboardingNotifier);
    final paymentId = st.payment?.id;
    if (paymentId == null) return;
    final ok = await ref
        .read(partnerOnboardingNotifier.notifier)
        .submitPaymentProof(
          paymentId: paymentId,
          fileUrl: 'pending://payment_proof',
        );
    if (ok) {
      setState(() => _proofUploaded = true);
      ref.read(partnerOnboardingNotifier.notifier).next();
    } else {
      _toast('Could not upload. Please retry.');
    }
  }

  void _toast(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }
}

class _PlanSummary extends StatelessWidget {
  const _PlanSummary({required this.plan});
  final SubscriptionPlan plan;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          const Icon(Icons.workspace_premium, color: AppColors.postbookPrimary),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(plan.name, style: AppTextStyles.h3),
                const SizedBox(height: 2),
                Text(
                  '${plan.durationDays} days · '
                  '${plan.isUnlimited ? "Unlimited" : "${plan.leadAllotment}"} leads',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
          Text(
            plan.isTrial ? 'FREE' : formatRupees(plan.priceInrPaise),
            style: AppTextStyles.h2.copyWith(color: AppColors.postbookPrimary),
          ),
        ],
      ),
    );
  }
}

class _MethodTile extends StatelessWidget {
  const _MethodTile({
    required this.value,
    required this.groupValue,
    required this.title,
    required this.subtitle,
    required this.icon,
    required this.onChanged,
    this.disabled = false,
    this.disabledReason,
  });

  final String value;
  final String groupValue;
  final String title;
  final String subtitle;
  final IconData icon;
  final bool disabled;
  final String? disabledReason;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    final selected = value == groupValue;
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: disabled ? null : () => onChanged(value),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.12)
              : AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Row(
          children: [
            Icon(
              selected ? Icons.radio_button_checked : Icons.radio_button_off,
              color: selected
                  ? AppColors.postbookPrimary
                  : AppColors.textTertiary,
              size: 20,
            ),
            const SizedBox(width: 12),
            Icon(icon, color: AppColors.textTertiary),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: AppTextStyles.h3),
                  const SizedBox(height: 2),
                  Text(
                    disabled && disabledReason != null
                        ? disabledReason!
                        : subtitle,
                    style: AppTextStyles.bodySmall.copyWith(
                      color: disabled
                          ? AppColors.statusError
                          : AppColors.textTertiary,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ManualProofPanel extends StatelessWidget {
  const _ManualProofPanel({
    required this.paymentId,
    required this.uploaded,
    required this.onUpload,
  });

  final String paymentId;
  final bool uploaded;
  final VoidCallback onUpload;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Pay to merchant: mopedu@hdfc',
            style: AppTextStyles.body,
          ),
          const SizedBox(height: 6),
          Text(
            'Reference this payment id in the note: $paymentId',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 12),
          ElevatedButton.icon(
            onPressed: uploaded ? null : onUpload,
            icon: Icon(
              uploaded ? Icons.verified : Icons.upload_file,
              size: 16,
            ),
            label: Text(uploaded ? 'Uploaded' : 'Upload screenshot'),
            style: ElevatedButton.styleFrom(
              backgroundColor: uploaded
                  ? AppColors.statusSuccess
                  : AppColors.postbookPrimary,
              foregroundColor: Colors.white,
            ),
          ),
        ],
      ),
    );
  }
}

class _PayBar extends StatelessWidget {
  const _PayBar({
    required this.plan,
    required this.method,
    required this.busy,
    required this.onTap,
  });

  final SubscriptionPlan plan;
  final String method;
  final bool busy;
  final VoidCallback onTap;

  String get _label {
    if (plan.isTrial) return 'Start trial';
    if (method == 'wallet') return 'Pay ${formatRupees(plan.priceInrPaise)} from Wallet';
    if (method == 'upi') return 'Open UPI app';
    return 'Submit payment proof';
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
      decoration: const BoxDecoration(
        color: AppColors.bgPrimary,
        border: Border(
          top: BorderSide(color: AppColors.borderSubtle, width: 0.5),
        ),
      ),
      child: SizedBox(
        width: double.infinity,
        height: 50,
        child: ElevatedButton(
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.postbookPrimary,
            foregroundColor: Colors.white,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
          ),
          onPressed: busy ? null : onTap,
          child: busy
              ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(
                    color: Colors.white,
                    strokeWidth: 2,
                  ),
                )
              : Text(
                  _label,
                  style: AppTextStyles.h3.copyWith(color: Colors.white),
                ),
        ),
      ),
    );
  }
}
