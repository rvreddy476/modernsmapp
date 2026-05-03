// Wallet models — Phase 2 Sprint 1 (consumer wallet).
//
// These mirror `wallet-service`'s HTTP surface at `/v1/wallet/*` (D1 of the
// Phase 2 decisions doc — AtPost is a BC of the partner bank's PPI). Money
// is in paise (₹1 = 100 paise) end-to-end; never mix paise and rupees in
// arithmetic — display via the `formatRupees(paise)` helper exported here.
//
// JSON keys are snake_case; Dart fields are camelCase. Defensive parsers so
// backend null/type drift does not crash the UI.

// ─── Resilience helpers ────────────────────────────────────────────────────

int _toInt(dynamic v) {
  if (v == null) return 0;
  if (v is int) return v;
  if (v is double) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
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

// ─── Money formatting ──────────────────────────────────────────────────────

/// Format paise as rupees with the rupee symbol and Indian-style thousands
/// separators (12,34,567.89). Negative values render with a leading minus.
///
/// ```
/// formatRupees(0)         -> '₹0'
/// formatRupees(50)        -> '₹0.50'
/// formatRupees(123456789) -> '₹12,34,567.89'
/// ```
String formatRupees(int paise, {bool withSymbol = true}) {
  final negative = paise < 0;
  final abs = paise.abs();
  final rupees = abs ~/ 100;
  final fract = abs % 100;

  // Indian grouping: last 3 digits, then groups of 2.
  final rs = rupees.toString();
  final buf = StringBuffer();
  if (rs.length <= 3) {
    buf.write(rs);
  } else {
    final tail = rs.substring(rs.length - 3);
    var head = rs.substring(0, rs.length - 3);
    final groups = <String>[];
    while (head.length > 2) {
      groups.insert(0, head.substring(head.length - 2));
      head = head.substring(0, head.length - 2);
    }
    if (head.isNotEmpty) groups.insert(0, head);
    buf.write(groups.join(','));
    buf.write(',');
    buf.write(tail);
  }

  final sym = withSymbol ? '₹' : '';
  final sign = negative ? '-' : '';
  if (fract == 0) {
    return '$sign$sym${buf.toString()}';
  }
  final f = fract.toString().padLeft(2, '0');
  return '$sign$sym${buf.toString()}.$f';
}

// ─── WalletBalance ─────────────────────────────────────────────────────────

class WalletBalance {
  final int availablePaise;
  final int pendingInPaise;
  final int pendingOutPaise;
  final String kycTier; // 'minimal' | 'full' | 'enhanced'
  final int monthlyLimitPaise;
  final bool isFrozen;
  final String? frozenReason;

  const WalletBalance({
    required this.availablePaise,
    required this.pendingInPaise,
    required this.pendingOutPaise,
    required this.kycTier,
    required this.monthlyLimitPaise,
    required this.isFrozen,
    this.frozenReason,
  });

  factory WalletBalance.fromJson(Map<String, dynamic> json) {
    return WalletBalance(
      availablePaise: _toInt(json['available_paise']),
      pendingInPaise: _toInt(json['pending_in_paise']),
      pendingOutPaise: _toInt(json['pending_out_paise']),
      kycTier: _toStr(json['kyc_tier'], 'minimal'),
      monthlyLimitPaise: _toInt(json['monthly_limit_paise']),
      isFrozen: _toBool(json['is_frozen']),
      frozenReason: _toStrOrNull(json['frozen_reason']),
    );
  }

  Map<String, dynamic> toJson() => {
        'available_paise': availablePaise,
        'pending_in_paise': pendingInPaise,
        'pending_out_paise': pendingOutPaise,
        'kyc_tier': kycTier,
        'monthly_limit_paise': monthlyLimitPaise,
        'is_frozen': isFrozen,
        if (frozenReason != null) 'frozen_reason': frozenReason,
      };

  /// Convenience: rupees label for the limit chip on the wallet home AppBar.
  /// `'minimal'` -> "₹10k limit"; `'full'` -> "₹2L limit"; otherwise the raw
  /// monthly limit rounded to the nearest sensible bucket.
  String get tierChipLabel {
    switch (kycTier) {
      case 'minimal':
        return '₹10k limit';
      case 'full':
        return '₹2L limit';
      case 'enhanced':
        return 'Enhanced';
      default:
        return formatRupees(monthlyLimitPaise);
    }
  }
}

// ─── WalletTransaction ─────────────────────────────────────────────────────

class WalletTransaction {
  final String id;
  final String type; // 'top_up' | 'send' | 'receive' | 'merchant_pay' | 'refund' | 'adjustment' | 'reversal'
  final String direction; // 'credit' | 'debit'
  final int amountPaise;
  final String? counterpartyUserId;
  final String? counterpartyPhone;
  final String? counterpartyLabel;
  final String? merchantService; // 'pulse' | 'commerce' | 'food' | 'billpay' | null
  final String? merchantRef;
  final String status; // 'pending' | 'succeeded' | 'failed' | 'reversed'
  final String? bankTxnRef;
  final String? failureReason;
  final DateTime createdAt;
  final DateTime? settledAt;

  const WalletTransaction({
    required this.id,
    required this.type,
    required this.direction,
    required this.amountPaise,
    this.counterpartyUserId,
    this.counterpartyPhone,
    this.counterpartyLabel,
    this.merchantService,
    this.merchantRef,
    required this.status,
    this.bankTxnRef,
    this.failureReason,
    required this.createdAt,
    this.settledAt,
  });

  factory WalletTransaction.fromJson(Map<String, dynamic> json) {
    return WalletTransaction(
      id: _toStr(json['id']),
      type: _toStr(json['type'], 'adjustment'),
      direction: _toStr(json['direction'], 'debit'),
      amountPaise: _toInt(json['amount_paise']),
      counterpartyUserId: _toStrOrNull(json['counterparty_user_id']),
      counterpartyPhone: _toStrOrNull(json['counterparty_phone']),
      counterpartyLabel: _toStrOrNull(json['counterparty_label']),
      merchantService: _toStrOrNull(json['merchant_service']),
      merchantRef: _toStrOrNull(json['merchant_ref']),
      status: _toStr(json['status'], 'pending'),
      bankTxnRef: _toStrOrNull(json['bank_txn_ref']),
      failureReason: _toStrOrNull(json['failure_reason']),
      createdAt: _toDate(json['created_at']),
      settledAt: _toDateOrNull(json['settled_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'type': type,
        'direction': direction,
        'amount_paise': amountPaise,
        if (counterpartyUserId != null) 'counterparty_user_id': counterpartyUserId,
        if (counterpartyPhone != null) 'counterparty_phone': counterpartyPhone,
        if (counterpartyLabel != null) 'counterparty_label': counterpartyLabel,
        if (merchantService != null) 'merchant_service': merchantService,
        if (merchantRef != null) 'merchant_ref': merchantRef,
        'status': status,
        if (bankTxnRef != null) 'bank_txn_ref': bankTxnRef,
        if (failureReason != null) 'failure_reason': failureReason,
        'created_at': createdAt.toUtc().toIso8601String(),
        if (settledAt != null) 'settled_at': settledAt!.toUtc().toIso8601String(),
      };

  bool get isCredit => direction == 'credit';
  bool get isPending => status == 'pending';
  bool get isFailed => status == 'failed';
  bool get isReversed => status == 'reversed';
}

// ─── Paged transactions ────────────────────────────────────────────────────

class WalletTransactionsPage {
  final List<WalletTransaction> items;
  final String? nextCursor;

  const WalletTransactionsPage({required this.items, this.nextCursor});

  factory WalletTransactionsPage.fromJson(Map<String, dynamic> json) {
    final raw = (json['items'] as List?) ?? const [];
    final items = raw
        .whereType<Map>()
        .map((m) => WalletTransaction.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
    return WalletTransactionsPage(
      items: items,
      nextCursor: _toStrOrNull(json['next_cursor']),
    );
  }
}

// ─── Top-up start ──────────────────────────────────────────────────────────

class WalletTopUpStart {
  final String transactionId;
  final String upiIntentUrl;
  final DateTime expiresAt;

  const WalletTopUpStart({
    required this.transactionId,
    required this.upiIntentUrl,
    required this.expiresAt,
  });

  factory WalletTopUpStart.fromJson(Map<String, dynamic> json) {
    return WalletTopUpStart(
      transactionId: _toStr(json['transaction_id']),
      upiIntentUrl: _toStr(json['upi_intent_url']),
      expiresAt: _toDate(json['expires_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'transaction_id': transactionId,
        'upi_intent_url': upiIntentUrl,
        'expires_at': expiresAt.toUtc().toIso8601String(),
      };
}

// ─── Top-up status ─────────────────────────────────────────────────────────

class WalletTopUpStatus {
  final String transactionId;
  final String status; // 'pending' | 'succeeded' | 'failed' | 'reversed'
  final String? failureReason;

  const WalletTopUpStatus({
    required this.transactionId,
    required this.status,
    this.failureReason,
  });

  factory WalletTopUpStatus.fromJson(Map<String, dynamic> json) {
    return WalletTopUpStatus(
      transactionId: _toStr(json['transaction_id']),
      status: _toStr(json['status'], 'pending'),
      failureReason: _toStrOrNull(json['failure_reason']),
    );
  }
}

// ─── Send result ───────────────────────────────────────────────────────────

class WalletSendResult {
  final String transactionId;
  final String status; // 'pending' | 'succeeded' | 'failed'
  final String? failureReason;

  const WalletSendResult({
    required this.transactionId,
    required this.status,
    this.failureReason,
  });

  factory WalletSendResult.fromJson(Map<String, dynamic> json) {
    return WalletSendResult(
      transactionId: _toStr(json['transaction_id']),
      status: _toStr(json['status'], 'pending'),
      failureReason: _toStrOrNull(json['failure_reason']),
    );
  }
}

// ─── KYC ───────────────────────────────────────────────────────────────────

class WalletKYCState {
  final String tier;
  final String? aadhaarStatus; // 'pending'|'verified'|'failed'
  final String? panStatus;
  final String? panMasked; // last 4 digits only
  final DateTime? verifiedAt;

  const WalletKYCState({
    required this.tier,
    this.aadhaarStatus,
    this.panStatus,
    this.panMasked,
    this.verifiedAt,
  });

  factory WalletKYCState.fromJson(Map<String, dynamic> json) {
    return WalletKYCState(
      tier: _toStr(json['tier'], 'minimal'),
      aadhaarStatus: _toStrOrNull(json['aadhaar_status']),
      panStatus: _toStrOrNull(json['pan_status']),
      panMasked: _toStrOrNull(json['pan_masked']),
      verifiedAt: _toDateOrNull(json['verified_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'tier': tier,
        if (aadhaarStatus != null) 'aadhaar_status': aadhaarStatus,
        if (panStatus != null) 'pan_status': panStatus,
        if (panMasked != null) 'pan_masked': panMasked,
        if (verifiedAt != null) 'verified_at': verifiedAt!.toUtc().toIso8601String(),
      };
}

// ─── KYC Aadhaar start ─────────────────────────────────────────────────────

class WalletAadhaarStart {
  final String digilockerAuthorizeUrl;
  final String state;

  const WalletAadhaarStart({
    required this.digilockerAuthorizeUrl,
    required this.state,
  });

  factory WalletAadhaarStart.fromJson(Map<String, dynamic> json) {
    return WalletAadhaarStart(
      digilockerAuthorizeUrl: _toStr(json['digilocker_authorize_url']),
      state: _toStr(json['state']),
    );
  }
}

// ─── Recipients ────────────────────────────────────────────────────────────

class WalletRecipient {
  final String? userId;
  final String? phone;
  final String? label;
  final DateTime? lastSentAt;
  final int sendCount;

  const WalletRecipient({
    this.userId,
    this.phone,
    this.label,
    this.lastSentAt,
    required this.sendCount,
  });

  factory WalletRecipient.fromJson(Map<String, dynamic> json) {
    return WalletRecipient(
      userId: _toStrOrNull(json['user_id']),
      phone: _toStrOrNull(json['phone']),
      label: _toStrOrNull(json['label']),
      lastSentAt: _toDateOrNull(json['last_sent_at']),
      sendCount: _toInt(json['send_count']),
    );
  }

  Map<String, dynamic> toJson() => {
        if (userId != null) 'user_id': userId,
        if (phone != null) 'phone': phone,
        if (label != null) 'label': label,
        if (lastSentAt != null) 'last_sent_at': lastSentAt!.toUtc().toIso8601String(),
        'send_count': sendCount,
      };

  /// A single human-readable identifier for chips and rows. Falls back through
  /// label → phone → userId so a row never renders empty.
  String get displayName {
    if (label != null && label!.isNotEmpty) return label!;
    if (phone != null && phone!.isNotEmpty) return phone!;
    if (userId != null && userId!.isNotEmpty) return userId!;
    return 'Recipient';
  }
}
