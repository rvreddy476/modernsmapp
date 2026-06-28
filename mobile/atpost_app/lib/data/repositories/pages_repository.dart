import 'package:atpost_app/data/models/business_page.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Follow-Only Public Pages — talks to user-service `/v1/pages/*`.
class PagesRepository {
  final ApiClient _api;
  PagesRepository(this._api);

  Future<List<BusinessPage>> discover({String? category, String? search, int limit = 30, int offset = 0}) async {
    final params = <String, dynamic>{'limit': limit, 'offset': offset};
    if (category != null && category.isNotEmpty) params['category'] = category;
    if (search != null && search.isNotEmpty) params['q'] = search;
    final res = await _api.get('/v1/pages', queryParameters: params);
    final items = (res.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => BusinessPage.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<List<BusinessPage>> myPages() async {
    final res = await _api.get('/v1/users/me/pages');
    final items = (res.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => BusinessPage.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Detail by handle OR id — returns the enriched envelope (actions, buttons).
  Future<BusinessPage> getPage(String handleOrId) async {
    final res = await _api.get('/v1/pages/$handleOrId');
    final payload = (res.data['data'] as Map<String, dynamic>?) ?? res.data as Map<String, dynamic>;
    return BusinessPage.fromJson(payload);
  }

  Future<BusinessPage> createPage({
    required String pageHandle,
    required String pageName,
    required String pageType,
    String? category,
    String? description,
    String? phone,
    String? website,
  }) async {
    final res = await _api.post('/v1/users/me/pages', data: {
      'page_handle': pageHandle,
      'page_name': pageName,
      'page_type': pageType,
      'category': ?category,
      'description': ?description,
      'phone': ?phone,
      'website': ?website,
    });
    final payload = (res.data['data'] as Map<String, dynamic>?) ?? res.data as Map<String, dynamic>;
    return BusinessPage.fromJson(payload);
  }

  /// Returns the new follower count.
  Future<int> follow(String pageId) async {
    final res = await _api.post('/v1/pages/$pageId/follow');
    final d = (res.data['data'] as Map<String, dynamic>?) ?? const {};
    return (d['followerCount'] as num?)?.toInt() ?? 0;
  }

  Future<int> unfollow(String pageId) async {
    final res = await _api.delete('/v1/pages/$pageId/follow');
    final d = (res.data['data'] as Map<String, dynamic>?) ?? const {};
    return (d['followerCount'] as num?)?.toInt() ?? 0;
  }

  // --- Verification documents + lifecycle ---

  Future<List<PageDocument>> listDocuments(String pageId) async {
    final res = await _api.get('/v1/pages/$pageId/documents');
    final items = (res.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => PageDocument.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<void> addDocument(String pageId, String documentType, String documentUrl) async {
    await _api.post('/v1/pages/$pageId/documents', data: {
      'document_type': documentType,
      'document_url': documentUrl,
    });
  }

  Future<void> submitForReview(String pageId) async {
    await _api.post('/v1/pages/$pageId/submit-review');
  }
}

final pagesRepositoryProvider = Provider<PagesRepository>((ref) {
  return PagesRepository(ref.watch(apiClientProvider));
});
