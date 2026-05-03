// Commerce telemetry — Sprint 1 of mobile commerce parity.
//
// Mirrors `pulse_telemetry.dart` exactly: in-memory queue, 30s flush, banned
// PII keys, primitive-only props. Same backend endpoint
// (`POST /v1/analytics/events`) so the analytics service handles commerce
// events alongside Pulse / feed events without any new infra.
//
// PRIVACY:
//   * `addToCart` — product/variant/qty only. No buyer id, no price.
//   * `checkoutStarted` — grand total amount only (paise resolution; we send
//     rupees rounded). No address, no cart contents.
//   * `orderPlaced` — order id + payment method enum. No amount, no items.
//   * `pincodeChecked` — first three digits of the pincode (state band) +
//     boolean deliverable. Never the full pincode.

import 'dart:async';
import 'dart:collection';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

const Set<String> _bannedPropKeys = {
  'phone',
  'email',
  'name',
  'address',
  'pincode_full',
  'address_line_1',
  'address_line_2',
  'gst_number',
  'pan_number',
  'card_number',
  'cvv',
  'upi_id',
  'lat',
  'lng',
  'latitude',
  'longitude',
};

class CommerceTelemetryEvents {
  CommerceTelemetryEvents._();

  static const categoryViewed = 'commerce.category.viewed';
  static const productViewed = 'commerce.product.viewed';
  static const addToCart = 'commerce.cart.added';
  static const checkoutStarted = 'commerce.checkout.started';
  static const orderPlaced = 'commerce.order.placed';
  static const pincodeChecked = 'commerce.pincode.checked';
  // Sprint 2.
  static const orderViewed = 'commerce.order.viewed';
  static const returnRequested = 'commerce.return.requested';
  static const reviewSubmitted = 'commerce.review.submitted';
  static const wishlistAdded = 'commerce.wishlist.added';
  static const wishlistRemoved = 'commerce.wishlist.removed';
  static const searchPerformed = 'commerce.search.performed';
  static const filterApplied = 'commerce.filter.applied';
  // Data-saver — tracked here because we don't have a dedicated
  // platform-telemetry channel and the analytics warehouse already
  // accepts arbitrary event names through this emitter. Recon §F.2.
  static const dataSaverToggled = 'data_saver.toggled';
  static const dataSaverSessionActive = 'data_saver.session.active';
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

class CommerceTelemetry {
  CommerceTelemetry(this._client);

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
    _buffer.add(_QueuedEvent(
      name: name,
      timestamp: DateTime.now(),
      props: cleaned,
    ));
    start();
  }

  // ── Convenience wrappers ──────────────────────────────────────────

  void categoryViewed({required String categoryId}) {
    emit(
      CommerceTelemetryEvents.categoryViewed,
      props: {'category_id': categoryId},
    );
  }

  void productViewed({required String productId}) {
    emit(
      CommerceTelemetryEvents.productViewed,
      props: {'product_id': productId},
    );
  }

  /// `addToCart` — no PII. Product + variant ids and qty only.
  void addToCart({
    required String productId,
    required String variantId,
    required int qty,
  }) {
    emit(
      CommerceTelemetryEvents.addToCart,
      props: {
        'product_id': productId,
        'variant_id': variantId,
        'qty': qty,
      },
    );
  }

  /// `checkoutStarted` — amount only. We send rupees rounded to nearest
  /// integer so the analytics warehouse stores compact integers; for paise
  /// resolution swap to `(grandTotal * 100).round()`.
  void checkoutStarted({required double grandTotal}) {
    emit(
      CommerceTelemetryEvents.checkoutStarted,
      props: {'amount_inr': grandTotal.round()},
    );
  }

  /// `orderPlaced` — order id and the payment method enum. No amount,
  /// no contents.
  void orderPlaced({
    required String orderId,
    required String paymentMethod,
  }) {
    emit(
      CommerceTelemetryEvents.orderPlaced,
      props: {
        'order_id': orderId,
        'payment_method': paymentMethod,
      },
    );
  }

  /// `pincodeChecked` — never log the full pincode. We log the first three
  /// digits (state band) + the boolean.
  void pincodeChecked({
    required String pincode,
    required bool deliverable,
  }) {
    final band = pincode.length >= 3 ? pincode.substring(0, 3) : pincode;
    emit(
      CommerceTelemetryEvents.pincodeChecked,
      props: {
        'pincode_band': band,
        'deliverable': deliverable,
      },
    );
  }

  // ── Sprint 2 wrappers ────────────────────────────────────────────

  void orderViewed({required String orderId}) {
    emit(
      CommerceTelemetryEvents.orderViewed,
      props: {'order_id': orderId},
    );
  }

  void returnRequested({required String orderId, required String reason}) {
    emit(
      CommerceTelemetryEvents.returnRequested,
      props: {'order_id': orderId, 'reason': reason},
    );
  }

  void reviewSubmitted({required String productId, required int rating}) {
    emit(
      CommerceTelemetryEvents.reviewSubmitted,
      props: {'product_id': productId, 'rating': rating},
    );
  }

  void wishlistAdded({required String productId}) {
    emit(
      CommerceTelemetryEvents.wishlistAdded,
      props: {'product_id': productId},
    );
  }

  void wishlistRemoved({required String productId}) {
    emit(
      CommerceTelemetryEvents.wishlistRemoved,
      props: {'product_id': productId},
    );
  }

  /// `searchPerformed` — privacy-conscious. We send the query length only
  /// (not the content) plus the integer result count. The brief explicitly
  /// calls this out: "query length only, not content".
  void searchPerformed({required String query, required int resultCount}) {
    emit(
      CommerceTelemetryEvents.searchPerformed,
      props: {
        'query_length': query.trim().length,
        'result_count': resultCount,
      },
    );
  }

  void filterApplied({required int filterCount}) {
    emit(
      CommerceTelemetryEvents.filterApplied,
      props: {'filter_count': filterCount},
    );
  }

  /// Data-saver toggled. `source` is 'manual' (user flipped the
  /// switch) or 'auto' (slow-connection heuristic flipped it).
  void dataSaverToggled({required bool enabled, required String source}) {
    emit(
      CommerceTelemetryEvents.dataSaverToggled,
      props: {'enabled': enabled, 'source': source},
    );
  }

  /// Increment the per-session counter for "data-saver was active at
  /// least once". Idempotent within a single session — call sites just
  /// fire it whenever the effective flag transitions to `true`.
  void dataSaverSessionActive() {
    emit(
      CommerceTelemetryEvents.dataSaverSessionActive,
      props: const {'count': 1},
    );
  }

  // ── Internals ─────────────────────────────────────────────────────

  Map<String, Object?> _sanitize(Map<String, Object?> input) {
    final out = <String, Object?>{};
    input.forEach((key, value) {
      if (_bannedPropKeys.contains(key.toLowerCase())) {
        AppLogger.warn(
          'CommerceTelemetry dropped banned key "$key"',
          tag: 'CommerceTelemetry',
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
          'CommerceTelemetry dropped unsupported type for key "$key"',
          tag: 'CommerceTelemetry',
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
        'CommerceTelemetry flush failed; will retry: $e',
        tag: 'CommerceTelemetry',
      );
    } finally {
      _flushing = false;
    }
  }

  /// Test/QA hook — caller-driven flush.
  Future<void> flushNow() => _flush();
}

final commerceTelemetryProvider = Provider<CommerceTelemetry>((ref) {
  final client = ref.watch(apiClientProvider);
  final tel = CommerceTelemetry(client);
  tel.start();
  ref.onDispose(tel.stop);
  return tel;
});
