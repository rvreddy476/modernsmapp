import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready repository for Q&A operations.
/// Synchronized with the inferred qa-service contract.
class QARepository {
  final ApiClient _api;

  QARepository(this._api);

  /// Fetch a list of questions with pagination.
  Future<QAPage> getQuestions({
    String? topic,
    String? communityId,
    String sort = 'trending',
    int limit = 20,
    String? cursor,
  }) async {
    final params = <String, dynamic>{
      'sort': sort,
      'limit': limit,
      'topic': ?topic,
      'community_id': ?communityId,
      'cursor': ?cursor,
    };

    final response = await _api.get(
      '/v1/qa/questions',
      queryParameters: params,
    );
    final data = response.data['data'] as List<dynamic>? ?? [];
    final meta = response.data['meta'] as Map<String, dynamic>?;

    return QAPage(
      items: data
          .map((e) => Question.fromJson(e as Map<String, dynamic>))
          .toList(),
      nextCursor: meta?['next_cursor'] as String?,
    );
  }

  /// Fetch a single question detail with its answers.
  Future<QuestionDetail> getQuestionDetail(String questionId) async {
    final response = await _api.get('/v1/qa/questions/$questionId');
    final data = response.data['data'] as Map<String, dynamic>;

    final question = Question.fromJson(data);
    final answersRaw = data['answers'] as List? ?? [];
    final answers = answersRaw
        .map((e) => Answer.fromJson(e as Map<String, dynamic>))
        .toList();

    return QuestionDetail(question: question, answers: answers);
  }

  /// Create a new question.
  Future<Question> createQuestion({
    required String title,
    required String body,
    List<String> topics = const [],
    String? communityId,
  }) async {
    final response = await _api.post(
      '/v1/qa/questions',
      data: {
        'title': title,
        'body': body,
        'topics': topics,
        'community_id': ?communityId,
      },
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return Question.fromJson(data);
  }

  /// Submit an answer to a question.
  Future<Answer> submitAnswer(String questionId, String body) async {
    final response = await _api.post(
      '/v1/qa/questions/$questionId/answers',
      data: {'body': body},
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return Answer.fromJson(data);
  }

  /// Vote on a question (1: up, -1: down, 0: remove).
  Future<void> voteQuestion(String questionId, int value) async {
    await _api.post(
      '/v1/qa/questions/$questionId/vote',
      data: {'value': value},
    );
  }

  /// Vote on an answer.
  Future<void> voteAnswer(String answerId, int value) async {
    await _api.post('/v1/qa/answers/$answerId/vote', data: {'value': value});
  }
}

final qaRepositoryProvider = Provider<QARepository>((ref) {
  return QARepository(ref.watch(apiClientProvider));
});
