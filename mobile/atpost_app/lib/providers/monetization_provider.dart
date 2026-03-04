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
