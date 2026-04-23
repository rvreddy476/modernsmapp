class Story {
  final String id;
  final String authorId;
  final String authorName;
  final String? avatarMediaId;
  final List<StoryItem> items;
  final DateTime createdAt;

  const Story({
    required this.id,
    required this.authorId,
    required this.authorName,
    this.avatarMediaId,
    required this.items,
    required this.createdAt,
  });

  factory Story.fromJson(Map<String, dynamic> json) {
    final rawItems = json['items'] as List<dynamic>?;
    final items = rawItems == null
        ? [
            StoryItem.fromJson({
              'id': json['id'],
              'media_url': json['media_url'],
              'media_id': json['media_url'],
              'media_type': json['media_type'],
              'text': json['caption'],
              'expires_at': json['expires_at'],
            }),
          ]
        : rawItems
            .map((e) => StoryItem.fromJson(e as Map<String, dynamic>))
            .toList();

    return Story(
      id: json['id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      authorName: (json['author_name'] ?? json['display_name'])?.toString() ?? '',
      avatarMediaId: json['avatar_media_id'] as String?,
      items: items,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

class StoryItem {
  final String id;
  final String mediaId;
  final String mediaType; // 'image' | 'video'
  final String? text;
  final DateTime expiresAt;

  const StoryItem({
    required this.id,
    required this.mediaId,
    required this.mediaType,
    this.text,
    required this.expiresAt,
  });

  factory StoryItem.fromJson(Map<String, dynamic> json) {
    return StoryItem(
      id: json['id'] as String? ?? '',
      mediaId: (json['media_url'] ?? json['media_id']) as String? ?? '',
      mediaType: json['media_type'] as String? ?? 'image',
      text: json['text'] as String?,
      expiresAt: json['expires_at'] != null
          ? DateTime.parse(json['expires_at'] as String)
          : DateTime.now().add(const Duration(hours: 24)),
    );
  }
}
