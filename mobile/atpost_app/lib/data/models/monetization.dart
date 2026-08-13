import 'package:atpost_app/core/utils/app_logger.dart';

/// A strict view of the authoritative creator ledger. Money stays in integer
/// paise; malformed responses throw instead of masquerading as zero.
class EarningsSummary {
  final int availablePaise;
  final int pendingPayoutPaise;
  final int lifetimeEarningsPaise;
  final String currency;
  final bool isFrozen;
  final bool hasActivity;
  final DateTime? updatedAt;

  const EarningsSummary({
    this.availablePaise = 0,
    this.pendingPayoutPaise = 0,
    this.lifetimeEarningsPaise = 0,
    this.currency = 'INR',
    this.isFrozen = false,
    this.hasActivity = false,
    this.updatedAt,
  });

  factory EarningsSummary.fromJson(Map<String, dynamic> json) {
    int requiredPaise(String key) {
      final value = json[key];
      if (value is int) return value;
      if (value is num && value == value.roundToDouble()) return value.toInt();
      throw FormatException('Missing or invalid $key');
    }

    final currency = json['currency'];
    final updated = json['updated_at'];
    final hasActivity = json['has_activity'];
    if (currency is! String || currency.length != 3 || hasActivity is! bool) {
      throw const FormatException('Invalid creator ledger metadata');
    }
    final updatedAt = updated is String ? DateTime.tryParse(updated) : null;
    if (hasActivity && updatedAt == null) {
      throw const FormatException('Invalid creator ledger updated_at');
    }
    return EarningsSummary(
      availablePaise: requiredPaise('balance_paise'),
      pendingPayoutPaise: requiredPaise('pending_payout_paise'),
      lifetimeEarningsPaise: requiredPaise('lifetime_earnings_paise'),
      currency: currency,
      isFrozen: json['is_frozen'] == true,
      hasActivity: hasActivity,
      updatedAt: updatedAt,
    );
  }

  String get _symbol => currency == 'INR' ? '₹' : currency;
  String _format(int paise) => '$_symbol ${(paise / 100).toStringAsFixed(2)}';
  String get formattedAvailable => _format(availablePaise);
  String get formattedPending => _format(pendingPayoutPaise);
  String get formattedLifetime => _format(lifetimeEarningsPaise);

  // Compatibility getters for older read-only widgets in this launch pass.
  double get thisMonth => lifetimeEarningsPaise / 100;
  double get pendingPayout => pendingPayoutPaise / 100;
  int get totalSubscribers => 0;
  String get formattedThisMonth => formattedLifetime;
}

/// Production-ready Payout record model.
/// Phase F1.2 — per-line commerce earning row from
/// /v1/commerce/seller/earnings (Phase 4.4 backend). Replaces the legacy
/// /v1/shop/payouts ledger on the seller monetization dashboard.
class SellerEarning {
  final String orderItemId;
  final String orderId;
  final String orderNumber;
  final String productTitle;
  final String sku;
  final int quantity;
  final double grossAmount;
  final double commissionAmount;
  final double platformFee;
  final double tdsAmount;
  final double netAmount;
  final String? paymentMethod;
  final String status;
  final DateTime? deliveredAt;

  const SellerEarning({
    required this.orderItemId,
    required this.orderId,
    required this.orderNumber,
    required this.productTitle,
    required this.sku,
    required this.quantity,
    required this.grossAmount,
    required this.commissionAmount,
    required this.platformFee,
    required this.tdsAmount,
    required this.netAmount,
    this.paymentMethod,
    required this.status,
    this.deliveredAt,
  });

  factory SellerEarning.fromJson(Map<String, dynamic> json) {
    try {
      return SellerEarning(
        orderItemId: (json['order_item_id'] ?? '').toString(),
        orderId: (json['order_id'] ?? '').toString(),
        orderNumber: (json['order_number'] ?? '').toString(),
        productTitle: (json['product_title'] ?? '').toString(),
        sku: (json['sku'] ?? '').toString(),
        quantity: (json['quantity'] is num)
            ? (json['quantity'] as num).toInt()
            : 0,
        grossAmount: _toDouble(json['gross_amount']),
        commissionAmount: _toDouble(json['commission_amount']),
        platformFee: _toDouble(json['platform_fee']),
        tdsAmount: _toDouble(json['tds_amount']),
        netAmount: _toDouble(json['net_amount']),
        paymentMethod: json['payment_method']?.toString(),
        status: (json['status'] ?? '').toString(),
        deliveredAt: json['delivered_at'] is String
            ? DateTime.tryParse(json['delivered_at'] as String)
            : null,
      );
    } catch (e, st) {
      AppLogger.error(
        'SellerEarning.fromJson failed',
        error: e,
        stackTrace: st,
      );
      return const SellerEarning(
        orderItemId: 'err',
        orderId: '',
        orderNumber: '',
        productTitle: '',
        sku: '',
        quantity: 0,
        grossAmount: 0,
        commissionAmount: 0,
        platformFee: 0,
        tdsAmount: 0,
        netAmount: 0,
        status: 'error',
      );
    }
  }
}

class PayoutRecord {
  final String id;
  final double amount;
  final String status; // 'completed', 'pending', 'failed'
  final DateTime createdAt;
  final String? method;

  const PayoutRecord({
    required this.id,
    required this.amount,
    required this.status,
    required this.createdAt,
    this.method,
  });

  factory PayoutRecord.fromJson(Map<String, dynamic> json) {
    try {
      return PayoutRecord(
        id: (json['id'] ?? '').toString(),
        amount: _toDouble(json['amount']),
        status: (json['status'] ?? 'pending').toString().toLowerCase(),
        createdAt: _parseDate(json['created_at']),
        method: json['method']?.toString(),
      );
    } catch (e, st) {
      AppLogger.error('PayoutRecord.fromJson failed', error: e, stackTrace: st);
      return PayoutRecord(
        id: 'err',
        amount: 0,
        status: 'error',
        createdAt: DateTime.now(),
      );
    }
  }

  factory PayoutRecord.fromLedgerJson(Map<String, dynamic> json) {
    final amount = json['amount_paise'];
    if (amount is! num) {
      throw const FormatException('Invalid payout amount_paise');
    }
    return PayoutRecord(
      id: (json['id'] ?? '').toString(),
      amount: amount.toInt() / 100,
      status: (json['status'] ?? 'pending').toString().toLowerCase(),
      createdAt: _parseDate(json['created_at']),
      method: json['reference_type']?.toString(),
    );
  }
}

class DailyStat {
  final String date;
  final int views;

  const DailyStat({required this.date, required this.views});

  factory DailyStat.fromJson(Map<String, dynamic> json) {
    return DailyStat(
      date: json['date']?.toString() ?? '',
      views: (json['views'] as num?)?.toInt() ?? 0,
    );
  }
}

class CreatorAnalytics {
  final int views;
  final int likes;
  final int comments;
  final int shares;
  final int followersGained;
  final List<DailyStat> dailyStats;
  final List<Map<String, dynamic>> topPosts;

  const CreatorAnalytics({
    this.views = 0,
    this.likes = 0,
    this.comments = 0,
    this.shares = 0,
    this.followersGained = 0,
    this.dailyStats = const [],
    this.topPosts = const [],
  });

  factory CreatorAnalytics.fromJson(Map<String, dynamic> json) {
    return CreatorAnalytics(
      views: (json['views'] as num?)?.toInt() ?? 0,
      likes: (json['likes'] as num?)?.toInt() ?? 0,
      comments: (json['comments'] as num?)?.toInt() ?? 0,
      shares: (json['shares'] as num?)?.toInt() ?? 0,
      followersGained: (json['followers_gained'] as num?)?.toInt() ?? 0,
      dailyStats: ((json['daily_stats'] as List?) ?? [])
          .map((e) => DailyStat.fromJson(e as Map<String, dynamic>))
          .toList(),
      topPosts: ((json['top_posts'] as List?) ?? [])
          .map((e) => Map<String, dynamic>.from(e as Map))
          .toList(),
    );
  }
}

// --- Resilience Helpers ---
double _toDouble(dynamic data) {
  if (data is double) return data;
  if (data is int) return data.toDouble();
  if (data is String) return double.tryParse(data) ?? 0.0;
  return 0.0;
}

DateTime _parseDate(dynamic data) {
  if (data is String) return DateTime.tryParse(data) ?? DateTime.now();
  return DateTime.now();
}
