import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/shop.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ShopRepository {
  final ApiClient _api;

  ShopRepository(this._api);

  /// List products with optional category filter.
  Future<List<Product>> getProducts({String? category, int limit = 20, int offset = 0}) async {
    final params = <String, dynamic>{'limit': limit, 'offset': offset};
    if (category != null) params['category'] = category;

    final response = await _api.get(
      '${Environment.shopPath}/products',
      queryParameters: params,
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => Product.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Get a single product.
  Future<Product> getProduct(String productId) async {
    final response = await _api.get('${Environment.shopPath}/products/$productId');
    return Product.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Get cart items.
  Future<List<CartItem>> getCart() async {
    final response = await _api.get('${Environment.shopPath}/cart');
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => CartItem.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Add item to cart.
  Future<void> addToCart(String productId, {int quantity = 1}) async {
    await _api.post(
      '${Environment.shopPath}/cart',
      data: {'product_id': productId, 'quantity': quantity},
    );
  }

  /// Remove item from cart.
  Future<void> removeFromCart(String productId) async {
    await _api.delete('${Environment.shopPath}/cart/$productId');
  }

  /// Checkout cart.
  Future<Order> checkout() async {
    final response = await _api.post('${Environment.shopPath}/checkout');
    return Order.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// List orders.
  Future<List<Order>> getOrders({String role = 'buyer', int limit = 20}) async {
    final response = await _api.get(
      '${Environment.shopPath}/orders',
      queryParameters: {'role': role, 'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => Order.fromJson(e as Map<String, dynamic>)).toList();
  }
}

final shopRepositoryProvider = Provider<ShopRepository>((ref) {
  return ShopRepository(ref.watch(apiClientProvider));
});
