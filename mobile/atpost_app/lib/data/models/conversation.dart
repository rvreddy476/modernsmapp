class Conversation {
  final String id;
  final String type; // 'direct', 'group'
  final String? name;
  final List<String> participantIds;
  final String? lastMessage;
  final DateTime? lastMessageAt;
  final int unreadCount;

  const Conversation({
    required this.id,
    this.type = 'direct',
    this.name,
    this.participantIds = const [],
    this.lastMessage,
    this.lastMessageAt,
    this.unreadCount = 0,
  });

  factory Conversation.fromJson(Map<String, dynamic> json) {
    return Conversation(
      id: json['id'] as String? ?? json['conversation_id'] as String? ?? '',
      type: json['type'] as String? ?? 'direct',
      name: json['name'] as String?,
      participantIds:
          (json['participant_ids'] as List<dynamic>?)?.cast<String>() ?? [],
      lastMessage: json['last_message'] as String?,
      lastMessageAt: json['last_message_at'] != null
          ? DateTime.parse(json['last_message_at'] as String)
          : null,
      unreadCount: json['unread_count'] as int? ?? 0,
    );
  }
}

class Message {
  final String id;
  final String conversationId;
  final String senderId;
  final String? senderName;
  final String content;
  final String contentType; // 'text', 'image', 'file', 'audio'
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
    return Message(
      id: json['id'] as String? ?? json['message_id'] as String? ?? '',
      conversationId: json['conversation_id'] as String? ?? '',
      senderId: json['sender_id'] as String? ?? '',
      senderName: json['sender_name'] as String?,
      content: json['content'] as String? ?? '',
      contentType: json['content_type'] as String? ?? 'text',
      mediaId: json['media_id'] as String?,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
