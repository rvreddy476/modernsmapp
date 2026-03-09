import 'dart:convert';

/// Base class for all real-time events received via WebSocket.
abstract class RealtimeEvent {
  final String type;
  final dynamic payload;

  const RealtimeEvent({required this.type, required this.payload});

  factory RealtimeEvent.fromJson(Map<String, dynamic> json) {
    final type = json['type'] as String? ?? 'unknown';
    final payload = json['payload'] ?? json;

    switch (type) {
      case 'message':
        return ChatMessageEvent(payload: payload);
      case 'reaction':
      case 'reaction_update':
        return ReactionUpdateEvent(payload: payload);
      case 'typing':
        return TypingEvent(payload: payload);
      case 'read_receipt':
        return ReadReceiptEvent(payload: payload);
      case 'call_offer':
      case 'call_answer':
      case 'ice_candidate':
      case 'call_end':
      case 'call_decline':
      case 'call_busy':
        return CallSignalEvent(type: type, payload: payload);
      case 'new_post':
        return FeedUpdateEvent(payload: payload);
      case 'post_update':
        return PostInteractionEvent(payload: payload);
      default:
        return UnknownEvent(type: type, payload: payload);
    }
  }
}

class ChatMessageEvent extends RealtimeEvent {
  ChatMessageEvent({required dynamic payload}) : super(type: 'message', payload: payload);

  String get messageId => payload['message_id'] as String? ?? payload['msg_id'] as String? ?? '';
  String get senderId => payload['sender_id'] as String? ?? '';
  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get text => payload['text'] as String? ?? '';
  String get type => payload['type'] as String? ?? 'text';
  String? get mediaId => payload['media_id'] as String?;
  DateTime get createdAt => DateTime.tryParse(payload['created_at'] as String? ?? '') ?? DateTime.now();
}

class ReactionUpdateEvent extends RealtimeEvent {
  ReactionUpdateEvent({required dynamic payload}) : super(type: 'reaction', payload: payload);

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get messageId => payload['message_id'] as String? ?? payload['msg_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get emoji => payload['emoji'] as String? ?? '';
  bool get added => payload['added'] as bool? ?? true;
}

class TypingEvent extends RealtimeEvent {
  TypingEvent({required dynamic payload}) : super(type: 'typing', payload: payload);

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  bool get isTyping => payload['is_typing'] as bool? ?? true;
}

class ReadReceiptEvent extends RealtimeEvent {
  ReadReceiptEvent({required dynamic payload}) : super(type: 'read_receipt', payload: payload);

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get messageId => payload['message_id'] as String? ?? '';
  DateTime get readAt => DateTime.tryParse(payload['read_at'] as String? ?? '') ?? DateTime.now();
}

class CallSignalEvent extends RealtimeEvent {
  CallSignalEvent({required String type, required dynamic payload}) : super(type: type, payload: payload);

  String get senderId => payload['sender_id'] as String? ?? '';
  String get targetUserId => payload['target_user_id'] as String? ?? '';
  String? get callType => payload['call_type'] as String?;
  String? get sdp => payload['sdp'] as String?;
  Map<String, dynamic>? get candidate => payload['candidate'] as Map<String, dynamic>?;
}

class FeedUpdateEvent extends RealtimeEvent {
  FeedUpdateEvent({required dynamic payload}) : super(type: 'new_post', payload: payload);

  String get postId => payload['post_id'] as String? ?? '';
  String get authorId => payload['author_id'] as String? ?? '';
  String get contentSnippet => payload['snippet'] as String? ?? '';
}

class PostInteractionEvent extends RealtimeEvent {
  PostInteractionEvent({required dynamic payload}) : super(type: 'post_update', payload: payload);

  String get postId => payload['post_id'] as String? ?? '';
  String get updateType => payload['update_type'] as String? ?? ''; // reaction, comment, share
  int? get likes => payload['likes'] as int?;
  int? get comments => payload['comments'] as int?;
}

class UnknownEvent extends RealtimeEvent {
  UnknownEvent({required String type, required dynamic payload}) : super(type: type, payload: payload);
}
