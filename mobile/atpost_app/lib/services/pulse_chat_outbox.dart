import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/connectivity_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Phase 1 — outbound chat-message offline queue (P0-4 acceptance test E).
///
/// When `sendMessage` is called while the device is offline (or the API
/// call fails on a transport error), we enqueue the message with a
/// generated `idempotency_key` and a UI state of `queued`. As soon as the
/// app sees connectivity restored the queue drains in order, calling the
/// real API; each row flips between `queued -> sending -> sent | failed`.
///
/// Persistence is `flutter_secure_storage` — the app's standard small-KV
/// store (already in pubspec). Hive would be overkill for what is at most
/// a few hundred bytes per queued message. Each message stays in storage
/// until it is acked by the server (so a crash mid-flight doesn't lose
/// data) and is wiped on `sent`. Idempotency_key in the body guarantees
/// the server discards duplicate deliveries.

enum PulseOutboxState { queued, sending, sent, failed }

const _storageKey = 'pulse.chat.outbox.v1';

class PulseOutboxEntry {
  final String id;
  final String conversationId;
  final String bodyText;
  final String idempotencyKey;
  final DateTime createdAt;
  PulseOutboxState state;
  String? errorMessage;

  PulseOutboxEntry({
    required this.id,
    required this.conversationId,
    required this.bodyText,
    required this.idempotencyKey,
    required this.createdAt,
    this.state = PulseOutboxState.queued,
    this.errorMessage,
  });

  Map<String, dynamic> toJson() => {
        'id': id,
        'conversation_id': conversationId,
        'body_text': bodyText,
        'idempotency_key': idempotencyKey,
        'created_at': createdAt.toIso8601String(),
        'state': state.name,
        if (errorMessage != null) 'error': errorMessage,
      };

  factory PulseOutboxEntry.fromJson(Map<String, dynamic> json) {
    final stateName = (json['state'] ?? 'queued').toString();
    final state = PulseOutboxState.values.firstWhere(
      (s) => s.name == stateName,
      orElse: () => PulseOutboxState.queued,
    );
    return PulseOutboxEntry(
      id: (json['id'] ?? '').toString(),
      conversationId: (json['conversation_id'] ?? '').toString(),
      bodyText: (json['body_text'] ?? '').toString(),
      idempotencyKey: (json['idempotency_key'] ?? '').toString(),
      createdAt:
          DateTime.tryParse((json['created_at'] ?? '').toString()) ??
              DateTime.now(),
      state: state,
      errorMessage: json['error'] as String?,
    );
  }
}

/// Per-message snapshot the chat screen renders next to its bubble.
class PulseOutboxView {
  final String id;
  final String idempotencyKey;
  final PulseOutboxState state;

  const PulseOutboxView({
    required this.id,
    required this.idempotencyKey,
    required this.state,
  });
}

class PulseChatOutbox {
  PulseChatOutbox(this._repo, this._ref);

  final PulseRepository _repo;
  final Ref _ref;
  final FlutterSecureStorage _storage = const FlutterSecureStorage();
  final _changes = StreamController<List<PulseOutboxEntry>>.broadcast();
  static const _tag = 'PulseChatOutbox';

  List<PulseOutboxEntry> _entries = [];
  bool _hydrated = false;
  bool _draining = false;

  Stream<List<PulseOutboxEntry>> get changes => _changes.stream;
  List<PulseOutboxEntry> get entries => List.unmodifiable(_entries);

  /// Hydrate from storage. Safe to call repeatedly.
  Future<void> hydrate() async {
    if (_hydrated) return;
    try {
      final raw = await _storage.read(key: _storageKey);
      if (raw != null && raw.isNotEmpty) {
        final list = (jsonDecode(raw) as List)
            .whereType<Map>()
            .map((m) => PulseOutboxEntry.fromJson(
                Map<String, dynamic>.from(m)))
            .toList();
        _entries = list;
      }
    } catch (e, st) {
      AppLogger.warn('Outbox hydrate failed', tag: _tag, error: e, stackTrace: st);
    }
    _hydrated = true;
    _emit();
  }

  /// Enqueue a new message. Returns the assigned idempotency key so the
  /// caller can correlate the optimistic bubble with the eventual server
  /// echo.
  Future<PulseOutboxEntry> enqueue({
    required String conversationId,
    required String bodyText,
  }) async {
    await hydrate();
    final entry = PulseOutboxEntry(
      id: _randomId(),
      conversationId: conversationId,
      bodyText: bodyText,
      idempotencyKey: _idempotencyKey(),
      createdAt: DateTime.now().toUtc(),
    );
    _entries = [..._entries, entry];
    _emit();
    await _persist();
    // Try sending immediately — drain handles the offline case.
    unawaited(drain());
    return entry;
  }

  /// Iterate queued + failed entries and re-send. Stops cleanly when
  /// the device looks offline. Called on enqueue and whenever the
  /// connectivity flag flips from offline -> online.
  Future<void> drain() async {
    if (_draining) return;
    _draining = true;
    try {
      await hydrate();
      // Snapshot the queue so concurrent enqueues during drain don't
      // confuse the iterator. New entries added mid-drain get picked up
      // by the next drain pass.
      final pending = _entries
          .where((e) =>
              e.state == PulseOutboxState.queued ||
              e.state == PulseOutboxState.failed)
          .toList();
      for (final entry in pending) {
        // Connectivity sentinel — if a prior send threw a network error
        // the global flag is already true, so bail and wait for the
        // reconnect signal.
        final offline = _ref.read(isOfflineProvider);
        if (offline) {
          AppLogger.info(
            'Outbox drain pausing — device is offline',
            tag: _tag,
          );
          break;
        }
        _setState(entry, PulseOutboxState.sending, error: null);
        try {
          await _repo.sendMessage(
            entry.conversationId,
            bodyText: entry.bodyText,
            idempotencyKey: entry.idempotencyKey,
          );
          _setState(entry, PulseOutboxState.sent);
          // Wipe sent rows so the queue stays small.
          _entries.removeWhere((e) => e.id == entry.id);
          _emit();
          await _persist();
        } on DioException catch (e) {
          if (_isTransport(e)) {
            // Network problem — flag offline and stop the drain so we
            // resume when the connection recovers.
            _ref.read(isOfflineProvider.notifier).state = true;
            _setState(
              entry,
              PulseOutboxState.queued,
              error: 'Waiting for network',
            );
            break;
          }
          _setState(
            entry,
            PulseOutboxState.failed,
            error: _errorMessage(e),
          );
        } catch (e) {
          _setState(
            entry,
            PulseOutboxState.failed,
            error: e.toString(),
          );
        }
      }
    } finally {
      _draining = false;
    }
  }

  /// Drop a failed entry (user explicitly tapped "discard").
  Future<void> discard(String entryId) async {
    _entries.removeWhere((e) => e.id == entryId);
    _emit();
    await _persist();
  }

  /// Look up the queued/sending/failed state for an in-flight message.
  PulseOutboxView? viewFor(String conversationId, String idempotencyKey) {
    for (final e in _entries) {
      if (e.conversationId == conversationId &&
          e.idempotencyKey == idempotencyKey) {
        return PulseOutboxView(
          id: e.id,
          idempotencyKey: e.idempotencyKey,
          state: e.state,
        );
      }
    }
    return null;
  }

  /// All pending entries for a conversation, oldest first. Used to
  /// render the optimistic bubbles in the chat screen.
  List<PulseOutboxEntry> entriesFor(String conversationId) {
    return _entries
        .where((e) =>
            e.conversationId == conversationId &&
            e.state != PulseOutboxState.sent)
        .toList();
  }

  void _setState(
    PulseOutboxEntry entry,
    PulseOutboxState state, {
    String? error,
  }) {
    entry.state = state;
    entry.errorMessage = error;
    _emit();
    // Best-effort persist — failures don't block the user flow.
    unawaited(_persist());
  }

  void _emit() {
    if (!_changes.isClosed) _changes.add(List.unmodifiable(_entries));
  }

  Future<void> _persist() async {
    try {
      final encoded = jsonEncode(_entries.map((e) => e.toJson()).toList());
      await _storage.write(key: _storageKey, value: encoded);
    } catch (e, st) {
      AppLogger.warn('Outbox persist failed',
          tag: _tag, error: e, stackTrace: st);
    }
  }

  bool _isTransport(DioException e) {
    switch (e.type) {
      case DioExceptionType.connectionError:
      case DioExceptionType.connectionTimeout:
      case DioExceptionType.receiveTimeout:
      case DioExceptionType.sendTimeout:
        return true;
      case DioExceptionType.unknown:
        // Dart-level SocketException etc. surface as unknown — treat
        // them as transport so we keep retrying.
        return e.response == null;
      default:
        return false;
    }
  }

  String _errorMessage(DioException e) {
    final body = e.response?.data;
    if (body is Map && body['error'] is Map) {
      final m = (body['error'] as Map)['message'];
      if (m is String && m.isNotEmpty) return m;
    }
    return e.message ?? 'Send failed';
  }

  static String _randomId() {
    final rand = Random.secure();
    final chars = '0123456789abcdef';
    return List.generate(16, (_) => chars[rand.nextInt(16)]).join();
  }

  static String _idempotencyKey() {
    // 128-bit hex string — wide enough to dedupe across the whole tenant
    // without collisions, narrow enough to log in plaintext.
    final rand = Random.secure();
    const chars = '0123456789abcdef';
    return List.generate(32, (_) => chars[rand.nextInt(chars.length)])
        .join();
  }

  void dispose() {
    _changes.close();
  }
}

/// Process-wide singleton. Drains automatically on reconnect via the
/// connectivity listener installed in `pulseChatOutboxProvider`.
final pulseChatOutboxProvider = Provider<PulseChatOutbox>((ref) {
  final repo = ref.watch(pulseRepositoryProvider);
  final outbox = PulseChatOutbox(repo, ref);
  // Hydrate eagerly so the very first message send sees the existing
  // queue even if the chat screen mounts immediately on cold start.
  unawaited(outbox.hydrate());
  // When connectivity flips from offline -> online, drain. The
  // connectivity provider doesn't expose a stream so we lean on the
  // ref.listen primitive.
  ref.listen<bool>(isOfflineProvider, (prev, next) {
    if (prev == true && next == false) {
      unawaited(outbox.drain());
    }
  });
  ref.onDispose(outbox.dispose);
  return outbox;
});
