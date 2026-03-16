import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SearchExtrasRepository {
  final ApiClient _api;

  SearchExtrasRepository(this._api);

  Future<List<Map<String, dynamic>>> getSavedSearches() async {
    final response = await _api.get('/v1/search/saved');
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        (response.data as List<dynamic>?) ??
        [];
    return items.cast<Map<String, dynamic>>();
  }

  Future<void> saveSearch(String query, String searchType) async {
    await _api.post('/v1/search/saved', data: {
      'query': query,
      'search_type': searchType,
    });
  }

  Future<void> deleteSavedSearch(String id) async {
    await _api.delete('/v1/search/saved/$id');
  }

  Future<List<Map<String, dynamic>>> getSearchHistory() async {
    final response = await _api.get('/v1/search/history');
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        (response.data as List<dynamic>?) ??
        [];
    return items.cast<Map<String, dynamic>>();
  }

  Future<void> clearSearchHistory() async {
    await _api.delete('/v1/search/history');
  }

  Future<List<Map<String, dynamic>>> searchProducts(String query) async {
    final response = await _api.get(
      '/v1/search/products',
      queryParameters: {'q': query},
    );
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items.cast<Map<String, dynamic>>();
  }

  Future<List<Map<String, dynamic>>> searchEvents(String query) async {
    final response = await _api.get(
      '/v1/search/events',
      queryParameters: {'q': query},
    );
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items.cast<Map<String, dynamic>>();
  }

  Future<List<Map<String, dynamic>>> searchMessages(String query) async {
    final response = await _api.get(
      '/v1/search/messages',
      queryParameters: {'q': query},
    );
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        (response.data['data'] as List<dynamic>?) ??
        [];
    return items.cast<Map<String, dynamic>>();
  }
}

final searchExtrasRepositoryProvider =
    Provider<SearchExtrasRepository>((ref) {
  return SearchExtrasRepository(ref.watch(apiClientProvider));
});
