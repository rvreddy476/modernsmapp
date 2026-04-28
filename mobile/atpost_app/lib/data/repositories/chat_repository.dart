import 'dart:math';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready response model for paginated chat messages.
class ChatPage {
  final List<Message> messages;
  final String? nextCursor;

  const ChatPage({required this.messages, this.nextCursor});
}

/// Repository for chat and messaging operations.
/// Synchronized with the 2026-03-19 OpenAPI mobile integration spec.
class ChatRepository {
  final ApiClient _api;
  final Random _random = Random.secure();

  ChatRepository(this._api);

  /// List all active conversations for the current user.
  /// Synchronized with GET /v1/chat/conversations
  Future<List<Conversation>> getConversations({
    int limit = 20,
    String? cursor,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      '/v1/chat/conversations',
      queryParameters: params,
    );
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items
        .map((item) => Conversation.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  /// Get details for a specific conversation.
  /// Synchronized with GET /v1/chat/conversations/{conversationId}
  Future<Conversation> getConversation(String conversationId) async {
    final response = await _api.get('/v1/chat/conversations/$conversationId');
    final data = response.data['data'] as Map<String, dynamic>;
    return Conversation.fromJson(data);
  }

  /// Create a direct (1-on-1) conversation with another user.
  /// Synchronized with POST /v1/chat/conversations/direct
  Future<Conversation> createDirectConversation(String otherUserId) async {
    final response = await _api.post(
      '/v1/chat/conversations/direct',
      data: {'other_user_id': otherUserId},
      options: Options(headers: {'Idempotency-Key': _idempotencyKey()}),
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return Conversation.fromJson(data);
  }

  /// Fetch messages for a conversation with pagination support.
  /// Synchronized with GET /v1/chat/conversations/{conversationId}/messages
  Future<ChatPage> getMessages(
    String conversationId, {
    int limit = 50,
    String? cursor,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      '/v1/chat/conversations/$conversationId/messages',
      queryParameters: params,
    );

    final items = (response.data['data'] as List<dynamic>?) ?? [];
    final nextCursor = response.data['meta']?['next_cursor'] as String?;

    return ChatPage(
      messages: items
          .map((item) => Message.fromJson(item as Map<String, dynamic>))
          .toList(),
      nextCursor: nextCursor,
    );
  }

  /// Send a new message to a conversation.
  /// Synchronized with POST /v1/chat/conversations/{conversationId}/messages
  Future<Message> sendMessage(
    String conversationId,
    String content, {
    String type = 'text',
    String? mediaId,
  }) async {
    final response = await _api.post(
      '/v1/chat/conversations/$conversationId/messages',
      data: {'type': type, 'text': content, 'media_id': mediaId},
      options: Options(headers: {'Idempotency-Key': _idempotencyKey()}),
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return Message.fromJson(data);
  }

  /// Get unread chat message count across all conversations.
  /// Synchronized with GET /v1/notifications/unread-count (mapped logic)
  Future<int> getUnreadCount() async {
    final response = await _api.get('/v1/notifications/unread-count');
    // The spec suggests a general unread count, often includes chat.
    return (response.data['data']?['chat_count'] ??
            response.data['data']?['unread_count'] ??
            0)
        as int;
  }

  /// Emit a typing indicator for the current user in a conversation.
  /// Backed by POST /v1/chat/conversations/{conversationId}/typing.
  Future<void> sendTyping(String conversationId) async {
    await _api.post('/v1/chat/conversations/$conversationId/typing');
  }

  /// Mark a conversation as read through the latest visible message.
  /// Backed by POST /v1/chat/conversations/{conversationId}/read.
  Future<void> markRead(String conversationId, String messageId) async {
    await _api.post(
      '/v1/chat/conversations/$conversationId/read',
      data: {'message_id': messageId},
    );
  }

  /// Resolve presence for a list of users.
  /// The backend currently exposes a single-user online-status endpoint,
  /// so the repository fans that out and returns a map for provider consumers.
  Future<Map<String, bool>> getPresence(List<String> userIds) async {
    if (userIds.isEmpty) return const <String, bool>{};

    final entries = await Future.wait(
      userIds.map((userId) async {
        final response = await _api.get('/v1/users/$userId/online');
        final data = response.data['data'] ?? response.data;
        final online = data is Map ? data['online'] == true : false;
        return MapEntry(userId, online);
      }),
    );

    return {for (final entry in entries) entry.key: entry.value};
  }

  /// Archive or unarchive a conversation.
  /// Backed by PATCH /v1/chat/conversations/{conversationId}/archive.
  Future<void> setArchived(String conversationId, bool archived) async {
    await _api.patch(
      '/v1/chat/conversations/$conversationId/archive',
      data: {'is_archived': archived},
    );
  }

  String _idempotencyKey() {
    final timestamp = DateTime.now().microsecondsSinceEpoch.toRadixString(16);
    final randomPart = _random.nextInt(1 << 32).toRadixString(16);
    return 'chat-$timestamp-$randomPart';
  }
}

final chatRepositoryProvider = Provider<ChatRepository>((ref) {
  return ChatRepository(ref.watch(apiClientProvider));
});
