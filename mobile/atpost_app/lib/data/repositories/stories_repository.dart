import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class StoriesRepository {
  final ApiClient _api;
  StoriesRepository(this._api);

  Future<List<Story>> getFeedStories() async {
    final response = await _api.get('/v1/stories/feed');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => Story.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<Story> getUserStories(String userId) async {
    final response = await _api.get('/v1/stories/$userId');
    return Story.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<void> createStory({
    required String mediaId,
    required String mediaType,
    String? text,
  }) async {
    await _api.post('/v1/stories', data: {
      'media_id': mediaId,
      'media_type': mediaType,
      'text': text,
    });
  }
}

final storiesRepositoryProvider = Provider<StoriesRepository>((ref) {
  return StoriesRepository(ref.watch(apiClientProvider));
});
