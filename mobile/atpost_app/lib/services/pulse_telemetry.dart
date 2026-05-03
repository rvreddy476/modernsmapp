// Pulse telemetry — Sprint 5.
//
// A single emitter class that buffers events in memory and flushes them on
// a 30-second cadence to `POST /v1/analytics/events`. The contract is:
//
//   POST /v1/analytics/events
//   body: { events: [ { name, ts, props } ] }
//
// PRIVACY (spec §17 + §13 DPDP):
//   - Never include message content, photo bytes, raw search queries, or
//     Aadhaar references in event props.
//   - Counters and enum-typed dimensions only (`target_kind`, `intent`,
//     `plan_id`, `match_id` UUID, `boolean` flags).
//   - No location data on `pulse.safety.panic` (the panic event itself is
//     observable; location goes through the dedicated safety endpoint).
//   - The emitter strips any prop whose key matches the deny-list below as a
//     defence-in-depth — callers that pass a banned key get a debug warning.
//
// Threading: `flushTimer` runs on the Flutter main isolate (mobile-only;
// web/iso isolates do not consume this service). Flush failures are
// swallowed and the queue is retried on the next interval.

import 'dart:async';
import 'dart:collection';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Keys we will never forward, even if a caller mistakenly includes them.
/// These are belt-and-braces — the call sites should never set them.
const Set<String> _bannedPropKeys = {
  'message',
  'message_body',
  'message_content',
  'body',
  'body_text',
  'photo',
  'photo_bytes',
  'photo_data',
  'image_data',
  'aadhaar',
  'aadhaar_number',
  'aadhaar_ref',
  'lat',
  'lng',
  'latitude',
  'longitude',
  'location',
  'phone',
  'email',
};

/// Stable event name catalog. Exported as constants so call sites stay
/// consistent and grep-able.
class PulseTelemetryEvents {
  PulseTelemetryEvents._();

  static const pulseOpened = 'pulse.opened';
  static const candidateViewed = 'pulse.candidate.viewed';
  static const sparkCreated = 'pulse.spark.created';
  static const stashAdded = 'pulse.stash.added';
  static const stashRemoved = 'pulse.stash.removed';
  static const pass = 'pulse.pass';
  static const matchFormed = 'pulse.match.formed';
  static const matchOpened = 'pulse.match.opened';
  static const messageSent = 'pulse.message.sent';
  static const boostUsed = 'pulse.boost.used';
  static const checkoutStarted = 'pulse.premium.checkout.started';
  static const checkoutCompleted = 'pulse.premium.checkout.completed';
  static const checkoutFailed = 'pulse.premium.checkout.failed';
  static const safetyPanic = 'pulse.safety.panic';
  static const safetyReport = 'pulse.safety.report';
}

class _QueuedEvent {
  _QueuedEvent({
    required this.name,
    required this.timestamp,
    required this.props,
  });

  final String name;
  final DateTime timestamp;
  final Map<String, Object?> props;

  Map<String, dynamic> toJson() => {
        'name': name,
        'ts': timestamp.toUtc().toIso8601String(),
        'props': props,
      };
}

class PulseTelemetry {
  PulseTelemetry(this._client);

  final ApiClient _client;
  final Queue<_QueuedEvent> _buffer = Queue<_QueuedEvent>();
  Timer? _timer;
  bool _flushing = false;

  /// Largest buffer we will hold in memory before dropping the oldest event.
  /// Prevents OOM on a long offline window.
  static const int _maxBuffered = 500;

  /// Flush cadence. Spec §17 says 30s.
  static const Duration _flushInterval = Duration(seconds: 30);

  void start() {
    _timer ??= Timer.periodic(_flushInterval, (_) => _flush());
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  /// Public entry point. Drops banned keys and enqueues.
  void emit(String name, {Map<String, Object?>? props}) {
    final cleaned = _sanitize(props ?? const {});
    if (_buffer.length >= _maxBuffered) {
      _buffer.removeFirst();
    }
    _buffer.add(
      _QueuedEvent(
        name: name,
        timestamp: DateTime.now(),
        props: cleaned,
      ),
    );
    start();
  }

  // ---- Convenience wrappers (named so call sites stay readable). ---------

  void pulseOpened() => emit(PulseTelemetryEvents.pulseOpened);

  void candidateViewed({required String candidateUserId}) {
    emit(
      PulseTelemetryEvents.candidateViewed,
      props: {'candidate_id': candidateUserId},
    );
  }

  void sparkCreated({required String targetKind}) {
    emit(
      PulseTelemetryEvents.sparkCreated,
      props: {'target_kind': targetKind},
    );
  }

  void stashAdded() => emit(PulseTelemetryEvents.stashAdded);
  void stashRemoved() => emit(PulseTelemetryEvents.stashRemoved);
  void pass() => emit(PulseTelemetryEvents.pass);

  void matchFormed({required String matchId, required String intent}) {
    emit(
      PulseTelemetryEvents.matchFormed,
      props: {'match_id': matchId, 'intent': intent},
    );
  }

  void matchOpened({required String matchId}) {
    emit(
      PulseTelemetryEvents.matchOpened,
      props: {'match_id': matchId},
    );
  }

  /// Count-only — we never log message content.
  void messageSent({required String matchId, int count = 1}) {
    emit(
      PulseTelemetryEvents.messageSent,
      props: {'match_id': matchId, 'count': count},
    );
  }

  void boostUsed() => emit(PulseTelemetryEvents.boostUsed);

  void checkoutStarted({required String planId}) {
    emit(
      PulseTelemetryEvents.checkoutStarted,
      props: {'plan_id': planId},
    );
  }

  void checkoutCompleted({required String planId}) {
    emit(
      PulseTelemetryEvents.checkoutCompleted,
      props: {'plan_id': planId},
    );
  }

  void checkoutFailed({required String planId, required String reason}) {
    emit(
      PulseTelemetryEvents.checkoutFailed,
      props: {'plan_id': planId, 'reason': reason},
    );
  }

  /// `targetKind` is one of `question | answer | user`.
  void safetyReport({required String targetKind}) {
    emit(
      PulseTelemetryEvents.safetyReport,
      props: {'target_kind': targetKind},
    );
  }

  /// Panic — never logs location. The location goes through the dedicated
  /// `/v1/dating/safety/panic` POST.
  void safetyPanic() => emit(PulseTelemetryEvents.safetyPanic);

  // ---- Internals ---------------------------------------------------------

  Map<String, Object?> _sanitize(Map<String, Object?> input) {
    final out = <String, Object?>{};
    input.forEach((key, value) {
      if (_bannedPropKeys.contains(key.toLowerCase())) {
        AppLogger.warn(
          'Telemetry dropped banned key "$key"',
          tag: 'PulseTelemetry',
        );
        return;
      }
      // Only allow primitive types in event props.
      if (value == null ||
          value is num ||
          value is bool ||
          value is String) {
        out[key] = value;
      } else if (value is List) {
        out[key] = value
            .where((e) => e == null || e is num || e is bool || e is String)
            .toList();
      } else {
        // Anything else (e.g. nested maps with PII) is rejected.
        AppLogger.warn(
          'Telemetry dropped unsupported type for key "$key"',
          tag: 'PulseTelemetry',
        );
      }
    });
    return out;
  }

  Future<void> _flush() async {
    if (_flushing || _buffer.isEmpty) return;
    _flushing = true;
    final batch = _buffer.toList(growable: false);
    try {
      await _client.post(
        '/v1/analytics/events',
        data: {
          'events': batch.map((e) => e.toJson()).toList(),
        },
      );
      // Clear only what we successfully sent.
      for (var i = 0; i < batch.length && _buffer.isNotEmpty; i++) {
        _buffer.removeFirst();
      }
    } catch (e) {
      AppLogger.warn(
        'Telemetry flush failed; will retry: $e',
        tag: 'PulseTelemetry',
      );
    } finally {
      _flushing = false;
    }
  }

  /// Test/QA hook — caller-driven flush.
  Future<void> flushNow() => _flush();
}

final pulseTelemetryProvider = Provider<PulseTelemetry>((ref) {
  final client = ref.watch(apiClientProvider);
  final tel = PulseTelemetry(client);
  tel.start();
  ref.onDispose(tel.stop);
  return tel;
});
