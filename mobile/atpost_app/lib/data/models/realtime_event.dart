/// Base class for all real-time events received via WebSocket.
abstract class RealtimeEvent {
  final String eventType;
  final dynamic payload;

  const RealtimeEvent({
    required this.eventType,
    required this.payload,
  });

  factory RealtimeEvent.fromJson(Map<String, dynamic> json) {
    final eventType = json['type'] as String? ?? 'unknown';
    final payload = json['payload'] ?? json;

    switch (eventType) {
      case 'message':
        return ChatMessageEvent(payload: payload);
      case 'reaction':
      case 'reaction_update':
        return ReactionUpdateEvent(payload: payload);
      case 'typing':
        return TypingEvent(payload: payload);
      case 'read_receipt':
        return ReadReceiptEvent(payload: payload);
      case 'presence_update':
        return PresenceUpdateEvent(payload: payload);
      case 'call_offer':
      case 'call_answer':
      case 'ice_candidate':
      case 'call_end':
      case 'call_decline':
      case 'call_busy':
      case 'call_ring':
      case 'call_accept':
      case 'call_reject':
        return CallSignalEvent(eventType: eventType, payload: payload);
      case 'new_post':
        return FeedUpdateEvent(payload: payload);
      case 'post_update':
        return PostInteractionEvent(payload: payload);
      case 'post.liked':
        return PostLikedEvent(payload: payload);
      case 'post.commented':
        return PostCommentedEvent(payload: payload);
      case 'user.followed':
        return UserFollowedEvent(payload: payload);
      default:
        if (eventType.startsWith('call_')) {
          return CallSignalEvent(eventType: eventType, payload: payload);
        }
        return UnknownEvent(eventType: eventType, payload: payload);
    }
  }
}

class ChatMessageEvent extends RealtimeEvent {
  ChatMessageEvent({required super.payload}) : super(eventType: 'message');

  String get messageId =>
      payload['message_id'] as String? ??
      payload['msg_id'] as String? ??
      payload['id'] as String? ??
      '';
  String get senderId => payload['sender_id'] as String? ?? '';
  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get messageType =>
      payload['type'] as String? ?? payload['content_type'] as String? ?? 'text';
  String get text =>
      payload['text'] as String? ?? payload['content'] as String? ?? '';
  String? get mediaId => payload['media_id'] as String?;
  DateTime get createdAt =>
      DateTime.tryParse(payload['created_at'] as String? ?? '') ??
      DateTime.now();
}

class ReactionUpdateEvent extends RealtimeEvent {
  ReactionUpdateEvent({required super.payload}) : super(eventType: 'reaction');

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get messageId =>
      payload['message_id'] as String? ?? payload['msg_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get emoji => payload['emoji'] as String? ?? '';
  bool get added => payload['added'] as bool? ?? true;
}

class TypingEvent extends RealtimeEvent {
  TypingEvent({required super.payload}) : super(eventType: 'typing');

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  bool get isTyping => payload['is_typing'] as bool? ?? true;
}

class ReadReceiptEvent extends RealtimeEvent {
  ReadReceiptEvent({required super.payload})
    : super(eventType: 'read_receipt');

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get messageId => payload['message_id'] as String? ?? '';
  DateTime get readAt =>
      DateTime.tryParse(payload['read_at'] as String? ?? '') ?? DateTime.now();
}

class PresenceUpdateEvent extends RealtimeEvent {
  PresenceUpdateEvent({required super.payload})
    : super(eventType: 'presence_update');

  String get userId => payload['user_id'] as String? ?? '';
  bool get isOnline => payload['online'] as bool? ?? false;
}

class CallSignalEvent extends RealtimeEvent {
  CallSignalEvent({required super.eventType, required super.payload});

  String get senderId => payload['sender_id'] as String? ?? '';
  String get targetUserId => payload['target_user_id'] as String? ?? '';
  String? get callId => payload['call_id'] as String?;
  String? get callType => payload['call_type'] as String?;
  String? get sdp => payload['sdp'] as String?;
  Map<String, dynamic>? get candidate =>
      payload['candidate'] as Map<String, dynamic>?;
  String? get inviteId => payload['invite_id'] as String?;
}

class FeedUpdateEvent extends RealtimeEvent {
  FeedUpdateEvent({required super.payload}) : super(eventType: 'new_post');

  String get postId => payload['post_id'] as String? ?? '';
  String get authorId => payload['author_id'] as String? ?? '';
  String get contentSnippet => payload['snippet'] as String? ?? '';
}

class PostInteractionEvent extends RealtimeEvent {
  PostInteractionEvent({required super.payload})
    : super(eventType: 'post_update');

  String get postId => payload['post_id'] as String? ?? '';
  String get updateType => payload['update_type'] as String? ?? '';
  int? get likes => payload['likes'] as int?;
  int? get comments => payload['comments'] as int?;
}

class PostLikedEvent extends RealtimeEvent {
  PostLikedEvent({required super.payload}) : super(eventType: 'post.liked');

  String get postId => payload['post_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  int? get likeCount => payload['like_count'] as int?;
}

class PostCommentedEvent extends RealtimeEvent {
  PostCommentedEvent({required super.payload})
    : super(eventType: 'post.commented');

  String get postId => payload['post_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get commentId => payload['comment_id'] as String? ?? '';
  int? get commentCount => payload['comment_count'] as int?;
}

class UserFollowedEvent extends RealtimeEvent {
  UserFollowedEvent({required super.payload})
    : super(eventType: 'user.followed');

  String get followerId => payload['follower_id'] as String? ?? '';
  String get followedId =>
      payload['followed_id'] as String? ?? payload['user_id'] as String? ?? '';
  int? get followerCount => payload['follower_count'] as int?;
}

class UnknownEvent extends RealtimeEvent {
  UnknownEvent({required super.eventType, required super.payload});
}
