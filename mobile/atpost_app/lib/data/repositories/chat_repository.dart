import 'dart:math';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ChatPage {
  final List<Message> messages;
  final String? nextCursor;

  const ChatPage({
    required this.messages,
    this.nextCursor,
  });
}

class ChatRepository {
  final ApiClient _api;
  final Random _random = Random.secure();

  ChatRepository(this._api);

  Future<List<Conversation>> getConversations({int limit = 20}) async {
    final response = await _api.get(
      '${Environment.chatPath}/conversations',
      queryParameters: {'limit': limit},
    );
    final items = (response.data['data'] as List<dynamic>? ?? const <dynamic>[]);
    return items
        .map((item) => Conversation.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  Future<Conversation> getConversation(String conversationId) async {
    final response = await _api.get(
      '${Environment.chatPath}/conversations/$conversationId',
    );
    return Conversation.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<Conversation> createDirectConversation(String otherUserId) async {
    final response = await _api.post(
      '${Environment.chatPath}/conversations/direct',
      data: {'other_user_id': otherUserId},
      options: Options(headers: {'Idempotency-Key': _idempotencyKey()}),
    );
    return Conversation.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<ChatPage> getMessages(
    String conversationId, {
    int limit = 50,
    String? cursor,
  }) async {
    final queryParameters = <String, dynamic>{'limit': limit};
    if (cursor != null && cursor.isNotEmpty) {
      queryParameters['cursor'] = cursor;
    }

    final response = await _api.get(
      '${Environment.chatPath}/conversations/$conversationId/messages',
      queryParameters: queryParameters,
    );
    final items = (response.data['data'] as List<dynamic>? ?? const <dynamic>[]);
    final nextCursor =
        response.data['meta']?['next_cursor'] as String? ??
        response.data['meta']?['nextCursor'] as String?;

    return ChatPage(
      messages:
          items
              .map((item) => Message.fromJson(item as Map<String, dynamic>))
              .toList(),
      nextCursor: nextCursor,
    );
  }

  Future<Message> sendMessage(
    String conversationId,
    String content, {
    String type = 'text',
    String? mediaId,
  }) async {
    final response = await _api.post(
      '${Environment.chatPath}/conversations/$conversationId/messages',
      data: {
        'type': type,
        'text': type == 'text' ? content : '',
        if (mediaId != null && mediaId.isNotEmpty) 'media_id': mediaId,
      },
      options: Options(headers: {'Idempotency-Key': _idempotencyKey()}),
    );
    return Message.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<void> sendTyping(String conversationId) async {
    await _api.post('${Environment.chatPath}/conversations/$conversationId/typing');
  }

  Future<void> markRead(String conversationId, String messageId) async {
    await _api.post(
      '${Environment.chatPath}/conversations/$conversationId/read',
      data: {'message_id': messageId},
    );
  }

  Future<Map<String, bool>> getPresence(List<String> userIds) async {
    final response = await _api.post(
      '${Environment.chatPath}/presence',
      data: {'user_ids': userIds},
    );
    final data =
        response.data['data'] as Map<String, dynamic>? ?? const <String, dynamic>{};
    return data.map((key, value) => MapEntry(key, value == true));
  }

  String _idempotencyKey() {
    final timestamp = DateTime.now().microsecondsSinceEpoch.toRadixString(16);
    final randomPart = _random.nextInt(1 << 32).toRadixString(16);
    return 'mobile-$timestamp-$randomPart';
  }
}

final chatRepositoryProvider = Provider<ChatRepository>((ref) {
  return ChatRepository(ref.watch(apiClientProvider));
});
