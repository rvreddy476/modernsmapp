import 'dart:async';

import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';

import '../helpers/fake_data.dart';
import '../helpers/mocks.dart';

void main() {
  late MockFeedRepository mockRepo;
  late MockRealtimeService mockRealtime;

  setUp(() {
    mockRepo = MockFeedRepository();
    mockRealtime = MockRealtimeService();
    when(() => mockRealtime.events).thenAnswer((_) => const Stream.empty());
  });

  HomeFeedNotifier createNotifier() {
    return HomeFeedNotifier(mockRepo, mockRealtime);
  }

  group('HomeFeedNotifier', () {
    test('fetchFirstPage loads posts and sets data state', () async {
      final posts = fakePosts(count: 3);
      when(() => mockRepo.getHomeFeedPage(
            feedMode: any(named: 'feedMode'),
            limit: any(named: 'limit'),
            cursor: any(named: 'cursor'),
          )).thenAnswer((_) async => FeedPage(items: posts));

      final notifier = createNotifier();

      // Wait for _init() to complete
      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);

      final state = notifier.state;
      expect(state.hasValue, true);
      expect(state.value!.posts.length, 3);
    });

    test('fetchFirstPage sets error state on failure', () async {
      when(() => mockRepo.getHomeFeedPage(
            feedMode: any(named: 'feedMode'),
            limit: any(named: 'limit'),
            cursor: any(named: 'cursor'),
          )).thenThrow(Exception('Network error'));

      final notifier = createNotifier();

      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);

      expect(notifier.state.hasError, true);
    });

    test('updateFilter triggers a new fetch', () async {
      when(() => mockRepo.getHomeFeedPage(
            feedMode: any(named: 'feedMode'),
            limit: any(named: 'limit'),
            cursor: any(named: 'cursor'),
          )).thenAnswer((_) async => FeedPage(items: fakePosts(count: 2)));

      final notifier = createNotifier();

      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);

      notifier.updateFilter('Following');

      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);

      // getHomeFeed called twice: once on init, once after filter change
      verify(() => mockRepo.getHomeFeedPage(
            feedMode: any(named: 'feedMode'),
            limit: any(named: 'limit'),
            cursor: any(named: 'cursor'),
          )).called(2);
    });

    test('updateFilter with same value is a no-op', () async {
      when(() => mockRepo.getHomeFeedPage(
            feedMode: any(named: 'feedMode'),
            limit: any(named: 'limit'),
            cursor: any(named: 'cursor'),
          )).thenAnswer((_) async => const FeedPage(items: []));

      final notifier = createNotifier();

      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);

      notifier.updateFilter('For You'); // same as default

      verify(() => mockRepo.getHomeFeedPage(
            feedMode: any(named: 'feedMode'),
            limit: any(named: 'limit'),
            cursor: any(named: 'cursor'),
          )).called(1); // only the initial call
    });
  });
}
