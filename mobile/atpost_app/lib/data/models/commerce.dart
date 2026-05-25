// Commerce models — Sprint 1 of mobile commerce parity.
//
// These model `/v1/commerce/*` (the production-grade commerce-service rebuild)
// rather than the legacy `/v1/shop/*` surface that `data/models/shop.dart`
// targets. Field shapes mirror `commerce-service/internal/store/postgres/
// models.go` and `postbook-ui/src/hooks/useCommerce.ts`.
//
// JSON keys are snake_case (matching the Go `json` tags); Dart fields are
// camelCase. All `fromJson` parsers are defensive — backend nulls, missing
// fields, type drift, and string/numeric coercion all degrade gracefully.

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:flutter/foundation.dart';

// ─── Resilience helpers ──────────────────────────────────────────────────

double _toDouble(dynamic v) {
  if (v == null) return 0.0;
  if (v is double) return v;
  if (v is int) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0.0;
  return 0.0;
}

int _toInt(dynamic v) {
  if (v == null) return 0;
  if (v is int) return v;
  if (v is double) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

bool _toBool(dynamic v) {
  if (v is bool) return v;
  if (v is String) return v.toLowerCase() == 'true';
  if (v is num) return v != 0;
  return false;
}

String _toStr(dynamic v, [String fallback = '']) {
  if (v == null) return fallback;
  return v.toString();
}

String? _toStrOrNull(dynamic v) {
  if (v == null) return null;
  final s = v.toString();
  return s.isEmpty ? null : s;
}

DateTime? _toDateOrNull(dynamic v) {
  if (v == null) return null;
  if (v is DateTime) return v;
  if (v is String) return DateTime.tryParse(v);
  return null;
}

DateTime _toDate(dynamic v) {
  return _toDateOrNull(v) ?? DateTime.now();
}

List<String> _toStringList(dynamic v) {
  if (v is List) {
    return v.map((e) => e?.toString() ?? '').where((s) => s.isNotEmpty).toList();
  }
  return const [];
}

Map<String, String> _toStringMap(dynamic v) {
  if (v is Map) {
    final out = <String, String>{};
    v.forEach((k, val) {
      if (k != null && val != null) out[k.toString()] = val.toString();
    });
    return out;
  }
  return const {};
}

// ─── Category ────────────────────────────────────────────────────────────

/// Category — backend `product_categories`. Tree shape: `parentId == null`
/// means top-level; `depth` is computed by the client when flattening.
class Category {
  const Category({
    required this.id,
    required this.slug,
    required this.name,
    this.parentId,
    this.depth = 0,
    this.imageUrl,
  });

  final String id;
  final String slug;
  final String name;
  final String? parentId;
  final int depth;
  final String? imageUrl;

  factory Category.fromJson(Map<String, dynamic> json) {
    try {
      return Category(
        id: _toStr(json['id']),
        slug: _toStr(json['slug']),
        name: _toStr(json['name'], 'Category'),
        parentId: _toStrOrNull(json['parent_id']),
        depth: _toInt(json['depth']),
        imageUrl: _toStrOrNull(json['image_url']),
      );
    } catch (e, st) {
      AppLogger.error('Category.fromJson failed', error: e, stackTrace: st);
      return const Category(id: '', slug: '', name: 'Unknown');
    }
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'slug': slug,
        'name': name,
        if (parentId != null) 'parent_id': parentId,
        'depth': depth,
        if (imageUrl != null) 'image_url': imageUrl,
      };
}

// ─── Product variant ─────────────────────────────────────────────────────

/// One sellable SKU under a Product. Backend stores up to 3 named option
/// pairs (`option_1_name`, `option_1_value`, …); the mobile app collapses
/// them into a `Map<String,String>` so screens can read `attributes['size']`
/// or iterate without thinking about the slot index.
class ProductVariant {
  const ProductVariant({
    required this.id,
    required this.productId,
    required this.sku,
    required this.mrp,
    required this.sellingPrice,
    required this.attributes,
    required this.stockQty,
    required this.isAvailable,
    this.imageUrl,
  });

  final String id;
  final String productId;
  final String sku;
  final double mrp;
  final double sellingPrice;
  final Map<String, String> attributes;
  final int stockQty;
  final bool isAvailable;
  final String? imageUrl;

  factory ProductVariant.fromJson(Map<String, dynamic> json) {
    final attrs = <String, String>{};
    for (var i = 1; i <= 3; i++) {
      final name = _toStrOrNull(json['option_${i}_name']);
      final value = _toStrOrNull(json['option_${i}_value']);
      if (name != null && value != null) attrs[name] = value;
    }
    final status = _toStr(json['status'], 'active');
    return ProductVariant(
      id: _toStr(json['id']),
      productId: _toStr(json['product_id']),
      sku: _toStr(json['sku']),
      mrp: _toDouble(json['mrp']),
      sellingPrice: _toDouble(json['selling_price']),
      attributes: attrs,
      stockQty: _toInt(json['stock_qty'] ?? json['available_qty']),
      isAvailable: status == 'active' && _toInt(json['stock_qty'] ?? 1) > 0,
      imageUrl: _toStrOrNull(json['image_url']),
    );
  }

  Map<String, dynamic> toJson() {
    final out = <String, dynamic>{
      'id': id,
      'product_id': productId,
      'sku': sku,
      'mrp': mrp,
      'selling_price': sellingPrice,
      'stock_qty': stockQty,
    };
    var slot = 1;
    attributes.forEach((k, v) {
      if (slot > 3) return;
      out['option_${slot}_name'] = k;
      out['option_${slot}_value'] = v;
      slot++;
    });
    if (imageUrl != null) out['image_url'] = imageUrl;
    return out;
  }

  /// Discount percentage rounded to nearest int. Returns `null` when MRP
  /// equals (or undercuts) the selling price — UI should skip the strike.
  int? get discountPct {
    if (mrp <= 0 || sellingPrice >= mrp) return null;
    return ((mrp - sellingPrice) / mrp * 100).round();
  }
}

// ─── Product review ──────────────────────────────────────────────────────

class ProductReview {
  const ProductReview({
    required this.id,
    required this.productId,
    required this.buyerId,
    required this.buyerName,
    required this.rating,
    required this.helpfulVotes,
    required this.createdAt,
    this.title,
    this.body,
  });

  final String id;
  final String productId;
  final String buyerId;
  final String buyerName;
  final int rating;
  final String? title;
  final String? body;
  final int helpfulVotes;
  final DateTime createdAt;

  factory ProductReview.fromJson(Map<String, dynamic> json) {
    return ProductReview(
      id: _toStr(json['id']),
      productId: _toStr(json['product_id']),
      buyerId: _toStr(json['reviewer_id'] ?? json['buyer_id']),
      buyerName: _toStr(json['buyer_name'] ?? json['reviewer_name'], 'Buyer'),
      rating: _toInt(json['rating']),
      title: _toStrOrNull(json['title']),
      body: _toStrOrNull(json['body']),
      helpfulVotes: _toInt(json['helpful_count'] ?? json['helpful_votes']),
      createdAt: _toDate(json['created_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'product_id': productId,
        'reviewer_id': buyerId,
        'buyer_name': buyerName,
        'rating': rating,
        if (title != null) 'title': title,
        if (body != null) 'body': body,
        'helpful_count': helpfulVotes,
        'created_at': createdAt.toIso8601String(),
      };
}

// ─── Product ─────────────────────────────────────────────────────────────

/// Variant-aware product. Replaces `data/models/shop.dart#Product` for the
/// `/v1/commerce` surface. The legacy model stays for `/v1/shop/*` callers.
class Product {
  const Product({
    required this.id,
    required this.sellerId,
    required this.title,
    required this.slug,
    required this.productType,
    required this.status,
    required this.basePrice,
    required this.currency,
    required this.mrp,
    required this.taxRatePct,
    required this.isReturnable,
    required this.returnWindowDays,
    required this.rating,
    required this.ratingCount,
    required this.imageUrls,
    required this.variants,
    required this.createdAt,
    this.brandId,
    this.categoryId,
    this.description,
    this.hsnCode,
    this.primaryImageUrl,
    this.reviews,
  });

  final String id;
  final String sellerId;
  final String title;
  final String slug;
  final String? description;
  final String productType; // physical|digital|service
  final String? brandId;
  final String? categoryId;
  final String status; // draft|pending|approved|live|out_of_stock|hidden|archived|rejected
  final double basePrice;
  final String currency; // INR
  final double mrp; // strike-through
  final String? hsnCode;
  final double taxRatePct; // 0|5|12|18|28
  final bool isReturnable;
  final int returnWindowDays;
  final double rating;
  final int ratingCount;
  final String? primaryImageUrl;
  final List<String> imageUrls;
  final List<ProductVariant> variants;
  final List<ProductReview>? reviews;
  final DateTime createdAt;

  factory Product.fromJson(Map<String, dynamic> json) {
    try {
      // Backend `GET /v1/commerce/products/:id` wraps as
      // `{ product: {...}, variants: [...] }`. Tolerate both shapes.
      Map<String, dynamic> p = json;
      List<dynamic> rawVariants = (json['variants'] as List?) ?? const [];
      if (json.containsKey('product') && json['product'] is Map) {
        p = Map<String, dynamic>.from(json['product'] as Map);
        rawVariants = (json['variants'] as List?) ?? rawVariants;
      }

      final variants = rawVariants
          .whereType<Map>()
          .map((v) => ProductVariant.fromJson(Map<String, dynamic>.from(v)))
          .toList(growable: false);

      // Pricing: backend stores mrp/selling_price on the variant row, not the
      // product. For grid display we surface the cheapest variant's price as
      // `basePrice` and its MRP as `mrp`. If a `base_price`/`mrp` is sent on
      // the product itself we honour that.
      double basePrice = _toDouble(p['base_price'] ?? p['selling_price']);
      double mrp = _toDouble(p['mrp']);
      if (variants.isNotEmpty) {
        if (basePrice <= 0) {
          basePrice = variants.first.sellingPrice;
          for (final v in variants) {
            if (v.sellingPrice > 0 && v.sellingPrice < basePrice) {
              basePrice = v.sellingPrice;
            }
          }
        }
        if (mrp <= 0) {
          for (final v in variants) {
            if (v.mrp > mrp) mrp = v.mrp;
          }
        }
      }

      // Image URLs: backend ships `primary_image_url` and `image_urls`
      // expanded by the service layer. Tolerate the older `media_ids` shape
      // (legacy shop-service payloads) for soft compatibility during cutover.
      final primary = _toStrOrNull(p['primary_image_url']);
      final gallery = _toStringList(p['image_urls'] ?? p['media_urls']);

      // Tax rate: backend exposes a `tax_class` join; if absent default to
      // 0 (UI shows "Inclusive of taxes" generically).
      double taxRate = _toDouble(p['tax_rate_pct']);
      if (taxRate == 0 && p['tax_class'] is Map) {
        final tc = Map<String, dynamic>.from(p['tax_class'] as Map);
        taxRate = _toDouble(tc['igst_pct'] ?? tc['rate']);
      }

      final returnDays = _toInt(p['return_policy_days']);

      List<ProductReview>? reviews;
      if (p['reviews'] is List) {
        reviews = (p['reviews'] as List)
            .whereType<Map>()
            .map((r) => ProductReview.fromJson(Map<String, dynamic>.from(r)))
            .toList(growable: false);
      }

      return Product(
        id: _toStr(p['id']),
        sellerId: _toStr(p['seller_id']),
        title: _toStr(p['title'], 'Untitled'),
        slug: _toStr(p['slug']),
        description: _toStrOrNull(p['description'] ?? p['short_description']),
        productType: _toStr(p['product_type'], 'physical'),
        brandId: _toStrOrNull(p['brand_id']),
        categoryId: _toStrOrNull(p['category_id']),
        status: _toStr(p['status'] ?? p['approval_status'], 'live'),
        basePrice: basePrice,
        currency: _toStr(p['currency_code'] ?? p['currency'], 'INR'),
        mrp: mrp,
        hsnCode: _toStrOrNull(p['hsn_code']),
        taxRatePct: taxRate,
        isReturnable: _toBool(p['is_returnable'] ?? (returnDays > 0)),
        returnWindowDays: returnDays,
        rating: _toDouble(p['avg_rating'] ?? p['rating']),
        ratingCount: _toInt(p['review_count'] ?? p['rating_count']),
        primaryImageUrl: primary,
        imageUrls: gallery,
        variants: variants,
        reviews: reviews,
        createdAt: _toDate(p['created_at']),
      );
    } catch (e, st) {
      AppLogger.error('Product.fromJson failed', error: e, stackTrace: st);
      return Product(
        id: _toStr(json['id'], 'error'),
        sellerId: '',
        title: 'Product unavailable',
        slug: '',
        productType: 'physical',
        status: 'error',
        basePrice: 0,
        currency: 'INR',
        mrp: 0,
        taxRatePct: 0,
        isReturnable: false,
        returnWindowDays: 0,
        rating: 0,
        ratingCount: 0,
        imageUrls: const [],
        variants: const [],
        createdAt: DateTime.now(),
      );
    }
  }

  /// Best discount across variants (or product-level if no variants).
  int? get discountPct {
    if (mrp <= 0 || basePrice >= mrp) return null;
    return ((mrp - basePrice) / mrp * 100).round();
  }

  ProductVariant? get defaultVariant =>
      variants.isEmpty ? null : variants.first;

  bool get inStock {
    if (variants.isEmpty) return status == 'live' || status == 'approved';
    return variants.any((v) => v.isAvailable);
  }
}

/// Paginated product page returned by `GET /v1/commerce/products`. Backend
/// uses offset pagination today; the brief asks for cursor — we surface both
/// (`offset` for the wire, `nextCursor` synthesised from `offset+limit`) so
/// upstream callers can switch to a real cursor field without churn.
class ProductPage {
  const ProductPage({
    required this.items,
    required this.total,
    required this.limit,
    required this.offset,
    this.nextCursor,
  });

  final List<Product> items;
  final int total;
  final int limit;
  final int offset;
  // nextCursor is non-null when the backend returned a cursor-paginated
  // response. When set, the legacy `total` is unreliable (cursor mode
  // doesn't compute it); callers should use `hasMore` instead.
  final String? nextCursor;

  bool get hasMore {
    if (nextCursor != null) return nextCursor!.isNotEmpty;
    return offset + items.length < total;
  }

  int get nextOffset => offset + items.length;

  factory ProductPage.fromJson(Map<String, dynamic> json) {
    final raw = (json['items'] as List?) ?? const [];
    final next = json['next_cursor'];
    return ProductPage(
      items: raw
          .whereType<Map>()
          .map((m) => Product.fromJson(Map<String, dynamic>.from(m)))
          .toList(growable: false),
      total: _toInt(json['total']),
      limit: _toInt(json['limit']),
      offset: _toInt(json['offset']),
      nextCursor: next is String && next.isNotEmpty ? next : null,
    );
  }
}

// ─── Cart ────────────────────────────────────────────────────────────────

class CartItem {
  const CartItem({
    required this.id,
    required this.productId,
    required this.variantId,
    required this.qty,
    required this.unitPrice,
    required this.taxRatePct,
    required this.lineSubtotal,
    required this.lineTax,
    required this.lineTotal,
    required this.productSnapshot,
  });

  final String id;
  final String productId;
  final String variantId;
  final int qty;
  final double unitPrice;
  final double taxRatePct;
  final double lineSubtotal;
  final double lineTax;
  final double lineTotal;
  final CartItemSnapshot productSnapshot;

  factory CartItem.fromJson(Map<String, dynamic> json) {
    // Backend `CartItemDetail` shape:
    //   { Item: {id, cart_id, variant_id, product_id, quantity, price_snapshot},
    //     Product: {id, title, slug, primary_image_url}, Variant: {...} }
    final item = (json['Item'] is Map)
        ? Map<String, dynamic>.from(json['Item'] as Map)
        : json;
    final product = (json['Product'] is Map)
        ? Map<String, dynamic>.from(json['Product'] as Map)
        : (json['product'] is Map
            ? Map<String, dynamic>.from(json['product'] as Map)
            : const <String, dynamic>{});
    final variant = (json['Variant'] is Map)
        ? Map<String, dynamic>.from(json['Variant'] as Map)
        : const <String, dynamic>{};

    final qty = _toInt(item['quantity'] ?? item['qty']);
    final unit = _toDouble(
      item['unit_price'] ?? item['price_snapshot'] ?? variant['selling_price'],
    );
    final taxPct = _toDouble(item['tax_rate_pct'] ?? product['tax_rate_pct']);
    final subtotal = unit * qty;
    final tax = subtotal * taxPct / 100.0;

    return CartItem(
      id: _toStr(item['id']),
      productId: _toStr(item['product_id']),
      variantId: _toStr(item['variant_id']),
      qty: qty,
      unitPrice: unit,
      taxRatePct: taxPct,
      lineSubtotal: _toDouble(item['line_subtotal']) > 0
          ? _toDouble(item['line_subtotal'])
          : subtotal,
      lineTax: _toDouble(item['line_tax']) > 0 ? _toDouble(item['line_tax']) : tax,
      lineTotal: _toDouble(item['line_total']) > 0
          ? _toDouble(item['line_total'])
          : (subtotal + tax),
      productSnapshot: CartItemSnapshot(
        title: _toStr(product['title'], 'Item'),
        primaryImageUrl: _toStrOrNull(product['primary_image_url']),
        sellerId: _toStr(product['seller_id']),
        variantLabel: _variantLabel(variant),
      ),
    );
  }

  static String? _variantLabel(Map<String, dynamic> variant) {
    if (variant.isEmpty) return null;
    final parts = <String>[];
    for (var i = 1; i <= 3; i++) {
      final name = _toStrOrNull(variant['option_${i}_name']);
      final value = _toStrOrNull(variant['option_${i}_value']);
      if (name != null && value != null) parts.add('$name: $value');
    }
    return parts.isEmpty ? null : parts.join(' / ');
  }
}

class CartItemSnapshot {
  const CartItemSnapshot({
    required this.title,
    required this.sellerId,
    this.primaryImageUrl,
    this.variantLabel,
  });

  final String title;
  final String? primaryImageUrl;
  final String sellerId;
  final String? variantLabel;
}

/// CouponPreview is the response shape of
/// GET /v1/commerce/cart/coupon-preview?code=XYZ. Read-only — the
/// actual coupon application happens at checkout, so the user can
/// try several codes without committing state.
class CouponPreview {
  const CouponPreview({
    required this.couponCode,
    required this.couponDiscount,
    required this.subtotal,
    required this.grandTotal,
    required this.applied,
  });

  final String couponCode;
  final double couponDiscount;
  final double subtotal;
  final double grandTotal;
  final bool applied;

  factory CouponPreview.fromJson(Map<String, dynamic> json) {
    return CouponPreview(
      couponCode: json['coupon_code']?.toString() ?? '',
      couponDiscount: _toDouble(json['coupon_discount']),
      subtotal: _toDouble(json['subtotal']),
      grandTotal: _toDouble(json['grand_total']),
      applied: json['applied'] == true,
    );
  }
}

class Cart {
  const Cart({
    required this.id,
    required this.items,
    required this.subtotal,
    required this.taxTotal,
    required this.shippingTotal,
    required this.discountTotal,
    required this.grandTotal,
    this.appliedCouponCode,
  });

  final String id;
  final List<CartItem> items;
  final double subtotal;
  final double taxTotal;
  final double shippingTotal;
  final double discountTotal;
  final double grandTotal;
  final String? appliedCouponCode;

  int get itemCount {
    var n = 0;
    for (final i in items) {
      n += i.qty;
    }
    return n;
  }

  bool get isEmpty => items.isEmpty;

  factory Cart.empty() => const Cart(
        id: '',
        items: [],
        subtotal: 0,
        taxTotal: 0,
        shippingTotal: 0,
        discountTotal: 0,
        grandTotal: 0,
      );

  factory Cart.fromJson(Map<String, dynamic> json) {
    // Backend `CartSummary` shape:
    //   { CartID, Items: [CartItemDetail...], Subtotal, ItemCount }
    // We synthesise the totals the brief asks for; tax/shipping/discount are
    // computed only at checkout server-side, so we derive line tax in CartItem
    // and sum here. The real grand total comes back on the order on submit.
    final id = _toStr(json['CartID'] ?? json['id'] ?? json['cart_id']);
    final rawItems = (json['Items'] ?? json['items']) as List? ?? const [];
    final items = rawItems
        .whereType<Map>()
        .map((m) => CartItem.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);

    double subtotal = _toDouble(json['Subtotal'] ?? json['subtotal']);
    if (subtotal == 0) {
      for (final it in items) {
        subtotal += it.lineSubtotal;
      }
    }
    double tax = _toDouble(json['tax_total']);
    if (tax == 0) {
      for (final it in items) {
        tax += it.lineTax;
      }
    }
    final shipping = _toDouble(json['shipping_total']);
    final discount = _toDouble(json['discount_total']);
    double grand = _toDouble(json['grand_total']);
    if (grand == 0) grand = subtotal + tax + shipping - discount;

    return Cart(
      id: id,
      items: items,
      subtotal: subtotal,
      taxTotal: tax,
      shippingTotal: shipping,
      discountTotal: discount,
      grandTotal: grand,
      appliedCouponCode: _toStrOrNull(json['applied_coupon_code']),
    );
  }
}

// ─── Address ─────────────────────────────────────────────────────────────

class Address {
  const Address({
    required this.id,
    required this.label,
    required this.fullName,
    required this.phone,
    required this.line1,
    required this.city,
    required this.state,
    required this.postalCode,
    required this.country,
    required this.isDefault,
    this.line2,
    this.landmark,
  });

  final String id;
  final String label; // "Home" | "Work" | "Other"
  final String fullName;
  final String phone;
  final String line1;
  final String? line2;
  final String city;
  final String state;
  final String postalCode;
  final String country;
  final String? landmark;
  final bool isDefault;

  factory Address.fromJson(Map<String, dynamic> json) {
    final type = _toStr(json['address_type'] ?? json['label'], 'home');
    final pretty = type.isEmpty
        ? 'Other'
        : type[0].toUpperCase() + type.substring(1).toLowerCase();
    return Address(
      id: _toStr(json['id']),
      label: pretty,
      fullName: _toStr(json['contact_name'] ?? json['full_name']),
      phone: _toStr(json['phone']),
      line1: _toStr(json['address_line_1']),
      line2: _toStrOrNull(json['address_line_2']),
      city: _toStr(json['city']),
      state: _toStr(json['state']),
      postalCode: _toStr(json['postal_code']),
      country: _toStr(json['country'], 'IN'),
      landmark: _toStrOrNull(json['landmark']),
      isDefault: _toBool(json['is_default']),
    );
  }

  /// Body for `POST /v1/commerce/addresses` and `PATCH …/addresses/:id`.
  Map<String, dynamic> toCreateJson() => {
        'address_type': label.toLowerCase(),
        'full_name': fullName,
        'phone': phone,
        'address_line_1': line1,
        if (line2 != null) 'address_line_2': line2,
        if (landmark != null) 'landmark': landmark,
        'city': city,
        'state': state,
        'postal_code': postalCode,
        'country': country,
        'is_default': isDefault,
      };

  Address copyWith({
    String? id,
    String? label,
    String? fullName,
    String? phone,
    String? line1,
    String? line2,
    String? city,
    String? state,
    String? postalCode,
    String? country,
    String? landmark,
    bool? isDefault,
  }) {
    return Address(
      id: id ?? this.id,
      label: label ?? this.label,
      fullName: fullName ?? this.fullName,
      phone: phone ?? this.phone,
      line1: line1 ?? this.line1,
      line2: line2 ?? this.line2,
      city: city ?? this.city,
      state: state ?? this.state,
      postalCode: postalCode ?? this.postalCode,
      country: country ?? this.country,
      landmark: landmark ?? this.landmark,
      isDefault: isDefault ?? this.isDefault,
    );
  }
}

// ─── Order, OrderItem, Shipment ─────────────────────────────────────────

class OrderItem {
  const OrderItem({
    required this.id,
    required this.productId,
    required this.variantId,
    required this.qty,
    required this.unitPrice,
    required this.lineTotal,
    required this.productSnapshot,
    required this.sellerId,
    required this.status,
  });

  final String id;
  final String productId;
  final String variantId;
  final int qty;
  final double unitPrice;
  final double lineTotal;
  final CartItemSnapshot productSnapshot;
  final String sellerId;
  final String status;

  factory OrderItem.fromJson(Map<String, dynamic> json) {
    return OrderItem(
      id: _toStr(json['id']),
      productId: _toStr(json['product_id']),
      variantId: _toStr(json['variant_id']),
      qty: _toInt(json['quantity'] ?? json['qty']),
      unitPrice: _toDouble(json['unit_price']),
      lineTotal: _toDouble(json['final_price'] ?? json['line_total']),
      productSnapshot: CartItemSnapshot(
        title: _toStr(json['product_title'], 'Item'),
        sellerId: _toStr(json['seller_id']),
        primaryImageUrl: _toStrOrNull(json['primary_image_url']),
      ),
      sellerId: _toStr(json['seller_id']),
      status: _toStr(json['status'], 'pending'),
    );
  }
}

class TrackingEvent {
  const TrackingEvent({
    required this.status,
    required this.occurredAt,
    this.location,
    this.remark,
  });

  final String status;
  final String? location;
  final String? remark;
  final DateTime occurredAt;

  factory TrackingEvent.fromJson(Map<String, dynamic> json) {
    return TrackingEvent(
      status: _toStr(json['status']),
      location: _toStrOrNull(json['location']),
      remark: _toStrOrNull(json['remark']),
      occurredAt: _toDate(json['occurred_at']),
    );
  }
}

class Shipment {
  const Shipment({
    required this.id,
    required this.courier,
    required this.awb,
    required this.status,
    required this.events,
    this.trackingUrl,
  });

  final String id;
  final String courier;
  final String awb;
  final String? trackingUrl;
  final String status;
  final List<TrackingEvent> events;

  factory Shipment.fromJson(Map<String, dynamic> json) {
    final raw = (json['events'] as List?) ?? const [];
    return Shipment(
      id: _toStr(json['id']),
      courier: _toStr(json['courier']),
      awb: _toStr(json['awb_number'] ?? json['tracking_number']),
      trackingUrl: _toStrOrNull(json['tracking_url']),
      status: _toStr(json['current_status'] ?? json['status'], 'pending'),
      events: raw
          .whereType<Map>()
          .map((e) => TrackingEvent.fromJson(Map<String, dynamic>.from(e)))
          .toList(growable: false),
    );
  }
}

class Order {
  const Order({
    required this.id,
    required this.orderNumber,
    required this.status,
    required this.items,
    required this.amountSubtotal,
    required this.amountTax,
    required this.amountShipping,
    required this.amountDiscount,
    required this.amountGrand,
    required this.currency,
    required this.paymentStatus,
    required this.placedAt,
    this.paymentMethod,
    this.shippingAddress,
    this.paidAt,
    this.shippedAt,
    this.deliveredAt,
    this.shipments,
    // Phase F4 mobile — Phase 5 / F2 B2B fields. Null on retail orders.
    this.organizationId,
    this.poNumber,
    this.costCenter,
    this.approvalStatus,
    this.creditTermsDays,
    this.paymentDueDate,
  });

  final String id;
  final String orderNumber;
  final String status; // 15 backend states
  final List<OrderItem> items;
  final double amountSubtotal;
  final double amountTax;
  final double amountShipping;
  final double amountDiscount;
  final double amountGrand;
  final String currency;
  final String? paymentMethod; // upi|card|wallet|cod|netbanking|credit
  final String paymentStatus; // pending|paid|failed|refunded
  final Address? shippingAddress;
  final DateTime placedAt;
  final DateTime? paidAt;
  final DateTime? shippedAt;
  final DateTime? deliveredAt;
  final List<Shipment>? shipments;
  // ─── Phase F4 — B2B fields ───────────────────────────────────────
  final String? organizationId;
  final String? poNumber;
  final String? costCenter;
  final String? approvalStatus; // not_required | pending | approved | rejected
  final int? creditTermsDays;
  final DateTime? paymentDueDate;

  bool get awaitingApproval => approvalStatus == 'pending';
  bool get isCreditOrder => (creditTermsDays ?? 0) > 0;

  factory Order.fromJson(Map<String, dynamic> json) {
    // Backend wraps `GET /v1/commerce/orders/:id/items` as `{order, items}`.
    Map<String, dynamic> o = json;
    List<dynamic> rawItems = (json['items'] as List?) ?? const [];
    if (json['order'] is Map) {
      o = Map<String, dynamic>.from(json['order'] as Map);
    }

    final items = rawItems
        .whereType<Map>()
        .map((m) => OrderItem.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);

    Address? addr;
    if (o['shipping_address'] is Map) {
      addr = Address.fromJson(Map<String, dynamic>.from(o['shipping_address'] as Map));
    } else if (o['delivery_address_snapshot'] is Map) {
      addr = Address.fromJson(
        Map<String, dynamic>.from(o['delivery_address_snapshot'] as Map),
      );
    }

    return Order(
      id: _toStr(o['id']),
      orderNumber: _toStr(o['order_number']),
      status: _toStr(o['status'], 'pending'),
      items: items,
      amountSubtotal: _toDouble(o['subtotal']),
      amountTax: _toDouble(o['tax_amount']),
      amountShipping: _toDouble(o['shipping_charges']),
      amountDiscount:
          _toDouble(o['discount_amount']) + _toDouble(o['coupon_discount']),
      amountGrand: _toDouble(o['final_amount']),
      currency: _toStr(o['currency_code'], 'INR'),
      paymentMethod: _toStrOrNull(o['payment_method']),
      paymentStatus: _toStr(o['payment_status'], 'pending'),
      shippingAddress: addr,
      placedAt: _toDate(o['created_at'] ?? o['placed_at']),
      paidAt: _toDateOrNull(o['paid_at']),
      shippedAt: _toDateOrNull(o['shipped_at']),
      deliveredAt: _toDateOrNull(o['delivered_at']),
      shipments: (o['shipments'] is List)
          ? (o['shipments'] as List)
              .whereType<Map>()
              .map((m) => Shipment.fromJson(Map<String, dynamic>.from(m)))
              .toList(growable: false)
          : null,
      // Phase F4 — B2B fields. Nullable so retail responses without
      // these columns still parse cleanly.
      organizationId: _toStrOrNull(o['organization_id']),
      poNumber: _toStrOrNull(o['po_number']),
      costCenter: _toStrOrNull(o['cost_center']),
      approvalStatus: _toStrOrNull(o['approval_status']),
      creditTermsDays: (o['credit_terms_days'] is num)
          ? (o['credit_terms_days'] as num).toInt()
          : null,
      paymentDueDate: _toDateOrNull(o['payment_due_date']),
    );
  }
}

// ─── Checkout quote ──────────────────────────────────────────────────────

/// Pre-checkout quote returned by `commerceQuote` repository call.
///
/// Note: backend doesn't yet expose a dedicated `/orders/quote` endpoint; the
/// repository falls back to deriving this client-side from the cart + tax
/// rules. When the backend ships one, the shape matches.
class CheckoutQuote {
  const CheckoutQuote({
    required this.cartId,
    required this.addressId,
    required this.paymentMethod,
    required this.subtotal,
    required this.taxTotal,
    required this.shippingTotal,
    required this.discountTotal,
    required this.grandTotal,
    required this.isCodAllowed,
    this.estimatedDeliveryDate,
  });

  final String cartId;
  final String addressId;
  final String paymentMethod;
  final double subtotal;
  final double taxTotal;
  final double shippingTotal;
  final double discountTotal;
  final double grandTotal;
  final DateTime? estimatedDeliveryDate;
  final bool isCodAllowed;

  factory CheckoutQuote.fromCart({
    required Cart cart,
    required String addressId,
    required String paymentMethod,
    DateTime? eta,
  }) {
    // COD policy: ₹5000 hard cap (matches §I.11 default in COMMERCE_RECON).
    final codAllowed = cart.grandTotal <= 5000;
    return CheckoutQuote(
      cartId: cart.id,
      addressId: addressId,
      paymentMethod: paymentMethod,
      subtotal: cart.subtotal,
      taxTotal: cart.taxTotal,
      shippingTotal: cart.shippingTotal,
      discountTotal: cart.discountTotal,
      grandTotal: cart.grandTotal,
      isCodAllowed: codAllowed,
      estimatedDeliveryDate: eta ?? DateTime.now().add(const Duration(days: 5)),
    );
  }
}

// ─── Pincode serviceability ──────────────────────────────────────────────

/// Result of a pincode check. Backend doesn't expose this endpoint yet
/// (see COMMERCE_RECON §F.2); the repository uses a heuristic stub keyed on
/// the first three digits so the UI is fully wired and Sprint 2 can swap in
/// the real endpoint without screen changes.
class PincodeServiceability {
  const PincodeServiceability({
    required this.deliverable,
    required this.etaDays,
    this.message,
  });

  final bool deliverable;
  final int etaDays;
  final String? message;

  factory PincodeServiceability.fromJson(Map<String, dynamic> json) {
    return PincodeServiceability(
      deliverable: _toBool(json['deliverable']),
      etaDays: _toInt(json['eta_days']),
      message: _toStrOrNull(json['message']),
    );
  }
}

// ─── OrderListItem (Sprint 2) ────────────────────────────────────────────

/// Light-weight order summary for `/commerce/orders` list views. Mirrors the
/// row shape `commerce-service` returns from `GET /v1/commerce/orders` —
/// without the `items` payload that `Order.fromJson` hydrates.
class OrderListItem {
  const OrderListItem({
    required this.id,
    required this.orderNumber,
    required this.status,
    required this.amountGrand,
    required this.currency,
    required this.itemCount,
    required this.placedAt,
    this.primaryThumbUrl,
    this.firstItemTitle,
  });

  final String id;
  final String orderNumber;
  final String status;
  final double amountGrand;
  final String currency;
  final int itemCount;
  final DateTime placedAt;
  final String? primaryThumbUrl;
  final String? firstItemTitle;

  /// Convenience: is the order still "active" (placed but not yet
  /// terminal)? Used by the `Active` tab on `MyOrdersScreen`.
  bool get isActive {
    switch (status) {
      case 'delivered':
      case 'cancelled':
      case 'refunded':
      case 'returned':
        return false;
      default:
        return true;
    }
  }

  bool get isDelivered => status == 'delivered';
  bool get isCancelled => status == 'cancelled';
  bool get isReturned => status == 'returned' || status == 'refunded';

  factory OrderListItem.fromJson(Map<String, dynamic> json) {
    // Try to extract a thumbnail + title from the first embedded item if
    // backend returns one. commerce-service ListOrders today does not, so we
    // gracefully degrade to ids-only.
    String? thumb;
    String? firstTitle;
    int count = _toInt(json['item_count'] ?? json['items_count']);
    final rawItems = json['items'];
    if (rawItems is List && rawItems.isNotEmpty) {
      count = count == 0 ? rawItems.length : count;
      final first = rawItems.first;
      if (first is Map) {
        thumb = _toStrOrNull(first['primary_image_url'] ?? first['image_url']);
        firstTitle = _toStrOrNull(first['product_title'] ?? first['title']);
      }
    }
    return OrderListItem(
      id: _toStr(json['id']),
      orderNumber: _toStr(json['order_number']),
      status: _toStr(json['status'], 'pending'),
      amountGrand: _toDouble(json['final_amount'] ?? json['grand_total']),
      currency: _toStr(json['currency_code'] ?? json['currency'], 'INR'),
      itemCount: count,
      placedAt: _toDate(json['created_at'] ?? json['placed_at']),
      primaryThumbUrl: thumb ?? _toStrOrNull(json['primary_image_url']),
      // Phase 2.1 backend ships `first_product_title`; older payloads
      // may carry `first_item_title`. Either is accepted.
      firstItemTitle: firstTitle ??
          _toStrOrNull(json['first_product_title'] ?? json['first_item_title']),
    );
  }
}

// ─── Returns (Sprint 2) ──────────────────────────────────────────────────

/// Reasons mirror the backend `return_requests.reason_code` enum. The
/// snake_case wire values match what `commerce-service` accepts on
/// `POST /v1/commerce/orders/:id/returns` per `handler.go#createReturnReq`.
enum ReturnReason {
  defective('defective', 'Defective product'),
  damaged('damaged', 'Arrived damaged'),
  wrongItem('wrong_item', 'Wrong item received'),
  sizeIssue('size_issue', 'Size or fit issue'),
  noLongerNeeded('no_longer_needed', 'No longer needed'),
  other('other', 'Other reason');

  const ReturnReason(this.wireValue, this.label);

  final String wireValue;
  final String label;

  static ReturnReason fromWire(String? wire) {
    if (wire == null) return ReturnReason.other;
    for (final r in ReturnReason.values) {
      if (r.wireValue == wire) return r;
    }
    return ReturnReason.other;
  }
}

/// One line item inside a return request.
class ReturnItem {
  const ReturnItem({
    required this.orderItemId,
    required this.qty,
    required this.reason,
    this.productTitle,
    this.primaryImageUrl,
  });

  final String orderItemId;
  final int qty;
  final ReturnReason reason;
  final String? productTitle;
  final String? primaryImageUrl;

  factory ReturnItem.fromJson(Map<String, dynamic> json) {
    return ReturnItem(
      orderItemId: _toStr(json['order_item_id']),
      qty: _toInt(json['quantity'] ?? json['qty']),
      reason: ReturnReason.fromWire(_toStrOrNull(json['reason_code'])),
      productTitle: _toStrOrNull(json['product_title']),
      primaryImageUrl: _toStrOrNull(json['primary_image_url']),
    );
  }

  Map<String, dynamic> toCreateJson() => {
        'order_item_id': orderItemId,
        'qty': qty,
        'reason': reason.wireValue,
      };
}

/// Customer-facing return request.
class ReturnRequest {
  const ReturnRequest({
    required this.id,
    required this.orderId,
    required this.items,
    required this.status,
    required this.reason,
    required this.createdAt,
    this.reasonDescription,
    this.pickupAddress,
    this.pickupScheduledAt,
    this.refundedAt,
    this.refundAmount,
  });

  final String id;
  final String orderId;
  final List<ReturnItem> items;
  // requested|approved|picked_up|in_transit|received|refunded|rejected
  final String status;
  final ReturnReason reason;
  final String? reasonDescription;
  final Address? pickupAddress;
  final DateTime? pickupScheduledAt;
  final DateTime? refundedAt;
  final double? refundAmount;
  final DateTime createdAt;

  factory ReturnRequest.fromJson(Map<String, dynamic> json) {
    Address? pickup;
    if (json['pickup_address'] is Map) {
      pickup = Address.fromJson(
        Map<String, dynamic>.from(json['pickup_address'] as Map),
      );
    }
    final rawItems = (json['items'] as List?) ?? const [];
    final items = rawItems
        .whereType<Map>()
        .map((m) => ReturnItem.fromJson(Map<String, dynamic>.from(m)))
        .toList(growable: false);

    // Backend single-item returns surface as a flat shape — synthesise a
    // one-item list so the UI can iterate uniformly.
    if (items.isEmpty && json['order_item_id'] != null) {
      items.add(ReturnItem(
        orderItemId: _toStr(json['order_item_id']),
        qty: _toInt(json['quantity'] ?? json['qty']),
        reason: ReturnReason.fromWire(_toStrOrNull(json['reason_code'])),
      ));
    }

    return ReturnRequest(
      id: _toStr(json['id']),
      orderId: _toStr(json['order_id']),
      items: items,
      status: _toStr(json['status'], 'requested'),
      reason: items.isNotEmpty
          ? items.first.reason
          : ReturnReason.fromWire(_toStrOrNull(json['reason_code'])),
      reasonDescription: _toStrOrNull(json['reason_description']),
      pickupAddress: pickup,
      pickupScheduledAt: _toDateOrNull(json['pickup_scheduled_at']),
      refundedAt: _toDateOrNull(json['refunded_at']),
      refundAmount: json['refund_amount'] == null
          ? null
          : _toDouble(json['refund_amount']),
      createdAt: _toDate(json['created_at']),
    );
  }
}

// ─── Wishlist (Sprint 2) ─────────────────────────────────────────────────

class WishlistItemSnapshot {
  const WishlistItemSnapshot({
    required this.title,
    required this.sellingPrice,
    this.primaryImageUrl,
    this.mrp,
  });

  final String title;
  final String? primaryImageUrl;
  final double sellingPrice;
  final double? mrp;

  int? get discountPct {
    final m = mrp;
    if (m == null || m <= 0 || sellingPrice >= m) return null;
    return ((m - sellingPrice) / m * 100).round();
  }
}

class WishlistItem {
  const WishlistItem({
    required this.productId,
    required this.savedAt,
    required this.productSnapshot,
  });

  final String productId;
  final DateTime savedAt;
  final WishlistItemSnapshot productSnapshot;

  factory WishlistItem.fromJson(Map<String, dynamic> json) {
    // Backend wishlist row shape (commerce_db.wishlists) joins
    // `products` so we get title + price; missing fields degrade to defaults.
    final p = (json['product'] is Map)
        ? Map<String, dynamic>.from(json['product'] as Map)
        : json;
    return WishlistItem(
      productId: _toStr(json['product_id'] ?? p['id']),
      savedAt: _toDate(json['saved_at'] ?? json['created_at']),
      productSnapshot: WishlistItemSnapshot(
        title: _toStr(p['title'] ?? p['product_title'], 'Item'),
        primaryImageUrl: _toStrOrNull(p['primary_image_url']),
        sellingPrice: _toDouble(
          p['selling_price'] ?? p['base_price'] ?? p['price'],
        ),
        mrp: p['mrp'] == null ? null : _toDouble(p['mrp']),
      ),
    );
  }
}

// ─── Search (Sprint 2) ───────────────────────────────────────────────────

/// Sort options for product search. Wire values mirror what the future
/// search endpoint will accept on `?sort=` (today the backend only honours
/// relevance; the others degrade to relevance — see Sprint 2 open notes).
enum SearchSort {
  relevance('relevance', 'Relevance'),
  priceLow('price_asc', 'Price: low to high'),
  priceHigh('price_desc', 'Price: high to low'),
  ratingDesc('rating_desc', 'Customer rating'),
  newest('newest', 'Newest first'),
  popularity('popularity', 'Popularity');

  const SearchSort(this.wireValue, this.label);

  final String wireValue;
  final String label;
}

/// Faceted search filters. Treated as a value type (immutable + ==) so
/// Riverpod families can dedupe identical queries.
@immutable
class SearchFilters {
  const SearchFilters({
    this.categoryIds = const [],
    this.brandIds = const [],
    this.priceMin,
    this.priceMax,
    this.ratingMin,
    this.hasFreeShipping = false,
    this.hasCod = false,
  });

  final List<String> categoryIds;
  final List<String> brandIds;
  final double? priceMin;
  final double? priceMax;
  final double? ratingMin;
  final bool hasFreeShipping;
  final bool hasCod;

  bool get isEmpty =>
      categoryIds.isEmpty &&
      brandIds.isEmpty &&
      priceMin == null &&
      priceMax == null &&
      ratingMin == null &&
      !hasFreeShipping &&
      !hasCod;

  int get appliedCount {
    var n = 0;
    if (categoryIds.isNotEmpty) n++;
    if (brandIds.isNotEmpty) n++;
    if (priceMin != null || priceMax != null) n++;
    if (ratingMin != null) n++;
    if (hasFreeShipping) n++;
    if (hasCod) n++;
    return n;
  }

  SearchFilters copyWith({
    List<String>? categoryIds,
    List<String>? brandIds,
    Object? priceMin = _unset,
    Object? priceMax = _unset,
    Object? ratingMin = _unset,
    bool? hasFreeShipping,
    bool? hasCod,
  }) {
    return SearchFilters(
      categoryIds: categoryIds ?? this.categoryIds,
      brandIds: brandIds ?? this.brandIds,
      priceMin: identical(priceMin, _unset) ? this.priceMin : priceMin as double?,
      priceMax: identical(priceMax, _unset) ? this.priceMax : priceMax as double?,
      ratingMin:
          identical(ratingMin, _unset) ? this.ratingMin : ratingMin as double?,
      hasFreeShipping: hasFreeShipping ?? this.hasFreeShipping,
      hasCod: hasCod ?? this.hasCod,
    );
  }

  static const _unset = Object();

  @override
  bool operator ==(Object other) {
    if (other is! SearchFilters) return false;
    if (other.priceMin != priceMin ||
        other.priceMax != priceMax ||
        other.ratingMin != ratingMin ||
        other.hasFreeShipping != hasFreeShipping ||
        other.hasCod != hasCod) {
      return false;
    }
    if (other.categoryIds.length != categoryIds.length ||
        other.brandIds.length != brandIds.length) {
      return false;
    }
    for (var i = 0; i < categoryIds.length; i++) {
      if (other.categoryIds[i] != categoryIds[i]) return false;
    }
    for (var i = 0; i < brandIds.length; i++) {
      if (other.brandIds[i] != brandIds[i]) return false;
    }
    return true;
  }

  @override
  int get hashCode => Object.hash(
        Object.hashAll(categoryIds),
        Object.hashAll(brandIds),
        priceMin,
        priceMax,
        ratingMin,
        hasFreeShipping,
        hasCod,
      );
}
