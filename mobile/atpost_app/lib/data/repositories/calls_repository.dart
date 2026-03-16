import 'package:atpost_app/data/models/call.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CallsRepository {
  final ApiClient _api;

  CallsRepository(this._api);

  Future<CallSession> createCall({
    required String callType,
    required String sourceType,
    String? sourceId,
    bool audioOnly = true,
    required List<String> inviteeUserIds,
  }) async {
    final response = await _api.post('/v1/calls', data: {
      'call_type': callType,
      'source_type': sourceType,
      'source_id': ?sourceId,
      'audio_only': audioOnly,
      'invitee_user_ids': inviteeUserIds,
    });
    return CallSession.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<CallSession> getCall(String callId) async {
    final response = await _api.get('/v1/calls/$callId');
    return CallSession.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<JoinResponse> joinCall(String callId) async {
    final response = await _api.post('/v1/calls/$callId/join');
    return JoinResponse.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<void> acceptInvite(String callId, String inviteId) async {
    await _api.post('/v1/calls/$callId/invites/$inviteId/accept');
  }

  Future<void> declineInvite(String callId, String inviteId) async {
    await _api.post('/v1/calls/$callId/invites/$inviteId/decline');
  }

  Future<void> leaveCall(String callId) async {
    await _api.post('/v1/calls/$callId/leave');
  }

  Future<void> endCall(String callId) async {
    await _api.post('/v1/calls/$callId/end');
  }

  Future<void> inviteParticipants(String callId, List<String> userIds) async {
    await _api.post('/v1/calls/$callId/participants/invite', data: {
      'user_ids': userIds,
    });
  }

  Future<void> muteParticipant(String callId, String userId) async {
    await _api.post('/v1/calls/$callId/participants/$userId/mute');
  }

  Future<void> removeParticipant(String callId, String userId) async {
    await _api.post('/v1/calls/$callId/participants/$userId/remove');
  }

  Future<List<CallHistoryItem>> getCallHistory({
    int limit = 20,
    String? cursor,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get('/v1/calls/history', queryParameters: params);
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items
        .map((e) => CallHistoryItem.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<CallSession> upgradeCall(String callId) async {
    final response = await _api.patch('/v1/calls/$callId/upgrade');
    return CallSession.fromJson(response.data['data'] as Map<String, dynamic>);
  }
}

final callsRepositoryProvider = Provider<CallsRepository>((ref) {
  final api = ref.watch(apiClientProvider);
  return CallsRepository(api);
});
