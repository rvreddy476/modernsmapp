import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/models/shop.dart';
import 'package:atpost_app/data/repositories/shop_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Phase F1.3 — variant-aware shop state.
///
/// Internal `cart` is the server-authoritative Cart envelope (subtotal,
/// tax, shipping, grand_total all come from the backend). The legacy
/// `cartItems` getter is preserved for callers that haven't migrated.
class ShopState {
  final List<Product> products;
  final Cart cart;
  final String selectedCategoryId; // commerce-service uses category UUID
  final String searchQuery;
  final bool isLoading;

  const ShopState({
    this.products = const [],
    this.cart = const Cart(id: '', items: []),
    this.selectedCategoryId = '',
    this.searchQuery = '',
    this.isLoading = false,
  });

  // Backward-compatible accessors used by shop_screen + cart UI.
  List<CartItem> get cartItems => cart.items;
  double get cartTotal => cart.grandTotal > 0 ? cart.grandTotal : cart.subtotal;
  int get cartCount => cart.itemCount;

  /// Display-only category label for the chips row. The provider only
  /// forwards UUID-shaped values to the backend (legacy string labels
  /// like "Electronics" are treated as "no filter" until the chips row
  /// is migrated to load category UUIDs from /v1/commerce/categories).
  String get selectedCategory =>
      selectedCategoryId.isEmpty ? 'All' : selectedCategoryId;

  ShopState copyWith({
    List<Product>? products,
    Cart? cart,
    String? selectedCategoryId,
    String? searchQuery,
    bool? isLoading,
  }) {
    return ShopState(
      products: products ?? this.products,
      cart: cart ?? this.cart,
      selectedCategoryId: selectedCategoryId ?? this.selectedCategoryId,
      searchQuery: searchQuery ?? this.searchQuery,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

class ShopNotifier extends StateNotifier<AsyncValue<ShopState>> {
  final ShopRepository _repo;

  ShopNotifier(this._repo) : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final results = await Future.wait([
        ErrorHandler.retry(() => _repo.getProducts()),
        ErrorHandler.retry(() => _repo.getCart()),
      ]);
      state = AsyncValue.data(ShopState(
        products: (results[0] as ShopPage).items,
        cart: results[1] as Cart,
      ));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> setCategory(String category) async {
    final currentState = state.value;
    if (currentState == null) return;
    // Only forward UUID-shaped categories to the backend; legacy string
    // labels ("All", "Electronics") are treated as "no filter" until the
    // chips row is migrated to load category UUIDs from
    // /v1/commerce/categories.
    final isUuid = RegExp(
      r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$',
    ).hasMatch(category);
    final categoryId = isUuid ? category : '';
    state = AsyncValue.data(currentState.copyWith(
      selectedCategoryId: categoryId,
      isLoading: true,
    ));
    try {
      final page = await ErrorHandler.retry(
        () => _repo.getProducts(categoryId: categoryId.isEmpty ? null : categoryId),
      );
      state = AsyncValue.data(state.value!.copyWith(products: page.items, isLoading: false));
    } catch (e, st) {
      state = AsyncValue.data(state.value!.copyWith(isLoading: false));
      ErrorHandler.handle(e, st, context: 'ShopNotifier.setCategory');
    }
  }

  void setSearchQuery(String query) {
    final currentState = state.value;
    if (currentState == null) return;
    state = AsyncValue.data(currentState.copyWith(searchQuery: query));
  }

  /// Adds a product's default variant to the cart. commerce-service is
  /// the source of truth for cart totals, so after the add we refetch
  /// the cart rather than building a local diff (eliminates the
  /// price-drift class of bug we saw on web before Phase 1.1).
  ///
  /// Returns false if the product has no default variant (legacy data
  /// or a draft/archived listing) — the screen can show a "see options"
  /// CTA instead of a silent failure.
  Future<bool> addToCart(Product product) async {
    final currentState = state.value;
    if (currentState == null) return false;
    final variantId = product.defaultVariantId;
    if (variantId == null || variantId.isEmpty) {
      return false;
    }
    try {
      await _repo.addToCart(variantId: variantId, productId: product.id);
      final updated = await _repo.getCart();
      state = AsyncValue.data(currentState.copyWith(cart: updated));
      return true;
    } catch (e, st) {
      ErrorHandler.handle(e, st, context: 'ShopNotifier.addToCart');
      return false;
    }
  }

  Future<void> removeFromCart(String variantId) async {
    final currentState = state.value;
    if (currentState == null) return;
    try {
      await _repo.removeFromCart(variantId);
      final updated = await _repo.getCart();
      state = AsyncValue.data(currentState.copyWith(cart: updated));
    } catch (e, st) {
      ErrorHandler.handle(e, st, context: 'ShopNotifier.removeFromCart');
    }
  }

  /// Checkout requires an address + payment method. shop_screen should
  /// drive the user to a checkout sheet that collects both before
  /// calling this. The legacy parameterless variant has been removed —
  /// callers that try it will get a compile error.
  Future<Order?> checkout({
    required String addressId,
    required String paymentMethod,
    String? couponCode,
    String? idempotencyKey,
  }) async {
    try {
      final order = await _repo.checkout(
        addressId: addressId,
        paymentMethod: paymentMethod,
        couponCode: couponCode,
        idempotencyKey: idempotencyKey,
      );
      refresh();
      return order;
    } catch (e, st) {
      ErrorHandler.handle(e, st, context: 'ShopNotifier.checkout');
      return null;
    }
  }
}

final shopProvider =
    StateNotifierProvider.autoDispose<ShopNotifier, AsyncValue<ShopState>>((ref) {
  return ShopNotifier(ref.watch(shopRepositoryProvider));
});

/// Memoized filtered products for performance.
final filteredProductsProvider = Provider.autoDispose<List<Product>>((ref) {
  final shopState = ref.watch(shopProvider).valueOrNull;
  if (shopState == null) return [];

  final query = shopState.searchQuery.toLowerCase();
  if (query.isEmpty) return shopState.products;

  return shopState.products.where((p) {
    return p.title.toLowerCase().contains(query) ||
        p.description.toLowerCase().contains(query);
  }).toList();
});
