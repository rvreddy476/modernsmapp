
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

  /// Creates a story and returns its id.
  ///
  /// Module 4 M4-P0-6. Two changes to the request, both required by the
  /// backend contract:
  ///
  ///  * `media_id` replaces `media_url`. The server resolves the canonical
  ///    asset itself and verifies the caller owns it, so a client-built URL is
  ///    no longer accepted — it never proved anything about ownership.
  ///  * `visibility` is the creator's choice. It used to be hardcoded to
  ///    'public', which silently published every story to everyone regardless
  ///    of what the user believed they were sharing.
  ///
  /// The response is 202: the story exists but is NOT yet visible to anyone
  /// else. Callers must surface the pending state rather than implying the
  /// story is live.
  Future<String> createStory({
    required String mediaId,
    required String mediaType,
    required StoryAudience audience,
    String? text,
    String? idempotencyKey,
    List<StoryInteractive> interactives = const [],
  }) async {
    final response = await _api.post(
      '/v1/stories',
      data: {
        'media_id': mediaId,
        'media_type': mediaType,
        'caption': text,
        'visibility': audience.wireValue,
        'idempotency_key': ?idempotencyKey,
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

// _mediaUrl was removed with M4-P0-6. The client no longer constructs a media
// URL for story creation: it sends the canonical media_id and the server
// resolves and authorizes delivery itself. A client-built URL asserted nothing
// about ownership and could not be authorized.

final storiesRepositoryProvider = Provider<StoriesRepository>((ref) {
  return StoriesRepository(ref.watch(apiClientProvider));
});

/// Who a story is shared with.
///
/// Module 4 M4-P0-6. The wire values match the server's closed set exactly; an
/// unrecognised visibility is denied server-side rather than defaulted to
/// visible, so a typo here fails closed rather than over-sharing.
enum StoryAudience {
  /// Anyone signed in who is not blocked.
  public,

  /// Accounts that follow the author.
  followers,

  /// Only accounts the author has put on their close friends list.
  closeFriends,
}

extension StoryAudienceWire on StoryAudience {
  String get wireValue {
    switch (this) {
      case StoryAudience.public:
        return 'public';
      case StoryAudience.followers:
        return 'followers';
      case StoryAudience.closeFriends:
        return 'close_friends';
    }
  }

  String get label {
    switch (this) {
      case StoryAudience.public:
        return 'Everyone';
      case StoryAudience.followers:
        return 'Followers';
      case StoryAudience.closeFriends:
        return 'Close friends';
    }
  }
}
