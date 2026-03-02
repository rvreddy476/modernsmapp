import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Home feed provider — fetches and caches home timeline.
final homeFeedProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final repo = ref.watch(feedRepositoryProvider);
  return repo.getHomeFeed();
});

/// Reel feed provider.
final reelFeedProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final repo = ref.watch(feedRepositoryProvider);
  return repo.getReelFeed();
});

/// Video (PostTube) feed provider.
final videoFeedProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final repo = ref.watch(feedRepositoryProvider);
  return repo.getVideoFeed();
});

/// Active feed filter (For You, Following, Trending).
final feedFilterProvider = StateProvider<String>((ref) => 'For You');
