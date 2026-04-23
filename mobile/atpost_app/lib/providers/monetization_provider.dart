import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/monetization.dart';
import 'package:atpost_app/data/repositories/monetization_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for the Monetization Dashboard.
class MonetizationState {
  final EarningsSummary earnings;
  final List<PayoutRecord> payouts;
  final List<Map<String, dynamic>> history;
  final bool isLoading;

  const MonetizationState({
    this.earnings = const EarningsSummary(),
    this.payouts = const [],
    this.history = const [],
    this.isLoading = false,
  });

  MonetizationState copyWith({
    EarningsSummary? earnings,
    List<PayoutRecord>? payouts,
    List<Map<String, dynamic>>? history,
    bool? isLoading,
  }) {
    return MonetizationState(
      earnings: earnings ?? this.earnings,
      payouts: payouts ?? this.payouts,
      history: history ?? this.history,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

/// Production-ready Monetization Notifier.
class MonetizationNotifier extends StateNotifier<AsyncValue<MonetizationState>> {
  final MonetizationRepository _repo;

  MonetizationNotifier(this._repo) : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final results = await Future.wait([
        ErrorHandler.retry(() => _repo.getEarningsSummary()),
        ErrorHandler.retry(() => _repo.getPayouts()),
        ErrorHandler.retry(() => _repo.getEarningsHistory()),
      ]);

      state = AsyncValue.data(MonetizationState(
        earnings: results[0] as EarningsSummary,
        payouts: results[1] as List<PayoutRecord>,
        history: results[2] as List<Map<String, dynamic>>,
      ));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }
}

/// Provider for creator analytics.
final creatorAnalyticsProvider =
    FutureProvider.family.autoDispose<CreatorAnalytics, String>(
  (ref, period) async {
    final repo = ref.watch(monetizationRepositoryProvider);
    final summary = await repo.getEarningsSummary();
    // In a real app, this would be a specific analytics call.
    return CreatorAnalytics(
      views: (summary.thisMonth * 100).toInt(),
      likes: (summary.thisMonth * 10).toInt(),
      comments: (summary.thisMonth).toInt(),
      shares: (summary.thisMonth / 2).toInt(),
      followersGained: summary.totalSubscribers,
      dailyStats: [],
      topPosts: [],
    );
  },
);

/// Provider for subscription tiers.
final myTiersProvider = FutureProvider.autoDispose<List<dynamic>>((ref) async {
  // Mock data for now
  return [];
});

final monetizationProvider = StateNotifierProvider.autoDispose<MonetizationNotifier, AsyncValue<MonetizationState>>((ref) {
  return MonetizationNotifier(ref.watch(monetizationRepositoryProvider));
});

// --- Legacy Compatibility Providers ---
final earningsSummaryProvider = Provider.autoDispose<AsyncValue<EarningsSummary>>((ref) {
  return ref.watch(monetizationProvider).whenData((s) => s.earnings);
});

final payoutsProvider = Provider.autoDispose<AsyncValue<List<PayoutRecord>>>((ref) {
  return ref.watch(monetizationProvider).whenData((s) => s.payouts);
});

final earningsHistoryProvider = Provider.autoDispose<AsyncValue<List<Map<String, dynamic>>>>((ref) {
  return ref.watch(monetizationProvider).whenData((s) => s.history);
});
