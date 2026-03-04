import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/data/repositories/stories_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final feedStoriesProvider = FutureProvider.autoDispose<List<Story>>((ref) async {
  return ref.watch(storiesRepositoryProvider).getFeedStories();
});

final userStoryProvider =
    FutureProvider.autoDispose.family<Story, String>((ref, userId) async {
  return ref.watch(storiesRepositoryProvider).getUserStories(userId);
});
