// StoriesRepository tests against the post-service stories routes
// (POST /v1/stories, GET /feed, GET /author/:id). The interactive
// subroutes are a documented backend TODO — creation must stay
// best-effort and never fail the story itself.
import 'package:social_domain/data/stories_repository.dart';
import 'package:social_domain/story.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;
  late StoriesRepository repo;

  setUp(() {
    api = MockApiClient();
    repo = StoriesRepository(api);
  });

  group('getFeedStories', () {
    test('groups the flat feed rows into one story per author', () async {
      when(() => api.get('/v1/stories/feed')).thenAnswer(
        (_) async => ok({
          'data': [
            {
              'id': 's1',
              'author_id': 'alice',
              'author_name': 'Alice',
              'media_url': 'https://cdn/x1.jpg',
              'media_type': 'image',
              'caption': 'first',
              'created_at': '2026-07-10T10:00:00Z',
              'expires_at': '2026-07-11T10:00:00Z',
            },
            {
              'id': 's2',
              'author_id': 'alice',
              'author_name': 'Alice',
              'media_url': 'https://cdn/x2.mp4',
              'media_type': 'video',
              'caption': 'second',
              'created_at': '2026-07-10T11:00:00Z',
              'expires_at': '2026-07-11T11:00:00Z',
            },
            {
              'id': 's3',
              'author_id': 'bob',
              'author_name': 'Bob',
              'media_url': 'https://cdn/y1.jpg',
              'media_type': 'image',
              'created_at': '2026-07-10T12:00:00Z',
              'expires_at': '2026-07-11T12:00:00Z',
            },
            // Rows without an author must be dropped, not crash.
            {'id': 'sX', 'media_url': 'https://cdn/orphan.jpg'},
          ],
        }),
      );

      final stories = await repo.getFeedStories();

      expect(stories, hasLength(2));
      final alice = stories.singleWhere((s) => s.authorId == 'alice');
      expect(alice.authorName, 'Alice');
      expect(alice.items, hasLength(2));
      // caption maps onto StoryItem.text
      expect(alice.items.first.text, 'first');
      final bob = stories.singleWhere((s) => s.authorId == 'bob');
      expect(bob.items, hasLength(1));
    });
  });

  group('createStory', () {
    test('posts media_url built from a media id and returns the story id',
        () async {
      when(() => api.post('/v1/stories', data: any(named: 'data'))).thenAnswer(
        (_) async => ok({
          'data': {'id': 'story-9'},
        }),
      );

      final id = await repo.createStory(mediaId: 'm123', mediaType: 'image');

      expect(id, 'story-9');
      final sent = verify(() =>
              api.post('/v1/stories', data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent['media_url'], endsWith('/v1/media/m123/serve'));
      expect(sent['media_type'], 'image');
      expect(sent['visibility'], 'public');
    });

    test('passes absolute media URLs through untouched', () async {
      when(() => api.post('/v1/stories', data: any(named: 'data'))).thenAnswer(
        (_) async => ok({
          'data': {'id': 'story-10'},
        }),
      );

      await repo.createStory(
        mediaId: 'https://cdn.example.com/x.jpg',
        mediaType: 'image',
      );

      final sent = verify(() =>
              api.post('/v1/stories', data: captureAny(named: 'data')))
          .captured
          .single as Map<String, dynamic>;
      expect(sent['media_url'], 'https://cdn.example.com/x.jpg');
    });

    test('a failing interactive subroute never fails the story create',
        () async {
      when(() => api.post('/v1/stories', data: any(named: 'data'))).thenAnswer(
        (_) async => ok({
          'data': {'id': 'story-11'},
        }),
      );
      // Backend has no /interactive handler yet — 404s today.
      when(() => api.post('/v1/stories/story-11/interactive',
              data: any(named: 'data')))
          .thenThrow(httpError(404));

      final id = await repo.createStory(
        mediaId: 'm1',
        mediaType: 'image',
        interactives: [
          const StoryInteractive(id: '', type: 'poll', question: 'Q?'),
        ],
      );

      expect(id, 'story-11');
    });
  });

  test('submitInteractiveResponse sends only the provided fields', () async {
    when(() => api.post('/v1/stories/s1/interactive/i1/respond',
            data: any(named: 'data')))
        .thenAnswer((_) async => ok({'data': <String, dynamic>{}}));

    await repo.submitInteractiveResponse(
      storyId: 's1',
      interactiveId: 'i1',
      optionId: 'opt-2',
    );

    final sent = verify(() => api.post('/v1/stories/s1/interactive/i1/respond',
            data: captureAny(named: 'data')))
        .captured
        .single as Map<String, dynamic>;
    expect(sent, {'option_id': 'opt-2'});
  });

  test('getUserStories falls back from author endpoint to the feed', () async {
    when(() => api.get('/v1/stories/author/carol'))
        .thenAnswer((_) async => ok({'data': <dynamic>[]}));
    when(() => api.get('/v1/stories/feed')).thenAnswer(
      (_) async => ok({
        'data': [
          {
            'id': 's5',
            'author_id': 'carol',
            'author_name': 'Carol',
            'media_url': 'https://cdn/c.jpg',
            'media_type': 'image',
            'created_at': '2026-07-10T09:00:00Z',
            'expires_at': '2026-07-11T09:00:00Z',
          },
        ],
      }),
    );

    final story = await repo.getUserStories('carol');

    expect(story.authorId, 'carol');
    expect(story.items, hasLength(1));
  });
}
