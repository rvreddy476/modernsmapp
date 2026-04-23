import 'package:atpost_app/core/utils/app_logger.dart';

/// Production-ready Earnings Summary model.
class EarningsSummary {
  final double thisMonth;
  final int totalSubscribers;
  final double pendingPayout;
  final String currency;

  const EarningsSummary({
    this.thisMonth = 0.0,
    this.totalSubscribers = 0,
    this.pendingPayout = 0.0,
    this.currency = '₹',
  });

  factory EarningsSummary.fromJson(Map<String, dynamic> json) {
    try {
      return EarningsSummary(
        thisMonth: _toDouble(json['earnings_this_month']),
        totalSubscribers: _toInt(json['total_subscribers']),
        pendingPayout: _toDouble(json['pending_payout']),
        currency: (json['currency'] ?? '₹').toString(),
      );
    } catch (e, st) {
      AppLogger.error('EarningsSummary.fromJson failed', error: e, stackTrace: st);
      return const EarningsSummary();
    }
  }

  String get formattedThisMonth => '$currency${thisMonth.toStringAsFixed(0)}';
  String get formattedPending => '$currency${pendingPayout.toStringAsFixed(0)}';
}

/// Production-ready Payout record model.
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
      return PayoutRecord(id: 'err', amount: 0, status: 'error', createdAt: DateTime.now());
    }
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

int _toInt(dynamic data) {
  if (data is int) return data;
  if (data is String) return int.tryParse(data) ?? 0;
  return 0;
}

DateTime _parseDate(dynamic data) {
  if (data is String) return DateTime.tryParse(data) ?? DateTime.now();
  return DateTime.now();
}
