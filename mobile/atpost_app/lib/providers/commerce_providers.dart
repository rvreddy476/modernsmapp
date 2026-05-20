// Commerce Riverpod providers — Sprint 1 of mobile commerce parity.
//
// Conventions follow the Pulse providers (`pulse_providers.dart`):
//   * `FutureProvider.autoDispose.family` for parametric reads.
//   * `StateNotifier` for mutations + invalidation.
//   * Cache-on-failure isn't done here — commerce reads are short-lived
//     enough that a transient failure resurfaces as a snackbar; cart reads
//     rebuild on every screen open.
//
// The `cartProvider` is a StateNotifier (not a FutureProvider) because cart
// mutations (add/update/remove) want optimistic local state while the
// backend round-trip is in flight.

import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/services/commerce_telemetry.dart';
// `flutter/foundation` exports a `Category` annotation that clashes with
// our `commerce.Category` model — only pull in the bits we actually use.
import 'package:flutter/foundation.dart' show immutable;
import 'package:flutter_riverpod/flutter_riverpod.dart';

// ─── Catalog ────────────────────────────────────────────────────────────

/// Top-level category list (flat — UI groups by `parentId`).
final categoriesProvider = FutureProvider<List<Category>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getCategories();
});

/// Query params for the global product browse. Identity-equal queries dedupe
/// in Riverpod's family cache.
@immutable
class ProductsQuery {
  const ProductsQuery({this.categoryId, this.q, this.sellerId, this.cursor});

  final String? categoryId;
  final String? q;
  final String? sellerId;
  final String? cursor;

  @override
  bool operator ==(Object other) {
    return other is ProductsQuery &&
        other.categoryId == categoryId &&
        other.q == q &&
        other.sellerId == sellerId &&
        other.cursor == cursor;
  }

  @override
  int get hashCode => Object.hash(categoryId, q, sellerId, cursor);
}

/// Paginated product list. `autoDispose.family` keys on the query — the
/// catalog screen invalidates the previous family entry on category change.
final productsProvider = FutureProvider.autoDispose
    .family<ProductPage, ProductsQuery>((ref, query) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listProducts(
    categoryId: query.categoryId,
    q: query.q,
    sellerId: query.sellerId,
    cursor: query.cursor,
  );
});

/// Full product detail, including variants. Keyed on product id.
final productProvider =
    FutureProvider.autoDispose.family<Product, String>((ref, id) async {
  final repo = ref.watch(commerceRepositoryProvider);
  final p = await repo.getProduct(id);
  // Fire-and-forget telemetry. Read happens once per family hit; remount
  // counts as a new view which matches the user's perception.
  ref.read(commerceTelemetryProvider).productViewed(productId: id);
  return p;
});

/// Reviews for a product. Limited to the first page; "see all" route can
/// page directly off the repository.
final productReviewsProvider = FutureProvider.autoDispose
    .family<List<ProductReview>, String>((ref, productId) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getProductReviews(productId);
});

// ─── Cart ───────────────────────────────────────────────────────────────

/// Cart state notifier. Holds an `AsyncValue<Cart>` so screens can render
/// loading / error / data uniformly while still performing optimistic
/// updates inside the notifier.
class CartNotifier extends StateNotifier<AsyncValue<Cart>> {
  CartNotifier(this._repo, this._telemetry) : super(const AsyncValue.loading()) {
    _refresh();
  }

  final CommerceRepository _repo;
  final CommerceTelemetry _telemetry;

  Future<void> _refresh() async {
    try {
      final cart = await _repo.getCart();
      if (mounted) state = AsyncValue.data(cart);
    } catch (e, st) {
      if (mounted) state = AsyncValue.error(e, st);
    }
  }

  /// Public hook so the cart screen can pull-to-refresh.
  Future<void> refresh() => _refresh();

  Future<void> addToCart({
    required String productId,
    required String variantId,
    required int qty,
  }) async {
    await _repo.addToCart(
      productId: productId,
      variantId: variantId,
      qty: qty,
    );
    _telemetry.addToCart(
      productId: productId,
      variantId: variantId,
      qty: qty,
    );
    await _refresh();
  }

  /// `itemId` here is the variant id (see `commerce_repository.dart`).
  Future<void> updateItem(String itemId, int qty, {String? productId}) async {
    await _repo.updateCartItem(itemId, qty, productId: productId);
    await _refresh();
  }

  Future<void> removeItem(String itemId) async {
    await _repo.removeCartItem(itemId);
    await _refresh();
  }
}

final cartProvider =
    StateNotifierProvider<CartNotifier, AsyncValue<Cart>>((ref) {
  final repo = ref.watch(commerceRepositoryProvider);
  final telemetry = ref.watch(commerceTelemetryProvider);
  return CartNotifier(repo, telemetry);
});

// ─── Addresses ──────────────────────────────────────────────────────────

final addressesProvider = FutureProvider<List<Address>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getAddresses();
});

/// Default address picked for checkout. Falls back to the first address
/// when no `is_default` flag is set server-side.
final defaultAddressProvider = Provider<Address?>((ref) {
  final addrs = ref.watch(addressesProvider).asData?.value ?? const <Address>[];
  if (addrs.isEmpty) return null;
  for (final a in addrs) {
    if (a.isDefault) return a;
  }
  return addrs.first;
});

// ─── Pincode serviceability ─────────────────────────────────────────────

final pincodeServiceabilityProvider = FutureProvider.autoDispose
    .family<PincodeServiceability, String>((ref, pincode) async {
  final repo = ref.watch(commerceRepositoryProvider);
  final result = await repo.checkPincodeServiceability(pincode);
  ref
      .read(commerceTelemetryProvider)
      .pincodeChecked(pincode: pincode, deliverable: result.deliverable);
  return result;
});

// ─── Orders (Sprint 2) ──────────────────────────────────────────────────

/// Light-weight order list for `MyOrdersScreen`. AutoDisposes when the user
/// navigates away — the screen invalidates this provider on pull-to-refresh
/// and after order-cancel / return-create mutations. Returns the first
/// page only; the dedicated paginated provider lives in the screen.
final myOrdersProvider =
    FutureProvider.autoDispose<List<OrderListItem>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  final page = await repo.getMyOrders();
  return page.items;
});

/// Full order detail (with items + shipments). Keyed on order id.
final orderDetailProvider =
    FutureProvider.autoDispose.family<Order, String>((ref, id) async {
  final repo = ref.watch(commerceRepositoryProvider);
  final order = await repo.getOrder(id);
  ref.read(commerceTelemetryProvider).orderViewed(orderId: id);
  return order;
});

/// Live shipment + tracking events for an order. Order detail polls this
/// every 60s while the order is shipped/out-for-delivery (see Sprint 2 §6).
final orderShipmentProvider =
    FutureProvider.autoDispose.family<Shipment, String>((ref, orderId) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getOrderShipment(orderId);
});

// ─── Returns (Sprint 2) ─────────────────────────────────────────────────

final myReturnsProvider =
    FutureProvider.autoDispose<List<ReturnRequest>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getMyReturns();
});

// ─── Wishlist (Sprint 2) ────────────────────────────────────────────────

class WishlistNotifier
    extends StateNotifier<AsyncValue<List<WishlistItem>>> {
  WishlistNotifier(this._repo, this._telemetry)
      : super(const AsyncValue.loading()) {
    _refresh();
  }

  final CommerceRepository _repo;
  final CommerceTelemetry _telemetry;

  Future<void> _refresh() async {
    try {
      final list = await _repo.getWishlist();
      if (mounted) state = AsyncValue.data(list);
    } catch (e, st) {
      if (mounted) state = AsyncValue.error(e, st);
    }
  }

  Future<void> refresh() => _refresh();

  /// Quick membership check used by the heart icon. O(n) over the cached
  /// list which is small (typically < 100 items).
  bool contains(String productId) {
    final list = state.asData?.value ?? const <WishlistItem>[];
    for (final w in list) {
      if (w.productId == productId) return true;
    }
    return false;
  }

  /// Adds a product. Optimistic — local state flips first, server in
  /// flight. On failure we re-fetch to reconcile.
  Future<void> add(String productId, {WishlistItemSnapshot? snapshot}) async {
    final previous = state.asData?.value ?? const <WishlistItem>[];
    if (contains(productId)) return;
    final optimistic = [
      WishlistItem(
        productId: productId,
        savedAt: DateTime.now(),
        productSnapshot: snapshot ??
            const WishlistItemSnapshot(
              title: 'Saved item',
              sellingPrice: 0,
            ),
      ),
      ...previous,
    ];
    state = AsyncValue.data(optimistic);
    _telemetry.wishlistAdded(productId: productId);
    try {
      await _repo.addToWishlist(productId);
    } catch (e, st) {
      state = AsyncValue.data(previous);
      state = AsyncValue.error(e, st);
      await _refresh();
    }
  }

  /// Removes a product. Returns the removed item so callers can offer
  /// "Undo".
  Future<WishlistItem?> remove(String productId) async {
    final previous = state.asData?.value ?? const <WishlistItem>[];
    WishlistItem? removed;
    final next = <WishlistItem>[];
    for (final w in previous) {
      if (w.productId == productId) {
        removed = w;
      } else {
        next.add(w);
      }
    }
    state = AsyncValue.data(next);
    _telemetry.wishlistRemoved(productId: productId);
    try {
      await _repo.removeFromWishlist(productId);
    } catch (e, st) {
      state = AsyncValue.data(previous);
      state = AsyncValue.error(e, st);
      await _refresh();
    }
    return removed;
  }

  /// Toggle helper for the heart button.
  Future<void> toggle(String productId, {WishlistItemSnapshot? snapshot}) async {
    if (contains(productId)) {
      await remove(productId);
    } else {
      await add(productId, snapshot: snapshot);
    }
  }
}

final wishlistProvider = StateNotifierProvider<WishlistNotifier,
    AsyncValue<List<WishlistItem>>>((ref) {
  final repo = ref.watch(commerceRepositoryProvider);
  final telemetry = ref.watch(commerceTelemetryProvider);
  return WishlistNotifier(repo, telemetry);
});

// ─── Search (Sprint 2) ──────────────────────────────────────────────────

@immutable
class SearchQuery {
  const SearchQuery({
    this.q,
    this.filters = const SearchFilters(),
    this.sort = SearchSort.relevance,
    this.cursor,
  });

  final String? q;
  final SearchFilters filters;
  final SearchSort sort;
  final String? cursor;

  SearchQuery copyWith({
    Object? q = _unset,
    SearchFilters? filters,
    SearchSort? sort,
    Object? cursor = _unset,
  }) {
    return SearchQuery(
      q: identical(q, _unset) ? this.q : q as String?,
      filters: filters ?? this.filters,
      sort: sort ?? this.sort,
      cursor: identical(cursor, _unset) ? this.cursor : cursor as String?,
    );
  }

  static const _unset = Object();

  @override
  bool operator ==(Object other) {
    return other is SearchQuery &&
        other.q == q &&
        other.filters == filters &&
        other.sort == sort &&
        other.cursor == cursor;
  }

  @override
  int get hashCode => Object.hash(q, filters, sort, cursor);
}

final productSearchProvider = FutureProvider.autoDispose
    .family<ProductPage, SearchQuery>((ref, query) async {
  final repo = ref.watch(commerceRepositoryProvider);
  final page = await repo.searchProducts(
    q: query.q,
    filters: query.filters,
    sort: query.sort,
    cursor: query.cursor,
  );
  ref.read(commerceTelemetryProvider).searchPerformed(
        query: query.q ?? '',
        resultCount: page.items.length,
      );
  if (!query.filters.isEmpty) {
    ref
        .read(commerceTelemetryProvider)
        .filterApplied(filterCount: query.filters.appliedCount);
  }
  return page;
});

// ─── Recommendations (Sprint 2) ─────────────────────────────────────────

final recommendationsProvider =
    FutureProvider.autoDispose<List<Product>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getRecommendations();
});
