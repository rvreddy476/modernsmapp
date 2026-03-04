import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class OrdersRepository {
  final ApiClient _api;

  OrdersRepository(this._api);

  Future<List<Order>> getOrders() async {
    final response = await _api.get('/v1/orders');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => Order.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<Order> getOrder(String orderId) async {
    final response = await _api.get('/v1/orders/$orderId');
    return Order.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<void> cancelOrder(String orderId) async {
    await _api.post('/v1/orders/$orderId/cancel');
  }
}

final ordersRepositoryProvider = Provider<OrdersRepository>((ref) {
  return OrdersRepository(ref.watch(apiClientProvider));
});
