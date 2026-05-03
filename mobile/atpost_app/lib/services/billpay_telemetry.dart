// Bill-pay telemetry — Phase 2.
//
// Mirrors `wallet_telemetry.dart`: in-memory queue, 30s flush, banned PII
// keys, primitive-only props. Same backend endpoint
// (`POST /v1/analytics/events`).
//
// PRIVACY (DPDP §13 + bill-pay PRD):
//   * Never log exact rupee/paise amounts. Always bucket via `amountBucket`.
//   * Never log bill identifier (consumer number, connection id), phone,
//     account_id, label, receipt number, BBPS RRN, idempotency key.
//   * `paymentMethod` is the categorical enum: `wallet | upi | card`.
//   * `categoryId` and `providerId` are stable system ids (not user PII)
//     and ARE allowed.
//   * `operator` is the carrier name (Jio/Airtel/Vi) — categorical, allowed.

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
  'identifier',
  'consumer_number',
  'connection_id',
  'account_id',
  'bill_id',
  'receipt_number',
  'bbps_rrn',
  'rrn',
  'lat',
  'lng',
  'latitude',
  'longitude',
};

class BillPayTelemetryEvents {
  BillPayTelemetryEvents._();

  static const homeOpened = 'billpay.home.opened';
  static const categoryOpened = 'billpay.category.opened';
  static const providerOpened = 'billpay.provider.opened';
  static const accountAdded = 'billpay.account.added';
  static const billFetched = 'billpay.bill.fetched';
  static const paymentStarted = 'billpay.payment.started';
  static const paymentCompleted = 'billpay.payment.completed';
  static const rechargeStarted = 'billpay.recharge.started';
  static const reminderSet = 'billpay.reminder.set';
  static const scheduledCreated = 'billpay.scheduled.created';
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

class BillPayTelemetry {
  BillPayTelemetry(this._client);

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

  void billpayHomeOpened() => emit(BillPayTelemetryEvents.homeOpened);

  void billpayCategoryOpened(String categoryId) {
    emit(
      BillPayTelemetryEvents.categoryOpened,
      props: {'category_id': categoryId},
    );
  }

  void billpayProviderOpened(String providerId) {
    emit(
      BillPayTelemetryEvents.providerOpened,
      props: {'provider_id': providerId},
    );
  }

  /// No identifier in payload — only the category id.
  void billpayAccountAdded({required String categoryId}) {
    emit(
      BillPayTelemetryEvents.accountAdded,
      props: {'category_id': categoryId},
    );
  }

  /// Boolean only — never the bill amount or identifier.
  void billpayBillFetched({
    required String categoryId,
    required bool hasBill,
  }) {
    emit(
      BillPayTelemetryEvents.billFetched,
      props: {
        'category_id': categoryId,
        'has_bill': hasBill,
      },
    );
  }

  void billpayPaymentStarted({
    required String categoryId,
    required String paymentMethod,
    required int amountPaise,
  }) {
    emit(
      BillPayTelemetryEvents.paymentStarted,
      props: {
        'category_id': categoryId,
        'payment_method': paymentMethod,
        'amount_bucket': amountBucket(amountPaise),
      },
    );
  }

  void billpayPaymentCompleted({
    required String categoryId,
    required String paymentMethod,
    required int amountPaise,
    required String status,
  }) {
    emit(
      BillPayTelemetryEvents.paymentCompleted,
      props: {
        'category_id': categoryId,
        'payment_method': paymentMethod,
        'amount_bucket': amountBucket(amountPaise),
        'status': status,
      },
    );
  }

  /// Operator is the carrier brand (Jio/Airtel/Vi/BSNL) — categorical.
  void billpayRechargeStarted({
    required String operator,
    required int amountPaise,
  }) {
    emit(
      BillPayTelemetryEvents.rechargeStarted,
      props: {
        'operator': operator,
        'amount_bucket': amountBucket(amountPaise),
      },
    );
  }

  void billpayReminderSet({required int daysBefore}) {
    emit(
      BillPayTelemetryEvents.reminderSet,
      props: {'days_before': daysBefore},
    );
  }

  void billpayScheduledCreated({required String scheduleKind}) {
    emit(
      BillPayTelemetryEvents.scheduledCreated,
      props: {'schedule_kind': scheduleKind},
    );
  }

  // ── Internals ────────────────────────────────────────────────────────

  Map<String, Object?> _sanitize(Map<String, Object?> input) {
    final out = <String, Object?>{};
    input.forEach((key, value) {
      if (_bannedPropKeys.contains(key.toLowerCase())) {
        AppLogger.warn(
          'BillPayTelemetry dropped banned key "$key"',
          tag: 'BillPayTelemetry',
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
          'BillPayTelemetry dropped unsupported type for key "$key"',
          tag: 'BillPayTelemetry',
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
        'BillPayTelemetry flush failed; will retry: $e',
        tag: 'BillPayTelemetry',
      );
    } finally {
      _flushing = false;
    }
  }

  Future<void> flushNow() => _flush();
}

final billpayTelemetryProvider = Provider<BillPayTelemetry>((ref) {
  final client = ref.watch(apiClientProvider);
  final tel = BillPayTelemetry(client);
  tel.start();
  ref.onDispose(tel.stop);
  return tel;
});
