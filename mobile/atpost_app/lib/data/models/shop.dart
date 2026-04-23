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
  });

  factory Product.fromJson(Map<String, dynamic> json) {
    try {
      return Product(
        id: (json['id'] ?? '').toString(),
        sellerId: (json['seller_id'] ?? '').toString(),
        title: (json['title'] ?? 'Untitled Product').toString(),
        description: (json['description'] ?? '').toString(),
        price: _toDouble(json['price']),
        currency: (json['currency'] ?? 'USD').toString(),
        category: (json['category'] ?? 'General').toString(),
        mediaIds: _parseList<String>(json['media_ids']),
        stock: _toInt(json['stock']),
        status: (json['status'] ?? 'active').toString(),
        createdAt: _parseDate(json['created_at']),
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
    currency: 'USD',
    category: '',
    mediaIds: const [],
    stock: 0,
    status: 'error',
    createdAt: DateTime.now(),
  );
}

class CartItem {
  final String productId;
  final int quantity;
  final Product? product;

  const CartItem({
    required this.productId,
    required this.quantity,
    this.product,
  });

  factory CartItem.fromJson(Map<String, dynamic> json) {
    try {
      return CartItem(
        productId: (json['product_id'] ?? '').toString(),
        quantity: _toInt(json['quantity'] ?? 1),
        product: json['product'] != null ? Product.fromJson(Map<String, dynamic>.from(json['product'])) : null,
      );
    } catch (e) {
      return CartItem(productId: 'err', quantity: 0);
    }
  }
}

// --- Resilience Helpers ---
double _toDouble(dynamic data) {
  if (data is double) return data;
  if (data is int) return data.toDouble();
  if (data is String) return double.tryParse(data) ?? 0.0;
  return 0.0;
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
