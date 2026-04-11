import 'dart:async';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Lightweight auth state.
class AuthState {
  final String? userId;
  final String? token;
  final String? refreshToken;
  final bool isAuthenticated;

  const AuthState({
    this.userId,
    this.token,
    this.refreshToken,
    this.isAuthenticated = false,
  });

  AuthState copyWith({
    String? userId,
    String? token,
    String? refreshToken,
    bool? isAuthenticated,
  }) {
    return AuthState(
      userId: userId ?? this.userId,
      token: token ?? this.token,
      refreshToken: refreshToken ?? this.refreshToken,
      isAuthenticated: isAuthenticated ?? this.isAuthenticated,
    );
  }
}

/// Manages authentication tokens and user session.
class AuthService {
  static const _keyUserId = 'auth_user_id';
  static const _keyToken = 'auth_token';
  static const _keyRefreshToken = 'auth_refresh_token';
  static const _tag = 'AuthService';

  final _stateController = StreamController<AuthState>.broadcast();
  final FlutterSecureStorage _storage;
  final Dio _dio;
  AuthState _state = const AuthState();
  late final Future<void> sessionReady;

  Stream<AuthState> get stateStream => _stateController.stream;
  AuthState get state => _state;
  String? get token => _state.token;
  String? get refreshToken => _state.refreshToken;
  String? get userId => _state.userId;
  bool get isAuthenticated => _state.isAuthenticated;

  AuthService({FlutterSecureStorage? storage, Dio? dio})
    : _storage = storage ?? const FlutterSecureStorage(),
      _dio =
          dio ??
          Dio(
            BaseOptions(
              baseUrl: Environment.apiBaseUrl,
              connectTimeout: const Duration(seconds: 10),
              headers: {
                'Content-Type': 'application/json',
                'X-Requested-With': 'XMLHttpRequest',
              },
            ),
          );

  /// Restore session from secure storage on app startup.
  Future<void> restoreSession() async {
    try {
      final userId = _normalizeStoredValue(
        await _storage.read(key: _keyUserId),
      );
      final token = _normalizeStoredValue(await _storage.read(key: _keyToken));
      final refreshToken = _normalizeStoredValue(
        await _storage.read(key: _keyRefreshToken),
      );

      final hasStoredAuthData =
          userId != null || token != null || refreshToken != null;
      if (userId != null && token != null) {
        _state = AuthState(
          userId: userId,
          token: token,
          refreshToken: refreshToken,
          isAuthenticated: true,
        );
        _stateController.add(_state);
        AppLogger.info('Session restored for user: $userId', tag: _tag);
      } else {
        if (hasStoredAuthData) {
          await _clearPersistedSession();
          AppLogger.warn(
            'Cleared incomplete auth session from secure storage',
            tag: _tag,
          );
        }
        AppLogger.info('No existing session found', tag: _tag);
      }
    } catch (e, stack) {
      AppLogger.error(
        'Failed to restore session',
        tag: _tag,
        error: e,
        stackTrace: stack,
      );
    }
  }

  /// Login with phone/email and password.
  Future<bool> login(String identifier, String password) async {
    try {
      AppLogger.info('Attempting login for: $identifier', tag: _tag);
      final response = await _dio.post(
        '${Environment.authPath}/login',
        data: {'identifier': identifier, 'password': password},
      );

      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      if (data != null) {
        final tokens = data['tokens'] as Map<String, dynamic>? ?? data;
        final user = data['user'] as Map<String, dynamic>?;

        final userId = user?['id'] as String? ?? data['user_id'] as String?;
        final accessToken =
            tokens['access_token'] as String? ?? tokens['accessToken'];
        final refreshToken =
            tokens['refresh_token'] as String? ?? tokens['refreshToken'];

        if (_hasValue(accessToken) && _hasValue(userId)) {
          _state = AuthState(
            userId: userId,
            token: accessToken,
            refreshToken: refreshToken,
            isAuthenticated: true,
          );
          _stateController.add(_state);
          await _persistSession();
          AppLogger.info('Login successful for user: $userId', tag: _tag);
          return true;
        }
      }
      AppLogger.warn('Login failed: Invalid response structure', tag: _tag);
    } on DioException catch (e, stack) {
      final statusCode = e.response?.statusCode;
      final responseData = e.response?.data;
      AppLogger.error(
        'Login request failed: status=$statusCode, response=$responseData, type=${e.type}, message=${e.message}',
        tag: _tag,
        error: e,
        stackTrace: stack,
      );
      // Rate limited — clear and retry hint
      if (statusCode == 429) {
        AppLogger.warn('Rate limited — wait a moment and retry', tag: _tag);
      }
    } catch (e, stack) {
      AppLogger.error(
        'Login request failed (non-Dio): ${e.runtimeType}: $e',
        tag: _tag,
        error: e,
        stackTrace: stack,
      );
    }
    return false;
  }

  /// Refresh the access token using the stored refresh token.
  Future<bool> refreshAccessToken() async {
    final currentRefreshToken = _normalizeStoredValue(_state.refreshToken);
    if (currentRefreshToken == null) {
      AppLogger.warn(
        'Token refresh aborted: No refresh token available',
        tag: _tag,
      );
      return false;
    }

    try {
      AppLogger.info('Refreshing access token...', tag: _tag);
      final response = await _dio.post(
        '${Environment.authPath}/refresh',
        data: {'refresh_token': currentRefreshToken},
      );

      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      final tokens = data['tokens'] as Map<String, dynamic>? ?? data;

      final newAccessToken =
          tokens['access_token'] as String? ?? tokens['accessToken'];
      final newRefreshToken =
          tokens['refresh_token'] as String? ?? tokens['refreshToken'];

      if (_hasValue(newAccessToken)) {
        _state = _state.copyWith(
          token: newAccessToken,
          refreshToken: newRefreshToken ?? currentRefreshToken,
        );
        _stateController.add(_state);
        await _persistSession();
        AppLogger.info('Access token refreshed successfully', tag: _tag);
        return true;
      }
      AppLogger.warn(
        'Refresh failed: No new access token in response',
        tag: _tag,
      );
    } catch (e, stack) {
      AppLogger.error(
        'Token refresh request failed',
        tag: _tag,
        error: e,
        stackTrace: stack,
      );
    }
    return false;
  }

  /// Set auth state directly (e.g. from register).
  void setSession({
    required String userId,
    required String token,
    String? refreshToken,
  }) {
    _state = AuthState(
      userId: userId,
      token: token,
      refreshToken: refreshToken,
      isAuthenticated: true,
    );
    _stateController.add(_state);
    unawaited(_persistSession());
    AppLogger.info('Direct session set for user: $userId', tag: _tag);
  }

  /// Clear session and remove persisted tokens.
  void logout() {
    AppLogger.info('Logging out user: ${_state.userId}', tag: _tag);
    _state = const AuthState();
    _stateController.add(_state);
    unawaited(_clearPersistedSession());
  }

  Future<void> _persistSession() async {
    try {
      await _writeOrDelete(_keyUserId, _state.userId);
      await _writeOrDelete(_keyToken, _state.token);
      await _writeOrDelete(_keyRefreshToken, _state.refreshToken);
    } catch (e, stack) {
      AppLogger.error(
        'Failed to persist session tokens',
        tag: _tag,
        error: e,
        stackTrace: stack,
      );
    }
  }

  Future<void> _writeOrDelete(String key, String? value) async {
    final normalized = _normalizeStoredValue(value);
    if (normalized == null) {
      await _storage.delete(key: key);
      return;
    }
    await _storage.write(key: key, value: normalized);
  }

  Future<void> _clearPersistedSession() async {
    await _storage.delete(key: _keyUserId);
    await _storage.delete(key: _keyToken);
    await _storage.delete(key: _keyRefreshToken);
  }

  bool _hasValue(String? value) => _normalizeStoredValue(value) != null;

  String? _normalizeStoredValue(String? value) {
    final trimmed = value?.trim();
    if (trimmed == null || trimmed.isEmpty) {
      return null;
    }
    return trimmed;
  }

  void dispose() {
    _stateController.close();
  }
}

/// Global auth service provider.
final authServiceProvider = Provider<AuthService>((ref) {
  final service = AuthService();
  ref.onDispose(service.dispose);
  service.sessionReady = service.restoreSession();
  return service;
});

/// Auth state provider for reactive UI updates.
final authStateProvider = StreamProvider<AuthState>((ref) {
  final auth = ref.watch(authServiceProvider);
  return auth.stateStream;
});
