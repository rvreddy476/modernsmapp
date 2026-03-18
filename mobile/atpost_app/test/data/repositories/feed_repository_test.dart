import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import '../../helpers/mocks.dart';
import '../../helpers/test_utils.dart';

void main() {
  late MockApiClient mockApi;
  late FeedRepository repo;

  setUp(() {
    mockApi = MockApiClient();
    repo = FeedRepository(mockApi);
  });

  group('getHomeFeed', () {
    test('parses {"data": [...]} format', () async {
      when(() => mockApi.get(any(), queryParameters: any(named: 'queryParameters')))
          .thenAnswer((_) async => mockResponse(data: {
                'data': [
                  {'id': 'p1', 'author_id': 'u1', 'content': 'Hello', 'created_at': '2026-03-16T00:00:00Z'},
                  {'id': 'p2', 'author_id': 'u2', 'content': 'World', 'created_at': '2026-03-16T00:00:00Z'},
                ],
              }));

      final posts = await repo.getHomeFeed();
      expect(posts.length, 2);
      expect(posts[0].id, 'p1');
      expect(posts[1].content, 'World');
    });

    test('parses {"data": {"items": [...]}} format', () async {
      when(() => mockApi.get(any(), queryParameters: any(named: 'queryParameters')))
          .thenAnswer((_) async => mockResponse(data: {
                'data': {
                  'items': [
                    {'id': 'p1', 'author_id': 'u1', 'content': 'Nested', 'created_at': '2026-03-16T00:00:00Z'},
                  ],
                },
              }));

      final posts = await repo.getHomeFeed();
      expect(posts.length, 1);
      expect(posts[0].content, 'Nested');
    });

    test('returns empty list for null data', () async {
      when(() => mockApi.get(any(), queryParameters: any(named: 'queryParameters')))
          .thenAnswer((_) async => mockResponse(data: {'data': null}));

      final posts = await repo.getHomeFeed();
      expect(posts, isEmpty);
    });

    test('passes cursor and feedMode as query params', () async {
      when(() => mockApi.get(any(), queryParameters: any(named: 'queryParameters')))
          .thenAnswer((_) async => mockResponse(data: {'data': []}));

      await repo.getHomeFeed(cursor: 'abc123', feedMode: 'following');

      final captured = verify(() => mockApi.get(any(), queryParameters: captureAny(named: 'queryParameters'))).captured.single as Map<String, dynamic>;
      expect(captured['cursor'], 'abc123');
      expect(captured['feed_mode'], 'following');
    });
  });

  group('getReelFeedPage', () {
    test('returns FeedPage with nextCursor from meta', () async {
      when(() => mockApi.get(any(), queryParameters: any(named: 'queryParameters')))
          .thenAnswer((_) async => mockResponse(data: {
                'data': [
                  {'id': 'r1', 'author_id': 'u1', 'content': 'Reel', 'created_at': '2026-03-16T00:00:00Z'},
                ],
                'meta': {'next_cursor': 'cursor-xyz'},
              }));

      final page = await repo.getReelFeedPage();
      expect(page.items.length, 1);
      expect(page.nextCursor, 'cursor-xyz');
    });

    test('returns null cursor when no meta', () async {
      when(() => mockApi.get(any(), queryParameters: any(named: 'queryParameters')))
          .thenAnswer((_) async => mockResponse(data: {
                'data': [
                  {'id': 'r1', 'author_id': 'u1', 'content': 'Reel', 'created_at': '2026-03-16T00:00:00Z'},
                ],
              }));

      final page = await repo.getReelFeedPage();
      expect(page.nextCursor, isNull);
    });
  });
}
