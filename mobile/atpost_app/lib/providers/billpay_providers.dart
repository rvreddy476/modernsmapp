// Bill-pay providers — Phase 2 (BBPS via Setu).
//
// Riverpod surface over `billpayRepositoryProvider`.
//
// IDEMPOTENCY: `BillPayNotifier` mints a fresh UUID v4 for every `pay()` and
// `rechargeMobile()` call. The backend dedupes on it so a double-tap or a
// retry never debits twice.
//
// MONEY: paise (int) end-to-end. Display via `formatRupees(paise)`.

import 'dart:math';

import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/repositories/billpay_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// ─── Idempotency key generation ────────────────────────────────────────────

final Random _rng = Random.secure();

/// RFC 4122 v4 UUID. We don't depend on the `uuid` package (not in pubspec)
/// so we mint one inline. Single mint point used by every mutation.
String _freshIdempotencyKey() {
  final b = List<int>.generate(16, (_) => _rng.nextInt(256));
  b[6] = (b[6] & 0x0F) | 0x40; // version 4
  b[8] = (b[8] & 0x3F) | 0x80; // variant 1
  String hex(int n) => n.toRadixString(16).padLeft(2, '0');
  final s = b.map(hex).join();
  return '${s.substring(0, 8)}-${s.substring(8, 12)}-${s.substring(12, 16)}'
      '-${s.substring(16, 20)}-${s.substring(20, 32)}';
}

/// Exposed for screens that want to know the idempotency key they will be
/// using before the call. The notifier itself mints its own keys; this helper
/// exists so any extra retry path stays consistent.
String generateBillPayIdempotencyKey() => _freshIdempotencyKey();

// ─── Read providers ────────────────────────────────────────────────────────

/// Categories rarely change — cache aggressively (kept alive while listened
/// to; refresh on pull-to-refresh).
final billCategoriesProvider =
    FutureProvider<List<BillCategory>>((ref) async {
  return ref.watch(billpayRepositoryProvider).getCategories();
});

/// Compound query for providers list — needs to be `==`-stable so the
/// `family` re-uses the cached future across rebuilds.
class ProvidersQuery {
  const ProvidersQuery({required this.categoryId, this.state});

  final String categoryId;
  final String? state;

  @override
  bool operator ==(Object other) {
    return other is ProvidersQuery &&
        other.categoryId == categoryId &&
        other.state == state;
  }

  @override
  int get hashCode => Object.hash(categoryId, state);
}

final billProvidersProvider = FutureProvider.autoDispose
    .family<List<BillProvider>, ProvidersQuery>((ref, q) async {
  return ref.watch(billpayRepositoryProvider).getProviders(
        categoryId: q.categoryId,
        state: q.state,
      );
});

final billProviderDetailProvider =
    FutureProvider.autoDispose.family<BillProvider, String>((ref, id) async {
  return ref.watch(billpayRepositoryProvider).getProvider(id);
});

/// Saved billers for the current user.
final billAccountsProvider =
    FutureProvider.autoDispose<List<BillAccount>>((ref) async {
  return ref.watch(billpayRepositoryProvider).getAccounts();
});

/// Latest bill for an account (server fetches from Setu and caches).
final billProvider =
    FutureProvider.autoDispose.family<Bill, String>((ref, accountId) async {
  return ref.watch(billpayRepositoryProvider).getLatestBill(accountId);
});

/// Compound query for paged payments.
class PaymentsQuery {
  const PaymentsQuery({
    this.limit = 20,
    this.cursor,
    this.status,
  });

  final int limit;
  final String? cursor;
  final String? status; // initiated|submitted|succeeded|failed|refunded

  @override
  bool operator ==(Object other) {
    return other is PaymentsQuery &&
        other.limit == limit &&
        other.cursor == cursor &&
        other.status == status;
  }

  @override
  int get hashCode => Object.hash(limit, cursor, status);
}

final billPaymentsProvider = FutureProvider.autoDispose
    .family<BillPaymentsPage, PaymentsQuery>((ref, q) async {
  return ref.watch(billpayRepositoryProvider).getPayments(
        limit: q.limit,
        cursor: q.cursor,
        status: q.status,
      );
});

final billPaymentDetailProvider =
    FutureProvider.autoDispose.family<BillPayment, String>((ref, id) async {
  return ref.watch(billpayRepositoryProvider).getPayment(id);
});

class OperatorCircleQuery {
  const OperatorCircleQuery({required this.operator, required this.circle});

  final String operator;
  final String circle;

  @override
  bool operator ==(Object other) {
    return other is OperatorCircleQuery &&
        other.operator == operator &&
        other.circle == circle;
  }

  @override
  int get hashCode => Object.hash(operator, circle);
}

final mobilePlansProvider = FutureProvider.autoDispose
    .family<List<MobilePlan>, OperatorCircleQuery>((ref, q) async {
  return ref.watch(billpayRepositoryProvider).getPlans(
        operator: q.operator,
        circle: q.circle,
      );
});

final mobileOperatorCircleProvider =
    FutureProvider.autoDispose.family<OperatorCircle, String>((ref, phone) async {
  return ref.watch(billpayRepositoryProvider).detectOperatorCircle(phone);
});

final billRemindersProvider =
    FutureProvider.autoDispose<List<BillReminder>>((ref) async {
  return ref.watch(billpayRepositoryProvider).getReminders();
});

final billScheduledProvider =
    FutureProvider.autoDispose<List<ScheduledPayment>>((ref) async {
  return ref.watch(billpayRepositoryProvider).getScheduled();
});

// ─── Mutation surface ──────────────────────────────────────────────────────

class BillPayMutationState {
  const BillPayMutationState({
    this.isInFlight = false,
    this.lastResult,
    this.error,
  });

  final bool isInFlight;
  final BillPayResult? lastResult;
  final Object? error;

  BillPayMutationState copyWith({
    bool? isInFlight,
    BillPayResult? lastResult,
    Object? error,
  }) {
    return BillPayMutationState(
      isInFlight: isInFlight ?? this.isInFlight,
      lastResult: lastResult ?? this.lastResult,
      error: error,
    );
  }
}

class BillPayNotifier extends StateNotifier<BillPayMutationState> {
  BillPayNotifier(this._ref) : super(const BillPayMutationState());

  final Ref _ref;

  /// Pay a bill. Mints a fresh UUID for the idempotency key.
  Future<BillPayResult?> pay({
    String? accountId,
    required String providerId,
    required String identifier,
    required int amountPaise,
    required String paymentMethod,
    String? billId,
  }) async {
    if (state.isInFlight) return null;
    state = state.copyWith(isInFlight: true, error: null);
    try {
      final repo = _ref.read(billpayRepositoryProvider);
      final result = await repo.pay(
        accountId: accountId,
        providerId: providerId,
        identifier: identifier,
        amountPaise: amountPaise,
        paymentMethod: paymentMethod,
        idempotencyKey: _freshIdempotencyKey(),
        billId: billId,
      );
      state = state.copyWith(isInFlight: false, lastResult: result);
      _invalidateAfterMutation();
      return result;
    } catch (e) {
      state = state.copyWith(isInFlight: false, error: e);
      return null;
    }
  }

  /// Mobile recharge. Mints a fresh UUID per call.
  Future<BillPayResult?> rechargeMobile({
    required String phone,
    required String operator,
    required String circle,
    required int amountPaise,
    String? planId,
    required String paymentMethod,
  }) async {
    if (state.isInFlight) return null;
    state = state.copyWith(isInFlight: true, error: null);
    try {
      final repo = _ref.read(billpayRepositoryProvider);
      final result = await repo.rechargeMobile(
        phone: phone,
        operator: operator,
        circle: circle,
        amountPaise: amountPaise,
        planId: planId,
        paymentMethod: paymentMethod,
        idempotencyKey: _freshIdempotencyKey(),
      );
      state = state.copyWith(isInFlight: false, lastResult: result);
      _invalidateAfterMutation();
      return result;
    } catch (e) {
      state = state.copyWith(isInFlight: false, error: e);
      return null;
    }
  }

  void clear() {
    state = const BillPayMutationState();
  }

  void _invalidateAfterMutation() {
    _ref.invalidate(billAccountsProvider);
  }
}

final billPayMutationProvider =
    StateNotifierProvider<BillPayNotifier, BillPayMutationState>(
  (ref) => BillPayNotifier(ref),
);
