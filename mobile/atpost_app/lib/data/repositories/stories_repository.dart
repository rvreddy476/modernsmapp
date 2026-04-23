import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class StoriesRepository {
  final ApiClient _api;
  StoriesRepository(this._api);

  Future<List<Story>> getFeedStories() async {
    final response = await _api.get('/v1/stories/feed');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return _groupFlatStories(items);
  }

  Future<Story> getUserStories(String userId) async {
    final stories = await getFeedStories();
    for (final story in stories) {
      if (story.authorId == userId || story.id == userId) return story;
    }

    final response = await _api.get('/v1/stories/$userId');
    return Story.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<void> createStory({
    required String mediaId,
    required String mediaType,
    String? text,
  }) async {
    await _api.post(
      '/v1/stories',
      data: {
        'media_url': _mediaUrl(mediaId),
        'media_type': mediaType,
        'caption': text,
        'visibility': 'public',
      },
    );
  }
}

List<Story> _groupFlatStories(List<dynamic> rawItems) {
  final grouped = <String, List<Map<String, dynamic>>>{};

  for (final raw in rawItems) {
    if (raw is! Map) continue;
    final item = Map<String, dynamic>.from(raw);
    final authorId = item['author_id']?.toString() ?? '';
    if (authorId.isEmpty) continue;
    grouped.putIfAbsent(authorId, () => []).add(item);
  }

  return grouped.entries.map((entry) {
    final first = entry.value.first;
    return Story.fromJson({
      'id': first['id'],
      'author_id': entry.key,
      'author_name': first['author_name'],
      'avatar_media_id': first['avatar_media_id'],
      'created_at': first['created_at'],
      'items': entry.value
          .map(
            (item) => {
              'id': item['id'],
              'media_url': item['media_url'],
              'media_type': item['media_type'],
              'text': item['caption'],
              'expires_at': item['expires_at'],
            },
          )
          .toList(),
    });
  }).toList();
}

String _mediaUrl(String mediaId) {
  final trimmed = mediaId.trim();
  if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
    return trimmed;
  }
  return '${Environment.apiBaseUrl}/v1/media/$trimmed/serve';
}

final storiesRepositoryProvider = Provider<StoriesRepository>((ref) {
  return StoriesRepository(ref.watch(apiClientProvider));
});
