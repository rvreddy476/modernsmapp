import 'dart:async';

import 'package:atpost_app/data/models/presence.dart';
import 'package:atpost_app/data/repositories/presence_repository.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Polled 10s refresh of the M1 conversation-presence rollup. Stays an
/// `AsyncValue<ConversationPresence>` (FutureProvider) so consumers can
/// pattern-match `when(data:loading:error:)` like every other async
/// provider in the app.
///
/// The poll deliberately lives in this single provider — *not* in the
/// notifier below — because Riverpod's `family + autoDispose` already
/// scopes the timer to the lifetime of the screen that watches it.
final conversationPresencePollProvider =
    FutureProvider.autoDispose.family<ConversationPresence, String>((
      ref,
      conversationId,
    ) async {
      final repo = ref.watch(presenceRepositoryProvider);

      // Re-fetch every 10s while the consumer is mounted. `ref.invalidateSelf`
      // re-runs this function; `autoDispose` cancels the timer once the
      // screen leaves.
      final timer = Timer.periodic(const Duration(seconds: 10), (_) {
        ref.invalidateSelf();
      });
      ref.onDispose(timer.cancel);

      return repo.getPresence(conversationId);
    });

/// Controller that owns the *outbound* presence WS lifecycle for a single
/// conversation. The chat screen creates one of these in `initState`,
/// which immediately:
///
///   1. Sends `conversation.enter`.
///   2. Starts a 15s `conversation.heartbeat` ticker (server TTL is
///      longer than that, so one missed beat doesn't drop us).
///
/// `dispose()` cancels the ticker and sends `conversation.leave`.
///
/// Typing pings are also exposed here so the composer's `onChanged`
/// hook can call `onTyping()` on every keystroke; the controller
/// throttles outbound `typing.start` to once per 3s.
class ConversationPresenceController {
  ConversationPresenceController(
    this._realtime, {
    required this.conversationId,
  }) {
    _enter();
  }

  final RealtimeService _realtime;
  final String conversationId;

  Timer? _heartbeatTimer;
  DateTime? _lastTypingSentAt;
  bool _disposed = false;

  static const _heartbeatInterval = Duration(seconds: 15);
  static const _typingThrottle = Duration(seconds: 3);

  void _enter() {
    _realtime.send({
      'type': 'conversation.enter',
      'conversation_id': conversationId,
    });
    _heartbeatTimer = Timer.periodic(_heartbeatInterval, (_) {
      if (_disposed) return;
      _realtime.send({
        'type': 'conversation.heartbeat',
        'conversation_id': conversationId,
      });
    });
  }

  /// Composer keystroke hook. Throttles outbound `typing.start` so a fast
  /// typist doesn't flood the WS — the server treats a single ping as
  /// "still typing" for several seconds.
  void onTyping() {
    if (_disposed) return;
    final now = DateTime.now();
    final last = _lastTypingSentAt;
    if (last != null && now.difference(last) < _typingThrottle) return;
    _lastTypingSentAt = now;
    _realtime.send({
      'type': 'typing.start',
      'conversation_id': conversationId,
    });
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _heartbeatTimer?.cancel();
    _heartbeatTimer = null;
    _realtime.send({
      'type': 'conversation.leave',
      'conversation_id': conversationId,
    });
  }
}

/// Family of per-conversation presence controllers. Scoped via
/// `autoDispose` so leaving the chat screen tears the controller down,
/// which fires `conversation.leave` on the way out.
final conversationPresenceControllerProvider = Provider.autoDispose
    .family<ConversationPresenceController, String>((ref, conversationId) {
      final realtime = ref.watch(realtimeServiceProvider);
      final controller = ConversationPresenceController(
        realtime,
        conversationId: conversationId,
      );
      ref.onDispose(controller.dispose);
      return controller;
    });
