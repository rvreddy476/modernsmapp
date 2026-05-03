// Premium checkout flow — Sprint 5.
//
// State machine:
//   idle            → user hasn't tapped Continue yet.
//   creatingOrder   → POST /v1/dating/premium/checkout in flight.
//   awaitingPayment → order back, Razorpay sheet (or stub) on screen.
//   verifying       → user came back from checkout; we poll
//                     /v1/dating/premium/me until it flips to active.
//   succeeded       → premium is live. UI shows the welcome sheet.
//   failed          → terminal-ish error; UI offers retry.
//
// Why a poll instead of a callback? Razorpay's payment success webhook is
// authoritative on the server, not the client (the device call could be
// closed mid-flow). Polling /premium/me for ~30 seconds gives us a robust
// "did the webhook land?" check.

import 'dart:async';

import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:atpost_app/services/razorpay_checkout_stub.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

enum CheckoutPhase {
  idle,
  creatingOrder,
  awaitingPayment,
  verifying,
  succeeded,
  failed,
}

class CheckoutState {
  const CheckoutState({
    this.phase = CheckoutPhase.idle,
    this.planId,
    this.order,
    this.errorMessage,
  });

  final CheckoutPhase phase;
  final String? planId;
  final PremiumCheckoutOrder? order;
  final String? errorMessage;

  CheckoutState copyWith({
    CheckoutPhase? phase,
    String? planId,
    PremiumCheckoutOrder? order,
    String? errorMessage,
    bool clearError = false,
  }) {
    return CheckoutState(
      phase: phase ?? this.phase,
      planId: planId ?? this.planId,
      order: order ?? this.order,
      errorMessage: clearError ? null : (errorMessage ?? this.errorMessage),
    );
  }
}

class CheckoutFlow extends StateNotifier<CheckoutState> {
  CheckoutFlow(this._repo, this._telemetry) : super(const CheckoutState());

  final PulseRepository _repo;
  final PulseTelemetry _telemetry;

  /// Top-level entry from the Premium screen / paywall. `source` is a short
  /// attribution string (`premium_screen`, `paywall:boost`, etc).
  Future<void> begin({
    required BuildContext context,
    required String planId,
    required String source,
  }) async {
    state = CheckoutState(
      phase: CheckoutPhase.creatingOrder,
      planId: planId,
    );
    _telemetry.checkoutStarted(planId: planId);
    PulseBreadcrumbs.premiumCheckoutStart(planId: planId);

    PremiumCheckoutOrder order;
    try {
      order = await _repo.startCheckout(planId: planId, source: source);
    } catch (e) {
      _telemetry.checkoutFailed(
        planId: planId,
        reason: 'create_order_failed',
      );
      PulseBreadcrumbs.premiumCheckoutFail(
        planId: planId,
        reason: 'create_order_failed',
      );
      state = state.copyWith(
        phase: CheckoutPhase.failed,
        errorMessage: 'Could not start checkout. Please try again.',
      );
      return;
    }

    state = state.copyWith(
      phase: CheckoutPhase.awaitingPayment,
      order: order,
    );

    if (!context.mounted) {
      _telemetry.checkoutFailed(planId: planId, reason: 'context_unmounted');
      state = state.copyWith(phase: CheckoutPhase.failed);
      return;
    }

    // TODO(sprint-6): if `razorpay_flutter` is added to pubspec.yaml, replace
    // the stub call below with the real `Razorpay()` instance and listen on
    // its `EVENT_PAYMENT_SUCCESS` / `EVENT_PAYMENT_ERROR` channels. The stub
    // returns a fake payment id we ignore — the server-side webhook is the
    // authoritative success signal.
    final result = await RazorpayCheckoutStub.open(context, order: order);

    if (!result.confirmed) {
      _telemetry.checkoutFailed(
        planId: planId,
        reason: result.failureReason ?? 'cancelled',
      );
      PulseBreadcrumbs.premiumCheckoutFail(
        planId: planId,
        reason: result.failureReason ?? 'cancelled',
      );
      state = state.copyWith(
        phase: CheckoutPhase.failed,
        errorMessage: 'Checkout cancelled.',
      );
      return;
    }

    // Poll /premium/me up to ~30s to confirm the webhook landed.
    state = state.copyWith(phase: CheckoutPhase.verifying);
    final confirmed = await _pollForActive();
    if (confirmed) {
      _telemetry.checkoutCompleted(planId: planId);
      PulseBreadcrumbs.premiumCheckoutComplete(planId: planId);
      state = state.copyWith(
        phase: CheckoutPhase.succeeded,
        clearError: true,
      );
    } else {
      _telemetry.checkoutFailed(
        planId: planId,
        reason: 'verification_timeout',
      );
      PulseBreadcrumbs.premiumCheckoutFail(
        planId: planId,
        reason: 'verification_timeout',
      );
      state = state.copyWith(
        phase: CheckoutPhase.failed,
        errorMessage:
            'Payment received but activation is still pending. We will email '
            'when your Premium is live.',
      );
    }
  }

  Future<bool> _pollForActive() async {
    for (var i = 0; i < 10; i++) {
      try {
        final p = await _repo.getPremium();
        if (p.active) return true;
      } catch (_) {
        // ignore — try again.
      }
      await Future.delayed(const Duration(seconds: 3));
    }
    return false;
  }

  void reset() {
    state = const CheckoutState();
  }
}

// Explicit type annotation breaks the inference cycle: the body's
// `ref.listen<CheckoutState>(checkoutFlowProvider, ...)` references the
// provider being declared, so without the explicit type Dart can't
// infer it.
final AutoDisposeStateNotifierProvider<CheckoutFlow, CheckoutState>
    checkoutFlowProvider =
    StateNotifierProvider.autoDispose<CheckoutFlow, CheckoutState>((ref) {
  final repo = ref.watch(pulseRepositoryProvider);
  final telemetry = ref.watch(pulseTelemetryProvider);
  final flow = CheckoutFlow(repo, telemetry);
  ref.listen<CheckoutState>(checkoutFlowProvider, (prev, next) {
    if (prev?.phase != CheckoutPhase.succeeded &&
        next.phase == CheckoutPhase.succeeded) {
      // Refresh dependent providers when premium activates.
      ref.invalidate(premiumStateProvider);
    }
  });
  return flow;
});
