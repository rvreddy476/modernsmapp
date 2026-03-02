import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/memory.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class MemoriesRepository {
  final ApiClient _api;

  MemoriesRepository(this._api);

  /// Get "On This Day" memories.
  Future<List<OnThisDayMemory>> getOnThisDay() async {
    final response = await _api.get('${Environment.memoriesPath}/on-this-day');
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => OnThisDayMemory.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// List memory collections.
  Future<List<MemoryCollection>> getCollections({int limit = 20}) async {
    final response = await _api.get(
      '${Environment.memoriesPath}/collections',
      queryParameters: {'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => MemoryCollection.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Create a memory collection.
  Future<MemoryCollection> createCollection({
    required String title,
    String description = '',
    String visibility = 'private',
  }) async {
    final response = await _api.post(
      '${Environment.memoriesPath}/collections',
      data: {'title': title, 'description': description, 'visibility': visibility},
    );
    return MemoryCollection.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Add item to a collection.
  Future<void> addCollectionItem(String collectionId, {String? postId, String? mediaUrl, String caption = ''}) async {
    await _api.post(
      '${Environment.memoriesPath}/collections/$collectionId/items',
      data: {'post_id': postId, 'media_url': mediaUrl, 'caption': caption},
    );
  }

  /// Delete a collection.
  Future<void> deleteCollection(String collectionId) async {
    await _api.delete('${Environment.memoriesPath}/collections/$collectionId');
  }
}

final memoriesRepositoryProvider = Provider<MemoriesRepository>((ref) {
  return MemoriesRepository(ref.watch(apiClientProvider));
});
