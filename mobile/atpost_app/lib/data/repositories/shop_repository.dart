import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/models/shop.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready repository for Marketplace operations.
/// Synchronized with the 2026-03-19 OpenAPI mobile integration spec.
class ShopRepository {
  final ApiClient _api;

  ShopRepository(this._api);

  /// List products with pagination and category support.
  /// Synchronized with GET /v1/shop/products
  Future<ShopPage> getProducts({String? category, int limit = 20, int offset = 0}) async {
    final params = <String, dynamic>{'limit': limit, 'offset': offset};
    if (category != null && category != 'All') params['category'] = category;

    final response = await _api.get('/v1/shop/products', queryParameters: params);
    final data = response.data['data'] as Map<String, dynamic>;
    final items = (data['items'] as List?)?.map((e) => Product.fromJson(e as Map<String, dynamic>)).toList() ?? [];

    return ShopPage(items: items, totalCount: (data['total'] ?? items.length) as int);
  }

  /// Fetch the current user's cart.
  /// Synchronized with GET /v1/shop/cart
  Future<List<CartItem>> getCart() async {
    final response = await _api.get('/v1/shop/cart');
    final items = (response.data['data']?['items'] as List?) ?? [];
    return items.map((e) => CartItem.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Add a product to the cart.
  /// Synchronized with POST /v1/shop/cart
  Future<void> addToCart(String productId, {int quantity = 1}) async {
    await _api.post('/v1/shop/cart', data: {
      'product_id': productId,
      'quantity': quantity,
    });
  }

  /// Remove a product from the cart.
  Future<void> removeFromCart(String productId) async {
    await _api.delete('/v1/shop/cart/$productId');
  }

  /// Finalize the purchase.
  Future<Order> checkout() async {
    final response = await _api.post('/v1/shop/checkout');
    return Order.fromJson(response.data['data'] as Map<String, dynamic>);
  }
}

class ShopPage {
  final List<Product> items;
  final int totalCount;
  ShopPage({required this.items, required this.totalCount});
}

final shopRepositoryProvider = Provider<ShopRepository>((ref) {
  return ShopRepository(ref.watch(apiClientProvider));
});
