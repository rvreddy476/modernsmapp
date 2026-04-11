import 'dart:convert';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/data/models/mini_app_manifest.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class MiniAppsRepository {
  final ApiClient _api;

  MiniAppsRepository(this._api);

  Future<List<MiniApp>> listApps({
    String? category,
    int limit = 20,
    int offset = 0,
  }) async {
    final params = <String, dynamic>{'limit': limit, 'offset': offset};
    if (category != null) params['category'] = category;

    final response = await _api.get('/v1/apps', queryParameters: params);
    return _extractEnvelopeItems(
      response.data,
    ).map((e) => MiniApp.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<MiniApp> getApp(String id) async {
    final response = await _api.get('/v1/apps/$id');
    final data = response.data['data'] ?? response.data;
    return MiniApp.fromJson(data as Map<String, dynamic>);
  }

  Future<MiniApp> getAppWithInstallationState(String id) async {
    final app = await getApp(id);

    try {
      final installedApps = await getInstalledApps();
      for (final installedApp in installedApps) {
        if (installedApp.id == app.id) {
          return app.withInstalledStateFrom(installedApp);
        }
      }
    } catch (e, st) {
      AppLogger.warn(
        'Mini app install-state fetch failed; falling back to catalog app',
        tag: 'MiniAppsRepository',
        error: e,
        stackTrace: st,
      );
    }

    return app;
  }

  Future<List<MiniApp>> getInstalledApps() async {
    final response = await _api.get('/v1/apps/installed');
    return _extractEnvelopeItems(
      response.data,
    ).map((e) => MiniApp.fromJson(e as Map<String, dynamic>)).toList();
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

  Future<Map<String, dynamic>> getAppSession(String appId) async {
    final response = await _api.get('/v1/apps/$appId/session');
    final data = response.data['data'] ?? response.data;
    return Map<String, dynamic>.from(data as Map);
  }

  Future<MiniAppLaunchConfig> resolveLaunchConfig(MiniApp app) async {
    final manifestUrl = app.manifestUrl.trim();
    final manifestUri = Uri.tryParse(manifestUrl);
    if (manifestUri == null ||
        !manifestUri.hasScheme ||
        (manifestUri.scheme != 'http' && manifestUri.scheme != 'https')) {
      throw const FormatException(
        'Mini app URL must be an absolute http or https URL',
      );
    }

    try {
      final response = await Dio(
        BaseOptions(
          followRedirects: true,
          responseType: ResponseType.plain,
          validateStatus: (status) =>
              status != null && status >= 200 && status < 400,
          headers: const {
            'Accept':
                'application/json, text/plain, text/html;q=0.9, */*;q=0.8',
          },
        ),
      ).get<String>(manifestUri.toString());

      final body = (response.data ?? '').trim();
      final contentType = response.headers.value('content-type') ?? '';
      if (!_looksLikeManifest(body, contentType)) {
        return MiniAppLaunchConfig.legacy(entryUri: manifestUri);
      }

      final decoded = jsonDecode(body);
      if (decoded is! Map<String, dynamic>) {
        throw const FormatException('Mini app manifest must be a JSON object');
      }

      final manifest = MiniAppManifest.fromJson(
        decoded,
        manifestUri: manifestUri,
      );
      return MiniAppLaunchConfig.fromManifest(
        manifestUri: manifestUri,
        manifest: manifest,
      );
    } on DioException catch (e, st) {
      AppLogger.warn(
        'Mini app manifest fetch failed, using direct launch URL fallback',
        tag: 'MiniAppsRepository',
        error: e,
        stackTrace: st,
      );
      return MiniAppLaunchConfig.legacy(entryUri: manifestUri);
    }
  }
}

bool _looksLikeManifest(String body, String contentType) {
  final normalizedContentType = contentType.toLowerCase();
  if (normalizedContentType.contains('json')) return true;
  return body.startsWith('{');
}

List<dynamic> _extractEnvelopeItems(dynamic payload) {
  final data = payload is Map ? payload['data'] : payload;

  if (data is List<dynamic>) return data;
  if (data is List) return List<dynamic>.from(data);

  final items = data is Map ? data['items'] : null;
  if (items is List<dynamic>) return items;
  if (items is List) return List<dynamic>.from(items);

  return const [];
}

final miniAppsRepositoryProvider = Provider<MiniAppsRepository>((ref) {
  return MiniAppsRepository(ref.watch(apiClientProvider));
});
