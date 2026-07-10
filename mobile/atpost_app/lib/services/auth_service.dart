import 'dart:async';
import 'package:atpost_core/config/environment.dart';
import 'package:atpost_core/utils/app_logger.dart';
import 'package:atpost_network/auth_session.dart';
import 'package:atpost_network/interceptors/csrf_interceptor.dart';
import 'package:atpost_network/ssl_pinning.dart';
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
///
/// Implements the network layer's [AuthSession] so ApiClient and the
/// interceptors stay decoupled from session storage and login flows.
class AuthService implements AuthSession {
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
  @override
  late final Future<void> sessionReady;

  Stream<AuthState> get stateStream => _stateController.stream;
  AuthState get state => _state;
  @override
  String? get token => _state.token;
  String? get refreshToken => _state.refreshToken;
  @override
  String? get userId => _state.userId;
  @override
  bool get isAuthenticated => _state.isAuthenticated;
  @override
  Stream<void> get sessionChanges => stateStream;

  /// Hardened storage: Android falls back from StrongBox/TEE-backed
  /// EncryptedSharedPreferences instead of plaintext prefs; iOS keeps
  /// tokens readable after first unlock (background refresh) but never
  /// migrates them to other devices via backup.
  static const _secureStorage = FlutterSecureStorage(
    aOptions: AndroidOptions(encryptedSharedPreferences: true),
    iOptions: IOSOptions(accessibility: KeychainAccessibility.first_unlock),
  );

  AuthService({FlutterSecureStorage? storage, Dio? dio})
    : _storage = storage ?? _secureStorage,
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
          ) {
    _configureSecurity();
    if (dio == null) {
      // Same server-driven CSRF handling as ApiClient so the token minted
      // during login is honored on the very next auth call. Only for the
      // Dio we own — an injected client brings its own interceptor chain.
      _dio.interceptors.add(CsrfInterceptor());
    }
  }

  /// Installs the shared SSL-pinning adapter on the dedicated auth Dio so
  /// pinning is active even for login/refresh (see atpost_network's
  /// ssl_pinning.dart).
  void _configureSecurity() => configureSslPinning(_dio, tag: _tag);

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

      // HIGH SECURITY: Validate token format before attempting to use it.
      if (userId != null && token != null && _isValidJwt(token)) {
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
        if (refreshToken != null && _isValidJwt(refreshToken)) {
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
        // Only clear if we have partial or invalid data (cleanup)
        if (userId != null || token != null) {
          await _clearPersistedSession();
          AppLogger.warn('Cleared incomplete or invalid auth session', tag: _tag);
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

  /// Sanity check that a string looks like a JWS-compact JWT: exactly three
  /// non-empty dot-separated segments (header.payload.signature). A 2-part
  /// token is unsigned, which we never accept — the backend always signs.
  bool _isValidJwt(String token) {
    final parts = token.split('.');
    return parts.length == 3 && parts.every((p) => p.isNotEmpty);
  }

  /// Extracts user + token material from an auth response body and, when
  /// complete, installs and persists the session. Single mint path shared
  /// by login / step-up / OTP / register so validation never drifts.
  Future<bool> _mintSession(Map<String, dynamic>? data) async {
    if (data == null) return false;

    final tokens = data['tokens'] as Map<String, dynamic>? ?? data;
    final user = data['user'] as Map<String, dynamic>?;
    final uId = user?['id']?.toString() ?? data['user_id']?.toString();
    final access = tokens['access_token']?.toString() ??
        tokens['accessToken']?.toString();
    final refresh = tokens['refresh_token']?.toString() ??
        tokens['refreshToken']?.toString();

    if (uId == null || uId.isEmpty || access == null || access.isEmpty) {
      return false;
    }

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

  /// Human-readable message from the backend's `{"error":{code,message}}`
  /// envelope. Falls back to [fallback] — raw exception strings never
  /// reach the UI.
  static String friendlyAuthError(Object e, String fallback) {
    if (e is DioException) {
      final body = e.response?.data;
      final rawErr = body is Map ? body['error'] : null;
      final message = rawErr is Map
          ? (rawErr['message'] as String? ?? rawErr['code'] as String?)
          : rawErr is String
              ? rawErr
              : (body is Map ? body['message'] as String? : null);
      if (message != null && message.isNotEmpty) return message;
      if (e.type == DioExceptionType.connectionTimeout ||
          e.type == DioExceptionType.receiveTimeout ||
          e.type == DioExceptionType.connectionError) {
        return 'Network error. Check your connection and try again.';
      }
    }
    return fallback;
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

      if (await _mintSession(data as Map<String, dynamic>?)) {
        return const LoginResult.success();
      }
      return const LoginResult.failure('Authentication response missing tokens.');
    } catch (e, st) {
      AppLogger.error('Login failed', tag: _tag, error: e, stackTrace: st);
      return LoginResult.failure(friendlyAuthError(
        e,
        'Login failed. Check credentials and try again.',
      ));
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
      return _mintSession(data as Map<String, dynamic>?);
    } catch (e, st) {
      AppLogger.error('Step-up verify failed',
          tag: _tag, error: e, stackTrace: st);
      return false;
    }
  }

  /// Requests a login OTP for [phone] (POST /request-otp — the backend's
  /// generic OTP channel is phone-only; password reset goes through
  /// [requestPasswordReset] instead). Returns null on success, or a
  /// user-facing error message. Runs on the pinned auth client — auth
  /// traffic never uses an ad-hoc Dio.
  Future<String?> requestOtp(String phone, {String purpose = 'login'}) async {
    try {
      await _dio.post(
        '${Environment.authPath}/request-otp',
        data: {'phone': phone, 'purpose': purpose},
      );
      return null;
    } catch (e, st) {
      AppLogger.error('OTP request failed',
          tag: _tag, error: e, stackTrace: st);
      return friendlyAuthError(e, 'Failed to send code. Please try again.');
    }
  }

  /// Starts the forgot-password flow: the server sends a reset code to
  /// [identifier] (email or phone). The endpoint always returns 200 for
  /// unknown accounts (anti-enumeration), so null only means "accepted".
  Future<String?> requestPasswordReset(String identifier) async {
    try {
      await _dio.post(
        '${Environment.authPath}/forgot-password',
        data: {'identifier': identifier},
      );
      return null;
    } catch (e, st) {
      AppLogger.error('Password-reset request failed',
          tag: _tag, error: e, stackTrace: st);
      return friendlyAuthError(
          e, 'Failed to send reset code. Please try again.');
    }
  }

  /// Completes the forgot-password flow: exchanges the emailed/texted
  /// [code] plus the [newPassword] in one call (the server validates the
  /// code at this step — there is no separate verify endpoint).
  Future<String?> resetPassword({
    required String identifier,
    required String code,
    required String newPassword,
  }) async {
    try {
      await _dio.post(
        '${Environment.authPath}/reset-password',
        data: {
          'identifier': identifier,
          'code': code,
          'new_password': newPassword,
        },
      );
      return null;
    } catch (e, st) {
      AppLogger.error('Password reset failed',
          tag: _tag, error: e, stackTrace: st);
      return friendlyAuthError(
          e, 'Password reset failed. Check the code and try again.');
    }
  }

  /// Verifies a login OTP for [phone] and mints the session
  /// (POST /verify-otp, purpose=login).
  Future<String?> verifyLoginOtp({
    required String phone,
    required String code,
  }) async {
    try {
      final response = await _dio.post(
        '${Environment.authPath}/verify-otp',
        data: {'phone': phone, 'otp': code, 'purpose': 'login'},
      );
      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      if (await _mintSession(data as Map<String, dynamic>?)) return null;
      // 200 without tokens = the server gated this login (2FA / anomaly
      // step-up envelope). The OTP surface doesn't carry those flows.
      return 'Additional verification is required for this account. '
          'Please sign in with your password.';
    } catch (e, st) {
      AppLogger.error('OTP verification failed',
          tag: _tag, error: e, stackTrace: st);
      return friendlyAuthError(e, 'Invalid code. Please try again.');
    }
  }

  /// Completes a 2FA-gated login (POST /2fa/verify): exchanges the
  /// one-shot [pendingToken] minted by login plus the authenticator
  /// [code] for a real session. Returns null on success.
  Future<String?> verify2fa({
    required String userId,
    required String code,
    required String pendingToken,
  }) async {
    try {
      final response = await _dio.post(
        '${Environment.authPath}/2fa/verify',
        data: {
          'user_id': userId,
          'code': code,
          'pending_token': pendingToken,
        },
      );
      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      if (await _mintSession(data as Map<String, dynamic>?)) return null;
      // A 2FA exchange MUST return tokens; treat a token-less 200 as a
      // failure instead of bouncing an unauthenticated user to `/`.
      return 'Verification succeeded but no session was issued. '
          'Please sign in again.';
    } catch (e, st) {
      AppLogger.error('2FA verification failed',
          tag: _tag, error: e, stackTrace: st);
      return friendlyAuthError(e, 'Invalid code. Please try again.');
    }
  }

  /// Registers a new account and signs it in. Returns null on success,
  /// or a user-facing error message.
  Future<String?> register({
    required String email,
    required String password,
    required String firstName,
    required String lastName,
  }) async {
    try {
      final response = await _dio.post(
        '${Environment.authPath}/register',
        data: {
          'email': email,
          'password': password,
          'first_name': firstName,
          'last_name': lastName,
        },
      );

      final data =
          response.data['data'] as Map<String, dynamic>? ?? response.data;
      if (await _mintSession(data as Map<String, dynamic>?)) return null;
      return 'Account created but sign-in failed. Please log in.';
    } catch (e, st) {
      AppLogger.error('Registration failed',
          tag: _tag, error: e, stackTrace: st);
      return friendlyAuthError(e, 'Registration failed. Please try again.');
    }
  }

  /// Refreshes the token and handles edge cases (like server downtime).
  @override
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
    } on DioException catch (e, st) {
      AppLogger.error(
        'Token refresh failed',
        tag: _tag,
        error: e,
        stackTrace: st,
      );
      final status = e.response?.statusCode;
      if (status == 401 || status == 403) {
        // The refresh token itself was rejected (revoked/expired) — the
        // session is dead server-side. Clear it locally so the app drops
        // to the login screen instead of hammering APIs with a stale
        // access token. Transient failures (5xx, network) keep the
        // session and retry later.
        AppLogger.warn(
          'Refresh token rejected ($status); clearing local session',
          tag: _tag,
        );
        _state = const AuthState();
        _stateController.add(_state);
        unawaited(_clearPersistedSession());
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

  /// Sets the session manually (e.g., dev bypasses and tests). Production
  /// flows mint through [_mintSession] instead. Empty credentials are
  /// rejected — an "authenticated" session without a token would wedge
  /// the router redirect loop.
  Future<void> setSession({
    required String userId,
    required String token,
    String? refreshToken,
  }) async {
    if (userId.isEmpty || token.isEmpty) {
      AppLogger.error(
        'Rejected setSession with empty userId/token',
        tag: _tag,
      );
      return;
    }
    _state = AuthState(
      userId: userId,
      token: token,
      refreshToken: refreshToken,
      isAuthenticated: true,
    );
    _stateController.add(_state);
    await _persistSession();
  }

  @override
  void logout() {
    final refresh = _normalize(_state.refreshToken);
    final access = _normalize(_state.token);
    _state = const AuthState();
    _stateController.add(_state);
    unawaited(_clearPersistedSession());
    if (refresh != null) {
      unawaited(_revokeServerSession(refresh, access));
    }
  }

  /// Best-effort server-side revocation so a leaked refresh token cannot
  /// outlive the sign-out. Local clearing never waits on this: the user
  /// is logged out immediately even when offline.
  Future<void> _revokeServerSession(
    String refreshToken,
    String? accessToken,
  ) async {
    try {
      await _dio.post(
        '${Environment.authPath}/logout',
        data: {'refresh_token': refreshToken},
        options: Options(
          headers: {
            if (accessToken != null) 'Authorization': 'Bearer $accessToken',
          },
        ),
      );
    } catch (e) {
      AppLogger.warn(
        'Server-side session revoke failed (already signed out locally): $e',
        tag: _tag,
      );
    }
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
