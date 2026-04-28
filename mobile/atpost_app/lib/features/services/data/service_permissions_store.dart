import 'package:atpost_app/core/cache/cache_manager.dart';
import 'package:atpost_app/features/services/models/service_app.dart';

/// Hive-backed store for which permissions a user has granted/denied per
/// internal mini-app. No backend; persists locally via [CacheManager].
class ServicePermissionsStore {
  ServicePermissionsStore(this._cache);

  static const _box = 'service_permissions';

  final CacheManager _cache;

  Future<List<ServicePermission>> getGranted(String appId) async {
    final entry = await _cache.get(_box, appId);
    if (entry == null) return const [];
    return _decodeList(entry['granted']);
  }

  Future<List<ServicePermission>> getDenied(String appId) async {
    final entry = await _cache.get(_box, appId);
    if (entry == null) return const [];
    return _decodeList(entry['denied']);
  }

  /// Returns the subset of [required] that hasn't yet been granted.
  Future<List<ServicePermission>> pendingFor(
    String appId,
    List<ServicePermission> required,
  ) async {
    if (required.isEmpty) return const [];
    final granted = (await getGranted(appId)).toSet();
    return required.where((p) => !granted.contains(p)).toList();
  }

  Future<void> grant(
    String appId,
    List<ServicePermission> permissions,
  ) async {
    final entry = await _cache.get(_box, appId);
    final currentGranted = _decodeList(entry?['granted']);
    final currentDenied = _decodeList(entry?['denied']);
    final granted = <ServicePermission>{...currentGranted, ...permissions};
    final denied = currentDenied.where((p) => !permissions.contains(p)).toList();
    await _cache.put(_box, appId, _encode(granted.toList(), denied));
  }

  Future<void> deny(
    String appId,
    List<ServicePermission> permissions,
  ) async {
    final entry = await _cache.get(_box, appId);
    final currentGranted = _decodeList(entry?['granted']);
    final currentDenied = _decodeList(entry?['denied']);
    final denied = <ServicePermission>{...currentDenied, ...permissions};
    final granted = currentGranted.where((p) => !permissions.contains(p)).toList();
    await _cache.put(_box, appId, _encode(granted, denied.toList()));
  }

  Future<void> revokeAll(String appId) async {
    await _cache.put(_box, appId, _encode(const [], const []));
  }

  Map<String, dynamic> _encode(
    List<ServicePermission> granted,
    List<ServicePermission> denied,
  ) {
    return {
      'granted': granted.map((p) => p.key).toList(),
      'denied': denied.map((p) => p.key).toList(),
      'updated_at': DateTime.now().toIso8601String(),
    };
  }

  List<ServicePermission> _decodeList(dynamic raw) {
    if (raw is! List) return const [];
    final out = <ServicePermission>[];
    for (final v in raw) {
      if (v is! String) continue;
      final p = ServicePermissionX.fromKey(v);
      if (p != null) out.add(p);
    }
    return out;
  }
}
