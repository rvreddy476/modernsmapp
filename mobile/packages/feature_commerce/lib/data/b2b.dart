// Phase F4 mobile — B2B models: organizations (Phase 5 backend), RFQs
// (F2.2), and variant price tiers (F2.1). All shape-tolerant — empty
// or missing fields parse to sane defaults so a malformed response
// can't crash the buyer flow mid-checkout.

class Organization {
  const Organization({
    required this.id,
    required this.name,
    this.legalName,
    this.gstin,
    this.billingEmail,
    this.approvalThreshold,
    required this.creditTermsDays,
    required this.status,
  });

  final String id;
  final String name;
  final String? legalName;
  final String? gstin;
  final String? billingEmail;
  final double? approvalThreshold;
  final int creditTermsDays;
  final String status;

  factory Organization.fromJson(Map<String, dynamic> json) {
    return Organization(
      id: (json['id'] ?? '').toString(),
      name: (json['name'] ?? '').toString(),
      legalName: json['legal_name']?.toString(),
      gstin: json['gstin']?.toString(),
      billingEmail: json['billing_email']?.toString(),
      approvalThreshold: _tryDouble(json['approval_threshold']),
      creditTermsDays:
          (json['credit_terms_days'] is num)
              ? (json['credit_terms_days'] as num).toInt()
              : 0,
      status: (json['status'] ?? 'active').toString(),
    );
  }

  bool get hasCreditTerms => creditTermsDays > 0;
}

class PriceTier {
  const PriceTier({
    required this.minQty,
    this.maxQty,
    required this.price,
  });

  final int minQty;
  final int? maxQty;
  final double price;

  factory PriceTier.fromJson(Map<String, dynamic> json) {
    return PriceTier(
      minQty: (json['min_qty'] is num) ? (json['min_qty'] as num).toInt() : 1,
      maxQty: (json['max_qty'] is num) ? (json['max_qty'] as num).toInt() : null,
      price: _toDouble(json['price']),
    );
  }
}

class RFQ {
  const RFQ({
    required this.id,
    required this.buyerUserId,
    this.organizationId,
    required this.sellerId,
    required this.status,
    this.messageText,
    required this.requestedAt,
    required this.expiresAt,
  });

  final String id;
  final String buyerUserId;
  final String? organizationId;
  final String sellerId;
  final String status;
  final String? messageText;
  final DateTime requestedAt;
  final DateTime expiresAt;

  factory RFQ.fromJson(Map<String, dynamic> json) {
    return RFQ(
      id: (json['id'] ?? '').toString(),
      buyerUserId: (json['buyer_user_id'] ?? '').toString(),
      organizationId: json['organization_id']?.toString(),
      sellerId: (json['seller_id'] ?? '').toString(),
      status: (json['status'] ?? 'requested').toString(),
      messageText: json['message_text']?.toString(),
      requestedAt: _parseDate(json['requested_at']),
      expiresAt: _parseDate(json['expires_at']),
    );
  }
}

class RFQItem {
  const RFQItem({
    required this.id,
    required this.rfqId,
    required this.variantId,
    required this.quantity,
    this.notes,
  });

  final String id;
  final String rfqId;
  final String variantId;
  final int quantity;
  final String? notes;

  factory RFQItem.fromJson(Map<String, dynamic> json) {
    return RFQItem(
      id: (json['id'] ?? '').toString(),
      rfqId: (json['rfq_id'] ?? '').toString(),
      variantId: (json['variant_id'] ?? '').toString(),
      quantity: (json['quantity'] is num) ? (json['quantity'] as num).toInt() : 0,
      notes: json['notes']?.toString(),
    );
  }
}

class RFQQuoteLine {
  const RFQQuoteLine({
    required this.rfqItemId,
    required this.variantId,
    required this.quantity,
    required this.unitPrice,
    required this.lineTotal,
  });

  final String rfqItemId;
  final String variantId;
  final int quantity;
  final double unitPrice;
  final double lineTotal;

  factory RFQQuoteLine.fromJson(Map<String, dynamic> json) {
    return RFQQuoteLine(
      rfqItemId: (json['rfq_item_id'] ?? '').toString(),
      variantId: (json['variant_id'] ?? '').toString(),
      quantity: (json['quantity'] is num) ? (json['quantity'] as num).toInt() : 0,
      unitPrice: _toDouble(json['unit_price']),
      lineTotal: _toDouble(json['line_total']),
    );
  }
}

class RFQQuote {
  const RFQQuote({
    required this.id,
    required this.rfqId,
    required this.quotedTotal,
    required this.linePrices,
    required this.validityDays,
    required this.quotedAt,
    required this.expiresAt,
    this.acceptedAt,
    this.orderId,
  });

  final String id;
  final String rfqId;
  final double quotedTotal;
  final List<RFQQuoteLine> linePrices;
  final int validityDays;
  final DateTime quotedAt;
  final DateTime expiresAt;
  final DateTime? acceptedAt;
  final String? orderId;

  factory RFQQuote.fromJson(Map<String, dynamic> json) {
    final raw = json['line_prices'];
    final lines = <RFQQuoteLine>[];
    if (raw is List) {
      for (final l in raw) {
        if (l is Map) lines.add(RFQQuoteLine.fromJson(Map<String, dynamic>.from(l)));
      }
    }
    return RFQQuote(
      id: (json['id'] ?? '').toString(),
      rfqId: (json['rfq_id'] ?? '').toString(),
      quotedTotal: _toDouble(json['quoted_total']),
      linePrices: lines,
      validityDays:
          (json['validity_days'] is num) ? (json['validity_days'] as num).toInt() : 0,
      quotedAt: _parseDate(json['quoted_at']),
      expiresAt: _parseDate(json['expires_at']),
      acceptedAt: json['accepted_at'] is String
          ? DateTime.tryParse(json['accepted_at'] as String)
          : null,
      orderId: json['order_id']?.toString(),
    );
  }

  bool get isAccepted => acceptedAt != null;
  bool get isExpired => DateTime.now().isAfter(expiresAt);
}

class RFQDetail {
  const RFQDetail({required this.rfq, required this.items, required this.quotes});

  final RFQ rfq;
  final List<RFQItem> items;
  final List<RFQQuote> quotes;

  factory RFQDetail.fromJson(Map<String, dynamic> json) {
    final items = <RFQItem>[];
    if (json['items'] is List) {
      for (final it in json['items'] as List) {
        if (it is Map) items.add(RFQItem.fromJson(Map<String, dynamic>.from(it)));
      }
    }
    final quotes = <RFQQuote>[];
    if (json['quotes'] is List) {
      for (final q in json['quotes'] as List) {
        if (q is Map) quotes.add(RFQQuote.fromJson(Map<String, dynamic>.from(q)));
      }
    }
    return RFQDetail(
      rfq: RFQ.fromJson(Map<String, dynamic>.from(json['rfq'] as Map)),
      items: items,
      quotes: quotes,
    );
  }

  /// Most recent non-accepted quote for the buyer to act on.
  RFQQuote? get liveQuote {
    for (final q in quotes) {
      if (!q.isAccepted && !q.isExpired) return q;
    }
    return quotes.isNotEmpty ? quotes.first : null;
  }
}

double _toDouble(dynamic v) {
  if (v is double) return v;
  if (v is int) return v.toDouble();
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0.0;
  return 0.0;
}

double? _tryDouble(dynamic v) {
  if (v == null) return null;
  if (v is double) return v;
  if (v is int) return v.toDouble();
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v);
  return null;
}

DateTime _parseDate(dynamic v) {
  if (v is String) return DateTime.tryParse(v) ?? DateTime.now();
  return DateTime.now();
}
