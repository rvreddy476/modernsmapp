// Product-tags repository — backs the in-video affiliate overlay.
// Wraps post-service's /v1/posts/:postId/product-tags endpoints.

import 'package:atpost_app/data/models/product_tag.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ProductTagsRepository {
  ProductTagsRepository(this._api);

  final ApiClient _api;

  // ─── reads ────────────────────────────────────────────────────────

  /// Fetch the active product tags on a post. Called once per video
  /// open; overlay state is derived locally from position cursor.
  Future<List<PostProductTag>> listByPost(String postId) async {
    final res = await _api.get('/v1/posts/$postId/product-tags');
    final raw = (res.data['data'] as List?) ?? const [];
    return raw
        .whereType<Map>()
        .map((m) => PostProductTag.fromJson(m.cast<String, dynamic>()))
        .toList();
  }

  // ─── writes ───────────────────────────────────────────────────────

  Future<PostProductTag> create({
    required String postId,
    required String affiliateLinkId,
    int? timeStartMs,
    int? timeEndMs,
    double? positionX,
    double? positionY,
    String? label,
    String? imageUrl,
  }) async {
    final body = <String, dynamic>{
      'affiliate_link_id': affiliateLinkId,
      'time_start_ms': ?timeStartMs,
      'time_end_ms': ?timeEndMs,
      'position_x': ?positionX,
      'position_y': ?positionY,
      'label': ?label,
      'image_url': ?imageUrl,
    };
    final res = await _api.post('/v1/posts/$postId/product-tags', data: body);
    return PostProductTag.fromJson(
      (res.data['data'] as Map).cast<String, dynamic>(),
    );
  }

  Future<void> delete({required String postId, required String tagId}) async {
    await _api.delete('/v1/posts/$postId/product-tags/$tagId');
  }

  // ─── analytics emitters ───────────────────────────────────────────
  //
  // Both are best-effort. The overlay calls them via unawaited so the
  // player frame loop never blocks on a network round-trip.

  Future<void> emitImpression({
    required String postId,
    required String tagId,
  }) async {
    try {
      await _api.post('/v1/posts/$postId/product-tags/$tagId/impression');
    } catch (_) {
      // best-effort; swallow
    }
  }

  Future<void> emitClick({
    required String postId,
    required String tagId,
  }) async {
    try {
      await _api.post('/v1/posts/$postId/product-tags/$tagId/click');
    } catch (_) {
      // best-effort; swallow
    }
  }
}

final productTagsRepositoryProvider = Provider<ProductTagsRepository>((ref) {
  return ProductTagsRepository(ref.watch(apiClientProvider));
});
