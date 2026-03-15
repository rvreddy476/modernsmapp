import 'package:atpost_app/data/repositories/analytics_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Raw creator analytics provider — keyed by period string ('7d', '30d', '90d').
/// Returns the untyped Map from the analytics repository.
/// Prefer [creatorAnalyticsProvider] from monetization_provider.dart for typed data.
final rawCreatorStatsProvider =
    FutureProvider.family.autoDispose<Map<String, dynamic>, String>(
        (ref, period) async {
  return ref.read(analyticsRepositoryProvider).getCreatorStats(period: period);
});
