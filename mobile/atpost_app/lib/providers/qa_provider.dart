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

  QAFeedNotifier(this._repo, {this.communityId})
      : super(const AsyncValue.loading()) {
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
    if (currentState == null ||
        currentState.isLoadingMore ||
        currentState.nextCursor == null) {
      return;
    }

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
  ///
  /// [voteType] = 'up' | 'down'. Tapping the same direction toggles it off
  /// (calls removeQuestionVote).
  void toggleVote(String questionId, String voteType) {
    final currentState = state.value;
    if (currentState == null) return;

    final index = currentState.questions.indexWhere((q) => q.id == questionId);
    if (index == -1) return;

    final question = currentState.questions[index];
    final wasUpvoted = question.viewerVote == true;
    final wasDownvoted = question.viewerVote == false;
    final tappedSame = (voteType == 'up' && wasUpvoted) ||
        (voteType == 'down' && wasDownvoted);

    int newUpvotes = question.upvoteCount;
    int newDownvotes = question.downvoteCount;

    if (wasUpvoted) newUpvotes--;
    if (wasDownvoted) newDownvotes--;

    bool? newViewerVote;
    if (tappedSame) {
      newViewerVote = null;
    } else {
      if (voteType == 'up') {
        newUpvotes++;
        newViewerVote = true;
      } else if (voteType == 'down') {
        newDownvotes++;
        newViewerVote = false;
      }
    }

    final updated = Question(
      id: question.id,
      authorId: question.authorId,
      authorName: question.authorName,
      authorAvatar: question.authorAvatar,
      title: question.title,
      body: question.body,
      bodyHtml: question.bodyHtml,
      topics: question.topics,
      topicObjects: question.topicObjects,
      communityId: question.communityId,
      community: question.community,
      upvoteCount: newUpvotes < 0 ? 0 : newUpvotes,
      downvoteCount: newDownvotes < 0 ? 0 : newDownvotes,
      answerCount: question.answerCount,
      viewCount: question.viewCount,
      isPinned: question.isPinned,
      isAnswered: question.isAnswered,
      isAnonymous: question.isAnonymous,
      createdAt: question.createdAt,
      viewerVote: newViewerVote,
    );

    final newList = List<Question>.from(currentState.questions)
      ..[index] = updated;
    state = AsyncValue.data(currentState.copyWith(questions: newList));

    final future = tappedSame
        ? _repo.removeQuestionVote(questionId)
        : _repo.voteQuestion(questionId, voteType);
    future.catchError((_) {
      refresh();
    });
  }
}

/// Global provider for the general Q&A feed.
final qaFeedProvider = StateNotifierProvider.autoDispose<QAFeedNotifier,
    AsyncValue<QAFeedState>>((ref) {
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
///
/// Combines the new community questions endpoint with the qa-settings call so
/// the existing community_detail_screen.dart consumer keeps working.
final communityQuestionsProvider = FutureProvider.autoDispose
    .family<CommunityQuestionsResult, CommunityQuestionsParams>(
  (ref, params) async {
    final repo = ref.watch(qaRepositoryProvider);
    final results = await Future.wait([
      repo.getCommunityQuestions(params.communityId),
      repo.getCommunityQASettings(params.communityId).catchError(
            (_) => const CommunityQaSettings(),
          ),
      repo
          .getCommunityPopularTopics(params.communityId)
          .catchError((_) => <QaTopic>[]),
    ]);

    final questions = results[0] as List<Question>;
    final settings = results[1] as CommunityQaSettings;
    final popularTopics = results[2] as List<QaTopic>;

    // Optional client-side filter by topic slug.
    final filtered = params.topicSlug == null
        ? questions
        : questions
            .where((q) => q.topics.contains(params.topicSlug))
            .toList();

    // Sort: pinned first, then by selected sort key.
    filtered.sort((a, b) {
      if (a.isPinned != b.isPinned) return a.isPinned ? -1 : 1;
      switch (params.sort) {
        case 'votes':
          return b.voteScore.compareTo(a.voteScore);
        case 'recent':
        default:
          return b.createdAt.compareTo(a.createdAt);
      }
    });

    final topicOptions = popularTopics
        .map((t) => QaTopicOption(name: t.name, slug: t.slug))
        .toList();

    return CommunityQuestionsResult(
      questions: filtered,
      availableTopics: topicOptions,
      settings: settings,
    );
  },
);

/// QA Topics provider — list all topics.
final qaTopicsProvider =
    FutureProvider.autoDispose<List<QaTopic>>((ref) async {
  return ref.watch(qaRepositoryProvider).listTopics();
});

/// Single QA Topic provider.
final qaTopicProvider =
    FutureProvider.autoDispose.family<QaTopic, String>((ref, topicId) async {
  return ref.watch(qaRepositoryProvider).getTopic(topicId);
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

// ---------------- New providers ----------------

/// Comments under a specific answer.
final questionAnswerCommentsProvider = FutureProvider.autoDispose
    .family<List<AnswerComment>, String>((ref, answerId) async {
  return ref.watch(qaRepositoryProvider).listComments(answerId);
});

/// Search params for Q&A search.
class QaSearchParams {
  final String query;
  final String? communityId;
  final String? topicId;

  const QaSearchParams({
    required this.query,
    this.communityId,
    this.topicId,
  });

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is QaSearchParams &&
          runtimeType == other.runtimeType &&
          query == other.query &&
          communityId == other.communityId &&
          topicId == other.topicId;

  @override
  int get hashCode =>
      query.hashCode ^ communityId.hashCode ^ topicId.hashCode;
}

final qaSearchProvider = FutureProvider.autoDispose
    .family<List<Question>, QaSearchParams>((ref, params) async {
  if (params.query.trim().isEmpty) return const <Question>[];
  return ref.watch(qaRepositoryProvider).searchQuestions(
        params.query,
        communityId: params.communityId,
        topicId: params.topicId,
      );
});

final qaProfileProvider = FutureProvider.autoDispose
    .family<QaProfile, String>((ref, userId) async {
  return ref.watch(qaRepositoryProvider).getProfile(userId);
});

final qaMyProfileProvider =
    FutureProvider.autoDispose<QaProfile>((ref) async {
  return ref.watch(qaRepositoryProvider).getMyProfile();
});

final qaLeaderboardProvider =
    FutureProvider.autoDispose<List<LeaderboardEntry>>((ref) async {
  return ref.watch(qaRepositoryProvider).getLeaderboard();
});

final qaSavedQuestionsProvider =
    FutureProvider.autoDispose<List<Question>>((ref) async {
  return ref.watch(qaRepositoryProvider).getSavedQuestions();
});

final qaSavedAnswersProvider =
    FutureProvider.autoDispose<List<Answer>>((ref) async {
  return ref.watch(qaRepositoryProvider).getSavedAnswers();
});

final qaQuestionDraftsProvider =
    FutureProvider.autoDispose<List<QuestionDraft>>((ref) async {
  return ref.watch(qaRepositoryProvider).listQuestionDrafts();
});

final qaAnswerDraftsProvider =
    FutureProvider.autoDispose<List<AnswerDraft>>((ref) async {
  return ref.watch(qaRepositoryProvider).listAnswerDrafts();
});

final qaCommunitySettingsProvider = FutureProvider.autoDispose
    .family<CommunityQaSettings, String>((ref, cid) async {
  return ref.watch(qaRepositoryProvider).getCommunityQASettings(cid);
});

final qaCommunityPopularTopicsProvider = FutureProvider.autoDispose
    .family<List<QaTopic>, String>((ref, cid) async {
  return ref.watch(qaRepositoryProvider).getCommunityPopularTopics(cid);
});

final qaUserQuestionsProvider = FutureProvider.autoDispose
    .family<List<Question>, String>((ref, userId) async {
  return ref.watch(qaRepositoryProvider).getUserQuestions(userId);
});

final qaUserAnswersProvider = FutureProvider.autoDispose
    .family<List<Answer>, String>((ref, userId) async {
  return ref.watch(qaRepositoryProvider).getUserAnswers(userId);
});

final qaUserBadgesProvider = FutureProvider.autoDispose
    .family<List<ContributorBadge>, String>((ref, userId) async {
  return ref.watch(qaRepositoryProvider).getBadges(userId);
});

final qaUserReputationProvider = FutureProvider.autoDispose
    .family<List<ReputationEvent>, String>((ref, userId) async {
  return ref.watch(qaRepositoryProvider).getReputationHistory(userId);
});
