import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:hive_flutter/hive_flutter.dart';

/// Lightweight cache layer backed by Hive for offline support.
///
/// Stores JSON maps with TTL timestamps. Expired entries return null on read.
class CacheManager {
  static const _tag = 'CacheManager';
  static bool _initialized = false;

  /// Initialize Hive. Must be called once before [runApp].
  static Future<void> init() async {
    if (_initialized) return;
    await Hive.initFlutter();
    _initialized = true;
    AppLogger.info('CacheManager initialized', tag: _tag);
  }

  /// Stores a JSON-encodable [data] map into [boxName] under [key].
  ///
  /// If [ttl] is provided, the entry will be considered expired after that duration.
  Future<void> put(
    String boxName,
    String key,
    Map<String, dynamic> data, {
    Duration? ttl,
  }) async {
    try {
      final box = await Hive.openBox<dynamic>(boxName);
      final entry = <String, dynamic>{
        '_data': data,
        '_cachedAt': DateTime.now().toIso8601String(),
        if (ttl != null) '_ttlMs': ttl.inMilliseconds,
      };
      await box.put(key, entry);
    } catch (e) {
      AppLogger.warn('Cache put failed for $boxName/$key', tag: _tag, error: e);
    }
  }

  /// Stores a list of JSON-encodable maps.
  Future<void> putList(
    String boxName,
    String key,
    List<Map<String, dynamic>> data, {
    Duration? ttl,
  }) async {
    try {
      final box = await Hive.openBox<dynamic>(boxName);
      final entry = <String, dynamic>{
        '_dataList': data.map((e) => Map<String, dynamic>.from(e)).toList(),
        '_cachedAt': DateTime.now().toIso8601String(),
        if (ttl != null) '_ttlMs': ttl.inMilliseconds,
      };
      await box.put(key, entry);
    } catch (e) {
      AppLogger.warn('Cache putList failed for $boxName/$key', tag: _tag, error: e);
    }
  }

  /// Retrieves a cached JSON map. Returns null if missing or expired.
  Future<Map<String, dynamic>?> get(String boxName, String key) async {
    try {
      final box = await Hive.openBox<dynamic>(boxName);
      final raw = box.get(key);
      if (raw == null) return null;

      final entry = Map<String, dynamic>.from(raw as Map);
      if (_isExpired(entry)) {
        await box.delete(key);
        return null;
      }
      return Map<String, dynamic>.from(entry['_data'] as Map);
    } catch (e) {
      AppLogger.warn('Cache get failed for $boxName/$key', tag: _tag, error: e);
      return null;
    }
  }

  /// Retrieves a cached list of JSON maps. Returns null if missing or expired.
  Future<List<Map<String, dynamic>>?> getList(String boxName, String key) async {
    try {
      final box = await Hive.openBox<dynamic>(boxName);
      final raw = box.get(key);
      if (raw == null) return null;

      final entry = Map<String, dynamic>.from(raw as Map);
      if (_isExpired(entry)) {
        await box.delete(key);
        return null;
      }
      final dataList = entry['_dataList'] as List<dynamic>;
      return dataList.map((e) => Map<String, dynamic>.from(e as Map)).toList();
    } catch (e) {
      AppLogger.warn('Cache getList failed for $boxName/$key', tag: _tag, error: e);
      return null;
    }
  }

  /// Clears all entries in a specific box.
  Future<void> clear(String boxName) async {
    try {
      final box = await Hive.openBox<dynamic>(boxName);
      await box.clear();
    } catch (e) {
      AppLogger.warn('Cache clear failed for $boxName', tag: _tag, error: e);
    }
  }

  /// Clears all cache boxes.
  Future<void> clearAll() async {
    try {
      await Hive.deleteFromDisk();
      _initialized = false;
      await init();
    } catch (e) {
      AppLogger.warn('Cache clearAll failed', tag: _tag, error: e);
    }
  }

  /// Checks if a cache entry has exceeded its TTL.
  bool _isExpired(Map<String, dynamic> entry) {
    final ttlMs = entry['_ttlMs'] as int?;
    if (ttlMs == null) return false; // No TTL means never expires.

    final cachedAtStr = entry['_cachedAt'] as String?;
    if (cachedAtStr == null) return true;

    final cachedAt = DateTime.parse(cachedAtStr);
    final expiresAt = cachedAt.add(Duration(milliseconds: ttlMs));
    return DateTime.now().isAfter(expiresAt);
  }
}
