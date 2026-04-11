import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/repositories/qa_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class QaTopicQuestionsParams {
  final String topicSlug;
  final String sort;
  final String scope;

  const QaTopicQuestionsParams({
    required this.topicSlug,
    this.sort = 'recent',
    this.scope = 'all',
  });

  @override
  bool operator ==(Object other) {
    return other is QaTopicQuestionsParams &&
        other.topicSlug == topicSlug &&
        other.sort == sort &&
        other.scope == scope;
  }

  @override
  int get hashCode => Object.hash(topicSlug, sort, scope);
}

class CommunityQuestionsParams {
  final String communityId;
  final String? topicSlug;
  final String sort;
  final String? status;

  const CommunityQuestionsParams({
    required this.communityId,
    this.topicSlug,
    this.sort = 'recent',
    this.status,
  });

  @override
  bool operator ==(Object other) {
    return other is CommunityQuestionsParams &&
        other.communityId == communityId &&
        other.topicSlug == topicSlug &&
        other.sort == sort &&
        other.status == status;
  }

  @override
  int get hashCode => Object.hash(communityId, topicSlug, sort, status);
}

class QaQuestionAnswersParams {
  final String questionId;
  final String sort;

  const QaQuestionAnswersParams({
    required this.questionId,
    this.sort = 'votes',
  });

  @override
  bool operator ==(Object other) {
    return other is QaQuestionAnswersParams &&
        other.questionId == questionId &&
        other.sort == sort;
  }

  @override
  int get hashCode => Object.hash(questionId, sort);
}

final qaTopicsProvider = FutureProvider.autoDispose<List<QaTopic>>((ref) async {
  return ref.watch(qaRepositoryProvider).listTopics();
});

final qaTopicProvider = FutureProvider.autoDispose.family<QaTopic, String>((
  ref,
  topicId,
) async {
  return ref.watch(qaRepositoryProvider).getTopic(topicId);
});

final qaTopicQuestionsProvider = FutureProvider.autoDispose
    .family<List<QaQuestionSummary>, QaTopicQuestionsParams>((
      ref,
      params,
    ) async {
      return ref
          .watch(qaRepositoryProvider)
          .listQuestions(
            topicSlug: params.topicSlug,
            sort: params.sort,
            scope: params.scope,
          );
    });

final communityQuestionsProvider = FutureProvider.autoDispose
    .family<CommunityQuestionsResult, CommunityQuestionsParams>((
      ref,
      params,
    ) async {
      return ref
          .watch(qaRepositoryProvider)
          .getCommunityQuestions(
            params.communityId,
            topicSlug: params.topicSlug,
            sort: params.sort,
            status: params.status,
          );
    });

final qaQuestionDetailProvider = FutureProvider.autoDispose
    .family<QaQuestion, String>((ref, questionId) {
      return ref.watch(qaRepositoryProvider).getQuestion(questionId);
    });

final qaQuestionAnswersProvider = FutureProvider.autoDispose
    .family<List<QaAnswer>, QaQuestionAnswersParams>((ref, params) {
      return ref
          .watch(qaRepositoryProvider)
          .getAnswers(params.questionId, sort: params.sort);
    });
