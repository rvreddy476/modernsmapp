// PostTube list-screen tests over the real post-service contracts:
//   GET /v1/posts/trending?content_type=long_video  (trending)
//   GET /v1/videos/continue-watching                (watch history)
import 'package:atpost_network/api_client.dart';
import 'package:feature_posttube/trending_screen.dart';
import 'package:feature_posttube/watch_history_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import 'helpers.dart';

void main() {
  late MockApiClient api;

  setUp(() {
    api = MockApiClient();
  });

  Future<void> pump(WidgetTester tester, Widget screen) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [apiClientProvider.overrideWithValue(api)],
        child: MaterialApp(home: screen),
      ),
    );
    await tester.pumpAndSettle();
  }

  group('PosttubeTrendingScreen', () {
    trendingStub(Object? body) {
      when(() => api.get('/v1/posts/trending',
              queryParameters: any(named: 'queryParameters')))
          .thenAnswer((_) async => ok(body));
    }

    testWidgets('renders ranked videos and requests long_video only',
        (tester) async {
      trendingStub({
        'data': {
          'items': [
            {
              'id': 'v1',
              'author_id': 'alice',
              'content': 'How to cook biryani',
              'created_at': '2026-07-01T10:00:00Z',
            },
            {
              'id': 'v2',
              'author_id': 'bob',
              'content': 'Hyderabad street food tour',
              'created_at': '2026-07-02T10:00:00Z',
            },
          ],
        },
      });

      await pump(tester, const PosttubeTrendingScreen());

      expect(find.text('Trending'), findsOneWidget);
      expect(find.text('How to cook biryani'), findsOneWidget);
      expect(find.text('Hyderabad street food tour'), findsOneWidget);

      final query = verify(() => api.get('/v1/posts/trending',
              queryParameters: captureAny(named: 'queryParameters')))
          .captured
          .single as Map<String, dynamic>;
      expect(query['content_type'], 'long_video');
    });

    testWidgets('shows the empty state when nothing is trending',
        (tester) async {
      trendingStub({
        'data': {'items': <dynamic>[]},
      });

      await pump(tester, const PosttubeTrendingScreen());

      expect(find.textContaining('Nothing trending yet'), findsOneWidget);
    });

    testWidgets('shows the error state when the endpoint fails',
        (tester) async {
      when(() => api.get('/v1/posts/trending',
              queryParameters: any(named: 'queryParameters')))
          .thenThrow(httpError(503, path: '/v1/posts/trending'));

      await pump(tester, const PosttubeTrendingScreen());

      expect(find.text('Could not load trending videos.'), findsOneWidget);
    });
  });

  group('WatchHistoryScreen', () {
    testWidgets('renders resume entries from continue-watching',
        (tester) async {
      when(() => api.get('/v1/videos/continue-watching')).thenAnswer(
        (_) async => ok({
          'data': [
            {
              'post_id': 'v1',
              'title': 'How to cook biryani',
              'percent_watched': 42,
            },
            // Legacy field names must keep parsing.
            {'id': 'v2', 'text': 'Old-shape entry', 'progress_pct': 80},
          ],
        }),
      );

      await pump(tester, const WatchHistoryScreen());

      expect(find.text('Watch history'), findsOneWidget);
      expect(find.text('How to cook biryani'), findsOneWidget);
      expect(find.text('Old-shape entry'), findsOneWidget);
    });

    testWidgets('shows the empty state with no history', (tester) async {
      when(() => api.get('/v1/videos/continue-watching'))
          .thenAnswer((_) async => ok({'data': <dynamic>[]}));

      await pump(tester, const WatchHistoryScreen());

      expect(find.textContaining('Nothing here yet'), findsOneWidget);
    });
  });
}
