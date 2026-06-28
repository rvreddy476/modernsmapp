import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Product feedback — wraps post-service `POST /v1/feedback`. Distinct from a
/// trust-safety Report (that flow is `postRepository.submitReport`).
class FeedbackRepository {
  final ApiClient _api;
  FeedbackRepository(this._api);

  /// [type] is one of: bug, feature, performance, content, ui, other.
  Future<void> submit({
    required String type,
    required String message,
    String? postId,
    String context = 'video_more_sheet',
  }) async {
    await _api.post(
      '/v1/feedback',
      data: {
        'feedback_type': type,
        'message': message,
        'context': context,
        'post_id': ?postId,
      },
    );
  }
}

final feedbackRepositoryProvider = Provider<FeedbackRepository>((ref) {
  return FeedbackRepository(ref.watch(apiClientProvider));
});
