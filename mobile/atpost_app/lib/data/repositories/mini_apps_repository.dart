import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class MiniAppsRepository {
  final ApiClient _api;

  MiniAppsRepository(this._api);

  Future<List<MiniApp>> listApps({
    String? category,
    int limit = 20,
    int offset = 0,
  }) async {
    final params = <String, dynamic>{
      'limit': limit,
      'offset': offset,
    };
    if (category != null) params['category'] = category;

    final response = await _api.get('/v1/apps', queryParameters: params);
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items
        .map((e) => MiniApp.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<MiniApp> getApp(String id) async {
    final response = await _api.get('/v1/apps/$id');
    final data = response.data['data'] ?? response.data;
    return MiniApp.fromJson(data as Map<String, dynamic>);
  }

  Future<List<MiniApp>> getInstalledApps() async {
    final response = await _api.get('/v1/apps/installed');
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items
        .map((e) => MiniApp.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<Map<String, dynamic>> installApp(
    String appId, {
    List<String> grantedPermissions = const [],
  }) async {
    final response = await _api.post(
      '/v1/apps/$appId/install',
      data: {'granted_permissions': grantedPermissions},
    );
    return (response.data['data'] as Map<String, dynamic>?) ?? {};
  }

  Future<void> uninstallApp(String appId) async {
    await _api.delete('/v1/apps/$appId/install');
  }
}

final miniAppsRepositoryProvider = Provider<MiniAppsRepository>((ref) {
  return MiniAppsRepository(ref.watch(apiClientProvider));
});
