import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import '../../helpers/mocks.dart';
import '../../helpers/test_utils.dart';

void main() {
  late MockApiClient mockApi;
  late PostRepository repo;

  setUp(() {
    mockApi = MockApiClient();
    repo = PostRepository(mockApi);
  });

  group('toggleReaction', () {
    test('posts default reaction type', () async {
      when(
        () => mockApi.post(any(), data: any(named: 'data')),
      ).thenAnswer((_) async => mockResponse(data: {}));

      await repo.toggleReaction('post-1');

      verify(
        () => mockApi.post(
          '/v1/posts/post-1/react',
          data: {'reaction_type': 'like'},
        ),
      ).called(1);
    });

    test('posts mapped custom reaction type', () async {
      when(
        () => mockApi.post(any(), data: any(named: 'data')),
      ).thenAnswer((_) async => mockResponse(data: {}));

      await repo.toggleReaction('post-1', emoji: '🔥');

      verify(
        () => mockApi.post(
          '/v1/posts/post-1/react',
          data: {'reaction_type': 'love'},
        ),
      ).called(1);
    });
  });

  group('getComments', () {
    test('parses comment list from data array', () async {
      when(() => mockApi.get(any())).thenAnswer(
        (_) async => mockResponse(
          data: {
            'data': [
              {
                'id': 'c1',
                'post_id': 'p1',
                'user_id': 'u1',
                'text': 'Nice!',
                'created_at': '2026-03-16T10:00:00Z',
              },
              {
                'id': 'c2',
                'post_id': 'p1',
                'user_id': 'u2',
                'text': 'Great!',
                'created_at': '2026-03-16T11:00:00Z',
              },
            ],
          },
        ),
      );

      final comments = await repo.getComments('p1');
      expect(comments.length, 2);
      expect(comments[0].text, 'Nice!');
      expect(comments[1].authorId, 'u2');
    });

    test('returns empty list when data is null', () async {
      when(
        () => mockApi.get(any()),
      ).thenAnswer((_) async => mockResponse(data: {'data': null}));

      final comments = await repo.getComments('p1');
      expect(comments, isEmpty);
    });
  });

  group('createPost', () {
    test('sends all fields in POST payload', () async {
      when(
        () => mockApi.post(any(), data: any(named: 'data')),
      ).thenAnswer((_) async => mockResponse(data: {}));

      await repo.createPost(
        text: 'Hello world',
        contentType: 'post',
        visibility: 'public',
        tags: ['test'],
        mediaIds: ['media-1'],
        feeling: 'happy',
      );

      final captured =
          verify(
                () => mockApi.post(any(), data: captureAny(named: 'data')),
              ).captured.single
              as Map<String, dynamic>;

      expect(captured['text'], 'Hello world');
      expect(captured['content_type'], 'post');
      expect(captured['tags'], ['test']);
      expect(captured['feeling'], 'happy');
    });
  });
}
