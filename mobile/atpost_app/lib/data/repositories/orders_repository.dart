import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Phase F1.1 — re-pointed from the retired `/v1/orders` (orders-service)
/// to the unified `/v1/commerce/orders` surface in commerce-service.
/// The orders-service has been deleted; this repository is the only
/// consumer of the canonical commerce order API on mobile.
class OrdersRepository {
  final ApiClient _api;

  OrdersRepository(this._api);

  /// List orders for the authenticated buyer with cursor-based pagination.
  ///
  /// Phase 2.1 — commerce-service switched to keyset pagination; pass the
  /// `next_cursor` returned in the previous page's `meta` envelope to get
  /// the next batch. Returns the rich [OrderListPage] with the cursor +
  /// the order cards.
  Future<OrderListPage> getOrders({String? cursor, int limit = 20}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null && cursor.isNotEmpty) {
      params['cursor'] = cursor;
    }
    final response = await _api.get('/v1/commerce/orders', queryParameters: params);
    final data = (response.data['data'] as List<dynamic>?) ?? [];
    final meta = (response.data['meta'] as Map<String, dynamic>?) ?? {};
    final orders = data
        .map((e) => Order.fromJson(e as Map<String, dynamic>))
        .toList();
    return OrderListPage(
      orders: orders,
      nextCursor: meta['next_cursor'] as String?,
    );
  }

  /// Get a single order's detail. commerce-service returns
  /// {order, items} under data — the model picks up nested items.
  Future<Order> getOrder(String orderId) async {
    final response = await _api.get('/v1/commerce/orders/$orderId/items');
    return Order.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Cancel an order. The new endpoint accepts an optional reason that
  /// is shown to the seller; pass it from the cancel-reason dialog.
  Future<void> cancelOrder(String orderId, {String? reason}) async {
    await _api.post(
      '/v1/commerce/orders/$orderId/cancel',
      data: reason != null && reason.isNotEmpty ? {'reason': reason} : null,
    );
  }
}

/// One page of orders + a keyset cursor for the next page (null when
/// the caller has reached the end).
class OrderListPage {
  final List<Order> orders;
  final String? nextCursor;
  const OrderListPage({required this.orders, required this.nextCursor});
}

final ordersRepositoryProvider = Provider<OrdersRepository>((ref) {
  return OrdersRepository(ref.watch(apiClientProvider));
});
