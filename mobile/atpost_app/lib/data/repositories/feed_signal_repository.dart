import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Feed personalization signals — wraps feed-service `POST /v1/feed/signal`.
/// The backend accepts `see_less` / `see_more` per post and the ranking layer
/// (feed-service/internal/ranking) consumes them to tune future results.
class FeedSignalRepository {
  final ApiClient _api;
  FeedSignalRepository(this._api);

  Future<void> seeLess(String postId) => _record(postId, 'see_less');
  Future<void> seeMore(String postId) => _record(postId, 'see_more');

  Future<void> _record(String postId, String signal) async {
    await _api.post(
      '/v1/feed/signal',
      data: {'post_id': postId, 'signal': signal},
    );
  }
}

final feedSignalRepositoryProvider = Provider<FeedSignalRepository>((ref) {
  return FeedSignalRepository(ref.watch(apiClientProvider));
});
