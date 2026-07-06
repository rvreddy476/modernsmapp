import 'package:atpost_core/utils/app_logger.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// The auth surface the network layer needs — nothing more. The app's
/// AuthService implements this; the network package never sees session
/// storage, login flows, or user models.
abstract interface class AuthSession {
  /// Current access token, or null when signed out.
  String? get token;

  /// Current user id, or null when signed out.
  String? get userId;

  /// Whether a user session is currently active.
  bool get isAuthenticated;

  /// Emits whenever the session changes (login, logout, refresh). Used
  /// by long-lived transports (websocket) to connect/disconnect.
  Stream<void> get sessionChanges;

  /// Attempts a refresh-token exchange. Returns true when a new access
  /// token is available via [token].
  Future<bool> refreshAccessToken();

  /// Tears the session down (called when a refresh fails on a 401).
  void logout();
}

/// Signed-out fallback so tests and previews work without wiring auth.
/// Requests go out without credentials; refresh always fails.
class UnauthenticatedSession implements AuthSession {
  const UnauthenticatedSession();

  @override
  String? get token => null;

  @override
  String? get userId => null;

  @override
  bool get isAuthenticated => false;

  @override
  Stream<void> get sessionChanges => const Stream.empty();

  @override
  Future<bool> refreshAccessToken() async => false;

  @override
  void logout() {}
}

/// The app MUST override this at its root ProviderScope with the real
/// AuthService. The default keeps widget tests working (signed-out) but
/// logs loudly so a missing override can't slip into a build silently.
final authSessionProvider = Provider<AuthSession>((ref) {
  AppLogger.warn(
    'authSessionProvider not overridden — network runs unauthenticated. '
    'Bind it to AuthService in the app root ProviderScope.',
    tag: 'atpost_network',
  );
  return const UnauthenticatedSession();
});
