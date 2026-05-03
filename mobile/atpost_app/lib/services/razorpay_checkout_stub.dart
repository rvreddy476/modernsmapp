// Razorpay checkout — placeholder UI for Sprint 5.
//
// `razorpay_flutter` is **not** in pubspec.yaml as of Sprint 5. This stub
// presents the same API surface so the checkout flow compiles and a real
// human can manually confirm a UPI transfer during dev/staging. Sprint 6
// will replace this with the real SDK.
//
// TODO(sprint-6): wire razorpay_flutter SDK. Add `razorpay_flutter: ^1.4.x`
// to pubspec.yaml and replace the `RazorpayCheckoutStub.open()` call site to
// use the real `Razorpay()` channel. The success path should call
// `onSuccess(paymentId)`; the failure path should call `onFailure(reason)`.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:flutter/material.dart';

/// Result type returned by the placeholder checkout sheet.
class StubCheckoutResult {
  const StubCheckoutResult({
    required this.confirmed,
    this.placeholderPaymentId,
    this.failureReason,
  });

  final bool confirmed;
  final String? placeholderPaymentId;
  final String? failureReason;
}

/// Sprint-5 placeholder. Renders a bottom sheet that explains the checkout
/// would be handed to Razorpay in production and lets the dev/QA tester
/// confirm a manual UPI transfer.
class RazorpayCheckoutStub {
  RazorpayCheckoutStub._();

  static Future<StubCheckoutResult> open(
    BuildContext context, {
    required PremiumCheckoutOrder order,
  }) async {
    final result = await showModalBottomSheet<StubCheckoutResult>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(
          top: Radius.circular(24),
        ),
      ),
      builder: (ctx) => _StubSheet(order: order),
    );
    return result ??
        const StubCheckoutResult(
          confirmed: false,
          failureReason: 'cancelled',
        );
  }
}

class _StubSheet extends StatelessWidget {
  const _StubSheet({required this.order});

  final PremiumCheckoutOrder order;

  @override
  Widget build(BuildContext context) {
    final rupees = order.amountInrPaise ~/ 100;
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
          Semantics(
            header: true,
            child: Text(
              'Razorpay UPI checkout',
              style: AppTextStyles.h2,
              textAlign: TextAlign.center,
            ),
          ),
          const SizedBox(height: AppSpacing.xs),
          Text(
            '(placeholder for SDK)',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: AppSpacing.xxl),
          _Row(label: 'Plan', value: order.planName),
          _Row(label: 'Order ID', value: order.razorpayOrderId),
          _Row(label: 'Amount', value: '₹$rupees (incl. GST)'),
          const SizedBox(height: AppSpacing.xxl),
          Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Text(
              'In production, Razorpay\'s UPI sheet appears here. For Sprint '
              '5 dev/staging, transfer manually to merchant ID '
              '"atpost-pulse@upi" and tap "I paid" below.',
              style: AppTextStyles.bodySmall,
            ),
          ),
          const SizedBox(height: AppSpacing.xxl),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            onPressed: () => Navigator.of(context).pop(
              StubCheckoutResult(
                confirmed: true,
                placeholderPaymentId:
                    'stub_${order.razorpayOrderId}_paid',
              ),
            ),
            child: Semantics(
              button: true,
              label: 'Confirm UPI payment placeholder',
              child: const Text('I paid via UPI — confirm'),
            ),
          ),
          const SizedBox(height: AppSpacing.m),
          TextButton(
            onPressed: () => Navigator.of(context).pop(
              const StubCheckoutResult(
                confirmed: false,
                failureReason: 'cancelled',
              ),
            ),
            child: const Text(
              'Cancel',
              style: TextStyle(color: AppColors.textTertiary),
            ),
          ),
        ],
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          SizedBox(
            width: 96,
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
              style: AppTextStyles.body,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}
