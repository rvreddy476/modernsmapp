
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
      case 'call_ring':
      case 'call_accept':
      case 'call_reject':
        return CallSignalEvent(type: type, payload: payload);
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
        if (type.startsWith('call_')) {
          return CallSignalEvent(type: type, payload: payload);
        }
        return UnknownEvent(type: type, payload: payload);
    }
  }
}

class ChatMessageEvent extends RealtimeEvent {
  ChatMessageEvent({required super.payload}) : super(type: 'message');

  String get messageId => payload['message_id'] as String? ?? payload['msg_id'] as String? ?? '';
  String get senderId => payload['sender_id'] as String? ?? '';
  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get text => payload['text'] as String? ?? '';
  @override
  String get type => payload['type'] as String? ?? 'text';
  String? get mediaId => payload['media_id'] as String?;
  DateTime get createdAt => DateTime.tryParse(payload['created_at'] as String? ?? '') ?? DateTime.now();
}

class ReactionUpdateEvent extends RealtimeEvent {
  ReactionUpdateEvent({required super.payload}) : super(type: 'reaction');

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get messageId => payload['message_id'] as String? ?? payload['msg_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get emoji => payload['emoji'] as String? ?? '';
  bool get added => payload['added'] as bool? ?? true;
}

class TypingEvent extends RealtimeEvent {
  TypingEvent({required super.payload}) : super(type: 'typing');

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  bool get isTyping => payload['is_typing'] as bool? ?? true;
}

class ReadReceiptEvent extends RealtimeEvent {
  ReadReceiptEvent({required super.payload}) : super(type: 'read_receipt');

  String get conversationId => payload['conversation_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get messageId => payload['message_id'] as String? ?? '';
  DateTime get readAt => DateTime.tryParse(payload['read_at'] as String? ?? '') ?? DateTime.now();
}

class CallSignalEvent extends RealtimeEvent {
  CallSignalEvent({required super.type, required super.payload});

  String get senderId => payload['sender_id'] as String? ?? '';
  String get targetUserId => payload['target_user_id'] as String? ?? '';
  String? get callType => payload['call_type'] as String?;
  String? get sdp => payload['sdp'] as String?;
  Map<String, dynamic>? get candidate => payload['candidate'] as Map<String, dynamic>?;
}

class FeedUpdateEvent extends RealtimeEvent {
  FeedUpdateEvent({required super.payload}) : super(type: 'new_post');

  String get postId => payload['post_id'] as String? ?? '';
  String get authorId => payload['author_id'] as String? ?? '';
  String get contentSnippet => payload['snippet'] as String? ?? '';
}

class PostInteractionEvent extends RealtimeEvent {
  PostInteractionEvent({required super.payload}) : super(type: 'post_update');

  String get postId => payload['post_id'] as String? ?? '';
  String get updateType => payload['update_type'] as String? ?? ''; // reaction, comment, share
  int? get likes => payload['likes'] as int?;
  int? get comments => payload['comments'] as int?;
}

/// Fired when a post receives a new like via real-time WebSocket.
class PostLikedEvent extends RealtimeEvent {
  PostLikedEvent({required super.payload}) : super(type: 'post.liked');

  String get postId => payload['post_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  /// The new total like count for the post, if provided by the server.
  int? get likeCount => payload['like_count'] as int?;
}

/// Fired when a post receives a new comment via real-time WebSocket.
class PostCommentedEvent extends RealtimeEvent {
  PostCommentedEvent({required super.payload}) : super(type: 'post.commented');

  String get postId => payload['post_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get commentId => payload['comment_id'] as String? ?? '';
  /// The new total comment count for the post, if provided by the server.
  int? get commentCount => payload['comment_count'] as int?;
}

/// Fired when the current user gains a new follower via real-time WebSocket.
class UserFollowedEvent extends RealtimeEvent {
  UserFollowedEvent({required super.payload}) : super(type: 'user.followed');

  /// The user ID of the person who followed.
  String get followerId => payload['follower_id'] as String? ?? '';
  /// The user ID of the person who was followed (typically the current user).
  String get followedId => payload['followed_id'] as String? ?? payload['user_id'] as String? ?? '';
  /// The new total follower count, if provided by the server.
  int? get followerCount => payload['follower_count'] as int?;
}

class UnknownEvent extends RealtimeEvent {
  UnknownEvent({required super.type, required super.payload});
}
