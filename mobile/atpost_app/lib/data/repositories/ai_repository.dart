import 'package:atpost_app/data/models/ai.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class AiRepository {
  final ApiClient _api;

  AiRepository(this._api);

  Future<CaptionSuggestions> getCaptionSuggestions({
    required String refId,
    required String refType,
    required String contextText,
  }) async {
    final res = await _api.post('/v1/ai/caption-suggestions', data: {
      'ref_id': refId,
      'ref_type': refType,
      'context_text': contextText,
    });
    final data = res.data['data'] ?? res.data;
    return CaptionSuggestions.fromJson(data as Map<String, dynamic>);
  }

  Future<HashtagSuggestions> getHashtagSuggestions({
    required String refId,
    required String refType,
    required String contextText,
  }) async {
    final res = await _api.post('/v1/ai/hashtag-suggestions', data: {
      'ref_id': refId,
      'ref_type': refType,
      'context_text': contextText,
    });
    final data = res.data['data'] ?? res.data;
    return HashtagSuggestions.fromJson(data as Map<String, dynamic>);
  }

  Future<SmartReplies> getSmartReplies({
    required String refId,
    required String refType,
    required String contextText,
  }) async {
    final res = await _api.post('/v1/ai/smart-replies', data: {
      'ref_id': refId,
      'ref_type': refType,
      'context_text': contextText,
    });
    final data = res.data['data'] ?? res.data;
    return SmartReplies.fromJson(data as Map<String, dynamic>);
  }
}

final aiRepositoryProvider = Provider<AiRepository>((ref) {
  return AiRepository(ref.watch(apiClientProvider));
});
