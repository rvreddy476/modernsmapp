// Bill-pay repository — Phase 2 (BBPS via Setu).
//
// Wraps `bill-pay-service`'s HTTP surface at `/v1/billpay/*`. Contract is
// locked in PHASE_2_DECISIONS.md §D2 (Setu BBPS aggregator). Idempotency
// keys are caller-supplied — every `pay()` and `rechargeMobile()` call MUST
// pass a fresh UUID; the backend dedupes on it.

import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class BillPayRepository {
  BillPayRepository(this._api);

  final ApiClient _api;

  // ─── Categories ───────────────────────────────────────────────────────

  /// `GET /v1/billpay/categories`
  Future<List<BillCategory>> getCategories() async {
    final res = await _api.get('/v1/billpay/categories');
    final data = res.data['data'];
    final list = (data is List ? data : (res.data as List? ?? const []));
    return list
        .whereType<Map>()
        .map((m) => BillCategory.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── Providers ────────────────────────────────────────────────────────

  /// `GET /v1/billpay/providers?category=&state=`
  Future<List<BillProvider>> getProviders({
    String? categoryId,
    String? state,
  }) async {
    final params = <String, dynamic>{};
    if (categoryId != null && categoryId.isNotEmpty) {
      params['category'] = categoryId;
    }
    if (state != null && state.isNotEmpty) params['state'] = state;
    final res = await _api.get(
      '/v1/billpay/providers',
      queryParameters: params,
    );
    final data = res.data['data'];
    final list = (data is List ? data : (res.data as List? ?? const []));
    return list
        .whereType<Map>()
        .map((m) => BillProvider.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// `GET /v1/billpay/providers/:id`
  Future<BillProvider> getProvider(String id) async {
    final res = await _api.get('/v1/billpay/providers/$id');
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return BillProvider.fromJson(Map<String, dynamic>.from(map));
  }

  // ─── Accounts (saved billers) ────────────────────────────────────────

  /// `GET /v1/billpay/accounts`
  Future<List<BillAccount>> getAccounts() async {
    final res = await _api.get('/v1/billpay/accounts');
    final data = res.data['data'];
    final list = (data is List ? data : (res.data as List? ?? const []));
    return list
        .whereType<Map>()
        .map((m) => BillAccount.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// `POST /v1/billpay/accounts`
  Future<BillAccount> addAccount({
    required String providerId,
    required String identifier,
    Map<String, String> extraParams = const {},
    String? label,
  }) async {
    final res = await _api.post(
      '/v1/billpay/accounts',
      data: {
        'provider_id': providerId,
        'identifier': identifier,
        if (extraParams.isNotEmpty) 'extra_params': extraParams,
        if (label != null && label.isNotEmpty) 'label': label,
      },
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return BillAccount.fromJson(Map<String, dynamic>.from(map));
  }

  /// `PATCH /v1/billpay/accounts/:id`
  Future<BillAccount> updateAccount(
    String id, {
    String? label,
    bool? isDefault,
    bool? autopayEnabled,
  }) async {
    final body = <String, dynamic>{};
    if (label != null) body['label'] = label;
    if (isDefault != null) body['is_default'] = isDefault;
    if (autopayEnabled != null) body['autopay_enabled'] = autopayEnabled;
    final res = await _api.patch(
      '/v1/billpay/accounts/$id',
      data: body,
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return BillAccount.fromJson(Map<String, dynamic>.from(map));
  }

  /// `DELETE /v1/billpay/accounts/:id`
  Future<void> deleteAccount(String id) async {
    await _api.delete('/v1/billpay/accounts/$id');
  }

  // ─── Bill (per-account latest) ───────────────────────────────────────

  /// `GET /v1/billpay/accounts/:id/bill` — fetches latest bill from Setu
  /// (cached server-side).
  Future<Bill> getLatestBill(String accountId) async {
    final res = await _api.get('/v1/billpay/accounts/$accountId/bill');
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return Bill.fromJson(Map<String, dynamic>.from(map));
  }

  // ─── Pay ─────────────────────────────────────────────────────────────

  /// `POST /v1/billpay/pay`. `idempotencyKey` MUST be a fresh UUID per call.
  Future<BillPayResult> pay({
    String? accountId,
    required String providerId,
    required String identifier,
    required int amountPaise,
    required String paymentMethod, // 'wallet' | 'upi' | 'card'
    required String idempotencyKey,
    String? billId,
  }) async {
    final res = await _api.post(
      '/v1/billpay/pay',
      data: {
        if (accountId != null && accountId.isNotEmpty) 'account_id': accountId,
        'provider_id': providerId,
        'identifier': identifier,
        'amount_paise': amountPaise,
        'payment_method': paymentMethod,
        'idempotency_key': idempotencyKey,
        if (billId != null && billId.isNotEmpty) 'bill_id': billId,
      },
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return BillPayResult.fromJson(Map<String, dynamic>.from(map));
  }

  // ─── Payments ────────────────────────────────────────────────────────

  /// `GET /v1/billpay/payments?limit=&cursor=&status=`
  Future<BillPaymentsPage> getPayments({
    int limit = 20,
    String? cursor,
    String? status,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null && cursor.isNotEmpty) params['cursor'] = cursor;
    if (status != null && status.isNotEmpty) params['status'] = status;
    final res = await _api.get(
      '/v1/billpay/payments',
      queryParameters: params,
    );
    final data = res.data['data'];
    if (data is Map) {
      return BillPaymentsPage.fromJson(Map<String, dynamic>.from(data));
    }
    if (data is List) {
      final items = data
          .whereType<Map>()
          .map((m) => BillPayment.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false);
      return BillPaymentsPage(items: items, nextCursor: null);
    }
    return const BillPaymentsPage(items: [], nextCursor: null);
  }

  /// `GET /v1/billpay/payments/:id` — receipt detail.
  Future<BillPayment> getPayment(String id) async {
    final res = await _api.get('/v1/billpay/payments/$id');
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return BillPayment.fromJson(Map<String, dynamic>.from(map));
  }

  // ─── Mobile recharge ─────────────────────────────────────────────────

  /// `POST /v1/billpay/recharge/mobile`. Fresh UUID per call.
  Future<BillPayResult> rechargeMobile({
    required String phone,
    required String operator,
    required String circle,
    required int amountPaise,
    String? planId,
    required String paymentMethod,
    required String idempotencyKey,
  }) async {
    final res = await _api.post(
      '/v1/billpay/recharge/mobile',
      data: {
        'phone': phone,
        'operator': operator,
        'circle': circle,
        'amount_paise': amountPaise,
        if (planId != null && planId.isNotEmpty) 'plan_id': planId,
        'payment_method': paymentMethod,
        'idempotency_key': idempotencyKey,
      },
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return BillPayResult.fromJson(Map<String, dynamic>.from(map));
  }

  /// `GET /v1/billpay/recharge/operator-circle?phone=`
  Future<OperatorCircle> detectOperatorCircle(String phone) async {
    final res = await _api.get(
      '/v1/billpay/recharge/operator-circle',
      queryParameters: {'phone': phone},
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return OperatorCircle.fromJson(Map<String, dynamic>.from(map));
  }

  /// `GET /v1/billpay/recharge/plans?operator=&circle=`
  Future<List<MobilePlan>> getPlans({
    required String operator,
    required String circle,
  }) async {
    final res = await _api.get(
      '/v1/billpay/recharge/plans',
      queryParameters: {'operator': operator, 'circle': circle},
    );
    final data = res.data['data'];
    final list = (data is List ? data : (res.data as List? ?? const []));
    return list
        .whereType<Map>()
        .map((m) => MobilePlan.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── Reminders ───────────────────────────────────────────────────────

  /// `GET /v1/billpay/reminders`
  Future<List<BillReminder>> getReminders() async {
    final res = await _api.get('/v1/billpay/reminders');
    final data = res.data['data'];
    final list = (data is List ? data : (res.data as List? ?? const []));
    return list
        .whereType<Map>()
        .map((m) => BillReminder.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// `POST /v1/billpay/reminders`
  Future<BillReminder> addReminder({
    required String accountId,
    required int daysBeforeDue,
    required List<String> channels,
  }) async {
    final res = await _api.post(
      '/v1/billpay/reminders',
      data: {
        'account_id': accountId,
        'days_before_due': daysBeforeDue,
        'channels': channels,
      },
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return BillReminder.fromJson(Map<String, dynamic>.from(map));
  }

  /// `DELETE /v1/billpay/reminders/:id`
  Future<void> deleteReminder(String id) async {
    await _api.delete('/v1/billpay/reminders/$id');
  }

  // ─── Scheduled payments ──────────────────────────────────────────────

  /// `GET /v1/billpay/scheduled`
  Future<List<ScheduledPayment>> getScheduled() async {
    final res = await _api.get('/v1/billpay/scheduled');
    final data = res.data['data'];
    final list = (data is List ? data : (res.data as List? ?? const []));
    return list
        .whereType<Map>()
        .map((m) => ScheduledPayment.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// `POST /v1/billpay/scheduled`
  Future<ScheduledPayment> addScheduled({
    required String accountId,
    int? amountPaise,
    required String paymentMethod,
    required String scheduleKind,
    required DateTime nextRunDate,
  }) async {
    final res = await _api.post(
      '/v1/billpay/scheduled',
      data: {
        'account_id': accountId,
        if (amountPaise != null) 'amount_paise': amountPaise,
        'payment_method': paymentMethod,
        'schedule_kind': scheduleKind,
        'next_run_date': nextRunDate.toUtc().toIso8601String(),
      },
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return ScheduledPayment.fromJson(Map<String, dynamic>.from(map));
  }

  /// `PATCH /v1/billpay/scheduled/:id`
  Future<ScheduledPayment> updateScheduled(
    String id, {
    bool? isActive,
  }) async {
    final body = <String, dynamic>{};
    if (isActive != null) body['is_active'] = isActive;
    final res = await _api.patch(
      '/v1/billpay/scheduled/$id',
      data: body,
    );
    final data = res.data['data'];
    final map = (data is Map ? data : res.data) as Map;
    return ScheduledPayment.fromJson(Map<String, dynamic>.from(map));
  }

  /// `DELETE /v1/billpay/scheduled/:id`
  Future<void> deleteScheduled(String id) async {
    await _api.delete('/v1/billpay/scheduled/$id');
  }
}

final billpayRepositoryProvider = Provider<BillPayRepository>((ref) {
  return BillPayRepository(ref.watch(apiClientProvider));
});
