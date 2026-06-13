// FiGo realtime push providers — Wave G2.
//
// The food-service /v1/food/realtime/token endpoint (shipped in Wave
// A1) issues an HMAC-signed topic token that grants the current user
// access to `food.order.{order_id}` topics for orders they own.
//
// foodOrderPushProvider is a StreamProvider<FoodOrderEvent> the FiGo
// customer screen listens to so order status changes (CONFIRMED →
// PREPARING → READY → DELIVERED, plus substitution proposals and
// refund decisions) refresh the UI within ~100ms of the event landing
// on the gateway instead of waiting on the next REST poll.
//
// Mirrors the Mopedu pattern in mopedu_providers.dart so future
// domains can lift the same shape.

import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/realtime_stream_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// One emitted event from the SSE gateway. `eventType` is the
/// food.order.* constant (e.g. `food.order.payment_succeeded`);
/// `data` is the raw payload map.
class FoodOrderEvent {
  const FoodOrderEvent({required this.eventType, required this.data});

  final String eventType;
  final Map<String, dynamic> data;

  /// Convenience accessor for the order id that nearly every payload
  /// carries — falls back to `''` so consumers can defensively skip.
  String get orderId => (data['id'] as String?) ?? (data['order_id'] as String?) ?? '';
}

/// Fetches the topic token + opens the SSE session. autoDispose so
/// the connection closes when the figo screen leaves view.
final _foodRealtimeSessionProvider =
    FutureProvider.autoDispose<RealtimeStreamService?>((ref) async {
  final api = ref.read(apiClientProvider);
  try {
    final res = await api.post('/v1/food/realtime/token');
    final body = (res.data is Map) ? (res.data as Map)['data'] : null;
    if (body is! Map) return null;
    final token = (body['token'] as String?) ?? '';
    final rawTopics = body['topics'];
    final topics = (rawTopics is List)
        ? rawTopics.whereType<String>().toList()
        : const <String>[];
    if (token.isEmpty || topics.isEmpty) return null;
    // Only subscribe to per-order topics from this session. The
    // restaurant + admin topics belong to other roles and would be
    // rejected by the gateway's TOPIC_FORBIDDEN check anyway.
    final orderTopics =
        topics.where((t) => t.startsWith('food.order.')).toList();
    if (orderTopics.isEmpty) return null;
    final auth = ref.read(authServiceProvider);
    final svc = RealtimeStreamService(
      auth: auth,
      token: token,
      topics: orderTopics,
    );
    svc.start();
    ref.onDispose(svc.dispose);
    return svc;
  } catch (_) {
    // Best-effort: a token-endpoint outage degrades to polling.
    return null;
  }
});

/// Streams FoodOrderEvent as they arrive over SSE. Consumers (e.g.
/// the FiGo customer screen) listen via ref.listen and trigger a
/// REST refresh.
final foodOrderPushProvider =
    StreamProvider.autoDispose<FoodOrderEvent>((ref) async* {
  final svc = await ref.watch(_foodRealtimeSessionProvider.future);
  if (svc == null) {
    return;
  }
  await for (final frame in svc.events) {
    if (!frame.event.startsWith('food.order.')) continue;
    yield FoodOrderEvent(eventType: frame.event, data: frame.data);
  }
});
