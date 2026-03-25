/// Base class for all real-time events received via WebSocket.
abstract class RealtimeEvent {
  final String eventType;
  final dynamic payload;

  const RealtimeEvent({required this.eventType, required this.payload});

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
      case 'live_chat_message':
        return LiveChatMessageEvent(payload: payload);
      case 'live_stream_viewers':
        return LiveStreamViewersEvent(payload: payload);
      case 'live_stream_likes':
        return LiveStreamLikesEvent(payload: payload);
      case 'live_message_pinned':
        return LiveMessagePinnedEvent(payload: payload);
      case 'live_stream_ended':
        return LiveStreamEndedEvent(payload: payload);
      case 'live_user_muted':
        return LiveUserMutedEvent(payload: payload);
      case 'live_user_unmuted':
        return LiveUserUnmutedEvent(payload: payload);
      case 'live_word_filter_added':
        return LiveWordFilterAddedEvent(payload: payload);
      case 'live_word_filter_removed':
        return LiveWordFilterRemovedEvent(payload: payload);
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
      payload['type'] as String? ??
      payload['content_type'] as String? ??
      'text';
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
  ReadReceiptEvent({required super.payload}) : super(eventType: 'read_receipt');

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

class LiveChatMessageEvent extends RealtimeEvent {
  LiveChatMessageEvent({required super.payload})
    : super(eventType: 'live_chat_message');

  String get streamId => payload['stream_id'] as String? ?? '';
  String get messageId =>
      payload['id'] as String? ?? payload['message_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get message => payload['message'] as String? ?? '';
  bool get isPinned => payload['is_pinned'] as bool? ?? false;
  DateTime get createdAt =>
      DateTime.tryParse(payload['created_at'] as String? ?? '')?.toLocal() ??
      DateTime.now();
}

class LiveStreamViewersEvent extends RealtimeEvent {
  LiveStreamViewersEvent({required super.payload})
    : super(eventType: 'live_stream_viewers');

  String get streamId => payload['stream_id'] as String? ?? '';
  int get viewerCount => payload['viewer_count'] as int? ?? 0;
  int? get peakViewers => payload['peak_viewers'] as int?;
  int? get totalViewers => payload['total_viewers'] as int?;
  String? get reason => payload['reason'] as String?;
  String? get actorId => payload['actor_id'] as String?;
}

class LiveStreamLikesEvent extends RealtimeEvent {
  LiveStreamLikesEvent({required super.payload})
    : super(eventType: 'live_stream_likes');

  String get streamId => payload['stream_id'] as String? ?? '';
  int get likeCount => payload['like_count'] as int? ?? 0;
}

class LiveMessagePinnedEvent extends RealtimeEvent {
  LiveMessagePinnedEvent({required super.payload})
    : super(eventType: 'live_message_pinned');

  String get streamId => payload['stream_id'] as String? ?? '';
  String get messageId => payload['message_id'] as String? ?? '';
  String get pinnedBy => payload['pinned_by'] as String? ?? '';
  DateTime get pinnedAt =>
      DateTime.tryParse(payload['pinned_at'] as String? ?? '')?.toLocal() ??
      DateTime.now();
}

class LiveStreamEndedEvent extends RealtimeEvent {
  LiveStreamEndedEvent({required super.payload})
    : super(eventType: 'live_stream_ended');

  String get streamId => payload['stream_id'] as String? ?? '';
  int? get peakViewers => payload['peak_viewers'] as int?;
  int? get totalViewers => payload['total_viewers'] as int?;
  int? get durationSeconds => payload['duration_secs'] as int?;
  DateTime get endedAt =>
      DateTime.tryParse(payload['ended_at'] as String? ?? '')?.toLocal() ??
      DateTime.now();
}

class LiveUserMutedEvent extends RealtimeEvent {
  LiveUserMutedEvent({required super.payload})
    : super(eventType: 'live_user_muted');

  String get streamId => payload['stream_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get mutedBy => payload['muted_by'] as String? ?? '';
  DateTime get mutedAt =>
      DateTime.tryParse(payload['muted_at'] as String? ?? '')?.toLocal() ??
      DateTime.now();
}

class LiveUserUnmutedEvent extends RealtimeEvent {
  LiveUserUnmutedEvent({required super.payload})
    : super(eventType: 'live_user_unmuted');

  String get streamId => payload['stream_id'] as String? ?? '';
  String get userId => payload['user_id'] as String? ?? '';
  String get unmutedBy => payload['unmuted_by'] as String? ?? '';
}

class LiveWordFilterAddedEvent extends RealtimeEvent {
  LiveWordFilterAddedEvent({required super.payload})
    : super(eventType: 'live_word_filter_added');

  String get streamId => payload['stream_id'] as String? ?? '';
  String get word => payload['word'] as String? ?? '';
  String get addedBy => payload['added_by'] as String? ?? '';
}

class LiveWordFilterRemovedEvent extends RealtimeEvent {
  LiveWordFilterRemovedEvent({required super.payload})
    : super(eventType: 'live_word_filter_removed');

  String get streamId => payload['stream_id'] as String? ?? '';
  String get word => payload['word'] as String? ?? '';
  String get removedBy => payload['removed_by'] as String? ?? '';
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
