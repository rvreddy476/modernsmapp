// Wallet top-up — Phase 2 Sprint 1.
//
// Flow:
//   1. User picks an amount (quick-amounts or types one).
//   2. Tap "Add money" → notifier mints a fresh UUID (idempotency_key)
//      and calls `POST /v1/wallet/top-up`.
//   3. On success we open the UPI Intent via `launchUPIIntent` and start
//      polling `GET /v1/wallet/top-up/:id` every 5s for confirmation.
//   4. On `succeeded` we invalidate balance + transactions and surface a
//      success card with a "Done" CTA.
//
// IDEMPOTENCY: the notifier mints a fresh UUID per `startTopUp` call. The
// backend dedupes on it; double-tapping the CTA before the response lands
// is safe.

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/data/repositories/wallet_repository.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/upi_intent_helper.dart';
import 'package:atpost_app/services/wallet_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

enum _TopUpPhase { input, launching, polling, success, failure, expired }

class WalletTopUpScreen extends ConsumerStatefulWidget {
  const WalletTopUpScreen({super.key});

  @override
  ConsumerState<WalletTopUpScreen> createState() => _WalletTopUpScreenState();
}

class _WalletTopUpScreenState extends ConsumerState<WalletTopUpScreen> {
  final TextEditingController _amountCtrl = TextEditingController();
  _TopUpPhase _phase = _TopUpPhase.input;
  WalletTopUpStart? _topUp;
  String? _errorMessage;
  Timer? _pollTimer;

  static const _quickAmountsPaise = [10000, 50000, 100000, 200000];

  @override
  void dispose() {
    _amountCtrl.dispose();
    _pollTimer?.cancel();
    super.dispose();
  }

  int? get _amountPaise {
    final raw = _amountCtrl.text.trim();
    if (raw.isEmpty) return null;
    final rupees = int.tryParse(raw);
    if (rupees == null || rupees <= 0) return null;
    return rupees * 100;
  }

  Future<void> _start() async {
    final amount = _amountPaise;
    if (amount == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a valid amount in rupees')),
      );
      return;
    }
    final balance = ref.read(walletBalanceProvider).asData?.value;
    if (balance != null && balance.monthlyLimitPaise > 0) {
      // Soft check — real enforcement is server-side.
      if (amount > balance.monthlyLimitPaise) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              'Amount exceeds your ${balance.tierChipLabel}. Upgrade KYC to '
              'increase the limit.',
            ),
          ),
        );
        return;
      }
    }
    setState(() {
      _phase = _TopUpPhase.launching;
      _errorMessage = null;
    });
    ref.read(walletTelemetryProvider).walletTopUpStarted(amountPaise: amount);
    final result =
        await ref.read(walletNotifier.notifier).startTopUp(amountPaise: amount);
    if (!mounted) return;
    if (result == null) {
      setState(() {
        _phase = _TopUpPhase.failure;
        _errorMessage = 'Could not start top-up. Please try again.';
      });
      return;
    }
    _topUp = result;
    final launched = await launchUPIIntent(context, result.upiIntentUrl);
    if (!mounted) return;
    if (!launched) {
      setState(() {
        _phase = _TopUpPhase.failure;
        _errorMessage = 'UPI launch was cancelled.';
      });
      ref.read(walletTelemetryProvider).walletTopUpFailed(
            amountPaise: amount,
            reason: 'upi_cancelled',
          );
      return;
    }
    setState(() => _phase = _TopUpPhase.polling);
    _startPolling(amountPaise: amount);
  }

  void _startPolling({required int amountPaise}) {
    _pollTimer?.cancel();
    _pollTimer = Timer.periodic(const Duration(seconds: 5), (timer) async {
      if (!mounted || _topUp == null) {
        timer.cancel();
        return;
      }
      // Bail if the top-up window has expired.
      if (DateTime.now().isAfter(_topUp!.expiresAt)) {
        timer.cancel();
        if (!mounted) return;
        setState(() {
          _phase = _TopUpPhase.expired;
          _errorMessage =
              'Top-up window expired. Please start a fresh top-up.';
        });
        return;
      }
      try {
        final repo = ref.read(walletRepositoryProvider);
        final status = await repo.getTopUp(_topUp!.transactionId);
        if (!mounted) return;
        if (status.status == 'succeeded') {
          timer.cancel();
          ref
              .read(walletTelemetryProvider)
              .walletTopUpCompleted(amountPaise: amountPaise);
          ref.invalidate(walletBalanceProvider);
          ref.invalidate(
            walletTransactionsProvider(const TransactionsQuery(limit: 5)),
          );
          setState(() => _phase = _TopUpPhase.success);
        } else if (status.status == 'failed' ||
            status.status == 'reversed') {
          timer.cancel();
          ref.read(walletTelemetryProvider).walletTopUpFailed(
                amountPaise: amountPaise,
                reason: status.failureReason ?? status.status,
              );
          if (!mounted) return;
          setState(() {
            _phase = _TopUpPhase.failure;
            _errorMessage =
                status.failureReason ?? 'Top-up failed. Please retry.';
          });
        }
      } catch (_) {
        // Transient — let the next tick try again.
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final balance = ref.watch(walletBalanceProvider).asData?.value;
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Add money', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
          onPressed: () => context.pop(),
        ),
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(AppSpacing.l),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _AmountInput(
                controller: _amountCtrl,
                enabled: _phase == _TopUpPhase.input ||
                    _phase == _TopUpPhase.failure ||
                    _phase == _TopUpPhase.expired,
              ),
              const SizedBox(height: AppSpacing.l),
              _QuickAmounts(
                amounts: _quickAmountsPaise,
                onTap: (paise) {
                  _amountCtrl.text = (paise ~/ 100).toString();
                  setState(() {});
                },
              ),
              if (balance != null) ...[
                const SizedBox(height: AppSpacing.l),
                _LimitWarning(balance: balance),
              ],
              const SizedBox(height: AppSpacing.xxl),
              if (_phase == _TopUpPhase.polling) const _PollingCard(),
              if (_phase == _TopUpPhase.success) const _SuccessCard(),
              if (_phase == _TopUpPhase.failure ||
                  _phase == _TopUpPhase.expired)
                _FailureCard(message: _errorMessage),
              const SizedBox(height: AppSpacing.l),
              _PrimaryCta(
                phase: _phase,
                onTap: _phase == _TopUpPhase.launching ||
                        _phase == _TopUpPhase.polling
                    ? null
                    : () {
                        if (_phase == _TopUpPhase.success) {
                          context.pop();
                        } else {
                          _start();
                        }
                      },
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Amount input ────────────────────────────────────────────────────────

class _AmountInput extends StatelessWidget {
  const _AmountInput({required this.controller, required this.enabled});

  final TextEditingController controller;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.xxl,
        vertical: AppSpacing.xxl,
      ),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Text('₹', style: AppTextStyles.h1.copyWith(fontSize: 32)),
          const SizedBox(width: AppSpacing.s),
          Expanded(
            child: TextField(
              controller: controller,
              enabled: enabled,
              keyboardType: TextInputType.number,
              inputFormatters: [FilteringTextInputFormatter.digitsOnly],
              style: AppTextStyles.h1.copyWith(fontSize: 32),
              decoration: InputDecoration(
                hintText: '0',
                hintStyle: AppTextStyles.h1.copyWith(
                  fontSize: 32,
                  color: AppColors.textGhost,
                ),
                border: InputBorder.none,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _QuickAmounts extends StatelessWidget {
  const _QuickAmounts({required this.amounts, required this.onTap});

  final List<int> amounts;
  final void Function(int paise) onTap;

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: AppSpacing.s,
      runSpacing: AppSpacing.s,
      children: [
        for (final paise in amounts)
          ActionChip(
            backgroundColor: AppColors.bgSecondary,
            side: const BorderSide(color: AppColors.borderSubtle),
            label: Text(
              formatRupees(paise, withSymbol: true),
              style: AppTextStyles.label,
            ),
            onPressed: () => onTap(paise),
          ),
      ],
    );
  }
}

class _LimitWarning extends StatelessWidget {
  const _LimitWarning({required this.balance});

  final WalletBalance balance;

  @override
  Widget build(BuildContext context) {
    final color = balance.kycTier == 'minimal'
        ? AppColors.statusWarning
        : AppColors.textTertiary;
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: color.withAlpha(20),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: color.withAlpha(80)),
      ),
      child: Row(
        children: [
          Icon(Icons.info_outline, color: color, size: 18),
          const SizedBox(width: AppSpacing.s),
          Expanded(
            child: Text(
              'Your KYC tier is ${balance.kycTier}. Monthly limit '
              '${balance.tierChipLabel}.'
              '${balance.kycTier == 'minimal' ? ' Upgrade to Full KYC to raise to ₹2L.' : ''}',
              style: AppTextStyles.bodySmall.copyWith(color: color),
            ),
          ),
          if (balance.kycTier == 'minimal')
            TextButton(
              onPressed: () => GoRouter.of(context).push('/wallet/kyc'),
              child: Text(
                'Upgrade',
                style: AppTextStyles.label.copyWith(
                  color: AppColors.posttubePrimary,
                ),
              ),
            ),
        ],
      ),
    );
  }
}

// ─── Status cards ────────────────────────────────────────────────────────

class _PollingCard extends StatelessWidget {
  const _PollingCard();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          const SizedBox(
            width: 18,
            height: 18,
            child: CircularProgressIndicator(
              strokeWidth: 2,
              color: AppColors.posttubePrimary,
            ),
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Text(
              'Waiting for your UPI app to confirm…',
              style: AppTextStyles.body,
            ),
          ),
        ],
      ),
    );
  }
}

class _SuccessCard extends StatelessWidget {
  const _SuccessCard();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.statusSuccess.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.statusSuccess.withAlpha(80)),
      ),
      child: Row(
        children: [
          const Icon(Icons.check_circle, color: AppColors.statusSuccess),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Text(
              'Money added to your wallet.',
              style: AppTextStyles.label,
            ),
          ),
        ],
      ),
    );
  }
}

class _FailureCard extends StatelessWidget {
  const _FailureCard({required this.message});

  final String? message;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.statusError.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.statusError.withAlpha(80)),
      ),
      child: Row(
        children: [
          const Icon(Icons.error_outline, color: AppColors.statusError),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Text(
              message ?? 'Top-up did not complete. Please retry.',
              style: AppTextStyles.bodySmall,
            ),
          ),
        ],
      ),
    );
  }
}

class _PrimaryCta extends StatelessWidget {
  const _PrimaryCta({required this.phase, required this.onTap});

  final _TopUpPhase phase;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final label = switch (phase) {
      _TopUpPhase.launching => 'Launching UPI…',
      _TopUpPhase.polling => 'Confirming…',
      _TopUpPhase.success => 'Done',
      _TopUpPhase.failure || _TopUpPhase.expired => 'Try again',
      _TopUpPhase.input => 'Add money',
    };
    return SizedBox(
      height: 52,
      child: FilledButton(
        onPressed: onTap,
        style: FilledButton.styleFrom(
          backgroundColor: AppColors.postbookPrimary,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.h3.copyWith(color: Colors.white),
        ),
      ),
    );
  }
}
