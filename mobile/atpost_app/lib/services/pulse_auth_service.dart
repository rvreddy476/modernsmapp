import 'dart:async';
import 'dart:convert';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

// formerly PostMatchAuthService
class PulseAuthService {
  static const _keyAccessToken = 'pulse_access_token';
  static const _keyRefreshToken = 'pulse_refresh_token';
  static const _keySession = 'pulse_session';

  final FlutterSecureStorage _storage;
  final Dio _dio;

  String? _accessToken;
  String? _refreshToken;
  PulseSession? _session;
  late final Future<void> sessionReady;

  PulseAuthService({FlutterSecureStorage? storage, Dio? dio})
    : _storage = storage ?? const FlutterSecureStorage(),
      _dio =
          dio ??
          Dio(
            BaseOptions(
              baseUrl: Environment.pulseBaseUrl,
              connectTimeout: const Duration(seconds: 20),
              receiveTimeout: const Duration(seconds: 20),
              headers: const {
                'Content-Type': 'application/json',
                'Accept': 'application/json',
              },
            ),
          ) {
    sessionReady = restoreSession();
  }

  String? get accessToken => _accessToken;
  String? get refreshToken => _refreshToken;
  PulseSession? get session => _session;
  bool get hasSession =>
      _accessToken != null &&
      _accessToken!.isNotEmpty &&
      _session != null &&
      _session!.userId.isNotEmpty;

  bool get isReady => _session?.onboardingStatus == 'ready';

  Future<void> restoreSession() async {
    final access = await _storage.read(key: _keyAccessToken);
    final refresh = await _storage.read(key: _keyRefreshToken);
    final rawSession = await _storage.read(key: _keySession);

    _accessToken = _normalize(access);
    _refreshToken = _normalize(refresh);
    if (rawSession != null && rawSession.isNotEmpty) {
      try {
        final decoded = jsonDecode(rawSession);
        if (decoded is Map<String, dynamic>) {
          _session = PulseSession.fromJson(decoded);
        }
      } catch (_) {
        _session = null;
      }
    }

    if (!hasSession &&
        (_accessToken != null || _refreshToken != null || rawSession != null)) {
      await clearSession();
    }
  }

  Future<bool> ssoFromPostbook({
    required String postbookUserId,
    String? email,
  }) async {
    try {
      final response = await _dio.post(
        '/api/v1/auth/postbook-sso',
        data: {
          'postbook_user_id': postbookUserId,
          if (email != null && email.trim().isNotEmpty) 'email': email.trim(),
        },
      );
      return _applyAuthPayload(response.data['data'] as Map<String, dynamic>?);
    } catch (_) {
      return false;
    }
  }

  Future<bool> refreshAccessToken() async {
    final refresh = _normalize(_refreshToken);
    if (refresh == null) return false;
    try {
      final response = await _dio.post(
        '/api/v1/auth/refresh',
        data: {'refresh_token': refresh},
      );
      final data = response.data['data'] as Map<String, dynamic>?;
      final accessToken = _normalize(data?['access_token'] as String?);
      final refreshToken =
          _normalize(data?['refresh_token'] as String?) ?? refresh;
      if (accessToken == null || accessToken.isEmpty) return false;
      _accessToken = accessToken;
      _refreshToken = refreshToken;
      await _persistSession();
      return true;
    } catch (_) {
      await clearSession();
      return false;
    }
  }

  Future<void> updateOnboardingStatus(String status) async {
    if (_session == null) return;
    _session = _session!.copyWith(onboardingStatus: status);
    await _persistSession();
  }

  Future<void> clearSession() async {
    _accessToken = null;
    _refreshToken = null;
    _session = null;
    await _storage.delete(key: _keyAccessToken);
    await _storage.delete(key: _keyRefreshToken);
    await _storage.delete(key: _keySession);
  }

  Future<bool> _applyAuthPayload(Map<String, dynamic>? data) async {
    if (data == null) return false;
    final user = data['user'] as Map<String, dynamic>? ?? const {};
    final access = _normalize(data['access_token'] as String?);
    final refresh = _normalize(data['refresh_token'] as String?);
    final userId = _normalize(user['id'] as String?);
    final onboardingStatus =
        _normalize(user['onboarding_status'] as String?) ?? 'new';
    if (access == null || userId == null) return false;

    _accessToken = access;
    _refreshToken = refresh;
    _session = PulseSession(
      userId: userId,
      onboardingStatus: onboardingStatus,
    );
    await _persistSession();
    return true;
  }

  Future<void> _persistSession() async {
    await _writeOrDelete(_keyAccessToken, _accessToken);
    await _writeOrDelete(_keyRefreshToken, _refreshToken);
    await _writeOrDelete(
      _keySession,
      _session == null ? null : jsonEncode(_session!.toJson()),
    );
  }

  Future<void> _writeOrDelete(String key, String? value) async {
    final normalized = _normalize(value);
    if (normalized == null) {
      await _storage.delete(key: key);
      return;
    }
    await _storage.write(key: key, value: normalized);
  }

  String? _normalize(String? value) {
    final trimmed = value?.trim();
    if (trimmed == null || trimmed.isEmpty) return null;
    return trimmed;
  }
}

final pulseAuthServiceProvider = Provider<PulseAuthService>((ref) {
  return PulseAuthService();
});
