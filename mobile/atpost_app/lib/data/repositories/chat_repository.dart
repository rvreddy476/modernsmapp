import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ChatRepository {
  final ApiClient _api;

  ChatRepository(this._api);

  /// Get list of conversations.
  Future<List<Conversation>> getConversations({int limit = 20}) async {
    final response = await _api.get(
      '${Environment.chatPath}/conversations',
      queryParameters: {'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items
        .map((e) => Conversation.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Get messages for a conversation.
  Future<List<Message>> getMessages(
    String conversationId, {
    int limit = 50,
    String? before,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (before != null) params['before'] = before;

    final response = await _api.get(
      '${Environment.chatPath}/conversations/$conversationId/messages',
      queryParameters: params,
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items
        .map((e) => Message.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Send a message.
  Future<Message> sendMessage(String conversationId, String content) async {
    final response = await _api.post(
      '${Environment.chatPath}/conversations/$conversationId/messages',
      data: {'content': content},
    );
    return Message.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Get total unread message count.
  Future<int> getUnreadCount() async {
    final response = await _api.get('${Environment.chatPath}/unread-count');
    return (response.data['data']?['count'] as int?) ?? 0;
  }
}

final chatRepositoryProvider = Provider<ChatRepository>((ref) {
  return ChatRepository(ref.watch(apiClientProvider));
});
