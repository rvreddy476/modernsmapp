// Bill-pay models — Phase 2 (BBPS via Setu, decision §D2).
//
// Mirror `bill-pay-service`'s HTTP surface at `/v1/billpay/*`. Money is in
// paise (int) end-to-end; never mix paise and rupees in arithmetic. Display
// via `formatRupees(paise)` from `services/money_format.dart`.
//
// JSON keys are snake_case; Dart fields are camelCase. Defensive parsers so
// backend null/type drift does not crash the UI.
//
// PRIVACY:
//   * Bill identifier (consumer number / connection id / phone) is plain text
//     here — required for the API call. UI surfaces MUST mask it (see
//     `BillAccount.maskedIdentifier`).
//   * Telemetry MUST NOT include identifier, label, phone, account_id.

// ─── Resilience helpers ────────────────────────────────────────────────────

int _toInt(dynamic v) {
  if (v == null) return 0;
  if (v is int) return v;
  if (v is double) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

int? _toIntOrNull(dynamic v) {
  if (v == null) return null;
  if (v is int) return v;
  if (v is double) return v.toInt();
  if (v is String) return int.tryParse(v);
  return null;
}

bool _toBool(dynamic v) {
  if (v is bool) return v;
  if (v is String) return v.toLowerCase() == 'true';
  if (v is num) return v != 0;
  return false;
}

String _toStr(dynamic v, [String fallback = '']) {
  if (v == null) return fallback;
  return v.toString();
}

String? _toStrOrNull(dynamic v) {
  if (v == null) return null;
  final s = v.toString();
  return s.isEmpty ? null : s;
}

DateTime? _toDateOrNull(dynamic v) {
  if (v == null) return null;
  if (v is DateTime) return v;
  if (v is String) return DateTime.tryParse(v);
  return null;
}

DateTime _toDate(dynamic v) {
  return _toDateOrNull(v) ?? DateTime.now();
}

List<String> _toStrList(dynamic v) {
  if (v is List) {
    return v.map((e) => e?.toString() ?? '').where((e) => e.isNotEmpty).toList(growable: false);
  }
  return const <String>[];
}

Map<String, String> _toStrMap(dynamic v) {
  if (v is Map) {
    return {
      for (final entry in v.entries)
        entry.key.toString(): entry.value?.toString() ?? '',
    };
  }
  return const <String, String>{};
}

/// Mask the tail of a string except the last 4 chars (or all `*` if too short).
/// Used to render bill identifiers and phone numbers safely in receipts.
String maskIdentifier(String value) {
  if (value.isEmpty) return '';
  if (value.length <= 4) return '*' * value.length;
  final tail = value.substring(value.length - 4);
  return '${'•' * (value.length - 4)}$tail';
}

// ─── BillCategory ──────────────────────────────────────────────────────────

class BillCategory {
  final String id;
  final String name;
  final String icon;
  final int sortOrder;
  final bool isActive;

  const BillCategory({
    required this.id,
    required this.name,
    required this.icon,
    required this.sortOrder,
    required this.isActive,
  });

  factory BillCategory.fromJson(Map<String, dynamic> json) {
    return BillCategory(
      id: _toStr(json['id']),
      name: _toStr(json['name']),
      icon: _toStr(json['icon'], 'receipt_long'),
      sortOrder: _toInt(json['sort_order']),
      isActive: _toBool(json['is_active']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'icon': icon,
        'sort_order': sortOrder,
        'is_active': isActive,
      };
}

// ─── CustomerParam ─────────────────────────────────────────────────────────

class CustomerParam {
  final String id;
  final String name;
  final String regex;

  const CustomerParam({
    required this.id,
    required this.name,
    required this.regex,
  });

  factory CustomerParam.fromJson(Map<String, dynamic> json) {
    return CustomerParam(
      id: _toStr(json['id']),
      name: _toStr(json['name']),
      regex: _toStr(json['regex']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'regex': regex,
      };
}

// ─── BillProvider ──────────────────────────────────────────────────────────

class BillProvider {
  final String id;
  final String setuBillerId;
  final String categoryId;
  final String name;
  final String shortName;
  final String? logoUrl;
  final List<String> states;
  final List<CustomerParam> customerParams;
  final bool billFetchSupported;
  final bool isActive;

  const BillProvider({
    required this.id,
    required this.setuBillerId,
    required this.categoryId,
    required this.name,
    required this.shortName,
    this.logoUrl,
    required this.states,
    required this.customerParams,
    required this.billFetchSupported,
    required this.isActive,
  });

  factory BillProvider.fromJson(Map<String, dynamic> json) {
    final params = (json['customer_params'] as List?) ?? const [];
    return BillProvider(
      id: _toStr(json['id']),
      setuBillerId: _toStr(json['setu_biller_id']),
      categoryId: _toStr(json['category_id']),
      name: _toStr(json['name']),
      shortName: _toStr(json['short_name'], _toStr(json['name'])),
      logoUrl: _toStrOrNull(json['logo_url']),
      states: _toStrList(json['states']),
      customerParams: params
          .whereType<Map>()
          .map((m) => CustomerParam.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false),
      billFetchSupported: _toBool(json['bill_fetch_supported']),
      isActive: _toBool(json['is_active']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'setu_biller_id': setuBillerId,
        'category_id': categoryId,
        'name': name,
        'short_name': shortName,
        if (logoUrl != null) 'logo_url': logoUrl,
        'states': states,
        'customer_params': customerParams.map((p) => p.toJson()).toList(),
        'bill_fetch_supported': billFetchSupported,
        'is_active': isActive,
      };
}

// ─── BillAccount ───────────────────────────────────────────────────────────

class BillAccount {
  final String id;
  final String userId;
  final String providerId;
  final String providerName;
  final String? providerLogoUrl;
  final String identifier;
  final Map<String, String> extraParams;
  final String label;
  final bool isDefault;
  final bool autopayEnabled;
  final DateTime createdAt;

  const BillAccount({
    required this.id,
    required this.userId,
    required this.providerId,
    required this.providerName,
    this.providerLogoUrl,
    required this.identifier,
    required this.extraParams,
    required this.label,
    required this.isDefault,
    required this.autopayEnabled,
    required this.createdAt,
  });

  factory BillAccount.fromJson(Map<String, dynamic> json) {
    return BillAccount(
      id: _toStr(json['id']),
      userId: _toStr(json['user_id']),
      providerId: _toStr(json['provider_id']),
      providerName: _toStr(json['provider_name']),
      providerLogoUrl: _toStrOrNull(json['provider_logo_url']),
      identifier: _toStr(json['identifier']),
      extraParams: _toStrMap(json['extra_params']),
      label: _toStr(json['label'], _toStr(json['provider_name'])),
      isDefault: _toBool(json['is_default']),
      autopayEnabled: _toBool(json['autopay_enabled']),
      createdAt: _toDate(json['created_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'user_id': userId,
        'provider_id': providerId,
        'provider_name': providerName,
        if (providerLogoUrl != null) 'provider_logo_url': providerLogoUrl,
        'identifier': identifier,
        'extra_params': extraParams,
        'label': label,
        'is_default': isDefault,
        'autopay_enabled': autopayEnabled,
        'created_at': createdAt.toUtc().toIso8601String(),
      };

  /// Identifier with all but last 4 chars masked. Use this in any UI surface
  /// that is not the dedicated "edit account" form.
  String get maskedIdentifier => maskIdentifier(identifier);
}

// ─── Bill ──────────────────────────────────────────────────────────────────

class Bill {
  final String id;
  final String accountId;
  final int billAmountPaise;
  final DateTime? billPeriodStart;
  final DateTime? billPeriodEnd;
  final DateTime? billDueDate;
  final String? billNumber;
  final String? customerName;
  final String status; // 'fetched' | 'paid' | 'expired' | 'failed'
  final DateTime fetchedAt;
  final DateTime? paidAt;

  const Bill({
    required this.id,
    required this.accountId,
    required this.billAmountPaise,
    this.billPeriodStart,
    this.billPeriodEnd,
    this.billDueDate,
    this.billNumber,
    this.customerName,
    required this.status,
    required this.fetchedAt,
    this.paidAt,
  });

  factory Bill.fromJson(Map<String, dynamic> json) {
    return Bill(
      id: _toStr(json['id']),
      accountId: _toStr(json['account_id']),
      billAmountPaise: _toInt(json['bill_amount_paise']),
      billPeriodStart: _toDateOrNull(json['bill_period_start']),
      billPeriodEnd: _toDateOrNull(json['bill_period_end']),
      billDueDate: _toDateOrNull(json['bill_due_date']),
      billNumber: _toStrOrNull(json['bill_number']),
      customerName: _toStrOrNull(json['customer_name']),
      status: _toStr(json['status'], 'fetched'),
      fetchedAt: _toDate(json['fetched_at']),
      paidAt: _toDateOrNull(json['paid_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'account_id': accountId,
        'bill_amount_paise': billAmountPaise,
        if (billPeriodStart != null)
          'bill_period_start': billPeriodStart!.toUtc().toIso8601String(),
        if (billPeriodEnd != null)
          'bill_period_end': billPeriodEnd!.toUtc().toIso8601String(),
        if (billDueDate != null)
          'bill_due_date': billDueDate!.toUtc().toIso8601String(),
        if (billNumber != null) 'bill_number': billNumber,
        if (customerName != null) 'customer_name': customerName,
        'status': status,
        'fetched_at': fetchedAt.toUtc().toIso8601String(),
        if (paidAt != null) 'paid_at': paidAt!.toUtc().toIso8601String(),
      };

  bool get isPaid => status == 'paid';

  /// Days remaining until due. Negative when overdue.
  int? get daysUntilDue {
    if (billDueDate == null) return null;
    final today = DateTime.now();
    final due = billDueDate!;
    return DateTime(due.year, due.month, due.day)
        .difference(DateTime(today.year, today.month, today.day))
        .inDays;
  }
}

// ─── BillPayment ───────────────────────────────────────────────────────────

class BillPayment {
  final String id;
  final String userId;
  final String? accountId;
  final String providerId;
  final String providerName;
  final int amountPaise;
  final int feePaise;
  final String paymentMethod; // 'wallet' | 'upi' | 'card'
  final String? walletTxnId;
  final String? upiTxnRef;
  final String status; // 'initiated' | 'submitted' | 'succeeded' | 'failed' | 'refunded'
  final String? failureReason;
  final String? receiptNumber; // BBPS RRN
  final String? billId;
  final String? identifier; // optional — for history display, masked at render
  final DateTime createdAt;
  final DateTime? settledAt;

  const BillPayment({
    required this.id,
    required this.userId,
    this.accountId,
    required this.providerId,
    required this.providerName,
    required this.amountPaise,
    required this.feePaise,
    required this.paymentMethod,
    this.walletTxnId,
    this.upiTxnRef,
    required this.status,
    this.failureReason,
    this.receiptNumber,
    this.billId,
    this.identifier,
    required this.createdAt,
    this.settledAt,
  });

  factory BillPayment.fromJson(Map<String, dynamic> json) {
    return BillPayment(
      id: _toStr(json['id']),
      userId: _toStr(json['user_id']),
      accountId: _toStrOrNull(json['account_id']),
      providerId: _toStr(json['provider_id']),
      providerName: _toStr(json['provider_name']),
      amountPaise: _toInt(json['amount_paise']),
      feePaise: _toInt(json['fee_paise']),
      paymentMethod: _toStr(json['payment_method'], 'wallet'),
      walletTxnId: _toStrOrNull(json['wallet_txn_id']),
      upiTxnRef: _toStrOrNull(json['upi_txn_ref']),
      status: _toStr(json['status'], 'initiated'),
      failureReason: _toStrOrNull(json['failure_reason']),
      receiptNumber: _toStrOrNull(json['receipt_number']),
      billId: _toStrOrNull(json['bill_id']),
      identifier: _toStrOrNull(json['identifier']),
      createdAt: _toDate(json['created_at']),
      settledAt: _toDateOrNull(json['settled_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'user_id': userId,
        if (accountId != null) 'account_id': accountId,
        'provider_id': providerId,
        'provider_name': providerName,
        'amount_paise': amountPaise,
        'fee_paise': feePaise,
        'payment_method': paymentMethod,
        if (walletTxnId != null) 'wallet_txn_id': walletTxnId,
        if (upiTxnRef != null) 'upi_txn_ref': upiTxnRef,
        'status': status,
        if (failureReason != null) 'failure_reason': failureReason,
        if (receiptNumber != null) 'receipt_number': receiptNumber,
        if (billId != null) 'bill_id': billId,
        if (identifier != null) 'identifier': identifier,
        'created_at': createdAt.toUtc().toIso8601String(),
        if (settledAt != null) 'settled_at': settledAt!.toUtc().toIso8601String(),
      };

  bool get isSucceeded => status == 'succeeded';
  bool get isFailed => status == 'failed';
  bool get isPending => status == 'initiated' || status == 'submitted';
  bool get isRefunded => status == 'refunded';

  String? get maskedIdentifier =>
      identifier == null ? null : maskIdentifier(identifier!);

  int get totalPaise => amountPaise + feePaise;
}

// ─── Paged BillPayments ────────────────────────────────────────────────────

class BillPaymentsPage {
  final List<BillPayment> items;
  final String? nextCursor;

  const BillPaymentsPage({required this.items, this.nextCursor});

  factory BillPaymentsPage.fromJson(Map<String, dynamic> json) {
    final raw = (json['items'] as List?) ?? const [];
    return BillPaymentsPage(
      items: raw
          .whereType<Map>()
          .map((m) => BillPayment.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false),
      nextCursor: _toStrOrNull(json['next_cursor']),
    );
  }
}

// ─── MobilePlan ────────────────────────────────────────────────────────────

class MobilePlan {
  final String id;
  final String operator;
  final String circle;
  final int planAmountPaise;
  final int? validityDays;
  final double? dataGbPerDay;
  final int? talktimePaise;
  final int? smsCountPerDay;
  final String description;
  final String category; // 'unlimited'|'data'|'talktime'|'topup'|'roaming'

  const MobilePlan({
    required this.id,
    required this.operator,
    required this.circle,
    required this.planAmountPaise,
    this.validityDays,
    this.dataGbPerDay,
    this.talktimePaise,
    this.smsCountPerDay,
    required this.description,
    required this.category,
  });

  factory MobilePlan.fromJson(Map<String, dynamic> json) {
    final dataRaw = json['data_gb_per_day'];
    double? dataGb;
    if (dataRaw is num) dataGb = dataRaw.toDouble();
    if (dataRaw is String) dataGb = double.tryParse(dataRaw);
    return MobilePlan(
      id: _toStr(json['id']),
      operator: _toStr(json['operator']),
      circle: _toStr(json['circle']),
      planAmountPaise: _toInt(json['plan_amount_paise']),
      validityDays: _toIntOrNull(json['validity_days']),
      dataGbPerDay: dataGb,
      talktimePaise: _toIntOrNull(json['talktime_paise']),
      smsCountPerDay: _toIntOrNull(json['sms_count_per_day']),
      description: _toStr(json['description']),
      category: _toStr(json['category'], 'topup'),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'operator': operator,
        'circle': circle,
        'plan_amount_paise': planAmountPaise,
        if (validityDays != null) 'validity_days': validityDays,
        if (dataGbPerDay != null) 'data_gb_per_day': dataGbPerDay,
        if (talktimePaise != null) 'talktime_paise': talktimePaise,
        if (smsCountPerDay != null) 'sms_count_per_day': smsCountPerDay,
        'description': description,
        'category': category,
      };
}

// ─── OperatorCircle (recharge auto-detect) ─────────────────────────────────

class OperatorCircle {
  final String operator;
  final String circle;

  const OperatorCircle({required this.operator, required this.circle});

  factory OperatorCircle.fromJson(Map<String, dynamic> json) {
    return OperatorCircle(
      operator: _toStr(json['operator']),
      circle: _toStr(json['circle']),
    );
  }

  Map<String, dynamic> toJson() => {
        'operator': operator,
        'circle': circle,
      };
}

// ─── BillReminder ──────────────────────────────────────────────────────────

class BillReminder {
  final String id;
  final String accountId;
  final String userId;
  final int daysBeforeDue;
  final List<String> channels; // 'push' | 'sms' | 'email'
  final bool isActive;
  final DateTime? lastSentAt;

  const BillReminder({
    required this.id,
    required this.accountId,
    required this.userId,
    required this.daysBeforeDue,
    required this.channels,
    required this.isActive,
    this.lastSentAt,
  });

  factory BillReminder.fromJson(Map<String, dynamic> json) {
    return BillReminder(
      id: _toStr(json['id']),
      accountId: _toStr(json['account_id']),
      userId: _toStr(json['user_id']),
      daysBeforeDue: _toInt(json['days_before_due']),
      channels: _toStrList(json['channels']),
      isActive: _toBool(json['is_active']),
      lastSentAt: _toDateOrNull(json['last_sent_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'account_id': accountId,
        'user_id': userId,
        'days_before_due': daysBeforeDue,
        'channels': channels,
        'is_active': isActive,
        if (lastSentAt != null) 'last_sent_at': lastSentAt!.toUtc().toIso8601String(),
      };
}

// ─── ScheduledPayment ──────────────────────────────────────────────────────

class ScheduledPayment {
  final String id;
  final String accountId;
  final String accountLabel;
  final int? amountPaise; // null = pay full bill
  final String paymentMethod; // 'wallet' | 'upi' | 'card'
  final String scheduleKind; // 'monthly' | 'weekly' | 'one_time'
  final DateTime nextRunDate;
  final DateTime? lastRunAt;
  final bool isActive;

  const ScheduledPayment({
    required this.id,
    required this.accountId,
    required this.accountLabel,
    this.amountPaise,
    required this.paymentMethod,
    required this.scheduleKind,
    required this.nextRunDate,
    this.lastRunAt,
    required this.isActive,
  });

  factory ScheduledPayment.fromJson(Map<String, dynamic> json) {
    return ScheduledPayment(
      id: _toStr(json['id']),
      accountId: _toStr(json['account_id']),
      accountLabel: _toStr(json['account_label']),
      amountPaise: _toIntOrNull(json['amount_paise']),
      paymentMethod: _toStr(json['payment_method'], 'wallet'),
      scheduleKind: _toStr(json['schedule_kind'], 'monthly'),
      nextRunDate: _toDate(json['next_run_date']),
      lastRunAt: _toDateOrNull(json['last_run_at']),
      isActive: _toBool(json['is_active']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'account_id': accountId,
        'account_label': accountLabel,
        if (amountPaise != null) 'amount_paise': amountPaise,
        'payment_method': paymentMethod,
        'schedule_kind': scheduleKind,
        'next_run_date': nextRunDate.toUtc().toIso8601String(),
        if (lastRunAt != null) 'last_run_at': lastRunAt!.toUtc().toIso8601String(),
        'is_active': isActive,
      };
}

// ─── Pay result ────────────────────────────────────────────────────────────

class BillPayResult {
  final String paymentId;
  final String status; // initiated | submitted | succeeded | failed
  final String? receiptNumber;
  final String? failureReason;

  const BillPayResult({
    required this.paymentId,
    required this.status,
    this.receiptNumber,
    this.failureReason,
  });

  factory BillPayResult.fromJson(Map<String, dynamic> json) {
    return BillPayResult(
      paymentId: _toStr(json['payment_id'], _toStr(json['id'])),
      status: _toStr(json['status'], 'initiated'),
      receiptNumber: _toStrOrNull(json['receipt_number']),
      failureReason: _toStrOrNull(json['failure_reason']),
    );
  }
}
