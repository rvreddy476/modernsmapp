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
    return Product(
      id: json['id'] as String? ?? '',
      sellerId: json['seller_id'] as String? ?? '',
      title: json['title'] as String? ?? '',
      description: json['description'] as String? ?? '',
      price: (json['price'] as num?)?.toDouble() ?? 0.0,
      currency: json['currency'] as String? ?? 'USD',
      category: json['category'] as String? ?? '',
      mediaIds: (json['media_ids'] as List<dynamic>?)
              ?.map((e) => e as String)
              .toList() ??
          [],
      stock: json['stock'] as int? ?? 0,
      status: json['status'] as String? ?? 'active',
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
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
    return CartItem(
      productId: json['product_id'] as String? ?? '',
      quantity: json['quantity'] as int? ?? 1,
      product: json['product'] != null
          ? Product.fromJson(json['product'] as Map<String, dynamic>)
          : null,
    );
  }
}

class Order {
  final String id;
  final String buyerId;
  final String sellerId;
  final String status;
  final double total;
  final String currency;
  final DateTime createdAt;

  const Order({
    required this.id,
    required this.buyerId,
    required this.sellerId,
    required this.status,
    required this.total,
    required this.currency,
    required this.createdAt,
  });

  factory Order.fromJson(Map<String, dynamic> json) {
    return Order(
      id: json['id'] as String? ?? '',
      buyerId: json['buyer_id'] as String? ?? '',
      sellerId: json['seller_id'] as String? ?? '',
      status: json['status'] as String? ?? 'pending',
      total: (json['total'] as num?)?.toDouble() ?? 0.0,
      currency: json['currency'] as String? ?? 'USD',
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
