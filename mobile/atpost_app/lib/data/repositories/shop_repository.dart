import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/models/shop.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Phase F1.1 — re-pointed from the retired `/v1/shop` (shop-service)
/// to the unified `/v1/commerce/*` surface in commerce-service.
///
/// Breaking changes from the legacy surface (callers in shop_provider +
/// shop_screen must be updated alongside this re-point):
///   - `addToCart` / `removeFromCart` are keyed on `variant_id`, not
///     `product_id`. Variants are the unit of inventory.
///   - `getCart` returns a wrapped Cart with subtotal/tax/shipping/grand
///     totals (not a bare CartItem list).
///   - `checkout()` now requires an address + payment method.
class ShopRepository {
  final ApiClient _api;

  ShopRepository(this._api);

  /// List products with offset pagination + optional category UUID.
  Future<ShopPage> getProducts({
    String? categoryId,
    String? query,
    int limit = 20,
    int offset = 0,
  }) async {
    final params = <String, dynamic>{'limit': limit, 'offset': offset};
    if (categoryId != null && categoryId.isNotEmpty) {
      params['category'] = categoryId;
    }
    if (query != null && query.isNotEmpty) {
      params['q'] = query;
    }

    final response = await _api.get('/v1/commerce/products', queryParameters: params);
    final data = response.data['data'] as Map<String, dynamic>;
    final items = (data['items'] as List?)
            ?.map((e) => Product.fromJson(e as Map<String, dynamic>))
            .toList() ??
        [];

    return ShopPage(items: items, totalCount: (data['total'] ?? items.length) as int);
  }

  /// Fetch the buyer's cart with line items + computed totals.
  Future<Cart> getCart() async {
    final response = await _api.get('/v1/commerce/cart');
    return Cart.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Add a variant to the cart. variantId is the inventory key — the
  /// product_id field is optional for the backend but useful for
  /// analytics; pass it when known.
  Future<void> addToCart({
    required String variantId,
    int quantity = 1,
    String? productId,
  }) async {
    await _api.post('/v1/commerce/cart/items', data: {
      'variant_id': variantId,
      'quantity': quantity,
      'product_id': ?productId,
    });
  }

  /// Update a cart line's quantity. Passing 0 deletes the line (matches
  /// the Phase 1.2 server-authoritative cart endpoint).
  Future<Cart> updateCartItemQuantity({
    required String variantId,
    required int quantity,
  }) async {
    final response = await _api.patch(
      '/v1/commerce/cart/items/by-variant/$variantId',
      data: {'quantity': quantity},
    );
    return Cart.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Remove a variant from the cart.
  Future<void> removeFromCart(String variantId) async {
    await _api.delete('/v1/commerce/cart/items/$variantId');
  }

  /// Finalize the purchase. The new endpoint requires the buyer's
  /// address + payment method; B2B context (organization, PO, cost
  /// center) is optional and only relevant for org checkouts.
  Future<Order> checkout({
    required String addressId,
    required String paymentMethod, // 'prepaid' | 'cod' | 'credit'
    String? couponCode,
    String? giftMessage,
    String? idempotencyKey,
    String? organizationId,
    String? poNumber,
    String? costCenter,
    String? invoiceEmail,
  }) async {
    final response = await _api.post('/v1/commerce/orders/checkout', data: {
      'address_id': addressId,
      'payment_method': paymentMethod,
      if (couponCode != null && couponCode.isNotEmpty) 'coupon_code': couponCode,
      'gift_message': ?giftMessage,
      'idempotency_key': ?idempotencyKey,
      'organization_id': ?organizationId,
      'po_number': ?poNumber,
      'cost_center': ?costCenter,
      'invoice_email': ?invoiceEmail,
    });
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
