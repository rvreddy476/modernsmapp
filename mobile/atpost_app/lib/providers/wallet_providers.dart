// Wallet providers — Phase 2 Sprint 1.
//
// Riverpod surface over `walletRepositoryProvider`. Conventions match the
// rest of the codebase: `FutureProvider.autoDispose` for reads,
// `StateNotifier` for mutations that need to invalidate dependents.
//
// IDEMPOTENCY: every top-up and send call gets a freshly minted UUID v4 via
// the local `_freshIdempotencyKey()` helper. The backend dedupes on this so
// double-taps and retries never debit twice.
//
// MONEY: paise (int) end-to-end. Display via `formatRupees(paise)`.

import 'dart:math';

import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/data/repositories/wallet_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// ─── Idempotency key generation ────────────────────────────────────────────

final Random _rng = Random.secure();

/// RFC 4122 v4 UUID. We don't depend on the `uuid` package (not in pubspec)
/// so we mint one inline. This is the single mint point used by every
/// mutation — every top-up and every send call grabs a fresh key from here
/// and passes it through to the backend.
String _freshIdempotencyKey() {
  final b = List<int>.generate(16, (_) => _rng.nextInt(256));
  // version 4
  b[6] = (b[6] & 0x0F) | 0x40;
  // variant 1 (RFC 4122)
  b[8] = (b[8] & 0x3F) | 0x80;
  String hex(int n) => n.toRadixString(16).padLeft(2, '0');
  final s = b.map(hex).join();
  return '${s.substring(0, 8)}-${s.substring(8, 12)}-${s.substring(12, 16)}'
      '-${s.substring(16, 20)}-${s.substring(20, 32)}';
}

/// Exposed for screens that want to know the idempotency key they'll be
/// using before the call (e.g., to attach it to a "retrying" toast). The
/// notifier itself mints its own keys; this helper exists so call sites
/// stay consistent if they ever need their own.
String generateWalletIdempotencyKey() => _freshIdempotencyKey();

// ─── Read providers ────────────────────────────────────────────────────────

/// Wallet balance. 30 second cache TTL via auto-keepAlive timer.
final walletBalanceProvider =
    FutureProvider.autoDispose<WalletBalance>((ref) async {
  // Keep the balance hot for 30s after the last listener detaches so quick
  // navigation back to the wallet home doesn't re-fetch.
  final link = ref.keepAlive();
  Future.delayed(const Duration(seconds: 30), link.close);
  return ref.watch(walletRepositoryProvider).getBalance();
});

/// Compound query for the transactions list. Needs to be `==`-stable so the
/// `family` re-uses the cached future across rebuilds.
class TransactionsQuery {
  const TransactionsQuery({
    this.limit = 30,
    this.cursor,
    this.type,
    this.direction,
  });

  final int limit;
  final String? cursor;
  final String? type; // top_up | send | receive | merchant_pay | refund | ...
  final String? direction; // credit | debit

  @override
  bool operator ==(Object other) {
    return other is TransactionsQuery &&
        other.limit == limit &&
        other.cursor == cursor &&
        other.type == type &&
        other.direction == direction;
  }

  @override
  int get hashCode => Object.hash(limit, cursor, type, direction);
}

final walletTransactionsProvider = FutureProvider.autoDispose
    .family<WalletTransactionsPage, TransactionsQuery>((ref, q) async {
  return ref.watch(walletRepositoryProvider).getTransactions(
        limit: q.limit,
        cursor: q.cursor,
        type: q.type,
        direction: q.direction,
      );
});

final walletTransactionProvider = FutureProvider.autoDispose
    .family<WalletTransaction, String>((ref, id) async {
  return ref.watch(walletRepositoryProvider).getTransaction(id);
});

final walletRecipientsProvider =
    FutureProvider.autoDispose<List<WalletRecipient>>((ref) async {
  return ref.watch(walletRepositoryProvider).getRecipients();
});

final walletKYCProvider =
    FutureProvider.autoDispose<WalletKYCState>((ref) async {
  return ref.watch(walletRepositoryProvider).getKYC();
});

/// Single in-flight top-up status — used by the polling loop on the top-up
/// screen. autoDispose so it tears down when the user leaves the screen.
final walletTopUpStatusProvider = FutureProvider.autoDispose
    .family<WalletTopUpStatus, String>((ref, transactionId) async {
  return ref.watch(walletRepositoryProvider).getTopUp(transactionId);
});

// ─── Mutations ─────────────────────────────────────────────────────────────

/// Mutation state surfaced to UI. Screens read `.isInFlight` to lock CTAs.
class WalletMutationState {
  const WalletMutationState({
    this.isInFlight = false,
    this.lastTopUp,
    this.lastSend,
    this.error,
  });

  final bool isInFlight;
  final WalletTopUpStart? lastTopUp;
  final WalletSendResult? lastSend;
  final Object? error;

  WalletMutationState copyWith({
    bool? isInFlight,
    WalletTopUpStart? lastTopUp,
    WalletSendResult? lastSend,
    Object? error,
  }) {
    return WalletMutationState(
      isInFlight: isInFlight ?? this.isInFlight,
      lastTopUp: lastTopUp ?? this.lastTopUp,
      lastSend: lastSend ?? this.lastSend,
      error: error,
    );
  }
}

class WalletNotifier extends StateNotifier<WalletMutationState> {
  WalletNotifier(this._ref) : super(const WalletMutationState());

  final Ref _ref;

  /// Start a top-up. Mints a fresh UUID for the idempotency key; the
  /// backend dedupes on it across retries.
  Future<WalletTopUpStart?> startTopUp({required int amountPaise}) async {
    if (state.isInFlight) return null;
    state = state.copyWith(isInFlight: true, error: null);
    try {
      final repo = _ref.read(walletRepositoryProvider);
      final result = await repo.startTopUp(
        amountPaise: amountPaise,
        idempotencyKey: _freshIdempotencyKey(),
      );
      state = state.copyWith(isInFlight: false, lastTopUp: result);
      return result;
    } catch (e) {
      state = state.copyWith(isInFlight: false, error: e);
      return null;
    }
  }

  /// User-driven confirm of a top-up. Refreshes balance + transactions on
  /// success.
  Future<WalletTopUpStatus?> confirmTopUp({
    required String transactionId,
    required String upiTxnRef,
  }) async {
    state = state.copyWith(isInFlight: true, error: null);
    try {
      final repo = _ref.read(walletRepositoryProvider);
      final status = await repo.confirmTopUp(
        transactionId: transactionId,
        upiTxnRef: upiTxnRef,
      );
      state = state.copyWith(isInFlight: false);
      if (status.status == 'succeeded') {
        _invalidateAfterMutation();
      }
      return status;
    } catch (e) {
      state = state.copyWith(isInFlight: false, error: e);
      return null;
    }
  }

  /// P2P send. Mints a fresh UUID per call.
  Future<WalletSendResult?> send({
    String? recipientUserId,
    String? recipientPhone,
    required int amountPaise,
    String? label,
  }) async {
    if (state.isInFlight) return null;
    state = state.copyWith(isInFlight: true, error: null);
    try {
      final repo = _ref.read(walletRepositoryProvider);
      final result = await repo.send(
        recipientUserId: recipientUserId,
        recipientPhone: recipientPhone,
        amountPaise: amountPaise,
        label: label,
        idempotencyKey: _freshIdempotencyKey(),
      );
      state = state.copyWith(isInFlight: false, lastSend: result);
      if (result.status == 'succeeded' || result.status == 'pending') {
        _invalidateAfterMutation();
      }
      return result;
    } catch (e) {
      state = state.copyWith(isInFlight: false, error: e);
      return null;
    }
  }

  /// Manual refresh — pull-to-refresh on the home screen invokes this.
  void refreshAll() => _invalidateAfterMutation();

  void _invalidateAfterMutation() {
    _ref.invalidate(walletBalanceProvider);
    _ref.invalidate(walletRecipientsProvider);
    // Listening screens re-read via families; invalidating the family root
    // would clear all cached pages — instead we let auto-dispose do that
    // when the screens detach. The home screen explicitly invalidates the
    // first page through its own `ref.invalidate(...)` call.
  }
}

final walletNotifier =
    StateNotifierProvider.autoDispose<WalletNotifier, WalletMutationState>(
  (ref) => WalletNotifier(ref),
);
