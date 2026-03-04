import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class AnalyticsRepository {
  final ApiClient _client;
  AnalyticsRepository(this._client);

  // Fetch creator analytics for the current user.
  // [period] is one of '7d', '30d', '90d'
  Future<Map<String, dynamic>> getCreatorStats({String period = '7d'}) async {
    final response = await _client.get(
      '/v1/analytics/creator/me',
      queryParameters: {'period': period},
    );
    return Map<String, dynamic>.from(response.data);
  }
}

final analyticsRepositoryProvider = Provider<AnalyticsRepository>((ref) {
  return AnalyticsRepository(ref.read(apiClientProvider));
});
