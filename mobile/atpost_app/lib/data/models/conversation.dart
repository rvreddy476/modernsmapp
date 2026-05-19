import 'package:atpost_app/core/utils/app_logger.dart';

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
      userId: (json['user_id'] ?? '').toString(),
      role: (json['role'] ?? 'member').toString(),
      joinedAt: _parseDateNullable(json['joined_at']),
      displayName: json['display_name']?.toString(),
      avatarMediaId: json['avatar_media_id']?.toString(),
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
  final bool isArchived;

  /// True when the conversation is a pending message request (spec §3.3).
  /// Request conversations are kept out of the main list and surfaced
  /// under a dedicated "Requests" folder until accepted.
  final bool isRequest;

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
    this.isArchived = false,
    this.isRequest = false,
  });

  factory Conversation.fromJson(Map<String, dynamic> json) {
    try {
      final members = (json['members'] as List? ?? [])
          .map((m) => ConversationMember.fromJson(Map<String, dynamic>.from(m)))
          .toList();

      final participantIds = (json['participant_ids'] as List? ?? [])
          .map((e) => e.toString())
          .toList();

      return Conversation(
        id: (json['id'] ?? json['conversation_id'] ?? '').toString(),
        type: (json['type'] ?? 'direct').toString(),
        name: (json['name'] ?? json['title'] ?? '').toString(),
        title: json['title']?.toString(),
        createdBy: json['created_by']?.toString(),
        members: members,
        participantIds: participantIds.isNotEmpty ? participantIds : members.map((m) => m.userId).toList(),
        lastMessage: (json['last_message'] ?? json['latest_message'] ?? json['preview'] ?? '').toString(),
        lastMessageAt: _parseDateNullable(json['last_message_at'] ?? json['updated_at']),
        createdAt: _parseDateNullable(json['created_at']),
        updatedAt: _parseDateNullable(json['updated_at']),
        unreadCount: _toInt(json['unread_count']),
        isArchived: json['is_archived'] == true,
        isRequest: json['is_request'] == true,
      );
    } catch (e, st) {
      AppLogger.error('Conversation.fromJson failed', error: e, stackTrace: st);
      return Conversation.empty();
    }
  }

  static Conversation empty() => Conversation(id: 'err_${DateTime.now().ms}', members: const []);

  String displayNameFor(String? currentUserId) {
    final explicitTitle = (title ?? name ?? '').trim();
    if (explicitTitle.isNotEmpty) return explicitTitle;
    final others = members
        .where((m) => m.userId != currentUserId)
        .map((m) => (m.displayName ?? '').trim())
        .where((value) => value.isNotEmpty)
        .toList();
    if (others.isEmpty) return type == 'group' ? 'Group Chat' : 'Direct Message';
    return type == 'group' ? others.join(', ') : others.first;
  }

  String? directPeerId(String? currentUserId) {
    if (type == 'group') return null;
    return participantIds.firstWhere((id) => id != currentUserId, orElse: () => '');
  }

  /// Resolves a member's display name by user id — used to label group
  /// typing indicators ("Ravi is typing…"). Returns null when the
  /// member is unknown or has no display name loaded.
  String? memberNameFor(String userId) {
    for (final member in members) {
      if (member.userId == userId) {
        final name = (member.displayName ?? '').trim();
        return name.isEmpty ? null : name;
      }
    }
    return null;
  }

  int participantCountFor(String? currentUserId) {
    final memberIds = members
        .map((member) => member.userId)
        .where((userId) => userId.isNotEmpty)
        .toSet();
    if (memberIds.isNotEmpty) {
      return type == 'group'
          ? memberIds.length
          : memberIds.where((userId) => userId != currentUserId).length;
    }
    final ids = participantIds.where((userId) => userId.isNotEmpty).toSet();
    return type == 'group'
        ? ids.length
        : ids.where((userId) => userId != currentUserId).length;
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
  final DateTime createdAt;

  const Message({
    required this.id,
    required this.conversationId,
    required this.senderId,
    this.senderName,
    required this.content,
    this.contentType = 'text',
    this.mediaId,
    required this.createdAt,
  });

  factory Message.fromJson(Map<String, dynamic> json) {
    try {
      return Message(
        id: (json['msg_id'] ?? json['message_id'] ?? json['id'] ?? '').toString(),
        conversationId: (json['conversation_id'] ?? '').toString(),
        senderId: (json['sender_id'] ?? '').toString(),
        senderName: json['sender_display_name']?.toString() ?? json['sender_name']?.toString(),
        content: (json['text'] ?? json['content'] ?? '').toString(),
        contentType: (json['type'] ?? json['content_type'] ?? 'text').toString(),
        mediaId: json['media_id']?.toString(),
        createdAt: _parseDate(json['created_at'] ?? json['ts']),
      );
    } catch (e, st) {
      AppLogger.error('Message.fromJson failed', error: e, stackTrace: st);
      return Message.empty();
    }
  }

  static Message empty() => Message(id: '', conversationId: '', senderId: '', content: 'Unavailable', createdAt: DateTime.now());

  String get previewText {
    if (content.isNotEmpty) return content;
    if (mediaId != null) return 'Attachment';
    return '';
  }
}

// --- Resilience Helpers ---

DateTime _parseDate(dynamic data) {
  if (data is String) return DateTime.tryParse(data) ?? DateTime.now();
  return DateTime.now();
}

DateTime? _parseDateNullable(dynamic data) {
  if (data == null) return null;
  return _parseDate(data);
}

int _toInt(dynamic data) {
  if (data is int) return data;
  if (data is String) return int.tryParse(data) ?? 0;
  return 0;
}

extension on DateTime {
  String get ms => millisecondsSinceEpoch.toString();
}
