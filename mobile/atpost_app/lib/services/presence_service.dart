import 'dart:async';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Keeps the current user marked "online" by periodically posting to
/// `POST /v1/users/me/heartbeat`.
///
/// Why this exists: the server stores presence as a Redis key
/// `presence:{userID}` with a 90 s TTL. That key is only ever written by
/// (a) this heartbeat or (b) the chat WebSocket gateway on connect/pong.
/// The mobile app's WebSocket (`RealtimeService`) is created lazily — only
/// when a chat/feed/notifications screen is mounted — so a user who opens,
/// say, the Friends tab right after login was never marked online, and
/// therefore appeared OFFLINE to everyone (and saw all friends offline).
///
/// This service runs independently of the WebSocket: while the app is
/// foregrounded and authenticated it pings every [_interval] (30 s), which
/// is well inside the 90 s TTL. It pauses when the app is backgrounded and
/// resumes (with an immediate beat) when it returns to the foreground.
class PresenceService {
  PresenceService(this._api, this._auth);

  final ApiClient _api;
  final AuthService _auth;

  static const _tag = 'PresenceService';

  /// TTL is 90 s server-side; 30 s leaves two missed beats of headroom.
  static const Duration _interval = Duration(seconds: 30);

  Timer? _timer;
  bool _started = false;
  bool _sending = false;

  /// Begin sending heartbeats. Safe to call repeatedly — only the first
  /// call has an effect. Fires one beat immediately so the user shows
  /// online without waiting a full interval.
  void start() {
    if (_started) return;
    _started = true;
    AppLogger.info('Presence heartbeat started', tag: _tag);
    _beat();
    _timer = Timer.periodic(_interval, (_) => _beat());
  }

  /// Stop sending heartbeats (e.g. on logout or app background). The
  /// server-side key then expires naturally within 90 s.
  void stop() {
    if (!_started) return;
    _started = false;
    _timer?.cancel();
    _timer = null;
    AppLogger.info('Presence heartbeat stopped', tag: _tag);
  }

  bool get isRunning => _started;

  /// Send a single heartbeat now (used on foreground resume so the dot
  /// flips back without waiting for the next tick). No-op when not started.
  void beatNow() {
    if (_started) _beat();
  }

  Future<void> _beat() async {
    if (!_auth.isAuthenticated) return;
    if (_sending) return; // never overlap if a request is slow
    _sending = true;
    try {
      await _api.post('/v1/users/me/heartbeat');
    } catch (e) {
      // Presence is best-effort: a missed beat just risks the dot going
      // stale, the next beat recovers it. Don't surface to the user.
      AppLogger.debug('Presence heartbeat failed: $e', tag: _tag);
    } finally {
      _sending = false;
    }
  }

  void dispose() {
    stop();
  }
}

/// App-wide presence heartbeat. Kept alive for the whole authenticated
/// session by [ShellScaffold]. The heartbeat auto-stops when auth is lost.
final presenceServiceProvider = Provider<PresenceService>((ref) {
  final auth = ref.watch(authServiceProvider);
  final service = PresenceService(ref.watch(apiClientProvider), auth);

  void syncAuth(AuthState state) {
    if (state.isAuthenticated) {
      service.start();
    } else {
      service.stop();
    }
  }

  syncAuth(auth.state);
  final sub = auth.stateStream.listen(syncAuth);

  ref.onDispose(() {
    sub.cancel();
    service.dispose();
  });

  return service;
});
