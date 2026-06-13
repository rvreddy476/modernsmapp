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

/// Result returned by [AuthService.login]. The success path mints
/// tokens and updates state; the gated paths return server-issued
/// `pending_token`s the UI must hand off to the matching follow-up
/// surface (A13 anomaly step-up screen, or 2FA verify screen).
class LoginResult {
  final bool success;
  final bool requiresStepUp;
  final bool requires2fa;
  final String? pendingToken;
  final String? userId;
  final List<String> stepUpMethods; // 'email_otp', 'totp'
  final String? error;

  const LoginResult._({
    required this.success,
    this.requiresStepUp = false,
    this.requires2fa = false,
    this.pendingToken,
    this.userId,
    this.stepUpMethods = const [],
    this.error,
  });

  const LoginResult.success() : this._(success: true);
  const LoginResult.failure(String msg)
      : this._(success: false, error: msg);
  const LoginResult.stepUp({
    required String token,
    required List<String> methods,
    String? userId,
  }) : this._(
          success: false,
          requiresStepUp: true,
          pendingToken: token,
          stepUpMethods: methods,
          userId: userId,
        );
  const LoginResult.twoFA({required String token, String? userId})
      : this._(
          success: false,
          requires2fa: true,
          pendingToken: token,
          userId: userId,
        );
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

  // Tracks the session-restore deadline Timer so dispose() can cancel
  // it explicitly. The previous code used `.timeout()` which leaks a
  // pending Timer in flutter_test when the secure-storage stub never
  // resolves — failing widget tests with the "Timer is still pending
  // after the widget tree was disposed" assertion. See
  // memory/deferred_nonblocking_bugs.md for the original report.
  Timer? _restoreDeadline;

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

  /// Restore session from secure storage with fault tolerance and timeout.
  Future<void> restoreSession() async {
    try {
      // Manual deadline + Completer instead of `.timeout()` so dispose()
      // can cancel the underlying Timer cleanly. Production semantics
      // unchanged: if secure storage stalls past 5s we throw a
      // TimeoutException just like the old `.timeout()` did.
      final deadlineCompleter = Completer<List<String?>>();
      _restoreDeadline?.cancel();
      _restoreDeadline = Timer(const Duration(seconds: 5), () {
        if (!deadlineCompleter.isCompleted) {
          deadlineCompleter.completeError(
            TimeoutException(
              'Session restore timed out',
              const Duration(seconds: 5),
            ),
          );
        }
      });

      // The storage reads race the deadline. Cancelling the Timer the
      // moment the reads complete is the critical part — that's what
      // unblocks flutter_test's "no pending Timers" assertion.
      Future.wait([
        _storage.read(key: _keyUserId),
        _storage.read(key: _keyToken),
        _storage.read(key: _keyRefreshToken),
      ]).then((r) {
        _restoreDeadline?.cancel();
        _restoreDeadline = null;
        if (!deadlineCompleter.isCompleted) {
          deadlineCompleter.complete(r);
        }
      }).catchError((Object e) {
        _restoreDeadline?.cancel();
        _restoreDeadline = null;
        if (!deadlineCompleter.isCompleted) {
          deadlineCompleter.completeError(e);
        }
      });

      final results = await deadlineCompleter.future;

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

        // Refresh at startup when possible so the app does not fan out a burst
        // of requests with an expired access token across multiple tabs.
        if (refreshToken != null) {
          final refreshed = await refreshAccessToken();
          if (refreshed) {
            AppLogger.info(
              'Refreshed access token during session restore',
              tag: _tag,
            );
          } else {
            AppLogger.warn(
              'Session restore kept the stored access token because refresh failed',
              tag: _tag,
            );
          }
        }
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
  Future<LoginResult> login(String identifier, String password) async {
    try {
      final response = await _dio.post(
        '${Environment.authPath}/login',
        data: {'identifier': identifier, 'password': password},
      );

      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      if (data == null) {
        return const LoginResult.failure('No data in login response.');
      }

      // A13 anomaly step-up — server flagged this login as high-risk
      // (new /24 + new device) and refused to mint tokens. The UI must
      // route to a step-up screen with the pending_token + available
      // methods. Takes precedence over requires_2fa because the gate
      // runs first server-side.
      if (data['requires_step_up'] == true) {
        final token = data['pending_token']?.toString() ?? '';
        final methods = (data['step_up_methods'] as List<dynamic>?)
                ?.map((m) => m.toString())
                .toList() ??
            const <String>[];
        final user = data['user'] as Map<String, dynamic>?;
        return LoginResult.stepUp(
          token: token,
          methods: methods,
          userId: user?['id']?.toString(),
        );
      }

      if (data['requires_2fa'] == true) {
        final token = data['pending_token']?.toString() ?? '';
        final user = data['user'] as Map<String, dynamic>?;
        return LoginResult.twoFA(
          token: token,
          userId: user?['id']?.toString(),
        );
      }

      final tokens = data['tokens'] as Map<String, dynamic>? ?? data;
      final user = data['user'] as Map<String, dynamic>?;

      final uId = user?['id']?.toString() ?? data['user_id']?.toString();
      final access = tokens['access_token']?.toString() ??
          tokens['accessToken']?.toString();
      final refresh = tokens['refresh_token']?.toString() ??
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
        return const LoginResult.success();
      }
      return const LoginResult.failure('Authentication response missing tokens.');
    } catch (e, st) {
      AppLogger.error('Login failed', tag: _tag, error: e, stackTrace: st);
      return LoginResult.failure(e.toString());
    }
  }

  /// Exchanges a [pendingToken] + verification code for a real session.
  /// Used by both the A13 anomaly step-up screen (email-OTP and 2FA
  /// surfaces) and any future flow that mints tokens out of a pending
  /// gate. Returns true and populates auth state on success.
  Future<bool> completeStepUp({
    required String path,
    required String pendingToken,
    required String code,
  }) async {
    try {
      final response = await _dio.post(
        path,
        data: {'pending_token': pendingToken, 'code': code},
      );
      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      if (data == null) return false;

      final tokens = data['tokens'] as Map<String, dynamic>? ?? data;
      final user = data['user'] as Map<String, dynamic>?;
      final uId = user?['id']?.toString() ?? data['user_id']?.toString();
      final access = tokens['access_token']?.toString() ??
          tokens['accessToken']?.toString();
      final refresh = tokens['refresh_token']?.toString() ??
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
      return false;
    } catch (e, st) {
      AppLogger.error('Step-up verify failed',
          tag: _tag, error: e, stackTrace: st);
      return false;
    }
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

  void dispose() {
    // Cancel any in-flight session-restore deadline so flutter_test
    // (and any other early-dispose path) doesn't leave a Timer pending.
    _restoreDeadline?.cancel();
    _restoreDeadline = null;
    _stateController.close();
  }
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
