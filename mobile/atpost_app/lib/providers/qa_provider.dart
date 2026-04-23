import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/repositories/qa_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for the paginated Questions feed.
class QAFeedState {
  final List<Question> questions;
  final String? nextCursor;
  final bool isLoadingMore;
  final String? activeTopic;
  final String sort;

  const QAFeedState({
    this.questions = const [],
    this.nextCursor,
    this.isLoadingMore = false,
    this.activeTopic,
    this.sort = 'trending',
  });

  QAFeedState copyWith({
    List<Question>? questions,
    String? nextCursor,
    bool? isLoadingMore,
    String? activeTopic,
    String? sort,
  }) {
    return QAFeedState(
      questions: questions ?? this.questions,
      nextCursor: nextCursor ?? this.nextCursor,
      isLoadingMore: isLoadingMore ?? this.isLoadingMore,
      activeTopic: activeTopic ?? this.activeTopic,
      sort: sort ?? this.sort,
    );
  }
}

/// Notifier for managing the Questions feed with pagination and sorting.
class QAFeedNotifier extends StateNotifier<AsyncValue<QAFeedState>> {
  final QARepository _repo;
  final String? communityId;

  QAFeedNotifier(this._repo, {this.communityId}) : super(const AsyncValue.loading()) {
    refresh();
  }

  Future<void> refresh({String? topic, String? sort}) async {
    state = const AsyncValue.loading();
    try {
      final page = await ErrorHandler.retry(() => _repo.getQuestions(
        communityId: communityId,
        topic: topic,
        sort: sort ?? 'trending',
      ));

      state = AsyncValue.data(QAFeedState(
        questions: page.items,
        nextCursor: page.nextCursor,
        activeTopic: topic,
        sort: sort ?? 'trending',
      ));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> loadMore() async {
    final currentState = state.value;
    if (currentState == null || currentState.isLoadingMore || currentState.nextCursor == null) return;

    state = AsyncValue.data(currentState.copyWith(isLoadingMore: true));
    try {
      final page = await ErrorHandler.retry(() => _repo.getQuestions(
        communityId: communityId,
        topic: currentState.activeTopic,
        sort: currentState.sort,
        cursor: currentState.nextCursor,
      ));

      state = AsyncValue.data(currentState.copyWith(
        questions: [...currentState.questions, ...page.items],
        nextCursor: page.nextCursor,
        isLoadingMore: false,
      ));
    } catch (e) {
      state = AsyncValue.data(currentState.copyWith(isLoadingMore: false));
    }
  }

  /// Optimistically updates a question's vote in the feed.
  void updateVote(String questionId, int value) {
    final currentState = state.value;
    if (currentState == null) return;

    final index = currentState.questions.indexWhere((q) => q.id == questionId);
    if (index == -1) return;

    final question = currentState.questions[index];
    final wasUpvoted = question.viewerVote == true;
    final wasDownvoted = question.viewerVote == false;

    int newUpvotes = question.upvoteCount;
    int newDownvotes = question.downvoteCount;

    // Reset previous vote
    if (wasUpvoted) newUpvotes--;
    if (wasDownvoted) newDownvotes--;

    // Apply new vote
    if (value == 1) newUpvotes++;
    if (value == -1) newDownvotes++;

    final updated = question.copyWith(
      upvoteCount: newUpvotes,
      downvoteCount: newDownvotes,
      viewerVote: value == 1 ? true : (value == -1 ? false : null),
    );

    final newList = List<Question>.from(currentState.questions)..[index] = updated;
    state = AsyncValue.data(currentState.copyWith(questions: newList));

    // Background API call
    _repo.voteQuestion(questionId, value).catchError((e) {
      // Rollback on failure (simple implementation)
      refresh();
    });
  }
}

/// Global provider for the general Q&A feed.
final qaFeedProvider = StateNotifierProvider.autoDispose<QAFeedNotifier, AsyncValue<QAFeedState>>((ref) {
  return QAFeedNotifier(ref.watch(qaRepositoryProvider));
});

/// Params for community-specific Q&A feed.
class CommunityQuestionsParams {
  final String communityId;
  final String? topicSlug;
  final String sort;

  const CommunityQuestionsParams({
    required this.communityId,
    this.topicSlug,
    this.sort = 'recent',
  });

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is CommunityQuestionsParams &&
          runtimeType == other.runtimeType &&
          communityId == other.communityId &&
          topicSlug == other.topicSlug &&
          sort == other.sort;

  @override
  int get hashCode => communityId.hashCode ^ topicSlug.hashCode ^ sort.hashCode;
}

class CommunityQuestionsResult {
  final List<Question> questions;
  final List<QaTopicOption> availableTopics;
  final CommunityQaSettings settings;

  const CommunityQuestionsResult({
    required this.questions,
    required this.availableTopics,
    required this.settings,
  });
}

/// Community-specific Q&A feed provider.
final communityQuestionsProvider = FutureProvider.autoDispose
    .family<CommunityQuestionsResult, CommunityQuestionsParams>(
  (ref, params) async {
    final repo = ref.watch(qaRepositoryProvider);
    final questions = await repo.getQuestions(
      communityId: params.communityId,
      topic: params.topicSlug,
      sort: params.sort,
    );

    // In a real app, these would come from the API too.
    return CommunityQuestionsResult(
      questions: questions.items,
      availableTopics: [],
      settings: const CommunityQaSettings(),
    );
  },
);

/// QA Topics provider.
final qaTopicsProvider = FutureProvider.autoDispose<List<QaTopic>>((ref) async {
  // In a real app, this would be a repo call.
  return [];
});

/// Single QA Topic provider.
final qaTopicProvider =
    FutureProvider.autoDispose.family<QaTopic, String>((ref, topicId) async {
  return const QaTopic(id: '1', name: 'General', slug: 'general');
});

/// Params for topic-specific questions.
class QaTopicQuestionsParams {
  final String topicSlug;
  final String sort;

  const QaTopicQuestionsParams({required this.topicSlug, this.sort = 'recent'});

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is QaTopicQuestionsParams &&
          runtimeType == other.runtimeType &&
          topicSlug == other.topicSlug &&
          sort == other.sort;

  @override
  int get hashCode => topicSlug.hashCode ^ sort.hashCode;
}

/// Topic-specific questions provider.
final qaTopicQuestionsProvider = FutureProvider.autoDispose
    .family<List<Question>, QaTopicQuestionsParams>((ref, params) async {
  final repo = ref.watch(qaRepositoryProvider);
  final res = await repo.getQuestions(topic: params.topicSlug, sort: params.sort);
  return res.items;
});

/// Question detail provider.
final questionDetailProvider =
    FutureProvider.autoDispose.family<QuestionDetail, String>(
  (ref, questionId) async {
    return ref.watch(qaRepositoryProvider).getQuestionDetail(questionId);
  },
);

/// Params for question answers.
class QaQuestionAnswersParams {
  final String questionId;
  const QaQuestionAnswersParams({required this.questionId});

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is QaQuestionAnswersParams &&
          runtimeType == other.runtimeType &&
          questionId == other.questionId;

  @override
  int get hashCode => questionId.hashCode;
}

/// Question answers provider.
final qaQuestionAnswersProvider = FutureProvider.autoDispose
    .family<List<Answer>, QaQuestionAnswersParams>((ref, params) async {
  final detail = await ref
      .watch(qaRepositoryProvider)
      .getQuestionDetail(params.questionId);
  return detail.answers;
});
