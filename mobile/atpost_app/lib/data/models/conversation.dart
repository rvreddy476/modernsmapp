class ConversationMember {
  final String userId;
  final String role;
  final DateTime? joinedAt;
  final String? displayName;
  final String? avatarMediaId;

  const ConversationMember({
    required this.userId,
    this.role = 'member',
    this.joinedAt,
    this.displayName,
    this.avatarMediaId,
  });

  factory ConversationMember.fromJson(Map<String, dynamic> json) {
    return ConversationMember(
      userId: json['user_id'] as String? ?? '',
      role: json['role'] as String? ?? 'member',
      joinedAt: _parseDateTime(json['joined_at']),
      displayName: json['display_name'] as String?,
      avatarMediaId: json['avatar_media_id'] as String?,
    );
  }
}

class Conversation {
  final String id;
  final String type;
  final String? name;
  final String? title;
  final String? createdBy;
  final List<ConversationMember> members;
  final List<String> participantIds;
  final String? lastMessage;
  final DateTime? lastMessageAt;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final int unreadCount;

  const Conversation({
    required this.id,
    this.type = 'direct',
    this.name,
    this.title,
    this.createdBy,
    this.members = const [],
    this.participantIds = const [],
    this.lastMessage,
    this.lastMessageAt,
    this.createdAt,
    this.updatedAt,
    this.unreadCount = 0,
  });

  factory Conversation.fromJson(Map<String, dynamic> json) {
    final members =
        (json['members'] as List<dynamic>?)
            ?.map(
              (member) =>
                  ConversationMember.fromJson(member as Map<String, dynamic>),
            )
            .toList() ??
        const <ConversationMember>[];

    final participantIds =
        (json['participant_ids'] as List<dynamic>?)?.cast<String>() ??
        members.map((member) => member.userId).toList();
    final fallbackName =
        members
            .map((member) => (member.displayName ?? member.userId).trim())
            .where((value) => value.isNotEmpty)
            .cast<String>()
            .toList();

    return Conversation(
      id: json['id'] as String? ?? json['conversation_id'] as String? ?? '',
      type: json['type'] as String? ?? 'direct',
      name:
          json['name'] as String? ??
          json['title'] as String? ??
          (fallbackName.isNotEmpty ? fallbackName.first : null),
      title: json['title'] as String? ?? json['name'] as String?,
      createdBy: json['created_by'] as String?,
      members: members,
      participantIds: participantIds,
      lastMessage:
          json['last_message'] as String? ??
          json['latest_message'] as String? ??
          json['preview'] as String?,
      lastMessageAt:
          _parseDateTime(json['last_message_at']) ??
          _parseDateTime(json['updated_at']),
      createdAt: _parseDateTime(json['created_at']),
      updatedAt: _parseDateTime(json['updated_at']),
      unreadCount: json['unread_count'] as int? ?? 0,
    );
  }

  String displayNameFor(String? currentUserId) {
    final explicitName = (title ?? name ?? '').trim();
    if (explicitName.isNotEmpty) {
      return explicitName;
    }

    final otherMembers =
        members
            .where(
              (member) =>
                  currentUserId == null || member.userId != currentUserId,
            )
            .toList();
    final names =
        otherMembers
            .map((member) => (member.displayName ?? member.userId).trim())
            .where((value) => value.isNotEmpty)
            .toList();

    if (names.isEmpty) {
      return type == 'group' ? 'Group Chat' : 'Direct Message';
    }
    if (type == 'group') {
      return names.join(', ');
    }
    return names.first;
  }

  String? directPeerId(String? currentUserId) {
    if (type == 'group') return null;

    for (final member in members) {
      if (member.userId.isNotEmpty && member.userId != currentUserId) {
        return member.userId;
      }
    }
    for (final participantId in participantIds) {
      if (participantId.isNotEmpty && participantId != currentUserId) {
        return participantId;
      }
    }
    return null;
  }
}

class MessageReaction {
  final String emoji;
  final List<String> userIds;

  const MessageReaction({
    required this.emoji,
    this.userIds = const [],
  });

  factory MessageReaction.fromJson(Map<String, dynamic> json) {
    return MessageReaction(
      emoji: json['emoji'] as String? ?? '',
      userIds:
          (json['user_ids'] as List<dynamic>?)?.cast<String>() ??
          const <String>[],
    );
  }
}

class Message {
  final String id;
  final String conversationId;
  final String senderId;
  final String? senderName;
  final String content;
  final String contentType;
  final String? mediaId;
  final String? bucket;
  final DateTime? ts;
  final DateTime createdAt;
  final List<MessageReaction> reactions;

  const Message({
    required this.id,
    required this.conversationId,
    required this.senderId,
    this.senderName,
    required this.content,
    this.contentType = 'text',
    this.mediaId,
    this.bucket,
    this.ts,
    required this.createdAt,
    this.reactions = const [],
  });

  factory Message.fromJson(Map<String, dynamic> json) {
    return Message(
      id:
          json['msg_id'] as String? ??
          json['message_id'] as String? ??
          json['id'] as String? ??
          '',
      conversationId: json['conversation_id'] as String? ?? '',
      senderId: json['sender_id'] as String? ?? '',
      senderName:
          json['sender_display_name'] as String? ??
          json['sender_name'] as String?,
      content:
          json['text'] as String? ??
          json['content'] as String? ??
          '',
      contentType:
          json['type'] as String? ?? json['content_type'] as String? ?? 'text',
      mediaId: json['media_id'] as String?,
      bucket: json['bucket'] as String?,
      ts: _parseDateTime(json['ts']),
      createdAt:
          _parseDateTime(json['created_at']) ??
          _parseDateTime(json['ts']) ??
          DateTime.now(),
      reactions:
          (json['reactions'] as List<dynamic>?)
              ?.map(
                (reaction) =>
                    MessageReaction.fromJson(reaction as Map<String, dynamic>),
              )
              .toList() ??
          const <MessageReaction>[],
    );
  }

  String get previewText {
    if (content.trim().isNotEmpty) {
      return content.trim();
    }
    if (mediaId != null && mediaId!.isNotEmpty) {
      return 'Attachment';
    }
    return '';
  }
}

DateTime? _parseDateTime(dynamic value) {
  if (value == null) return null;
  if (value is String) return DateTime.tryParse(value)?.toLocal();
  return null;
}
