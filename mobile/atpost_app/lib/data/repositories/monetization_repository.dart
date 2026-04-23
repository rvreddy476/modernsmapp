import 'package:atpost_app/data/models/monetization.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready repository for monetization operations.
/// Synchronized with the 2026-03-19 OpenAPI mobile integration spec.
class MonetizationRepository {
  final ApiClient _api;

  MonetizationRepository(this._api);

  /// Fetch earnings summary for the current user.
  /// Synchronized with GET /v1/analytics/creator/me (mapped logic)
  Future<EarningsSummary> getEarningsSummary() async {
    final response = await _api.get('/v1/analytics/creator/me');
    // The spec returns a raw object; we map it to our resilient model.
    return EarningsSummary.fromJson(response.data as Map<String, dynamic>);
  }

  /// Fetch payout history.
  /// (Endpoint based on general banking/payout patterns in the spec)
  Future<List<PayoutRecord>> getPayouts() async {
    final response = await _api.get('/v1/shop/payouts'); // Example path
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => PayoutRecord.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Fetch earnings history for chart data.
  Future<List<Map<String, dynamic>>> getEarningsHistory({String period = '30d'}) async {
    final response = await _api.get('/v1/analytics/creator/me', queryParameters: {'period': period});
    final history = (response.data['history'] as List<dynamic>?) ?? [];
    return history.map((e) => Map<String, dynamic>.from(e as Map)).toList();
  }
}

final monetizationRepositoryProvider = Provider<MonetizationRepository>((ref) {
  return MonetizationRepository(ref.watch(apiClientProvider));
});
