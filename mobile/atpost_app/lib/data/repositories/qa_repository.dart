import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class QaRepository {
  final ApiClient _api;

  QaRepository(this._api);

  Future<List<QaTopic>> listTopics({bool featuredOnly = false}) async {
    final response = await _api.get(
      '/v1/qa/topics',
      queryParameters: featuredOnly ? {'featured': 'true'} : null,
    );
    return _extractList(response.data)
        .whereType<Map>()
        .map((item) => QaTopic.fromJson(Map<String, dynamic>.from(item)))
        .toList();
  }

  Future<QaTopic> getTopic(String topicId) async {
    final response = await _api.get('/v1/qa/topics/$topicId');
    return QaTopic.fromJson(_extractMap(response.data));
  }

  Future<List<QaQuestionSummary>> listQuestions({
    String? topicSlug,
    String? communityId,
    String? scope,
    String sort = 'recent',
    String? status,
  }) async {
    final queryParameters = <String, dynamic>{
      'sort': sort,
      if (topicSlug != null && topicSlug.isNotEmpty) 'topic': topicSlug,
      if (communityId != null && communityId.isNotEmpty)
        'community_id': communityId,
      if (scope != null && scope.isNotEmpty) 'scope': scope,
      if (status != null && status.isNotEmpty) 'status': status,
    };

    final response = await _api.get(
      '/v1/qa/questions',
      queryParameters: queryParameters,
    );
    final payload = _extractMap(response.data);
    final items = payload['questions'] as List<dynamic>? ?? const [];
    return items
        .whereType<Map>()
        .map(
          (item) => QaQuestionSummary.fromJson(Map<String, dynamic>.from(item)),
        )
        .toList();
  }

  Future<CommunityQuestionsResult> getCommunityQuestions(
    String communityId, {
    String? topicSlug,
    String sort = 'recent',
    String? status,
  }) async {
    final queryParameters = <String, dynamic>{
      'sort': sort,
      if (topicSlug != null && topicSlug.isNotEmpty) 'topic': topicSlug,
      if (status != null && status.isNotEmpty) 'status': status,
    };
    final response = await _api.get(
      '/v1/communities/$communityId/questions',
      queryParameters: queryParameters,
    );
    return CommunityQuestionsResult.fromJson(_extractMap(response.data));
  }

  Future<QaQuestion> getQuestion(String questionId) async {
    final response = await _api.get('/v1/qa/questions/$questionId');
    return QaQuestion.fromJson(_extractMap(response.data));
  }

  Future<List<QaAnswer>> getAnswers(
    String questionId, {
    String sort = 'votes',
  }) async {
    final response = await _api.get(
      '/v1/qa/questions/$questionId/answers',
      queryParameters: {'sort': sort},
    );
    return _extractList(response.data)
        .whereType<Map>()
        .map((item) => QaAnswer.fromJson(Map<String, dynamic>.from(item)))
        .toList();
  }

  static List<dynamic> _extractList(dynamic data) {
    if (data is List<dynamic>) {
      return data;
    }
    if (data is Map<String, dynamic>) {
      final direct = data['data'];
      if (direct is List<dynamic>) {
        return direct;
      }
      if (direct is Map<String, dynamic>) {
        final items = direct['items'];
        if (items is List<dynamic>) {
          return items;
        }
      }
      final items = data['items'];
      if (items is List<dynamic>) {
        return items;
      }
    }
    return const [];
  }

  static Map<String, dynamic> _extractMap(dynamic data) {
    if (data is Map<String, dynamic>) {
      final direct = data['data'];
      if (direct is Map<String, dynamic>) {
        return direct;
      }
      return data;
    }
    return const <String, dynamic>{};
  }
}

final qaRepositoryProvider = Provider<QaRepository>((ref) {
  return QaRepository(ref.watch(apiClientProvider));
});
