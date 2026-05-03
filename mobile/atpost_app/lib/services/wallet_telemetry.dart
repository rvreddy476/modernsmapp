// Wallet telemetry — Phase 2 Sprint 1.
//
// Mirrors `pulse_telemetry.dart` and `commerce_telemetry.dart`: in-memory
// queue, 30s flush, banned PII keys, primitive-only props. Same backend
// endpoint (`POST /v1/analytics/events`) so the analytics service handles
// wallet events alongside everything else.
//
// PRIVACY (DPDP §13 + wallet PRD):
//   * Never log exact rupee/paise amounts. Always bucket via `amountBucket`.
//   * Never log recipient phone, recipient user id, label, or any KYC ref.
//   * `recipientType` is the categorical enum: `frequent | atpost_user | phone`.
//   * The KYC tier is the enum string only.
//   * UPI URLs, transaction ids, and idempotency keys never leave the device
//     via this channel.

import 'dart:async';
import 'dart:collection';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

const Set<String> _bannedPropKeys = {
  'phone',
  'email',
  'name',
  'address',
  'pincode_full',
  'recipient_user_id',
  'recipient_phone',
  'recipient_label',
  'label',
  'pan_number',
  'aadhaar',
  'aadhaar_number',
  'aadhaar_ref',
  'upi_id',
  'upi_url',
  'upi_intent_url',
  'amount_paise',
  'amount_inr',
  'amount',
  'rupees',
  'transaction_id',
  'idempotency_key',
  'lat',
  'lng',
  'latitude',
  'longitude',
};

class WalletTelemetryEvents {
  WalletTelemetryEvents._();

  static const opened = 'wallet.opened';
  static const topUpStarted = 'wallet.topup.started';
  static const topUpCompleted = 'wallet.topup.completed';
  static const topUpFailed = 'wallet.topup.failed';
  static const sendStarted = 'wallet.send.started';
  static const sendCompleted = 'wallet.send.completed';
  static const sendFailed = 'wallet.send.failed';
  static const kycStarted = 'wallet.kyc.started';
  static const kycCompleted = 'wallet.kyc.completed';
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

class WalletTelemetry {
  WalletTelemetry(this._client);

  final ApiClient _client;
  final Queue<_QueuedEvent> _buffer = Queue<_QueuedEvent>();
  Timer? _timer;
  bool _flushing = false;

  static const int _maxBuffered = 500;
  static const Duration _flushInterval = Duration(seconds: 30);

  void start() {
    _timer ??= Timer.periodic(_flushInterval, (_) => _flush());
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

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

  // ── Convenience wrappers ─────────────────────────────────────────────

  void walletOpened() => emit(WalletTelemetryEvents.opened);

  /// Bucketed amount only — never raw paise.
  void walletTopUpStarted({required int amountPaise}) {
    emit(
      WalletTelemetryEvents.topUpStarted,
      props: {'amount_bucket': amountBucket(amountPaise)},
    );
  }

  void walletTopUpCompleted({required int amountPaise}) {
    emit(
      WalletTelemetryEvents.topUpCompleted,
      props: {'amount_bucket': amountBucket(amountPaise)},
    );
  }

  void walletTopUpFailed({
    required int amountPaise,
    required String reason,
  }) {
    emit(
      WalletTelemetryEvents.topUpFailed,
      props: {
        'amount_bucket': amountBucket(amountPaise),
        'reason': reason,
      },
    );
  }

  /// `recipientType` is one of `frequent | atpost_user | phone`.
  void walletSendStarted({
    required String recipientType,
    required int amountPaise,
  }) {
    emit(
      WalletTelemetryEvents.sendStarted,
      props: {
        'recipient_type': recipientType,
        'amount_bucket': amountBucket(amountPaise),
      },
    );
  }

  void walletSendCompleted({
    required String recipientType,
    required int amountPaise,
  }) {
    emit(
      WalletTelemetryEvents.sendCompleted,
      props: {
        'recipient_type': recipientType,
        'amount_bucket': amountBucket(amountPaise),
      },
    );
  }

  void walletSendFailed({
    required String recipientType,
    required int amountPaise,
    required String reason,
  }) {
    emit(
      WalletTelemetryEvents.sendFailed,
      props: {
        'recipient_type': recipientType,
        'amount_bucket': amountBucket(amountPaise),
        'reason': reason,
      },
    );
  }

  /// `tier` is the enum string `minimal | full | enhanced`.
  void walletKYCStarted({required String tier}) {
    emit(
      WalletTelemetryEvents.kycStarted,
      props: {'tier': tier},
    );
  }

  void walletKYCCompleted({required String tier}) {
    emit(
      WalletTelemetryEvents.kycCompleted,
      props: {'tier': tier},
    );
  }

  // ── Internals ────────────────────────────────────────────────────────

  Map<String, Object?> _sanitize(Map<String, Object?> input) {
    final out = <String, Object?>{};
    input.forEach((key, value) {
      if (_bannedPropKeys.contains(key.toLowerCase())) {
        AppLogger.warn(
          'WalletTelemetry dropped banned key "$key"',
          tag: 'WalletTelemetry',
        );
        return;
      }
      if (value == null || value is num || value is bool || value is String) {
        out[key] = value;
      } else if (value is List) {
        out[key] = value
            .where((e) => e == null || e is num || e is bool || e is String)
            .toList();
      } else {
        AppLogger.warn(
          'WalletTelemetry dropped unsupported type for key "$key"',
          tag: 'WalletTelemetry',
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
      for (var i = 0; i < batch.length && _buffer.isNotEmpty; i++) {
        _buffer.removeFirst();
      }
    } catch (e) {
      AppLogger.warn(
        'WalletTelemetry flush failed; will retry: $e',
        tag: 'WalletTelemetry',
      );
    } finally {
      _flushing = false;
    }
  }

  Future<void> flushNow() => _flush();
}

final walletTelemetryProvider = Provider<WalletTelemetry>((ref) {
  final client = ref.watch(apiClientProvider);
  final tel = WalletTelemetry(client);
  tel.start();
  ref.onDispose(tel.stop);
  return tel;
});
