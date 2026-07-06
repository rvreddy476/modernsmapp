class MemoryCollection {
  final String id;
  final String userId;
  final String title;
  final String description;
  final String? coverUrl;
  final String visibility;
  final int itemCount;
  final DateTime createdAt;

  const MemoryCollection({
    required this.id,
    required this.userId,
    required this.title,
    required this.description,
    this.coverUrl,
    required this.visibility,
    required this.itemCount,
    required this.createdAt,
  });

  factory MemoryCollection.fromJson(Map<String, dynamic> json) {
    return MemoryCollection(
      id: json['id'] as String? ?? '',
      userId: json['user_id'] as String? ?? '',
      title: json['title'] as String? ?? '',
      description: json['description'] as String? ?? '',
      coverUrl: json['cover_url'] as String?,
      visibility: json['visibility'] as String? ?? 'private',
      itemCount: json['item_count'] as int? ?? 0,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

class OnThisDayMemory {
  final String id;
  final String postId;
  final int yearsAgo;
  final String snippet;
  final String? mediaUrl;

  const OnThisDayMemory({
    required this.id,
    required this.postId,
    required this.yearsAgo,
    required this.snippet,
    this.mediaUrl,
  });

  factory OnThisDayMemory.fromJson(Map<String, dynamic> json) {
    return OnThisDayMemory(
      id: json['id'] as String? ?? '',
      postId: json['post_id'] as String? ?? '',
      yearsAgo: json['years_ago'] as int? ?? 0,
      snippet: json['snippet'] as String? ?? '',
      mediaUrl: json['media_url'] as String?,
    );
  }
}
