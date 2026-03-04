class Order {
  final String id;
  final String status; // 'pending', 'confirmed', 'shipped', 'delivered', 'cancelled'
  final List<OrderItem> items;
  final double total;
  final String currency;
  final String? shippingAddress;
  final DateTime createdAt;
  final DateTime? estimatedDelivery;

  const Order({
    required this.id,
    required this.status,
    required this.items,
    required this.total,
    this.currency = 'INR',
    this.shippingAddress,
    required this.createdAt,
    this.estimatedDelivery,
  });

  factory Order.fromJson(Map<String, dynamic> json) {
    return Order(
      id: json['id'] as String? ?? json['order_id'] as String? ?? '',
      status: json['status'] as String? ?? 'pending',
      items: ((json['items'] as List<dynamic>?) ?? [])
          .map((e) => OrderItem.fromJson(e as Map<String, dynamic>))
          .toList(),
      total: (json['total'] as num?)?.toDouble() ?? 0.0,
      currency: json['currency'] as String? ?? 'INR',
      shippingAddress: json['shipping_address'] as String?,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
      estimatedDelivery: json['estimated_delivery'] != null
          ? DateTime.parse(json['estimated_delivery'] as String)
          : null,
    );
  }

  bool get isActive => ['pending', 'confirmed', 'shipped'].contains(status);
}

class OrderItem {
  final String productId;
  final String productName;
  final String? imageMediaId;
  final int quantity;
  final double price;

  const OrderItem({
    required this.productId,
    required this.productName,
    this.imageMediaId,
    required this.quantity,
    required this.price,
  });

  factory OrderItem.fromJson(Map<String, dynamic> json) {
    return OrderItem(
      productId: json['product_id'] as String? ?? '',
      productName: json['product_name'] as String? ?? '',
      imageMediaId: json['image_media_id'] as String?,
      quantity: json['quantity'] as int? ?? 1,
      price: (json['price'] as num?)?.toDouble() ?? 0.0,
    );
  }
}
