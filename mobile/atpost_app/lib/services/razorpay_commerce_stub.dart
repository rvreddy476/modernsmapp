// Razorpay commerce checkout — Sprint 1 placeholder.
//
// `razorpay_flutter` is not in pubspec.yaml. This stub presents the same API
// surface so the commerce checkout flow compiles and a real human can
// manually confirm the UPI transfer during dev/staging. Pattern matches
// `razorpay_checkout_stub.dart` (Pulse Sprint 5).
//
// TODO(sprint-2): wire the real `razorpay_flutter` SDK. The `confirm` button
// here will be replaced with the SDK's success/failure callbacks; on success
// we post `razorpay_payment_id` + `razorpay_order_id` + `razorpay_signature`
// to `/v1/commerce/orders/:id/payment/confirm`.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

/// What happened in the (placeholder) Razorpay sheet.
class CommerceStubResult {
  const CommerceStubResult({
    required this.confirmed,
    this.razorpayOrderId,
    this.razorpayPaymentId,
    this.razorpaySignature,
    this.failureReason,
  });

  final bool confirmed;
  final String? razorpayOrderId;
  final String? razorpayPaymentId;
  final String? razorpaySignature;
  final String? failureReason;
}

/// Args for the placeholder. The `orderId` here is the AtPost order id —
/// Razorpay's gateway-side order id is created server-side at checkout and
/// is what the real SDK consumes. For the stub we surface it the same way.
class CommerceStubArgs {
  const CommerceStubArgs({
    required this.orderId,
    required this.amountInPaise,
    this.razorpayOrderId,
  });

  final String orderId;
  final int amountInPaise;
  final String? razorpayOrderId;
}

class RazorpayCommerceStub {
  RazorpayCommerceStub._();

  static Future<CommerceStubResult> open(
    BuildContext context, {
    required CommerceStubArgs args,
  }) async {
    final result = await showModalBottomSheet<CommerceStubResult>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (ctx) => _StubSheet(args: args),
    );
    return result ??
        const CommerceStubResult(
          confirmed: false,
          failureReason: 'cancelled',
        );
  }
}

class _StubSheet extends StatelessWidget {
  const _StubSheet({required this.args});

  final CommerceStubArgs args;

  @override
  Widget build(BuildContext context) {
    final rupees = args.amountInPaise ~/ 100;
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
            '(SDK to wire in next sprint)',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: AppSpacing.xxl),
          _Row(label: 'Order ID', value: args.orderId),
          if (args.razorpayOrderId != null)
            _Row(label: 'Razorpay', value: args.razorpayOrderId!),
          _Row(label: 'Amount', value: 'Rs. $rupees'),
          const SizedBox(height: AppSpacing.xxl),
          Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Text(
              'In production, the Razorpay UPI/cards/netbanking sheet appears '
              'here. For Sprint 1 dev/staging, transfer manually to merchant '
              'ID "atpost-commerce@upi" and tap "I paid, confirm" below.',
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
              CommerceStubResult(
                confirmed: true,
                razorpayOrderId:
                    args.razorpayOrderId ?? 'stub_${args.orderId}_rzp',
                razorpayPaymentId: 'stub_${args.orderId}_pay',
                razorpaySignature: 'stub_signature',
              ),
            ),
            child: Semantics(
              button: true,
              label: 'Confirm UPI payment placeholder',
              child: const Text('I paid, confirm'),
            ),
          ),
          const SizedBox(height: AppSpacing.m),
          TextButton(
            onPressed: () => Navigator.of(context).pop(
              const CommerceStubResult(
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
