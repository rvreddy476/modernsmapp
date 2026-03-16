import 'package:atpost_app/data/models/wellbeing.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class WellbeingRepository {
  final ApiClient _api;

  WellbeingRepository(this._api);

  Future<DigitalWellbeing> getWellbeing() async {
    final response = await _api.get('/v1/users/me/wellbeing');
    final data = response.data['data'] ?? response.data;
    return DigitalWellbeing.fromJson(data as Map<String, dynamic>);
  }

  Future<DigitalWellbeing> updateWellbeing(Map<String, dynamic> fields) async {
    final response = await _api.put('/v1/users/me/wellbeing', data: fields);
    final data = response.data['data'] ?? response.data;
    return DigitalWellbeing.fromJson(data as Map<String, dynamic>);
  }

  Future<List<ScreenTimeLog>> getScreenTime() async {
    final response = await _api.get('/v1/users/me/screen-time');
    final items =
        (response.data['data'] as List<dynamic>?) ??
        (response.data as List<dynamic>?) ??
        [];
    return items
        .map((e) => ScreenTimeLog.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> logScreenTime(int minutes) async {
    await _api.post('/v1/users/me/screen-time', data: {'minutes': minutes});
  }
}

final wellbeingRepositoryProvider = Provider<WellbeingRepository>((ref) {
  return WellbeingRepository(ref.watch(apiClientProvider));
});
