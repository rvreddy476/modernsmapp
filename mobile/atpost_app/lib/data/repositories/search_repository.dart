// Repository for the search-service multi-entity ranked API.
//
// The existing `SearchExtrasRepository` covers saved searches, history,
// products/events/messages legacy endpoints; this repository is just the
// new ranked surfaces (multi-entity search, click analytics, multi-entity
// autocomplete).

import 'package:atpost_app/data/models/search_results.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SearchRepository {
  final ApiClient _api;

  SearchRepository(this._api);

  /// Hit GET /v1/search with `types=` to invoke the ranked multi-entity
  /// path. `cursors` keys are the entity wire names (`posts`, `users`,
  /// ...) and map to opaque next_cursor strings the server returned on
  /// the previous page.
  Future<MultiEntitySearchResults> multiEntitySearch({
    required String query,
    List<SearchEntity>? types,
    int limit = 20,
    Map<SearchEntity, String> cursors = const {},
  }) async {
    final entities = (types == null || types.isEmpty)
        ? SearchEntity.values
        : types;
    final params = <String, dynamic>{
      'q': query,
      'types': entities.map((e) => e.wire).join(','),
      'limit': limit,
    };
    cursors.forEach((entity, cursor) {
      if (cursor.isNotEmpty) {
        params['cursor.${entity.wire}'] = cursor;
      }
    });

    final response = await _api.get('/v1/search', queryParameters: params);
    final data = response.data is Map<String, dynamic>
        ? (response.data['data'] as Map<String, dynamic>? ??
            <String, dynamic>{})
        : <String, dynamic>{};
    return MultiEntitySearchResults.fromJson(data);
  }

  /// Best-effort click analytics. Backend returns 204 unconditionally,
  /// so we swallow any network error — never block navigation on this.
  Future<void> recordClick({
    required String queryId,
    required SearchEntity entityType,
    required String entityId,
    required int position,
  }) async {
    try {
      await _api.post('/v1/search/click', data: {
        'query_id': queryId,
        'entity_type': entityType.wire,
        'entity_id': entityId,
        'position': position,
      });
    } catch (_) {
      // Best-effort. Click analytics aren't load-bearing.
    }
  }

  /// Multi-entity autocomplete. Pass `kinds: 'users'` to get the
  /// legacy users-only shape (for @mention pickers etc.); default is
  /// the new merged users + hashtags + communities surface.
  Future<List<AutocompleteItem>> autocomplete({
    required String query,
    String? kinds,
    int limit = 8,
  }) async {
    final params = <String, dynamic>{'q': query, 'limit': limit};
    if (kinds != null && kinds.isNotEmpty) params['kinds'] = kinds;

    final response =
        await _api.get('/v1/search/autocomplete', queryParameters: params);
    final data = response.data is Map<String, dynamic>
        ? (response.data['data'] as Map<String, dynamic>? ??
            <String, dynamic>{})
        : <String, dynamic>{};
    // New shape: {results: [...]}. Older builds returned a flat list.
    final raw = (data['results'] as List<dynamic>?) ??
        (data['items'] as List<dynamic>?) ??
        (response.data is List ? response.data as List<dynamic> : const []);
    return raw
        .map((e) => AutocompleteItem.fromJson(e as Map<String, dynamic>))
        .toList(growable: false);
  }
}

final searchRepositoryProvider = Provider<SearchRepository>((ref) {
  return SearchRepository(ref.watch(apiClientProvider));
});
