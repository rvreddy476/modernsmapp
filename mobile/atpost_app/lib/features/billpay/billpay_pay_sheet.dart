// Bill-pay pay sheet — Phase 2.
//
// Modal bottom sheet for both bill payments and mobile recharges. Picks a
// payment method (wallet vs UPI), shows in-flight progress, then a success
// or failure terminal screen.
//
// IDEMPOTENCY: every confirmed press of "Pay" mints a fresh UUID via
// `BillPayNotifier`. The notifier guards against double-tap with `isInFlight`
// so a fast double press cannot fire a second call. `BillPayResult` carries
// back the payment id; on success the sheet pops with that id and the caller
// routes to the receipt.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/billpay_telemetry.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Payload describing what we're about to pay. Either a bill (with an
/// `accountId`) or a recharge (with `phone` + `operator` + `circle` set).
class BillPayRequest {
  const BillPayRequest({
    required this.providerId,
    required this.providerName,
    required this.identifier,
    required this.amountPaise,
    this.accountId,
    this.billId,
    this.categoryId = '',
    this.allowAmountEdit = true,
    // Recharge-only fields. When `phone` is non-empty the sheet routes
    // through `rechargeMobile()` instead of `pay()`.
    this.phone,
    this.operator,
    this.circle,
    this.planId,
  });

  final String providerId;
  final String providerName;
  final String identifier;
  final int amountPaise;
  final String? accountId;
  final String? billId;
  final String categoryId;
  final bool allowAmountEdit;

  final String? phone;
  final String? operator;
  final String? circle;
  final String? planId;

  bool get isRecharge => phone != null && phone!.isNotEmpty;
}

/// Open the modal pay sheet. Returns the BBPS payment id on success, or null
/// when the user dismisses or the call fails terminally.
Future<String?> showBillPaySheet(
  BuildContext context,
  BillPayRequest request,
) {
  return showModalBottomSheet<String>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgPrimary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
    ),
    builder: (_) => _PaySheet(request: request),
  );
}

class _PaySheet extends ConsumerStatefulWidget {
  const _PaySheet({required this.request});

  final BillPayRequest request;

  @override
  ConsumerState<_PaySheet> createState() => _PaySheetState();
}

enum _SheetStage { picker, paying, success, failure }

class _PaySheetState extends ConsumerState<_PaySheet> {
  late final TextEditingController _amountController;
  String _paymentMethod = 'wallet';
  _SheetStage _stage = _SheetStage.picker;
  String? _resultPaymentId;
  String? _receiptNumber;
  String? _failureReason;

  @override
  void initState() {
    super.initState();
    _amountController = TextEditingController(
      text: (widget.request.amountPaise / 100).toStringAsFixed(2),
    );
  }

  @override
  void dispose() {
    _amountController.dispose();
    super.dispose();
  }

  int get _amountPaise {
    final raw = _amountController.text.trim();
    if (raw.isEmpty) return widget.request.amountPaise;
    final parsed = double.tryParse(raw);
    if (parsed == null) return widget.request.amountPaise;
    return (parsed * 100).round();
  }

  Future<void> _onPay() async {
    final amount = _amountPaise;
    if (amount <= 0) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a valid amount')),
      );
      return;
    }

    // Wallet sufficiency check (best-effort; backend re-checks).
    if (_paymentMethod == 'wallet') {
      final balance = ref.read(walletBalanceProvider).valueOrNull;
      if (balance != null && balance.availablePaise < amount) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text(
              'Wallet balance is insufficient. Top up first or pick UPI.',
            ),
          ),
        );
        return;
      }
    }

    final telemetry = ref.read(billpayTelemetryProvider);
    telemetry.billpayPaymentStarted(
      categoryId: widget.request.categoryId,
      paymentMethod: _paymentMethod,
      amountPaise: amount,
    );

    if (widget.request.isRecharge) {
      telemetry.billpayRechargeStarted(
        operator: widget.request.operator ?? 'unknown',
        amountPaise: amount,
      );
    }

    setState(() => _stage = _SheetStage.paying);

    final notifier = ref.read(billPayMutationProvider.notifier);
    final BillPayResult? result;
    if (widget.request.isRecharge) {
      result = await notifier.rechargeMobile(
        phone: widget.request.phone!,
        operator: widget.request.operator ?? '',
        circle: widget.request.circle ?? '',
        amountPaise: amount,
        planId: widget.request.planId,
        paymentMethod: _paymentMethod,
      );
    } else {
      result = await notifier.pay(
        accountId: widget.request.accountId,
        providerId: widget.request.providerId,
        identifier: widget.request.identifier,
        amountPaise: amount,
        paymentMethod: _paymentMethod,
        billId: widget.request.billId,
      );
    }

    if (!mounted) return;

    if (result == null) {
      final err = ref.read(billPayMutationProvider).error;
      setState(() {
        _stage = _SheetStage.failure;
        _failureReason = err?.toString() ?? 'Network error';
      });
      telemetry.billpayPaymentCompleted(
        categoryId: widget.request.categoryId,
        paymentMethod: _paymentMethod,
        amountPaise: amount,
        status: 'failed',
      );
      return;
    }

    // For UPI flow we may need to bounce the user out to their UPI app. We
    // surface the helper sheet but don't block on it — the backend will
    // settle async.
    if (_paymentMethod == 'upi') {
      // Server returns the upi_intent_url separately on a future revision.
      // For now we ack and rely on backend status push.
    }

    // Hoist to a non-null local: Dart's flow analysis drops the
    // null-promotion of `result` inside the setState closures below.
    final res = result;
    if (res.status == 'failed') {
      setState(() {
        _stage = _SheetStage.failure;
        _failureReason = res.failureReason ?? 'Unknown error';
      });
      telemetry.billpayPaymentCompleted(
        categoryId: widget.request.categoryId,
        paymentMethod: _paymentMethod,
        amountPaise: amount,
        status: 'failed',
      );
      return;
    }

    setState(() {
      _stage = _SheetStage.success;
      _resultPaymentId = res.paymentId;
      _receiptNumber = res.receiptNumber;
    });
    telemetry.billpayPaymentCompleted(
      categoryId: widget.request.categoryId,
      paymentMethod: _paymentMethod,
      amountPaise: amount,
      status: result.status,
    );

    // Auto-dismiss in 5s if user does not act.
    Future.delayed(const Duration(seconds: 5), () {
      if (!mounted) return;
      if (_stage == _SheetStage.success) {
        Navigator.of(context).pop(_resultPaymentId);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(
        AppSpacing.xxl,
        AppSpacing.l,
        AppSpacing.xxl,
        AppSpacing.xxl + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderSubtle,
                borderRadius: BorderRadius.circular(999),
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.l),
          switch (_stage) {
            _SheetStage.picker => _buildPicker(),
            _SheetStage.paying => _buildPaying(),
            _SheetStage.success => _buildSuccess(),
            _SheetStage.failure => _buildFailure(),
          },
        ],
      ),
    );
  }

  Widget _buildPicker() {
    final balance = ref.watch(walletBalanceProvider);
    final amount = _amountPaise;
    final insufficient = balance.maybeWhen(
      data: (b) => _paymentMethod == 'wallet' && b.availablePaise < amount,
      orElse: () => false,
    );

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          'Pay ${formatRupees(amount)} to ${widget.request.providerName}',
          style: AppTextStyles.h2,
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: AppSpacing.xxl),
        if (widget.request.allowAmountEdit) ...[
          Text(
            'Amount',
            style: AppTextStyles.label.copyWith(color: AppColors.textPrimary),
          ),
          const SizedBox(height: AppSpacing.s),
          TextField(
            controller: _amountController,
            keyboardType: const TextInputType.numberWithOptions(decimal: true),
            onChanged: (_) => setState(() {}),
            style: AppTextStyles.h1.copyWith(
              color: AppColors.textPrimary,
              fontSize: 24,
            ),
            decoration: InputDecoration(
              prefixText: '₹ ',
              prefixStyle: AppTextStyles.h1.copyWith(
                color: AppColors.textPrimary,
                fontSize: 24,
              ),
              filled: true,
              fillColor: AppColors.bgTertiary,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                borderSide: BorderSide.none,
              ),
              contentPadding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.l,
                vertical: AppSpacing.l,
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.xxl),
        ],
        Text(
          'Payment method',
          style: AppTextStyles.label.copyWith(color: AppColors.textPrimary),
        ),
        const SizedBox(height: AppSpacing.s),
        _MethodTile(
          method: 'wallet',
          selected: _paymentMethod == 'wallet',
          icon: Icons.account_balance_wallet_outlined,
          title: 'AtPost Wallet',
          subtitle: balance.maybeWhen(
            data: (b) => 'Balance: ${formatRupees(b.availablePaise)}',
            loading: () => 'Loading balance...',
            orElse: () => 'Balance unavailable',
          ),
          recommended: true,
          onTap: () => setState(() => _paymentMethod = 'wallet'),
          warning: insufficient ? 'Insufficient balance' : null,
        ),
        const SizedBox(height: AppSpacing.s),
        _MethodTile(
          method: 'upi',
          selected: _paymentMethod == 'upi',
          icon: Icons.qr_code_2_rounded,
          title: 'UPI',
          subtitle: 'Pay using GPay / PhonePe / BHIM',
          onTap: () => setState(() => _paymentMethod = 'upi'),
        ),
        const SizedBox(height: AppSpacing.xxl),
        ElevatedButton(
          onPressed: insufficient ? null : _onPay,
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.postbookPrimary,
            disabledBackgroundColor: AppColors.bgTertiary,
            padding: const EdgeInsets.symmetric(vertical: 14),
          ),
          child: Text('Pay ${formatRupees(amount)}'),
        ),
        const SizedBox(height: AppSpacing.s),
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(
            'Cancel',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildPaying() {
    return Column(
      children: [
        const SizedBox(height: AppSpacing.xxxxl),
        const CircularProgressIndicator(color: AppColors.postbookPrimary),
        const SizedBox(height: AppSpacing.xxl),
        Text('Submitting to BBPS network...', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        Text(
          'Hold tight — this can take up to 30 seconds.',
          style: AppTextStyles.bodySmall,
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: AppSpacing.xxxxl),
      ],
    );
  }

  Widget _buildSuccess() {
    return Column(
      children: [
        const SizedBox(height: AppSpacing.xxl),
        Container(
          width: 64,
          height: 64,
          decoration: BoxDecoration(
            color: AppColors.statusSuccess.withAlpha(40),
            shape: BoxShape.circle,
          ),
          child: const Icon(
            Icons.check_rounded,
            color: AppColors.statusSuccess,
            size: 32,
          ),
        ),
        const SizedBox(height: AppSpacing.l),
        Text(
          widget.request.isRecharge ? 'Recharge done!' : 'Bill paid!',
          style: AppTextStyles.h1,
        ),
        if (_receiptNumber != null) ...[
          const SizedBox(height: AppSpacing.s),
          Text(
            'BBPS RRN: $_receiptNumber',
            style: AppTextStyles.mono,
          ),
        ],
        const SizedBox(height: AppSpacing.xxxxl),
        if (_resultPaymentId != null)
          ElevatedButton(
            onPressed: () => Navigator.of(context).pop(_resultPaymentId),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: const Text('View receipt'),
          ),
        const SizedBox(height: AppSpacing.s),
        TextButton(
          onPressed: () => Navigator.of(context).pop(_resultPaymentId),
          child: Text(
            'Done',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildFailure() {
    return Column(
      children: [
        const SizedBox(height: AppSpacing.xxl),
        Container(
          width: 64,
          height: 64,
          decoration: BoxDecoration(
            color: AppColors.statusError.withAlpha(40),
            shape: BoxShape.circle,
          ),
          child: const Icon(
            Icons.close_rounded,
            color: AppColors.statusError,
            size: 32,
          ),
        ),
        const SizedBox(height: AppSpacing.l),
        Text('Payment failed', style: AppTextStyles.h1),
        const SizedBox(height: AppSpacing.s),
        Text(
          _failureReason ?? 'Please try again.',
          style: AppTextStyles.bodySmall,
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: AppSpacing.xxxxl),
        ElevatedButton(
          onPressed: () => setState(() => _stage = _SheetStage.picker),
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.postbookPrimary,
            padding: const EdgeInsets.symmetric(vertical: 14),
          ),
          child: const Text('Retry'),
        ),
        const SizedBox(height: AppSpacing.s),
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(
            'Close',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
          ),
        ),
      ],
    );
  }
}

class _MethodTile extends StatelessWidget {
  const _MethodTile({
    required this.method,
    required this.selected,
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.onTap,
    this.recommended = false,
    this.warning,
  });

  // Kept for callers; not used inside the tile body.
  // ignore: unused_field
  final String method;
  final bool selected;
  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;
  final bool recommended;
  final String? warning;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
            width: selected ? 1.5 : 1,
          ),
        ),
        child: Row(
          children: [
            Icon(icon, color: AppColors.textPrimary, size: 22),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Text(title, style: AppTextStyles.h3),
                      if (recommended) ...[
                        const SizedBox(width: AppSpacing.s),
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 6,
                            vertical: 2,
                          ),
                          decoration: BoxDecoration(
                            color: AppColors.postbookPrimary.withAlpha(40),
                            borderRadius: BorderRadius.circular(4),
                          ),
                          child: Text(
                            'RECOMMENDED',
                            style: AppTextStyles.labelTiny.copyWith(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 2),
                  Text(
                    warning ?? subtitle,
                    style: AppTextStyles.bodySmall.copyWith(
                      color: warning != null
                          ? AppColors.statusError
                          : AppColors.textTertiary,
                    ),
                  ),
                ],
              ),
            ),
            Radio<bool>(
              value: true,
              groupValue: selected,
              onChanged: (_) => onTap(),
              activeColor: AppColors.postbookPrimary,
            ),
          ],
        ),
      ),
    );
  }
}
