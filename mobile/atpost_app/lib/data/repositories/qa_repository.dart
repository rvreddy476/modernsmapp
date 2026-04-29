import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready repository for Q&A operations.
/// Synchronized with the qa-service contract.
class QARepository {
  final ApiClient _api;

  QARepository(this._api);

  // ---------------- Questions ----------------

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
    bool isAnonymous = false,
  }) async {
    final response = await _api.post(
      '/v1/qa/questions',
      data: {
        'title': title,
        'body': body,
        'topics': topics,
        'community_id': ?communityId,
        'is_anonymous': isAnonymous,
      },
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return Question.fromJson(data);
  }

  /// Submit an answer to a question.
  Future<Answer> submitAnswer(
    String questionId,
    String body, {
    bool isAnonymous = false,
  }) async {
    final response = await _api.post(
      '/v1/qa/questions/$questionId/answers',
      data: {'body': body, 'is_anonymous': isAnonymous},
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return Answer.fromJson(data);
  }

  // ---------------- Voting ----------------

  /// Vote on a question. [voteType] = 'up' | 'down'.
  Future<void> voteQuestion(String questionId, String voteType) async {
    await _api.post(
      '/v1/qa/questions/$questionId/vote',
      data: {'vote_type': voteType},
    );
  }

  /// Remove the viewer's vote from a question.
  Future<void> removeQuestionVote(String questionId) async {
    await _api.delete('/v1/qa/questions/$questionId/vote');
  }

  /// Vote on an answer. [voteType] = 'up' | 'down'.
  Future<void> voteAnswer(String answerId, String voteType) async {
    await _api.post(
      '/v1/qa/answers/$answerId/vote',
      data: {'vote_type': voteType},
    );
  }

  /// Remove the viewer's vote from an answer.
  Future<void> removeAnswerVote(String answerId) async {
    await _api.delete('/v1/qa/answers/$answerId/vote');
  }

  /// Vote on a comment.
  Future<void> voteComment(String commentId, String voteType) async {
    await _api.post(
      '/v1/qa/comments/$commentId/vote',
      data: {'vote_type': voteType},
    );
  }

  Future<void> removeCommentVote(String commentId) async {
    await _api.delete('/v1/qa/comments/$commentId/vote');
  }

  // ---------------- Best answer ----------------

  Future<void> selectBestAnswer(String questionId, String answerId) async {
    await _api.post(
      '/v1/qa/questions/$questionId/best-answer',
      data: {'answer_id': answerId},
    );
  }

  Future<void> unselectBestAnswer(String questionId) async {
    await _api.delete('/v1/qa/questions/$questionId/best-answer');
  }

  // ---------------- Comments ----------------

  Future<List<AnswerComment>> listComments(String answerId) async {
    final response = await _api.get('/v1/qa/answers/$answerId/comments');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => AnswerComment.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<AnswerComment> createComment(String answerId, String body) async {
    final response = await _api.post(
      '/v1/qa/answers/$answerId/comments',
      data: {'body': body},
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return AnswerComment.fromJson(data);
  }

  Future<AnswerComment> updateComment(String commentId, String body) async {
    final response = await _api.put(
      '/v1/qa/comments/$commentId',
      data: {'body': body},
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return AnswerComment.fromJson(data);
  }

  Future<void> deleteComment(String commentId) async {
    await _api.delete('/v1/qa/comments/$commentId');
  }

  // ---------------- Follow / Save ----------------

  Future<void> followQuestion(String id) =>
      _api.post('/v1/qa/questions/$id/follow');

  Future<void> unfollowQuestion(String id) =>
      _api.delete('/v1/qa/questions/$id/follow');

  Future<void> followTopic(String id) =>
      _api.post('/v1/qa/topics/$id/follow');

  Future<void> unfollowTopic(String id) =>
      _api.delete('/v1/qa/topics/$id/follow');

  Future<void> followContributor(String userId) =>
      _api.post('/v1/qa/contributors/$userId/follow');

  Future<void> unfollowContributor(String userId) =>
      _api.delete('/v1/qa/contributors/$userId/follow');

  Future<void> saveQuestion(String id) =>
      _api.post('/v1/qa/questions/$id/save');

  Future<void> unsaveQuestion(String id) =>
      _api.delete('/v1/qa/questions/$id/save');

  Future<void> saveAnswer(String id) => _api.post('/v1/qa/answers/$id/save');

  Future<void> unsaveAnswer(String id) =>
      _api.delete('/v1/qa/answers/$id/save');

  Future<List<Question>> getSavedQuestions() async {
    final response = await _api.get('/v1/qa/saved/questions');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Question.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<Answer>> getSavedAnswers() async {
    final response = await _api.get('/v1/qa/saved/answers');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Answer.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ---------------- Answer requests ----------------

  Future<void> requestAnswer(String questionId, String requestedUserId) async {
    await _api.post(
      '/v1/qa/questions/$questionId/request-answer',
      data: {'requested_user_id': requestedUserId},
    );
  }

  // ---------------- Profile / Reputation / Badges ----------------

  Future<QaProfile> getMyProfile() async {
    final response = await _api.get('/v1/qa/profile');
    final data = response.data['data'] as Map<String, dynamic>;
    return QaProfile.fromJson(data);
  }

  Future<QaProfile> getProfile(String userId) async {
    final response = await _api.get('/v1/qa/profile/$userId');
    final data = response.data['data'] as Map<String, dynamic>;
    return QaProfile.fromJson(data);
  }

  Future<QaProfile> updateProfile(Map<String, dynamic> payload) async {
    final response = await _api.put('/v1/qa/profile', data: payload);
    final data = response.data['data'] as Map<String, dynamic>;
    return QaProfile.fromJson(data);
  }

  Future<List<ReputationEvent>> getReputationHistory(String userId) async {
    final response = await _api.get('/v1/qa/profile/$userId/reputation');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => ReputationEvent.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<ContributorBadge>> getBadges(String userId) async {
    final response = await _api.get('/v1/qa/profile/$userId/badges');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => ContributorBadge.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<Question>> getUserQuestions(String userId) async {
    final response = await _api.get('/v1/qa/profile/$userId/questions');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Question.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<Answer>> getUserAnswers(String userId) async {
    final response = await _api.get('/v1/qa/profile/$userId/answers');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Answer.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<LeaderboardEntry>> getLeaderboard() async {
    final response = await _api.get('/v1/qa/leaderboard');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => LeaderboardEntry.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ---------------- Topics ----------------

  Future<List<QaTopic>> listTopics() async {
    final response = await _api.get('/v1/qa/topics');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => QaTopic.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<QaTopic> getTopic(String id) async {
    final response = await _api.get('/v1/qa/topics/$id');
    final data = response.data['data'] as Map<String, dynamic>;
    return QaTopic.fromJson(data);
  }

  Future<List<Question>> getTopicQuestions(String id) async {
    final response = await _api.get('/v1/qa/topics/$id/questions');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Question.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ---------------- Community Q&A ----------------

  Future<List<Question>> getCommunityQuestions(String cid) async {
    final response = await _api.get('/v1/qa/communities/$cid/questions');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Question.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<CommunityQaSettings> getCommunityQASettings(String cid) async {
    final response = await _api.get('/v1/qa/communities/$cid/qa-settings');
    final data = response.data['data'] as Map<String, dynamic>? ??
        response.data as Map<String, dynamic>;
    return CommunityQaSettings.fromJson(data);
  }

  Future<CommunityQaSettings> updateCommunityQASettings(
    String cid,
    CommunityQaSettings settings,
  ) async {
    final response = await _api.put(
      '/v1/qa/communities/$cid/qa-settings',
      data: settings.toJson(),
    );
    final data = response.data['data'] as Map<String, dynamic>? ??
        response.data as Map<String, dynamic>;
    return CommunityQaSettings.fromJson(data);
  }

  Future<void> pinCommunityQuestion(String cid, String qid) async {
    await _api.post('/v1/qa/communities/$cid/questions/$qid/pin');
  }

  Future<void> unpinCommunityQuestion(String cid, String qid) async {
    await _api.delete('/v1/qa/communities/$cid/questions/$qid/pin');
  }

  Future<List<QaTopic>> getCommunityPopularTopics(String cid) async {
    final response =
        await _api.get('/v1/qa/communities/$cid/topics/popular');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => QaTopic.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ---------------- Search ----------------

  Future<List<Question>> searchQuestions(
    String q, {
    String? communityId,
    String? topicId,
  }) async {
    final response = await _api.get(
      '/v1/qa/search',
      queryParameters: {
        'q': q,
        'community_id': ?communityId,
        'topic_id': ?topicId,
      },
    );
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Question.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<List<Question>> getSimilarQuestions(String title) async {
    final response = await _api.get(
      '/v1/qa/questions/similar',
      queryParameters: {'title': title},
    );
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => Question.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ---------------- Reports ----------------

  Future<void> createQuestionReport(
    String questionId,
    String reason,
    String details,
  ) async {
    await _api.post(
      '/v1/qa/reports',
      data: {
        'target_type': 'question',
        'target_id': questionId,
        'reason': reason,
        'details': details,
      },
    );
  }

  Future<void> createAnswerReport(
    String answerId,
    String reason,
    String details,
  ) async {
    await _api.post(
      '/v1/qa/reports',
      data: {
        'target_type': 'answer',
        'target_id': answerId,
        'reason': reason,
        'details': details,
      },
    );
  }

  // ---------------- Drafts ----------------

  Future<List<QuestionDraft>> listQuestionDrafts() async {
    final response = await _api.get('/v1/qa/drafts/questions');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => QuestionDraft.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<QuestionDraft> upsertQuestionDraft(
    Map<String, dynamic> payload,
  ) async {
    final response = await _api.post(
      '/v1/qa/drafts/questions',
      data: payload,
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return QuestionDraft.fromJson(data);
  }

  Future<void> deleteQuestionDraft(String id) async {
    await _api.delete('/v1/qa/drafts/questions/$id');
  }

  Future<List<AnswerDraft>> listAnswerDrafts() async {
    final response = await _api.get('/v1/qa/drafts/answers');
    final data = response.data['data'] as List<dynamic>? ?? [];
    return data
        .map((e) => AnswerDraft.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<AnswerDraft> upsertAnswerDraft(Map<String, dynamic> payload) async {
    final response = await _api.post(
      '/v1/qa/drafts/answers',
      data: payload,
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return AnswerDraft.fromJson(data);
  }

  Future<void> deleteAnswerDraft(String id) async {
    await _api.delete('/v1/qa/drafts/answers/$id');
  }
}

final qaRepositoryProvider = Provider<QARepository>((ref) {
  return QARepository(ref.watch(apiClientProvider));
});
