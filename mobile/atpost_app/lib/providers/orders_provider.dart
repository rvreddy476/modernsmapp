import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/repositories/orders_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Phase F1.3 — re-point provider to the cursor-based commerce orders
/// list. The legacy provider returned a bare List<Order>; existing
/// callers that didn't paginate keep working because we still expose
/// just the orders list. Callers that want pagination should switch to
/// the [ordersPageProvider] family below.
final ordersProvider = FutureProvider.autoDispose<List<Order>>((ref) async {
  final page = await ref.watch(ordersRepositoryProvider).getOrders();
  return page.orders;
});

/// Cursor-aware variant. Pass null for the first page; the returned
/// page carries `nextCursor` for the next call. Used by infinite-scroll
/// screens (orders_screen.dart) that load more on scroll-end.
final ordersPageProvider = FutureProvider.autoDispose
    .family<OrderListPage, String?>((ref, cursor) async {
  return ref.watch(ordersRepositoryProvider).getOrders(cursor: cursor);
});

final orderDetailProvider =
    FutureProvider.autoDispose.family<Order, String>((ref, orderId) async {
  return ref.watch(ordersRepositoryProvider).getOrder(orderId);
});
