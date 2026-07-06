// Mirrors monetization-service's AffiliateLink postgres row. Web
// equivalent: postbook-ui/src/hooks/useAffiliateLinks.ts AffiliateLink.

class AffiliateLink {
  AffiliateLink({
    required this.id,
    required this.creatorId,
    required this.listingId,
    required this.commissionPct,
    required this.linkCode,
    required this.clickCount,
    required this.conversionCount,
    required this.totalEarned,
    required this.isActive,
    required this.createdAt,
    this.commissionFlat,
  });

  final String id;
  final String creatorId;
  final String listingId;
  final double commissionPct;
  final double? commissionFlat;
  final String linkCode;
  final int clickCount;
  final int conversionCount;
  final double totalEarned;
  final bool isActive;
  final DateTime createdAt;

  factory AffiliateLink.fromJson(Map<String, dynamic> json) => AffiliateLink(
        id: json['id'] as String,
        creatorId: json['creator_id'] as String,
        listingId: json['listing_id'] as String,
        commissionPct: (json['commission_pct'] as num?)?.toDouble() ?? 0.0,
        commissionFlat: (json['commission_flat'] as num?)?.toDouble(),
        linkCode: (json['link_code'] as String?) ?? '',
        clickCount: (json['click_count'] as num?)?.toInt() ?? 0,
        conversionCount: (json['conversion_count'] as num?)?.toInt() ?? 0,
        totalEarned: (json['total_earned'] as num?)?.toDouble() ?? 0.0,
        isActive: (json['is_active'] as bool?) ?? true,
        createdAt: json['created_at'] != null
            ? DateTime.tryParse(json['created_at'] as String) ?? DateTime.now()
            : DateTime.now(),
      );
}

/// Compact product projection from /v1/commerce/products/:id/preview.
/// Composer renders chips from it; tag-create copies title/image into
/// the tag's cached label/image_url fields.
class ProductPreview {
  ProductPreview({
    required this.id,
    required this.title,
    required this.slug,
    required this.status,
    required this.visibility,
    this.primaryImageMediaId,
    this.price,
    this.currency,
  });

  final String id;
  final String title;
  final String slug;
  final String? primaryImageMediaId;
  final double? price;
  final String? currency;
  final String status;
  final String visibility;

  factory ProductPreview.fromJson(Map<String, dynamic> json) => ProductPreview(
        id: json['id'] as String,
        title: (json['title'] as String?) ?? '',
        slug: (json['slug'] as String?) ?? '',
        primaryImageMediaId: json['primary_image_media_id'] as String?,
        price: (json['price'] as num?)?.toDouble(),
        currency: json['currency'] as String?,
        status: (json['status'] as String?) ?? '',
        visibility: (json['visibility'] as String?) ?? '',
      );
}
