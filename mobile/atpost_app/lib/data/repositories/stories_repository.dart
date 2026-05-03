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
    final response = await _api.get('/v1/stories/author/$userId');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    final grouped = _groupFlatStories(items);
    if (grouped.isNotEmpty) {
      return grouped.first;
    }

    final stories = await getFeedStories();
    for (final story in stories) {
      if (story.authorId == userId || story.id == userId) {
        return story;
      }
    }

    final legacyResponse = await _api.get('/v1/stories/$userId');
    return Story.fromJson(legacyResponse.data['data'] as Map<String, dynamic>);
  }

  Future<String> createStory({
    required String mediaId,
    required String mediaType,
    String? text,
    List<StoryInteractive> interactives = const [],
  }) async {
    final response = await _api.post(
      '/v1/stories',
      data: {
        'media_url': _mediaUrl(mediaId),
        'media_type': mediaType,
        'caption': text,
        'visibility': 'public',
      },
    );
    final data = response.data['data'];
    final storyId = data is Map ? data['id']?.toString() ?? '' : '';

    // Best-effort: persist any interactive elements after the story exists.
    // Backend currently has no handler for the /interactive subroute (the
    // schema is migrated but not wired in the post-service). Each call is
    // wrapped to stay non-fatal.
    for (final interactive in interactives) {
      if (storyId.isEmpty) break;
      try {
        await addInteractive(storyId: storyId, interactive: interactive);
      } catch (_) {
        // swallow — UI will still create the story; backend wire is a TODO.
      }
    }
    return storyId;
  }

  /// Attaches an interactive element (poll/quiz/countdown/question/slider)
  /// to an existing story.
  ///
  /// Wire (proposed):
  ///   POST /v1/stories/:storyId/interactive
  ///   body  -> StoryInteractive.toCreateJson()
  ///   resp  -> { data: { id, type, ... } }
  Future<StoryInteractive?> addInteractive({
    required String storyId,
    required StoryInteractive interactive,
  }) async {
    final response = await _api.post(
      '/v1/stories/$storyId/interactive',
      data: interactive.toCreateJson(),
    );
    final raw = response.data['data'];
    if (raw is Map) {
      return StoryInteractive.fromJson(Map<String, dynamic>.from(raw));
    }
    return null;
  }

  /// Submits a viewer's response to an interactive element.
  ///
  /// Wire (proposed):
  ///   POST /v1/stories/:storyId/interactive/:interactiveId/respond
  ///   body  -> { option_id?, text?, slider_value?, reminder? }
  Future<void> submitInteractiveResponse({
    required String storyId,
    required String interactiveId,
    String? optionId,
    String? text,
    int? sliderValue,
    bool? reminder,
  }) async {
    final body = <String, dynamic>{};
    if (optionId != null) body['option_id'] = optionId;
    if (text != null) body['text'] = text;
    if (sliderValue != null) body['slider_value'] = sliderValue;
    if (reminder != null) body['reminder'] = reminder;
    await _api.post(
      '/v1/stories/$storyId/interactive/$interactiveId/respond',
      data: body,
    );
  }

  /// Fetches aggregated results for the creator of the story.
  ///
  /// Wire (proposed):
  ///   GET /v1/stories/:storyId/interactive/:interactiveId/results
  ///   resp -> StoryInteractiveResults JSON
  Future<StoryInteractiveResults?> getInteractiveResults({
    required String storyId,
    required String interactiveId,
  }) async {
    final response = await _api.get(
      '/v1/stories/$storyId/interactive/$interactiveId/results',
    );
    final raw = response.data['data'];
    if (raw is Map) {
      return StoryInteractiveResults.fromJson(
        Map<String, dynamic>.from(raw),
      );
    }
    return null;
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
              'interactives': item['interactives'],
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
