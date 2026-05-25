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

// ─── Seller dashboard ────────────────────────────────────────────────

/// mySellerProfileProvider — caller's seller profile. null when the
/// user hasn't onboarded as a seller yet; the dashboard renders an
/// "onboard first" CTA in that case. autoDispose so we don't hold the
/// profile in memory when the user leaves the seller section.
final mySellerProfileProvider = FutureProvider.autoDispose<SellerProfile?>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getMySellerProfile();
});

/// sellerDashboardProvider — stats for the seller home screen. Refreshed
/// each time the screen is entered (autoDispose + watched via the
/// invalidate call in the screen's RefreshIndicator).
final sellerDashboardProvider = FutureProvider.autoDispose<SellerDashboardStats>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.getSellerDashboard();
});

/// mySellerProductsProvider — the seller's own catalog. autoDispose so
/// pull-to-refresh just re-fires the family. No cursor today; if the
/// list grows past 50 a follow-up should add pagination.
final mySellerProductsProvider = FutureProvider.autoDispose<List<SellerProductSummary>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listMyProducts();
});

/// productVariantsProvider — full variant list for one product, used
/// by the seller's variants management screen. Family on productId so
/// each product's variants are cached independently.
final productVariantsProvider = FutureProvider.autoDispose
    .family<List<ProductVariantDetail>, String>((ref, productId) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listProductVariants(productId);
});

/// sellerOrdersProvider — seller fulfillment queue, family on the
/// stage filter (all / unshipped / in_transit / delivered / cancelled).
final sellerOrdersProvider = FutureProvider.autoDispose
    .family<List<SellerOrderCard>, String>((ref, stage) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listSellerOrders(stage: stage);
});

/// sellerReturnsProvider — seller returns inbox.
final sellerReturnsProvider = FutureProvider.autoDispose<List<SellerReturnCard>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listSellerReturns();
});

/// sellerEarningsProvider — delivered prepaid items payout ledger.
final sellerEarningsProvider = FutureProvider.autoDispose<List<SellerEarning>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listSellerEarnings();
});

/// sellerCODRemittancesProvider — COD payout ledger. Family on status
/// filter (empty = all).
final sellerCODRemittancesProvider = FutureProvider.autoDispose
    .family<List<CODRemittance>, String>((ref, status) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listCODRemittances(status: status.isEmpty ? null : status);
});

/// bulkImportJobsProvider — seller's bulk SKU import jobs. Mobile is
/// monitor + execute only; uploads happen on web.
final bulkImportJobsProvider =
    FutureProvider.autoDispose<List<BulkImportJob>>((ref) async {
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.listBulkImportJobs();
});

// couponPreviewProvider — read-only preview of what `code` would do to
// the current cart total. autoDispose + family so it requeries when
// the user edits the code input, and clears when the cart screen
// unmounts. The actual coupon application still happens at checkout.
final couponPreviewProvider = FutureProvider.autoDispose
    .family<CouponPreview, String>((ref, code) async {
  if (code.trim().isEmpty) {
    return const CouponPreview(
      couponCode: '',
      couponDiscount: 0,
      subtotal: 0,
      grandTotal: 0,
      applied: false,
    );
  }
  final repo = ref.watch(commerceRepositoryProvider);
  return repo.previewCoupon(code.trim());
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
