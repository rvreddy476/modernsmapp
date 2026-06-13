// AffiliateLinks repository — composer-side surface for the in-video
// product-tag flow. Wraps monetization-service's /v1/monetization/
// affiliate/* endpoints plus commerce-service's lightweight product
// preview.
//
// Why this lives next to the existing CommerceRepository instead of
// merging in: monetization-service is a separate service, the
// affiliate-link domain has its own conversion / payout story, and
// merging would hide the cross-service hop behind a single repo
// surface.

import 'package:atpost_app/data/models/affiliate_link.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class AffiliateLinksRepository {
  AffiliateLinksRepository(this._api);

  final ApiClient _api;

  /// Caller's active affiliate links. Default limit 50 matches the
  /// web composer's first-pane size.
  Future<List<AffiliateLink>> listMine({int limit = 50, int offset = 0}) async {
    final res = await _api.get(
      '/v1/monetization/affiliate/links',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final raw = (res.data['data'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => AffiliateLink.fromJson(m.cast<String, dynamic>()))
        .toList();
  }

  /// Lazily mint a new affiliate link when the creator picks a
  /// product they haven't linked before. Optional commission knobs —
  /// if both null, monetization-service falls back to its default.
  Future<AffiliateLink> create({
    required String listingId,
    double? commissionPct,
    double? commissionFlat,
  }) async {
    final res = await _api.post(
      '/v1/monetization/affiliate/links',
      data: <String, dynamic>{
        'listing_id': listingId,
        'commission_pct': ?commissionPct,
        'commission_flat': ?commissionFlat,
      },
    );
    return AffiliateLink.fromJson(
      (res.data['data'] as Map).cast<String, dynamic>(),
    );
  }

  /// Compact product preview for the picker chip + tag-create copy.
  /// Returns null on 404 so the caller can surface "unavailable"
  /// inline instead of throwing.
  Future<ProductPreview?> getProductPreview(String productId) async {
    try {
      final res = await _api.get('/v1/commerce/products/$productId/preview');
      final m = res.data['data'] as Map?;
      if (m == null) return null;
      return ProductPreview.fromJson(m.cast<String, dynamic>());
    } catch (_) {
      // 404 / network blip / etc — composer treats absence as null.
      return null;
    }
  }
}

final affiliateLinksRepositoryProvider = Provider<AffiliateLinksRepository>((ref) {
  return AffiliateLinksRepository(ref.watch(apiClientProvider));
});

/// Caller's active affiliate links, autoDispose so leaving the
/// composer drops the cached AsyncValue.
final myAffiliateLinksProvider =
    FutureProvider.autoDispose<List<AffiliateLink>>((ref) {
  return ref.watch(affiliateLinksRepositoryProvider).listMine();
});

/// Compact product preview keyed by listing ID. Used to fill in the
/// chip + the tag-create label/image_url payload.
final productPreviewProvider =
    FutureProvider.autoDispose.family<ProductPreview?, String>((ref, listingId) {
  return ref.watch(affiliateLinksRepositoryProvider).getProductPreview(listingId);
});
