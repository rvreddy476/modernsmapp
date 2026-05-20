// Razorpay commerce checkout — real `razorpay_flutter` adapter (Phase 1.4).
//
// Mirrors the surface of [RazorpayCommerceStub] so the checkout screen can
// pick one or the other behind a single env flag without branching on
// result types. Resolves with [CommerceStubResult] on success, error, or
// user cancel — caller stays linear.
//
// Release builds always use this adapter; debug builds may fall back to
// the stub when `--dart-define=ENABLE_STUB_PAYMENTS=true` is set.

import 'dart:async';

import 'package:atpost_app/services/razorpay_commerce_stub.dart';
import 'package:flutter/widgets.dart';
import 'package:razorpay_flutter/razorpay_flutter.dart';

class RazorpayCommerce {
  RazorpayCommerce._();

  /// Opens the Razorpay UPI/cards/netbanking sheet.
  ///
  /// [args.razorpayOrderId] MUST be a payments-service provider_ref — a
  /// raw AtPost order id will fail because Razorpay validates that the
  /// open() call's order_id was issued by their /orders API.
  /// [razorpayKeyId] is the public key (passed in at build time via
  /// --dart-define=RAZORPAY_KEY_ID=...). Empty → caller should have
  /// surfaced a config error before reaching this method.
  static Future<CommerceStubResult> open(
    BuildContext context, {
    required CommerceStubArgs args,
    required String razorpayKeyId,
    String? customerName,
    String? customerEmail,
    String? customerPhone,
  }) async {
    if (args.razorpayOrderId == null || args.razorpayOrderId!.isEmpty) {
      return const CommerceStubResult(
        confirmed: false,
        failureReason: 'missing_razorpay_order_id',
      );
    }
    if (razorpayKeyId.isEmpty) {
      return const CommerceStubResult(
        confirmed: false,
        failureReason: 'missing_razorpay_key',
      );
    }

    final completer = Completer<CommerceStubResult>();
    final rzp = Razorpay();

    rzp.on(Razorpay.EVENT_PAYMENT_SUCCESS, (PaymentSuccessResponse r) {
      if (!completer.isCompleted) {
        completer.complete(
          CommerceStubResult(
            confirmed: true,
            razorpayOrderId: r.orderId ?? args.razorpayOrderId,
            razorpayPaymentId: r.paymentId,
            razorpaySignature: r.signature,
          ),
        );
      }
      rzp.clear();
    });
    rzp.on(Razorpay.EVENT_PAYMENT_ERROR, (PaymentFailureResponse r) {
      if (!completer.isCompleted) {
        completer.complete(
          CommerceStubResult(
            confirmed: false,
            failureReason: r.message ?? 'payment_failed',
          ),
        );
      }
      rzp.clear();
    });
    rzp.on(Razorpay.EVENT_EXTERNAL_WALLET, (ExternalWalletResponse r) {
      // The success/error event still fires when the user returns from
      // an external wallet (e.g. PhonePe deep-link), so we deliberately
      // don't complete here — that would race the actual outcome.
    });

    final prefill = <String, dynamic>{};
    if (customerName != null && customerName.isNotEmpty) {
      prefill['name'] = customerName;
    }
    if (customerEmail != null && customerEmail.isNotEmpty) {
      prefill['email'] = customerEmail;
    }
    if (customerPhone != null && customerPhone.isNotEmpty) {
      prefill['contact'] = customerPhone;
    }

    rzp.open({
      'key': razorpayKeyId,
      'order_id': args.razorpayOrderId,
      'amount': args.amountInPaise,
      'currency': 'INR',
      'name': 'VChat',
      'description': 'Order ${args.orderId}',
      if (prefill.isNotEmpty) 'prefill': prefill,
    });

    return completer.future;
  }
}
