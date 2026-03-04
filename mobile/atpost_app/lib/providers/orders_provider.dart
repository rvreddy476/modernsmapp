import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/repositories/orders_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final ordersProvider = FutureProvider.autoDispose<List<Order>>((ref) async {
  return ref.watch(ordersRepositoryProvider).getOrders();
});

final orderDetailProvider =
    FutureProvider.autoDispose.family<Order, String>((ref, orderId) async {
  return ref.watch(ordersRepositoryProvider).getOrder(orderId);
});
