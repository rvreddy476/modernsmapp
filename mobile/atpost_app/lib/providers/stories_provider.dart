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

/// Args for the results provider — story + interactive ID together.
class InteractiveResultsKey {
  final String storyId;
  final String interactiveId;
  const InteractiveResultsKey(this.storyId, this.interactiveId);

  @override
  bool operator ==(Object other) =>
      other is InteractiveResultsKey &&
      other.storyId == storyId &&
      other.interactiveId == interactiveId;

  @override
  int get hashCode => Object.hash(storyId, interactiveId);
}

final interactiveResultsProvider = FutureProvider.autoDispose
    .family<StoryInteractiveResults?, InteractiveResultsKey>((ref, key) async {
  return ref.watch(storiesRepositoryProvider).getInteractiveResults(
        storyId: key.storyId,
        interactiveId: key.interactiveId,
      );
});
