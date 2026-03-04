import 'package:atpost_app/data/repositories/analytics_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Creator analytics provider — keyed by period string ('7d', '30d', '90d').
final creatorStatsProvider =
    FutureProvider.family.autoDispose<Map<String, dynamic>, String>(
        (ref, period) async {
  return ref.read(analyticsRepositoryProvider).getCreatorStats(period: period);
});
