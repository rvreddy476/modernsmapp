/// Phase F1.2 — extended to match the commerce-service order DTO.
/// Reads from both the `/v1/commerce/orders` keyset list (rich order
/// cards) and the `/v1/commerce/orders/:id/items` detail endpoint
/// (`{order, items}` wrapper). All new fields are nullable so the same
/// model serves both surfaces.
class Order {
  final String id;
  final String orderNumber;
  final String status; // payment_pending | awaiting_approval | confirmed | packed | shipped | in_transit | delivered | cancelled | return_approved
  final String paymentStatus; // pending | processing | paid | failed | refund_*
  final String? paymentMethod; // prepaid | cod | credit | razorpay
  final List<OrderItem> items;
  final double total;
  final double subtotal;
  final double shippingCharges;
  final double taxAmount;
  final double discountAmount;
  final String currency;
  final String? shippingAddress;
  final DateTime createdAt;
  final DateTime? estimatedDelivery;
  // Phase 2 order-card extras (only populated on the list endpoint).
  final int? itemCount;
  final int? sellerCount;
  final String? primaryItemTitle;
  final String? primaryItemImageMediaId;
  // Phase 5 B2B fields (only populated on org orders).
  final String? organizationId;
  final String? poNumber;
  final String? approvalStatus;
  final int? creditTermsDays;
  final DateTime? paymentDueDate;

  const Order({
    required this.id,
    required this.orderNumber,
    required this.status,
    required this.paymentStatus,
    this.paymentMethod,
    required this.items,
    required this.total,
    this.subtotal = 0,
    this.shippingCharges = 0,
    this.taxAmount = 0,
    this.discountAmount = 0,
    this.currency = 'INR',
    this.shippingAddress,
    required this.createdAt,
    this.estimatedDelivery,
    this.itemCount,
    this.sellerCount,
    this.primaryItemTitle,
    this.primaryItemImageMediaId,
    this.organizationId,
    this.poNumber,
    this.approvalStatus,
    this.creditTermsDays,
    this.paymentDueDate,
  });

  factory Order.fromJson(Map<String, dynamic> json) {
    // The detail endpoint wraps the order in {order, items} — unwrap if
    // present so callers don't need to know which endpoint they hit.
    final root = (json['order'] is Map<String, dynamic>)
        ? json['order'] as Map<String, dynamic>
        : json;
    final itemsRaw = (json['items'] as List<dynamic>?) ?? const [];

    return Order(
      id: root['id'] as String? ?? root['order_id'] as String? ?? '',
      orderNumber: root['order_number'] as String? ?? '',
      status: root['status'] as String? ?? 'pending',
      paymentStatus: root['payment_status'] as String? ?? 'pending',
      paymentMethod: root['payment_method'] as String?,
      items: itemsRaw
          .map((e) => OrderItem.fromJson(e as Map<String, dynamic>))
          .toList(),
      total: _toDouble(root['final_amount'] ?? root['total']),
      subtotal: _toDouble(root['subtotal']),
      shippingCharges: _toDouble(root['shipping_charges']),
      taxAmount: _toDouble(root['tax_amount']),
      discountAmount: _toDouble(root['discount_amount']),
      currency: root['currency_code'] as String? ?? root['currency'] as String? ?? 'INR',
      shippingAddress: root['shipping_address'] as String?,
      createdAt: _parseDate(root['created_at']),
      estimatedDelivery: _tryDate(root['estimated_delivery']),
      itemCount: root['item_count'] as int?,
      sellerCount: root['seller_count'] as int?,
      primaryItemTitle: root['primary_item_title'] as String?,
      primaryItemImageMediaId: root['primary_item_image_media_id'] as String?,
      organizationId: root['organization_id'] as String?,
      poNumber: root['po_number'] as String?,
      approvalStatus: root['approval_status'] as String?,
      creditTermsDays: root['credit_terms_days'] as int?,
      paymentDueDate: _tryDate(root['payment_due_date']),
    );
  }

  bool get isActive => const [
        'payment_pending',
        'awaiting_approval',
        'confirmed',
        'packed',
        'shipped',
        'in_transit',
      ].contains(status);

  bool get awaitingApproval => approvalStatus == 'pending';

  bool get isCreditOrder => (creditTermsDays ?? 0) > 0;
}

class OrderItem {
  final String id;
  final String productId;
  final String? variantId;
  final String sellerId;
  final String productTitle;
  final String? imageMediaId;
  final String sku;
  final int quantity;
  final double unitPrice;
  final double finalPrice;
  final String status;

  const OrderItem({
    required this.id,
    required this.productId,
    this.variantId,
    required this.sellerId,
    required this.productTitle,
    this.imageMediaId,
    required this.sku,
    required this.quantity,
    required this.unitPrice,
    required this.finalPrice,
    required this.status,
  });

  factory OrderItem.fromJson(Map<String, dynamic> json) {
    return OrderItem(
      id: json['id'] as String? ?? '',
      productId: json['product_id'] as String? ?? '',
      variantId: json['variant_id'] as String?,
      sellerId: json['seller_id'] as String? ?? '',
      productTitle:
          json['product_title'] as String? ?? json['product_name'] as String? ?? '',
      imageMediaId:
          json['image_media_id'] as String? ?? json['primary_image_media_id'] as String?,
      sku: json['sku'] as String? ?? '',
      quantity: (json['quantity'] as num?)?.toInt() ?? 1,
      unitPrice: _toDouble(json['unit_price'] ?? json['price']),
      finalPrice: _toDouble(json['final_price'] ?? json['price']),
      status: json['status'] as String? ?? '',
    );
  }
}

double _toDouble(dynamic v) {
  if (v is double) return v;
  if (v is int) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0.0;
  if (v is num) return v.toDouble();
  return 0.0;
}

DateTime _parseDate(dynamic v) {
  if (v is String) return DateTime.tryParse(v) ?? DateTime.now();
  return DateTime.now();
}

DateTime? _tryDate(dynamic v) {
  if (v is String && v.isNotEmpty) return DateTime.tryParse(v);
  return null;
}
