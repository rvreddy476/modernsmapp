import 'dart:async';
import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Lightweight auth state for production scale.
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

/// Manages authentication tokens and user session with high-resilience logic.
class AuthService {
  static const _keyUserId = 'auth_user_id';
  static const _keyToken = 'auth_token';
  static const _keyRefreshToken = 'auth_refresh_token';
  static const _tag = 'AuthService';

  final _stateController = StreamController<AuthState>.broadcast();
  final FlutterSecureStorage _storage;
  final Dio _dio;
  AuthState _state = const AuthState();

  // Future to track session readiness for GoRouter redirects
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
              connectTimeout: const Duration(seconds: 15),
              headers: {
                'Content-Type': 'application/json',
                'X-Requested-With': 'XMLHttpRequest',
              },
            ),
          );

  /// Restore session from secure storage with fault tolerance.
  Future<void> restoreSession() async {
    try {
      final results = await Future.wait([
        _storage.read(key: _keyUserId),
        _storage.read(key: _keyToken),
        _storage.read(key: _keyRefreshToken),
      ]);

      final userId = _normalize(results[0]);
      final token = _normalize(results[1]);
      final refreshToken = _normalize(results[2]);

      // PRODUCTION FIX: Only require userId and token.
      // If refreshToken is missing, session is still valid until token expires.
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
        // Only clear if we have partial data (cleanup)
        if (userId != null || token != null) {
          await _clearPersistedSession();
          AppLogger.warn('Cleared incomplete auth session', tag: _tag);
        }
      }
    } catch (e, stack) {
      AppLogger.error(
        'Session restoration failed',
        tag: _tag,
        error: e,
        stackTrace: stack,
      );
    }
  }

  /// Login with phone/email and password.
  Future<bool> login(String identifier, String password) async {
    try {
      final response = await _dio.post(
        '${Environment.authPath}/login',
        data: {'identifier': identifier, 'password': password},
      );

      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      if (data != null) {
        final tokens = data['tokens'] as Map<String, dynamic>? ?? data;
        final user = data['user'] as Map<String, dynamic>?;

        final uId = user?['id']?.toString() ?? data['user_id']?.toString();
        final access =
            tokens['access_token']?.toString() ??
            tokens['accessToken']?.toString();
        final refresh =
            tokens['refresh_token']?.toString() ??
            tokens['refreshToken']?.toString();

        if (access != null && uId != null) {
          _state = AuthState(
            userId: uId,
            token: access,
            refreshToken: refresh,
            isAuthenticated: true,
          );
          _stateController.add(_state);
          await _persistSession();
          return true;
        }
      }
    } catch (e, st) {
      AppLogger.error('Login failed', tag: _tag, error: e, stackTrace: st);
    }
    return false;
  }

  /// Refreshes the token and handles edge cases (like server downtime).
  Future<bool> refreshAccessToken() async {
    final currentRefresh = _normalize(_state.refreshToken);
    if (currentRefresh == null) return false;

    try {
      final response = await _dio.post(
        '${Environment.authPath}/refresh',
        data: {'refresh_token': currentRefresh},
      );

      final tokens =
          (response.data['data'] ?? response.data)['tokens'] ?? response.data;
      final newAccess =
          tokens['access_token']?.toString() ??
          tokens['accessToken']?.toString();
      final newRefresh =
          tokens['refresh_token']?.toString() ??
          tokens['refreshToken']?.toString();

      if (newAccess != null) {
        _state = _state.copyWith(
          token: newAccess,
          refreshToken: newRefresh ?? currentRefresh,
        );
        _stateController.add(_state);
        await _persistSession();
        return true;
      }
    } catch (e, st) {
      AppLogger.error(
        'Token refresh failed',
        tag: _tag,
        error: e,
        stackTrace: st,
      );
    }
    return false;
  }

  /// Sets the session manually (e.g., after registration or OTP verification).
  Future<void> setSession({
    required String userId,
    required String token,
    String? refreshToken,
  }) async {
    _state = AuthState(
      userId: userId,
      token: token,
      refreshToken: refreshToken,
      isAuthenticated: true,
    );
    _stateController.add(_state);
    await _persistSession();
  }

  void logout() {
    _state = const AuthState();
    _stateController.add(_state);
    unawaited(_clearPersistedSession());
  }

  Future<void> _persistSession() async {
    try {
      await Future.wait([
        _storage.write(key: _keyUserId, value: _state.userId),
        _storage.write(key: _keyToken, value: _state.token),
        if (_state.refreshToken != null)
          _storage.write(key: _keyRefreshToken, value: _state.refreshToken!)
        else
          _storage.delete(key: _keyRefreshToken),
      ]);
    } catch (e) {
      AppLogger.error('Token persistence failed', tag: _tag, error: e);
    }
  }

  Future<void> _clearPersistedSession() async {
    await Future.wait([
      _storage.delete(key: _keyUserId),
      _storage.delete(key: _keyToken),
      _storage.delete(key: _keyRefreshToken),
    ]);
  }

  String? _normalize(String? value) {
    final t = value?.trim();
    return (t == null || t.isEmpty) ? null : t;
  }

  void dispose() => _stateController.close();
}

final authServiceProvider = Provider<AuthService>((ref) {
  final service = AuthService();
  ref.onDispose(service.dispose);
  service.sessionReady = service.restoreSession();
  return service;
});

final authStateProvider = StreamProvider<AuthState>((ref) {
  return ref.watch(authServiceProvider).stateStream;
});
