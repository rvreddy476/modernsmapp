import 'package:atpost_app/core/utils/app_logger.dart';

/// Production-ready Product model with Total Resilience parsing.
class Product {
  final String id;
  final String sellerId;
  final String title;
  final String description;
  final double price;
  final String currency;
  final String category;
  final List<String> mediaIds;
  final int stock;
  final String status;
  final DateTime createdAt;
  // Phase F1.2 — commerce-service list enrichment. defaultVariantId is
  // the variant the screen should add when the user taps "Add to Cart"
  // without first opening the PDP. primaryImageMediaId is the cover
  // image for the card. Empty / null on legacy responses.
  final String? primaryImageMediaId;
  final String? defaultVariantId;
  final double? minSellingPrice;
  final double? minMrp;

  const Product({
    required this.id,
    required this.sellerId,
    required this.title,
    required this.description,
    required this.price,
    required this.currency,
    required this.category,
    required this.mediaIds,
    required this.stock,
    required this.status,
    required this.createdAt,
    this.primaryImageMediaId,
    this.defaultVariantId,
    this.minSellingPrice,
    this.minMrp,
  });

  factory Product.fromJson(Map<String, dynamic> json) {
    try {
      final minSelling = _tryDouble(json['min_selling_price']);
      // Fall back: prefer the variant min price for display when the
      // legacy `price` field is absent (commerce response always has
      // min_selling_price; shop-service legacy had price).
      final priceVal = _tryDouble(json['price']) ?? minSelling ?? 0.0;
      return Product(
        id: (json['id'] ?? '').toString(),
        sellerId: (json['seller_id'] ?? '').toString(),
        title: (json['title'] ?? 'Untitled Product').toString(),
        description: (json['description'] ?? '').toString(),
        price: priceVal,
        currency: (json['currency'] ?? 'INR').toString(),
        category: (json['category'] ?? json['category_id'] ?? 'General').toString(),
        mediaIds: _parseList<String>(json['media_ids']),
        stock: _toInt(json['stock'] ?? json['total_stock']),
        status: (json['status'] ?? 'active').toString(),
        createdAt: _parseDate(json['created_at']),
        primaryImageMediaId: json['primary_image_media_id']?.toString(),
        defaultVariantId: json['default_variant_id']?.toString(),
        minSellingPrice: minSelling,
        minMrp: _tryDouble(json['min_mrp']),
      );
    } catch (e, st) {
      AppLogger.error('Product.fromJson failed', error: e, stackTrace: st);
      return Product.empty();
    }
  }

  static Product empty() => Product(
    id: 'error',
    sellerId: '',
    title: 'Product Unavailable',
    description: '',
    price: 0.0,
    currency: 'INR',
    category: '',
    mediaIds: const [],
    stock: 0,
    status: 'error',
    createdAt: DateTime.now(),
  );
}

/// Phase F1.2 — keyed on variant_id (the commerce inventory unit), not
/// product_id. The legacy product_id is still exposed for analytics +
/// fallback rendering when variant detail is sparse.
class CartItem {
  final String id; // cart_items.id
  final String variantId;
  final String? productId;
  final String? sellerId;
  final String? productTitle;
  final String? imageMediaId;
  final String sku;
  final int quantity;
  final double mrp;
  final double sellingPrice;
  final double finalPrice;
  final int stockQty;
  final Product? product;

  const CartItem({
    required this.id,
    required this.variantId,
    this.productId,
    this.sellerId,
    this.productTitle,
    this.imageMediaId,
    this.sku = '',
    required this.quantity,
    this.mrp = 0,
    this.sellingPrice = 0,
    this.finalPrice = 0,
    this.stockQty = 0,
    this.product,
  });

  factory CartItem.fromJson(Map<String, dynamic> json) {
    try {
      return CartItem(
        id: (json['id'] ?? '').toString(),
        variantId: (json['variant_id'] ?? '').toString(),
        productId: json['product_id']?.toString(),
        sellerId: json['seller_id']?.toString(),
        productTitle: json['product_title']?.toString(),
        imageMediaId: (json['image_media_id'] ?? json['primary_image_media_id'])?.toString(),
        sku: (json['sku'] ?? '').toString(),
        quantity: _toInt(json['quantity'] ?? 1),
        mrp: _toDouble(json['mrp']),
        sellingPrice: _toDouble(json['selling_price'] ?? json['unit_price']),
        finalPrice: _toDouble(json['final_price']),
        stockQty: _toInt(json['stock_qty']),
        product: json['product'] != null
            ? Product.fromJson(Map<String, dynamic>.from(json['product']))
            : null,
      );
    } catch (e) {
      return CartItem(id: 'err', variantId: 'err', quantity: 0);
    }
  }
}

/// Phase F1.2 — server-authoritative Cart envelope returned by
/// /v1/commerce/cart. All totals come from the backend (priceCart);
/// the client never recomputes pricing locally.
class Cart {
  final String id;
  final List<CartItem> items;
  final double subtotal;
  final double shipping;
  final double tax;
  final double discount;
  final double grandTotal;
  final String currency;

  const Cart({
    required this.id,
    required this.items,
    this.subtotal = 0,
    this.shipping = 0,
    this.tax = 0,
    this.discount = 0,
    this.grandTotal = 0,
    this.currency = 'INR',
  });

  factory Cart.fromJson(Map<String, dynamic> json) {
    final itemsRaw = (json['items'] as List<dynamic>?) ?? const [];
    return Cart(
      id: (json['id'] ?? '').toString(),
      items: itemsRaw
          .map((e) => CartItem.fromJson(Map<String, dynamic>.from(e as Map)))
          .toList(),
      subtotal: _toDouble(json['subtotal']),
      shipping: _toDouble(json['shipping']),
      tax: _toDouble(json['tax']),
      discount: _toDouble(json['discount']),
      grandTotal: _toDouble(json['grand_total'] ?? json['final_amount']),
      currency: (json['currency'] ?? 'INR').toString(),
    );
  }

  int get itemCount =>
      items.fold<int>(0, (sum, item) => sum + item.quantity);
}

// --- Resilience Helpers ---
double _toDouble(dynamic data) {
  if (data is double) return data;
  if (data is int) return data.toDouble();
  if (data is String) return double.tryParse(data) ?? 0.0;
  return 0.0;
}

double? _tryDouble(dynamic data) {
  if (data == null) return null;
  if (data is double) return data;
  if (data is int) return data.toDouble();
  if (data is String) return double.tryParse(data);
  if (data is num) return data.toDouble();
  return null;
}

int _toInt(dynamic data) {
  if (data is int) return data;
  if (data is String) return int.tryParse(data) ?? 0;
  return 0;
}

DateTime _parseDate(dynamic data) {
  if (data is String) return DateTime.tryParse(data) ?? DateTime.now();
  return DateTime.now();
}

List<T> _parseList<T>(dynamic data) {
  if (data is List) return data.cast<T>();
  return const [];
}
