import 'package:atpost_app/data/models/presence.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Repository for the M1 conversation-presence endpoint.
///
/// Synchronized with `GET /v1/conversations/:id/presence`. Routes through
/// the API gateway like every other message-service endpoint and returns
/// 403 when the caller is not a member.
class PresenceRepository {
  final ApiClient _api;

  PresenceRepository(this._api);

  Future<ConversationPresence> getPresence(String conversationId) async {
    final response = await _api.get(
      '/v1/conversations/$conversationId/presence',
    );
    // Backend wraps responses as { data, error, meta }; accept either form
    // so we don't fight envelope drift between gateways.
    final body = response.data;
    final payload = body is Map<String, dynamic>
        ? (body['data'] is Map<String, dynamic>
              ? body['data'] as Map<String, dynamic>
              : body)
        : <String, dynamic>{};
    return ConversationPresence.fromJson(payload);
  }
}

final presenceRepositoryProvider = Provider<PresenceRepository>((ref) {
  return PresenceRepository(ref.watch(apiClientProvider));
});
