// Commerce repository — Sprint 1 of mobile commerce parity.
//
// Wraps the production `commerce-service` HTTP surface at `/v1/commerce/*`.
// The contract mirrors `postbook-ui/src/hooks/useCommerce.ts`. Where the
// brief asks for a method that the backend doesn't expose yet (cursor-based
// product list, item-id-keyed cart mutations, dedicated checkout-quote /
// pincode endpoints), we adapt to what's there and surface a stable Dart
// API to upstream callers.

import 'dart:typed_data';

import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CommerceRepository {
  CommerceRepository(this._api);

  final ApiClient _api;

  // ─── Catalog ──────────────────────────────────────────────────────

  /// Returns the category tree. Backend currently returns a flat list; the
  /// caller flattens it for chips and we leave the tree-build to the UI.
  Future<List<Category>> getCategories() async {
    final res = await _api.get('/v1/commerce/categories');
    final raw = (res.data['data'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => Category.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// Lists products with optional category / search / seller filters.
  /// Cursor pagination — the backend keyset query stays O(log n)
  /// regardless of page depth, so an infinite-scroll session is
  /// cheap even at celebrity catalog sizes. Pass `cursor: null` for
  /// page one; pass the previous response's `nextCursor` for each
  /// subsequent page.
  ///
  /// Seller-scoped browse still uses the legacy offset endpoint
  /// (`/sellers/:id/products`) for now — there's no cursor surface
  /// on that path. We stringify offset into the cursor so callers
  /// don't need a separate idiom.
  Future<ProductPage> listProducts({
    String? categoryId,
    String? q,
    String? sellerId,
    int limit = 20,
    String? cursor,
  }) async {
    // Seller-scoped browse uses a different endpoint.
    if (sellerId != null && sellerId.isNotEmpty) {
      final offset = int.tryParse(cursor ?? '') ?? 0;
      final res = await _api.get(
        '/v1/commerce/sellers/$sellerId/products',
        queryParameters: {'limit': limit, 'offset': offset},
      );
      final list = (res.data['data'] as List?) ?? const [];
      final items = list
          .whereType<Map>()
          .map((m) => Product.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false);
      return ProductPage(
        items: items,
        total: items.length,
        limit: limit,
        offset: offset,
      );
    }

    final params = <String, dynamic>{'limit': limit};
    if (categoryId != null && categoryId.isNotEmpty) {
      params['category'] = categoryId;
    }
    if (q != null && q.isNotEmpty) params['q'] = q;
    // Cursor mode: empty/null cursor on first page hints
    // paginate=cursor to the backend so it returns a next_cursor.
    if (cursor != null && cursor.isNotEmpty) {
      params['cursor'] = cursor;
    } else {
      params['paginate'] = 'cursor';
    }

    final res = await _api.get('/v1/commerce/products', queryParameters: params);
    final data = res.data['data'];
    if (data is List) {
      final items = data
          .whereType<Map>()
          .map((m) => Product.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false);
      return ProductPage(
        items: items,
        total: items.length,
        limit: limit,
        offset: 0,
      );
    }
    return ProductPage.fromJson(Map<String, dynamic>.from(data as Map));
  }

  /// Single product with variants. Backend wraps as `{product, variants}`;
  /// `Product.fromJson` is shape-tolerant so callers see one consistent type.
  Future<Product> getProduct(String id) async {
    final res = await _api.get('/v1/commerce/products/$id');
    return Product.fromJson(Map<String, dynamic>.from(res.data['data'] as Map));
  }

  /// Reviews for a product. Backend pages with offset; we forward the cursor
  /// as offset (same convention as `listProducts`).
  Future<List<ProductReview>> getProductReviews(
    String productId, {
    int limit = 20,
    String? cursor,
  }) async {
    final offset = int.tryParse(cursor ?? '') ?? 0;
    final res = await _api.get(
      '/v1/commerce/products/$productId/reviews',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final raw = (res.data['data']?['reviews'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => ProductReview.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── Cart ─────────────────────────────────────────────────────────

  /// Returns the user's cart, creating an empty one server-side if needed.
  Future<Cart> getCart() async {
    final res = await _api.get('/v1/commerce/cart');
    final data = res.data['data'];
    if (data == null) return Cart.empty();
    return Cart.fromJson(Map<String, dynamic>.from(data as Map));
  }

  /// previewCoupon returns the discount + new totals if `code` were
  /// applied to the current cart. Pure preview — no DB write. The
  /// actual application happens at checkout. Backend endpoint:
  /// GET /v1/commerce/cart/coupon-preview?code=XYZ.
  Future<CouponPreview> previewCoupon(String code) async {
    final res = await _api.get(
      '/v1/commerce/cart/coupon-preview',
      queryParameters: {'code': code},
    );
    final data = Map<String, dynamic>.from(res.data['data'] as Map);
    return CouponPreview.fromJson(data);
  }

  /// Adds a variant to the cart. `productId` is unused on the wire — backend
  /// derives it from the variant — but we keep it in the API so the call site
  /// reads naturally and we can switch to a product-id wire if needed.
  Future<void> addToCart({
    required String productId,
    required String variantId,
    required int qty,
  }) async {
    await _api.post(
      '/v1/commerce/cart/items',
      data: {
        'product_id': productId,
        'variant_id': variantId,
        'quantity': qty,
      },
    );
  }

  /// Sets a cart item's quantity. Backend has no "patch by item id" route
  /// today — the existing path keys on `variant_id`. We expose an
  /// `updateCartItem(itemId, qty)` to match the brief but the underlying
  /// caller passes the variant id and we forward it. If a real item-id PATCH
  /// route ships later, only this method changes.
  ///
  /// Implementation note: we delete + re-add to achieve a quantity change.
  /// The handler doesn't currently expose a quantity update endpoint.
  Future<void> updateCartItem(String variantId, int qty, {String? productId}) async {
    if (qty <= 0) {
      await removeCartItem(variantId);
      return;
    }
    await _api.delete('/v1/commerce/cart/items/$variantId');
    await _api.post(
      '/v1/commerce/cart/items',
      data: {
        'product_id': ?productId,
        'variant_id': variantId,
        'quantity': qty,
      },
    );
  }

  /// Removes a cart item. The wire id is the variant id (see note above).
  Future<void> removeCartItem(String variantId) async {
    await _api.delete('/v1/commerce/cart/items/$variantId');
  }

  // ─── Addresses ────────────────────────────────────────────────────

  Future<List<Address>> getAddresses() async {
    final res = await _api.get('/v1/commerce/addresses');
    final raw = (res.data['data'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => Address.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  Future<Address> createAddress(Address addr) async {
    final res = await _api.post(
      '/v1/commerce/addresses',
      data: addr.toCreateJson(),
    );
    return Address.fromJson(Map<String, dynamic>.from(res.data['data'] as Map));
  }

  Future<void> updateAddress(String id, Address addr) async {
    await _api.patch('/v1/commerce/addresses/$id', data: addr.toCreateJson());
  }

  Future<void> deleteAddress(String id) async {
    await _api.delete('/v1/commerce/addresses/$id');
  }

  Future<void> setDefaultAddress(String id) async {
    await _api.post('/v1/commerce/addresses/$id/default');
  }

  // ─── Checkout & orders ───────────────────────────────────────────

  /// Pre-checkout quote. Backend has no dedicated endpoint yet — we read the
  /// cart and project from there. The shape matches the future server one
  /// (subtotal/tax/shipping/discount/grand) so screens can swap callers.
  Future<CheckoutQuote> checkoutQuote({
    required String addressId,
    required String paymentMethod,
  }) async {
    final cart = await getCart();
    return CheckoutQuote.fromCart(
      cart: cart,
      addressId: addressId,
      paymentMethod: paymentMethod,
    );
  }

  /// Places an order from the current cart against the chosen address +
  /// payment method. Returns the freshly created order; for prepaid methods
  /// the next step is `confirmOrderPayment` (after the gateway succeeds).
  Future<Order> placeOrder({
    required String addressId,
    required String paymentMethod,
    String? couponCode,
    String? idempotencyKey,
    // Phase F4 mobile — optional B2B context. organizationId routes
    // the order through the Phase 5 approval / credit-terms paths;
    // PO / cost-center / invoice-email are stamped on the order for
    // finance reconciliation downstream.
    String? organizationId,
    String? poNumber,
    String? costCenter,
    String? invoiceEmail,
  }) async {
    final res = await _api.post(
      '/v1/commerce/orders/checkout',
      data: {
        'address_id': addressId,
        'payment_method': paymentMethod,
        if (couponCode != null && couponCode.isNotEmpty)
          'coupon_code': couponCode,
        'idempotency_key': ?idempotencyKey,
        if (organizationId != null && organizationId.isNotEmpty)
          'organization_id': organizationId,
        if (poNumber != null && poNumber.isNotEmpty) 'po_number': poNumber,
        if (costCenter != null && costCenter.isNotEmpty)
          'cost_center': costCenter,
        if (invoiceEmail != null && invoiceEmail.isNotEmpty)
          'invoice_email': invoiceEmail,
      },
    );
    return Order.fromJson(Map<String, dynamic>.from(res.data['data'] as Map));
  }

  /// Fetches an order with items + shipment data. Uses the `/items` route so
  /// the order detail screen renders cards immediately instead of a second
  /// round-trip.
  Future<Order> getOrder(String orderId) async {
    final res = await _api.get('/v1/commerce/orders/$orderId/items');
    return Order.fromJson(Map<String, dynamic>.from(res.data['data'] as Map));
  }

  /// Creates a payments-service intent for the order. Returns the intent
  /// id (used by Razorpay verification) and the provider_ref (the Razorpay
  /// gateway order id the SDK consumes). Phase 1.4.
  Future<({String intentId, String providerRef, double amount})>
  createPaymentIntent({
    required String orderId,
    required double amount,
    String method = 'razorpay',
  }) async {
    final res = await _api.post(
      '/v1/payments/intents',
      data: {
        'payee_id': orderId,
        'reference_type': 'order',
        'reference_id': orderId,
        'amount': amount,
        'currency': 'INR',
        'method': method,
      },
    );
    final data = Map<String, dynamic>.from(res.data['data'] as Map);
    return (
      intentId: (data['id'] ?? '').toString(),
      providerRef: (data['provider_ref'] ?? '').toString(),
      amount: (data['amount'] as num?)?.toDouble() ?? amount,
    );
  }

  /// Confirms a successful Razorpay payment with commerce-service using the
  /// Phase-0.1 secure body shape. The backend forwards the signature triple
  /// to payments-service for HMAC verification before marking the order
  /// paid; the Kafka payment.succeeded consumer remains the resilient
  /// backup path if the app dies before this returns.
  Future<void> confirmOrderPayment(
    String orderId, {
    required String paymentIntentId,
    required String razorpayOrderId,
    required String razorpayPaymentId,
    required String razorpaySignature,
    required int amountMinor,
    String gateway = 'razorpay',
  }) async {
    await _api.post(
      '/v1/commerce/orders/$orderId/payment/confirm',
      data: {
        'payment_intent_id': paymentIntentId,
        'razorpay_order_id': razorpayOrderId,
        'razorpay_payment_id': razorpayPaymentId,
        'razorpay_signature': razorpaySignature,
        'amount_minor': amountMinor,
        'gateway': gateway,
      },
    );
  }

  Future<void> cancelOrder(String orderId, String reason) async {
    await _api.post(
      '/v1/commerce/orders/$orderId/cancel',
      data: {'reason': reason},
    );
  }

  // ─── Pincode serviceability ───────────────────────────────────────

  /// Backend doesn't expose a pincode endpoint yet (see COMMERCE_RECON §F.2).
  /// We use a deterministic heuristic on the first 3 digits so the UI is
  /// fully wired today and Sprint 2 can swap in the real endpoint without a
  /// screen change.
  ///
  /// Heuristic — pincodes starting with 1xx (Delhi NCR), 4xx (West/Mumbai),
  /// 5xx (South), 7xx (East) are 2–4 day metros; everything else gets 5–7.
  /// Pincodes that don't parse to a 6-digit number return `deliverable=false`.
  Future<PincodeServiceability> checkPincodeServiceability(
    String pincode, {
    String? productId,
  }) async {
    final clean = pincode.trim();
    final n = int.tryParse(clean);
    if (n == null || clean.length != 6) {
      return const PincodeServiceability(
        deliverable: false,
        etaDays: 0,
        message: 'Enter a valid 6-digit pincode',
      );
    }
    final first = clean[0];
    int eta;
    switch (first) {
      case '1':
      case '4':
        eta = 3;
        break;
      case '5':
      case '6':
      case '7':
        eta = 4;
        break;
      case '2':
      case '3':
        eta = 5;
        break;
      default:
        eta = 6;
    }
    return PincodeServiceability(
      deliverable: true,
      etaDays: eta,
      message: 'Usually delivered in $eta days',
    );
  }

  // ─── Orders list (Sprint 2) ───────────────────────────────────────

  /// Page of light-weight order summaries for the "My orders" screen.
  /// Backend route: `GET /v1/commerce/orders?limit=&cursor=`. Phase 2.1
  /// switched the customer order list to keyset cursors — the previous
  /// offset path triggered a full-table COUNT(*) on every page. The
  /// response now also carries item/seller counts + the first item's
  /// product so the customer order-list screen can render rich cards.
  ///
  /// Returns the page + the opaque next-page cursor (empty on last page).
  Future<({List<OrderListItem> items, String nextCursor})> getMyOrders({
    int limit = 20,
    String? cursor,
  }) async {
    final res = await _api.get(
      '/v1/commerce/orders',
      queryParameters: {
        'limit': limit,
        if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
      },
    );
    final data = res.data['data'];
    final raw = data is List
        ? data
        : (data is Map ? (data['items'] as List? ?? const []) : const []);
    final items = raw
        .whereType<Map>()
        .map((m) => OrderListItem.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
    final meta = res.data['meta'];
    final next = (meta is Map ? meta['next_cursor'] : null)?.toString() ?? '';
    return (items: items, nextCursor: next);
  }

  // ─── Shipment / live tracking (Sprint 2) ──────────────────────────

  /// Returns the latest shipment (with tracking events) for the order.
  /// Backend route: `GET /v1/commerce/orders/:id/shipment`. Multi-seller
  /// orders return one shipment here — use the `/shipments` plural route
  /// for the full set if needed.
  Future<Shipment> getOrderShipment(String orderId) async {
    final res = await _api.get('/v1/commerce/orders/$orderId/shipment');
    final data = Map<String, dynamic>.from(res.data['data'] as Map);
    final shipMap = (data['shipment'] is Map)
        ? Map<String, dynamic>.from(data['shipment'] as Map)
        : data;
    final events = (data['events'] as List?) ?? const [];
    if (events.isNotEmpty && shipMap['events'] == null) {
      shipMap['events'] = events;
    }
    return Shipment.fromJson(shipMap);
  }

  // ─── Returns (Sprint 2) ───────────────────────────────────────────

  /// Submits a multi-item return in a single backend call. Phase 2.3
  /// replaced the N-call fan-out with a bulk `{items:[...]}` endpoint —
  /// the previous loop here lost atomicity (partial successes were
  /// invisible to the caller). Returns the first return request created
  /// (mirrors the prior contract); the others are persisted server-side.
  Future<ReturnRequest> requestReturn({
    required String orderId,
    required List<ReturnItem> items,
    required String pickupAddressId,
    required String sellerId,
    String? reasonDescription,
  }) async {
    if (items.isEmpty) {
      throw ArgumentError('requestReturn requires at least one item');
    }
    final res = await _api.post(
      '/v1/commerce/orders/$orderId/returns',
      data: {
        'pickup_address_id': pickupAddressId,
        'items': [
          for (final item in items)
            {
              'order_item_id': item.orderItemId,
              'seller_id': sellerId,
              'reason_code': item.reason.wireValue,
              'quantity': item.qty,
              if (reasonDescription != null && reasonDescription.isNotEmpty)
                'reason_description': reasonDescription,
            },
        ],
      },
    );
    final body = res.data['data'];
    // Backend may answer with `{items:[...]}` (multi-item path) or a bare
    // single item when only one was requested — handle both.
    if (body is Map && body['items'] is List) {
      final list = body['items'] as List;
      if (list.isEmpty) throw StateError('empty return response');
      return ReturnRequest.fromJson(Map<String, dynamic>.from(list.first as Map));
    }
    return ReturnRequest.fromJson(Map<String, dynamic>.from(body as Map));
  }

  /// Fetches a single return — Phase 2.2 detail endpoint. Replaces the
  /// previous "list /me/returns and find the one I want" workaround.
  Future<ReturnRequest> getReturn(String returnId) async {
    final res = await _api.get('/v1/commerce/returns/$returnId');
    return ReturnRequest.fromJson(Map<String, dynamic>.from(res.data['data'] as Map));
  }

  /// Backend route: `GET /v1/commerce/me/returns`.
  Future<List<ReturnRequest>> getMyReturns({
    int limit = 20,
    String? cursor,
  }) async {
    final offset = int.tryParse(cursor ?? '') ?? 0;
    final res = await _api.get(
      '/v1/commerce/me/returns',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final data = res.data['data'];
    final raw = data is List
        ? data
        : (data is Map ? (data['items'] as List? ?? const []) : const []);
    return raw
        .whereType<Map>()
        .map((m) => ReturnRequest.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── Invoice download (Sprint 2) ──────────────────────────────────

  /// Returns the invoice PDF bytes for a paid order. Backend route:
  /// `GET /v1/commerce/orders/:id/invoice` returns a JSON envelope
  /// `{invoice, download_url}` (see `commerce-service/.../shipments.go#GetInvoice`)
  /// rather than the raw PDF. We hit it as JSON, then follow the
  /// `download_url` with a fresh Dio (no auth interceptors) to fetch
  /// the bytes so the caller can `Share` / `Save`.
  Future<Uint8List> getOrderInvoice(String orderId) async {
    final res = await _api.get('/v1/commerce/orders/$orderId/invoice');
    final data = res.data is Map ? res.data['data'] : null;
    if (data is Map) {
      final url = data['download_url'];
      if (url is String && url.isNotEmpty) {
        final dl = Dio();
        final got = await dl.get<List<int>>(
          url,
          options: Options(responseType: ResponseType.bytes),
        );
        return Uint8List.fromList(got.data ?? const []);
      }
    }
    return Uint8List(0);
  }

  // ─── Reviews (Sprint 2) ───────────────────────────────────────────

  /// Submits a product review. Backend `POST /v1/commerce/products/:id/reviews`
  /// requires `{seller_id, order_item_id, rating, title, body}`; the brief
  /// asks for `{rating, title, body}` only. Callers must provide the
  /// seller + order-item ids — usually sourced from the order detail screen
  /// where the "Rate this product" button lives.
  Future<ProductReview> submitProductReview(
    String productId, {
    required int rating,
    required String sellerId,
    required String orderItemId,
    String? title,
    String? body,
  }) async {
    final res = await _api.post(
      '/v1/commerce/products/$productId/reviews',
      data: {
        'seller_id': sellerId,
        'order_item_id': orderItemId,
        'rating': rating,
        if (title != null && title.isNotEmpty) 'title': title,
        if (body != null && body.isNotEmpty) 'body': body,
      },
    );
    return ProductReview.fromJson(Map<String, dynamic>.from(res.data['data'] as Map));
  }

  // ─── Wishlist (Sprint 2) ──────────────────────────────────────────

  /// Backend doesn't expose a wishlist endpoint on commerce-service today
  /// (only the legacy `/v1/shop/wishlist` exists; see COMMERCE_RECON §B.2).
  /// We target the canonical `/v1/commerce/wishlist` path the brief
  /// specifies — when commerce-service ships it, the call lights up
  /// without a screen change. The repo gracefully handles 404 by returning
  /// an empty list so the UI doesn't crash pre-cutover.
  Future<List<WishlistItem>> getWishlist() async {
    try {
      final res = await _api.get('/v1/commerce/wishlist');
      final data = res.data['data'];
      final raw = data is List
          ? data
          : (data is Map ? (data['items'] as List? ?? const []) : const []);
      return raw
          .whereType<Map>()
          .map((m) => WishlistItem.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false);
    } on DioException catch (e) {
      if (e.response?.statusCode == 404) return const [];
      rethrow;
    }
  }

  Future<void> addToWishlist(String productId) async {
    await _api.post(
      '/v1/commerce/wishlist',
      data: {'product_id': productId},
    );
  }

  Future<void> removeFromWishlist(String productId) async {
    await _api.delete('/v1/commerce/wishlist/$productId');
  }

  // ─── Search (Sprint 2) ────────────────────────────────────────────

  /// Faceted product search. Backend `GET /v1/commerce/products` today
  /// supports `q` + `category` only (see `handler.go#ListProducts`); the
  /// other filter / sort params are forwarded so they activate when the
  /// search backend ships. Client-side filtering of the response keeps the
  /// UX honest until then — see Sprint 3 task.
  Future<ProductPage> searchProducts({
    String? q,
    SearchFilters filters = const SearchFilters(),
    SearchSort sort = SearchSort.relevance,
    int limit = 20,
    String? cursor,
  }) async {
    final offset = int.tryParse(cursor ?? '') ?? 0;
    final params = <String, dynamic>{'limit': limit, 'offset': offset};
    if (q != null && q.isNotEmpty) params['q'] = q;
    if (filters.categoryIds.isNotEmpty) {
      // Backend honours a single category today; we send the first id and
      // attach the rest as a comma list under `categories` for forward
      // compatibility with a future multi-category endpoint.
      params['category'] = filters.categoryIds.first;
      if (filters.categoryIds.length > 1) {
        params['categories'] = filters.categoryIds.join(',');
      }
    }
    if (filters.brandIds.isNotEmpty) {
      params['brands'] = filters.brandIds.join(',');
    }
    if (filters.priceMin != null) params['price_min'] = filters.priceMin;
    if (filters.priceMax != null) params['price_max'] = filters.priceMax;
    if (filters.ratingMin != null) params['rating_min'] = filters.ratingMin;
    if (filters.hasFreeShipping) params['free_shipping'] = true;
    if (filters.hasCod) params['cod'] = true;
    if (sort != SearchSort.relevance) params['sort'] = sort.wireValue;

    final res = await _api.get('/v1/commerce/products', queryParameters: params);
    final data = res.data['data'];
    if (data is List) {
      final items = data
          .whereType<Map>()
          .map((m) => Product.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false);
      final filtered = _applyClientFilters(items, filters, sort);
      return ProductPage(
        items: filtered,
        total: filtered.length,
        limit: limit,
        offset: offset,
      );
    }
    final page = ProductPage.fromJson(Map<String, dynamic>.from(data as Map));
    final filtered = _applyClientFilters(page.items, filters, sort);
    return ProductPage(
      items: filtered,
      total: page.total,
      limit: page.limit,
      offset: page.offset,
    );
  }

  /// Applies filter + sort post-hoc so the UI behaves correctly even
  /// against a backend that doesn't yet honour the params. Removed once
  /// the search service ships.
  List<Product> _applyClientFilters(
    List<Product> input,
    SearchFilters filters,
    SearchSort sort,
  ) {
    Iterable<Product> out = input;
    if (filters.priceMin != null) {
      out = out.where((p) => p.basePrice >= filters.priceMin!);
    }
    if (filters.priceMax != null) {
      out = out.where((p) => p.basePrice <= filters.priceMax!);
    }
    if (filters.ratingMin != null) {
      out = out.where((p) => p.rating >= filters.ratingMin!);
    }
    if (filters.brandIds.isNotEmpty) {
      out = out.where(
        (p) => p.brandId != null && filters.brandIds.contains(p.brandId),
      );
    }
    final list = out.toList();
    switch (sort) {
      case SearchSort.priceLow:
        list.sort((a, b) => a.basePrice.compareTo(b.basePrice));
        break;
      case SearchSort.priceHigh:
        list.sort((a, b) => b.basePrice.compareTo(a.basePrice));
        break;
      case SearchSort.ratingDesc:
        list.sort((a, b) => b.rating.compareTo(a.rating));
        break;
      case SearchSort.newest:
        list.sort((a, b) => b.createdAt.compareTo(a.createdAt));
        break;
      case SearchSort.popularity:
        list.sort((a, b) => b.ratingCount.compareTo(a.ratingCount));
        break;
      case SearchSort.relevance:
        break;
    }
    return list;
  }

  // ─── Recommendations (Sprint 2 → Sprint 3) ────────────────────────

  /// "Recommended for you" carousel feed. Backend has no ranker yet
  /// (COMMERCE_RECON §I.12 — default is "Postgres view over `order_items`
  /// + view_count + wishlist_count"). For v1 we fall back to the trending
  /// browse — newest products in the catalogue — so the surface ships and
  /// Sprint 3 can swap in `suggestion-service` without a screen change.
  Future<List<Product>> getRecommendations({int limit = 10}) async {
    final res = await _api.get(
      '/v1/commerce/products',
      queryParameters: {'limit': limit, 'offset': 0},
    );
    final data = res.data['data'];
    final raw = data is List
        ? data
        : (data is Map ? (data['items'] as List? ?? const []) : const []);
    return raw
        .whereType<Map>()
        .map((m) => Product.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── Seller surfaces ────────────────────────────────────────────────

  /// Fetches the caller's seller profile. Returns null when the user
  /// hasn't completed onboarding — the dashboard screen renders an
  /// onboarding-required state in that case.
  Future<SellerProfile?> getMySellerProfile() async {
    try {
      final res = await _api.get('/v1/commerce/sellers/me');
      final data = res.data['data'];
      if (data == null) return null;
      return SellerProfile.fromJson(Map<String, dynamic>.from(data as Map));
    } catch (_) {
      // 404 from the server means "no seller account yet" — that's the
      // expected path for an unboarded user; treat as null.
      return null;
    }
  }

  /// Seller dashboard stats. The backend computes these per request so
  /// no caching is needed client-side; the auto-refresh on screen
  /// re-enter is sufficient.
  Future<SellerDashboardStats> getSellerDashboard() async {
    final res = await _api.get('/v1/commerce/dashboard');
    final data = res.data['data'];
    return SellerDashboardStats.fromJson(Map<String, dynamic>.from(data as Map));
  }

  /// Lists the seller's own products. Backend: GET
  /// /v1/commerce/sellers/:sellerId/products. We hydrate the seller's
  /// own ID via getMySellerProfile rather than making the caller pass
  /// it through every UI hop.
  Future<List<SellerProductSummary>> listMyProducts({int limit = 50, int offset = 0}) async {
    final profile = await getMySellerProfile();
    if (profile == null) return const [];
    final res = await _api.get(
      '/v1/commerce/sellers/${profile.id}/products',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final raw = (res.data['data'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => SellerProductSummary.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// Submits a draft product for review. Backend:
  /// POST /v1/commerce/products/:id/submit.
  Future<void> submitProductForReview(String productId) async {
    await _api.post('/v1/commerce/products/$productId/submit');
  }

  // ─── Variant CRUD (seller) ─────────────────────────────────────────

  Future<List<ProductVariantDetail>> listProductVariants(String productId) async {
    final res = await _api.get('/v1/commerce/products/$productId/variants');
    final raw = (res.data['data']?['items'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => ProductVariantDetail.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  Future<ProductVariantDetail> addProductVariant(
    String productId,
    CreateVariantInput input,
  ) async {
    final res = await _api.post(
      '/v1/commerce/products/$productId/variants',
      data: input.toJson(),
    );
    return ProductVariantDetail.fromJson(
      Map<String, dynamic>.from(res.data['data'] as Map),
    );
  }

  /// updateProductVariant sends a sparse PATCH — only the fields in
  /// `patch` are written server-side. SKU is intentionally not
  /// updatable (bulk-import merge key); attempting to set it is a
  /// no-op at the store layer.
  Future<ProductVariantDetail> updateProductVariant(
    String variantId,
    Map<String, dynamic> patch,
  ) async {
    final res = await _api.patch(
      '/v1/commerce/variants/$variantId',
      data: patch,
    );
    return ProductVariantDetail.fromJson(
      Map<String, dynamic>.from(res.data['data'] as Map),
    );
  }

  /// archiveProductVariant flips a variant to status='archived'. Soft
  /// delete — existing orders + cart_items keep resolving the variant
  /// by ID; customers just can't add new units.
  Future<void> archiveProductVariant(String variantId) async {
    await _api.delete('/v1/commerce/variants/$variantId');
  }

  // ─── Seller orders / returns / earnings ────────────────────────────

  /// Seller-facing order fulfillment list. Each card carries the
  /// seller's items, their shipment (if booked), and the order's
  /// status — enough to render a fulfillment queue without a second
  /// detail fetch.
  Future<List<SellerOrderCard>> listSellerOrders({
    String stage = 'all',
    int limit = 20,
    int offset = 0,
  }) async {
    final res = await _api.get(
      '/v1/commerce/seller/fulfillment',
      queryParameters: {'stage': stage, 'limit': limit, 'offset': offset},
    );
    final raw = (res.data['data']?['orders'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => SellerOrderCard.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// Seller returns inbox.
  Future<List<SellerReturnCard>> listSellerReturns({
    int limit = 20,
    int offset = 0,
  }) async {
    final res = await _api.get(
      '/v1/commerce/seller/returns',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final raw = (res.data['data'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => SellerReturnCard.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  /// Seller earnings ledger — prepaid (non-COD) delivered items with
  /// gross / commission / fee / TDS / net broken out. COD lives in
  /// /seller/cod-remittances.
  Future<List<SellerEarning>> listSellerEarnings({
    int limit = 50,
    int offset = 0,
  }) async {
    final res = await _api.get(
      '/v1/commerce/seller/earnings',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final raw = (res.data['data']?['earnings'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => SellerEarning.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  // ─── Bulk import (read + execute; upload stays web-only) ─────────

  /// Lists the seller's bulk import jobs newest-first. Mobile shows
  /// jobs created via the web flow so a seller can monitor + finalize
  /// from their phone; new uploads require the desktop file picker.
  Future<List<BulkImportJob>> listBulkImportJobs() async {
    final res = await _api.get('/v1/commerce/seller/bulk-import');
    final raw = (res.data['data']?['items'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => BulkImportJob.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }

  Future<BulkImportJob> getBulkImportJob(String jobId) async {
    final res = await _api.get('/v1/commerce/seller/bulk-import/$jobId');
    final data = res.data['data'];
    return BulkImportJob.fromJson(Map<String, dynamic>.from(data as Map));
  }

  /// Triggers the worker to upsert validated rows. Idempotent at the
  /// backend; calling after the job is already executed returns the
  /// existing row count rather than re-importing.
  Future<void> executeBulkImport(String jobId) async {
    await _api.post('/v1/commerce/seller/bulk-import/$jobId/execute');
  }

  /// COD remittance ledger — one row per COD shipment whose cash the
  /// courier has collected. Status pending → settled when Ops pays out.
  Future<List<CODRemittance>> listCODRemittances({
    String? status,
    int limit = 20,
    int offset = 0,
  }) async {
    final params = <String, dynamic>{'limit': limit, 'offset': offset};
    if (status != null && status.isNotEmpty) params['status'] = status;
    final res = await _api.get(
      '/v1/commerce/seller/cod-remittances',
      queryParameters: params,
    );
    final raw = (res.data['data']?['items'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => CODRemittance.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);
  }
}

final commerceRepositoryProvider = Provider<CommerceRepository>((ref) {
  return CommerceRepository(ref.watch(apiClientProvider));
});
