import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/models/shop.dart';
import 'package:atpost_app/data/repositories/shop_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for the Marketplace system.
class ShopState {
  final List<Product> products;
  final List<CartItem> cartItems;
  final String selectedCategory;
  final String searchQuery;
  final bool isLoading;

  const ShopState({
    this.products = const [],
    this.cartItems = const [],
    this.selectedCategory = 'All',
    this.searchQuery = '',
    this.isLoading = false,
  });

  double get cartTotal => cartItems.fold(0, (sum, item) => sum + ((item.product?.price ?? 0) * item.quantity));
  int get cartCount => cartItems.length;

  ShopState copyWith({
    List<Product>? products,
    List<CartItem>? cartItems,
    String? selectedCategory,
    String? searchQuery,
    bool? isLoading,
  }) {
    return ShopState(
      products: products ?? this.products,
      cartItems: cartItems ?? this.cartItems,
      selectedCategory: selectedCategory ?? this.selectedCategory,
      searchQuery: searchQuery ?? this.searchQuery,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

/// Production-ready Shop Notifier with optimistic cart updates.
class ShopNotifier extends StateNotifier<AsyncValue<ShopState>> {
  final ShopRepository _repo;

  ShopNotifier(this._repo) : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    try {
      final results = await Future.wait([
        ErrorHandler.retry(() => _repo.getProducts(category: 'All')),
        ErrorHandler.retry(() => _repo.getCart()),
      ]);

      state = AsyncValue.data(ShopState(
        products: (results[0] as ShopPage).items,
        cartItems: results[1] as List<CartItem>,
      ));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> setCategory(String category) async {
    final currentState = state.value;
    if (currentState == null) return;

    state = AsyncValue.data(currentState.copyWith(selectedCategory: category, isLoading: true));
    try {
      final page = await ErrorHandler.retry(() => _repo.getProducts(category: category));
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

  /// Optimistically adds an item to the cart.
  Future<void> addToCart(Product product) async {
    final currentState = state.value;
    if (currentState == null) return;

    // Check if already in cart
    final existingIndex = currentState.cartItems.indexWhere((item) => item.productId == product.id);
    final newItems = List<CartItem>.from(currentState.cartItems);

    if (existingIndex != -1) {
      final item = newItems[existingIndex];
      newItems[existingIndex] = CartItem(productId: item.productId, quantity: item.quantity + 1, product: item.product);
    } else {
      newItems.add(CartItem(productId: product.id, quantity: 1, product: product));
    }

    // 1. Optimistic update
    state = AsyncValue.data(currentState.copyWith(cartItems: newItems));

    // 2. API call
    try {
      await _repo.addToCart(product.id);
    } catch (e, st) {
      // 3. Rollback
      state = AsyncValue.data(currentState);
      ErrorHandler.handle(e, st, context: 'ShopNotifier.addToCart');
    }
  }

  Future<void> removeFromCart(String productId) async {
    final currentState = state.value;
    if (currentState == null) return;

    final newItems = currentState.cartItems.where((item) => item.productId != productId).toList();

    // 1. Optimistic update
    state = AsyncValue.data(currentState.copyWith(cartItems: newItems));

    // 2. API call
    try {
      await _repo.removeFromCart(productId);
    } catch (e, st) {
      // 3. Rollback
      state = AsyncValue.data(currentState);
      ErrorHandler.handle(e, st, context: 'ShopNotifier.removeFromCart');
    }
  }

  Future<Order?> checkout() async {
    try {
      final order = await _repo.checkout();
      refresh(); // Reload everything after checkout
      return order;
    } catch (e, st) {
      ErrorHandler.handle(e, st, context: 'ShopNotifier.checkout');
      return null;
    }
  }
}

final shopProvider = StateNotifierProvider.autoDispose<ShopNotifier, AsyncValue<ShopState>>((ref) {
  return ShopNotifier(ref.watch(shopRepositoryProvider));
});

/// Memoized filtered products for performance.
final filteredProductsProvider = Provider.autoDispose<List<Product>>((ref) {
  final shopState = ref.watch(shopProvider).valueOrNull;
  if (shopState == null) return [];

  final query = shopState.searchQuery.toLowerCase();
  if (query.isEmpty) return shopState.products;

  return shopState.products.where((p) {
    return p.title.toLowerCase().contains(query) || p.description.toLowerCase().contains(query);
  }).toList();
});
