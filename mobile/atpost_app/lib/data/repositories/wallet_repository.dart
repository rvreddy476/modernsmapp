// Wallet repository — Phase 2 Sprint 1 (consumer wallet).
//
// Wraps `wallet-service`'s HTTP surface at `/v1/wallet/*`. The contract is
// locked in PHASE_2_DECISIONS.md §D1 — AtPost is a Business Correspondent of
// the partner bank that holds the PPI license. This client is shape-only;
// idempotency keys are caller-supplied (mutations need a fresh UUID per call
// — the backend dedupes on it).

import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class WalletRepository {
  WalletRepository(this._api);

  final ApiClient _api;

  // ─── Balance ──────────────────────────────────────────────────────────

  /// `GET /v1/wallet/balance` — current available + pending balances, KYC
  /// tier, monthly limit, freeze flag.
  Future<WalletBalance> getBalance() async {
    final res = await _api.get('/v1/wallet/balance');
    final data = res.data['data'];
    if (data is Map) {
      return WalletBalance.fromJson(Map<String, dynamic>.from(data));
    }
    return WalletBalance.fromJson(Map<String, dynamic>.from(res.data as Map));
  }

  // ─── Top-up ───────────────────────────────────────────────────────────

  /// `POST /v1/wallet/top-up` — start a UPI Intent top-up. Returns the UPI
  /// URL to launch in the user's UPI app and the AtPost-side transaction id
  /// to poll. `idempotencyKey` MUST be a fresh UUID per call.
  Future<WalletTopUpStart> startTopUp({
    required int amountPaise,
    required String idempotencyKey,
  }) async {
    final res = await _api.post(
      '/v1/wallet/top-up',
      data: {
        'amount_paise': amountPaise,
        'idempotency_key': idempotencyKey,
      },
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletTopUpStart.fromJson(Map<String, dynamic>.from(map));
  }

  /// `POST /v1/wallet/top-up/:id/confirm` — user-driven confirmation when
  /// the UPI app does not auto-callback. Optional but improves UX.
  Future<WalletTopUpStatus> confirmTopUp({
    required String transactionId,
    required String upiTxnRef,
  }) async {
    final res = await _api.post(
      '/v1/wallet/top-up/$transactionId/confirm',
      data: {'upi_txn_ref': upiTxnRef},
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletTopUpStatus.fromJson(Map<String, dynamic>.from(map));
  }

  /// `GET /v1/wallet/top-up/:id` — poll for top-up status.
  Future<WalletTopUpStatus> getTopUp(String transactionId) async {
    final res = await _api.get('/v1/wallet/top-up/$transactionId');
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletTopUpStatus.fromJson(Map<String, dynamic>.from(map));
  }

  // ─── Send ─────────────────────────────────────────────────────────────

  /// `POST /v1/wallet/send` — P2P send to another AtPost user (by user id)
  /// or to a phone number (the backend either resolves to a user or queues
  /// the receive for first login). `idempotencyKey` MUST be a fresh UUID.
  Future<WalletSendResult> send({
    String? recipientUserId,
    String? recipientPhone,
    required int amountPaise,
    String? label,
    required String idempotencyKey,
  }) async {
    assert(
      (recipientUserId != null && recipientUserId.isNotEmpty) ||
          (recipientPhone != null && recipientPhone.isNotEmpty),
      'send requires either recipientUserId or recipientPhone',
    );
    final res = await _api.post(
      '/v1/wallet/send',
      data: {
        if (recipientUserId != null && recipientUserId.isNotEmpty)
          'recipient_user_id': recipientUserId,
        if (recipientPhone != null && recipientPhone.isNotEmpty)
          'recipient_phone': recipientPhone,
        'amount_paise': amountPaise,
        if (label != null && label.isNotEmpty) 'label': label,
        'idempotency_key': idempotencyKey,
      },
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletSendResult.fromJson(Map<String, dynamic>.from(map));
  }

  // ─── Transactions ────────────────────────────────────────────────────

  /// `GET /v1/wallet/transactions` — cursor-paged. Filters: `type` (one of
  /// the WalletTransaction.type enum) and `direction` (`credit`|`debit`).
  Future<WalletTransactionsPage> getTransactions({
    int limit = 30,
    String? cursor,
    String? type,
    String? direction,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null && cursor.isNotEmpty) params['cursor'] = cursor;
    if (type != null && type.isNotEmpty) params['type'] = type;
    if (direction != null && direction.isNotEmpty) {
      params['direction'] = direction;
    }
    final res = await _api.get(
      '/v1/wallet/transactions',
      queryParameters: params,
    );
    final data = res.data['data'];
    if (data is Map) {
      return WalletTransactionsPage.fromJson(Map<String, dynamic>.from(data));
    }
    if (data is List) {
      // Backend may flatten when nextCursor is null.
      final items = data
          .whereType<Map>()
          .map((m) => WalletTransaction.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false);
      return WalletTransactionsPage(items: items, nextCursor: null);
    }
    return const WalletTransactionsPage(items: [], nextCursor: null);
  }

  /// `GET /v1/wallet/transactions/:id` — single transaction detail.
  Future<WalletTransaction> getTransaction(String id) async {
    final res = await _api.get('/v1/wallet/transactions/$id');
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletTransaction.fromJson(Map<String, dynamic>.from(map));
  }

  // ─── Recipients ──────────────────────────────────────────────────────

  /// `GET /v1/wallet/recipients` — top frequent recipients (server-ranked).
  Future<List<WalletRecipient>> getRecipients() async {
    final res = await _api.get('/v1/wallet/recipients');
    final data = res.data['data'];
    final list = (data is List ? data : (res.data as List? ?? const []));
    return list
        .whereType<Map>()
        .map((m) => WalletRecipient.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── KYC ─────────────────────────────────────────────────────────────

  /// `GET /v1/wallet/kyc` — current KYC state.
  Future<WalletKYCState> getKYC() async {
    final res = await _api.get('/v1/wallet/kyc');
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletKYCState.fromJson(Map<String, dynamic>.from(map));
  }

  /// `POST /v1/wallet/kyc/aadhaar/start` — mints DigiLocker authorize URL
  /// and a state nonce. The same DPDP disclosure as Pulse applies — the
  /// caller must show "We use DigiLocker. We never store your Aadhaar number."
  Future<WalletAadhaarStart> startAadhaarKYC() async {
    final res = await _api.post('/v1/wallet/kyc/aadhaar/start');
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletAadhaarStart.fromJson(Map<String, dynamic>.from(map));
  }

  /// `POST /v1/wallet/kyc/aadhaar/callback` — completes the DigiLocker flow
  /// using the redirect's `code` and the original `state`. On success the
  /// tier upgrades server-side and the next `getKYC`/`getBalance` reflects it.
  Future<WalletKYCState> completeAadhaarKYC({
    required String code,
    required String state,
  }) async {
    final res = await _api.post(
      '/v1/wallet/kyc/aadhaar/callback',
      data: {'code': code, 'state': state},
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletKYCState.fromJson(Map<String, dynamic>.from(map));
  }

  /// `POST /v1/wallet/kyc/pan` — submit PAN for queued verification. The
  /// backend stores only a masked form; the response surfaces the masked
  /// last-4 in `pan_masked`.
  Future<WalletKYCState> submitPAN(String panNumber) async {
    final res = await _api.post(
      '/v1/wallet/kyc/pan',
      data: {'pan_number': panNumber},
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return WalletKYCState.fromJson(Map<String, dynamic>.from(map));
  }
}

final walletRepositoryProvider = Provider<WalletRepository>((ref) {
  final api = ref.watch(apiClientProvider);
  return WalletRepository(api);
});
