import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// Raw earnings summary data
final earningsSummaryProvider =
    FutureProvider.autoDispose<Map<String, dynamic>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/monetization/earnings/summary');
  return Map<String, dynamic>.from(
      response.data['data'] as Map? ?? {});
});

// Subscription tiers list
final myTiersProvider =
    FutureProvider.autoDispose<List<Map<String, dynamic>>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/monetization/tiers');
  final items = (response.data['data'] as List<dynamic>?) ?? [];
  return items
      .map((e) => Map<String, dynamic>.from(e as Map))
      .toList();
});

// Payouts list
final payoutsProvider =
    FutureProvider.autoDispose<List<Map<String, dynamic>>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/monetization/payouts');
  final items = (response.data['data'] as List<dynamic>?) ?? [];
  return items
      .map((e) => Map<String, dynamic>.from(e as Map))
      .toList();
});

// Creator stats - parameterized by period
final creatorStatsProvider = FutureProvider.autoDispose
    .family<Map<String, dynamic>, String>((ref, period) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/analytics/creator/me',
    queryParameters: {'period': period},
  );
  return Map<String, dynamic>.from(
      response.data['data'] as Map? ?? response.data as Map? ?? {});
});

/// Parsed daily stat entry for charts.
class DailyStat {
  final String date;
  final int views;
  final int likes;
  final int comments;
  final int shares;

  const DailyStat({
    required this.date,
    this.views = 0,
    this.likes = 0,
    this.comments = 0,
    this.shares = 0,
  });

  factory DailyStat.fromJson(Map<String, dynamic> json) {
    return DailyStat(
      date: json['date'] as String? ?? '',
      views: json['views'] as int? ?? 0,
      likes: json['likes'] as int? ?? 0,
      comments: json['comments'] as int? ?? 0,
      shares: json['shares'] as int? ?? 0,
    );
  }
}

/// Parsed creator analytics with typed fields.
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
    final dailyList = (json['daily_stats'] as List<dynamic>?) ?? [];
    final topList = (json['top_posts'] as List<dynamic>?) ?? [];
    return CreatorAnalytics(
      views: json['views'] as int? ??
          json['total_views'] as int? ?? 0,
      likes: json['likes'] as int? ??
          json['total_likes'] as int? ?? 0,
      comments: json['comments'] as int? ??
          json['total_comments'] as int? ?? 0,
      shares: json['shares'] as int? ?? 0,
      followersGained: json['followers_gained'] as int? ??
          json['follower_growth'] as int? ?? 0,
      dailyStats: dailyList
          .map((e) => DailyStat.fromJson(Map<String, dynamic>.from(e as Map)))
          .toList(),
      topPosts: topList
          .map((e) => Map<String, dynamic>.from(e as Map))
          .toList(),
    );
  }
}

/// Typed creator analytics provider.
final creatorAnalyticsProvider = FutureProvider.autoDispose
    .family<CreatorAnalytics, String>((ref, period) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/analytics/creator/me',
    queryParameters: {'period': period},
  );
  final data = response.data['data'] as Map? ?? response.data as Map? ?? {};
  return CreatorAnalytics.fromJson(Map<String, dynamic>.from(data));
});

/// Earnings history for the line chart on the dashboard.
final earningsHistoryProvider =
    FutureProvider.autoDispose<List<Map<String, dynamic>>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/analytics/creator/me',
    queryParameters: {'period': '30d'},
  );
  final data = response.data['data'] as Map? ?? response.data as Map? ?? {};
  final dailyList = (data['daily_stats'] as List<dynamic>?) ?? [];
  return dailyList
      .map((e) => Map<String, dynamic>.from(e as Map))
      .toList();
});
