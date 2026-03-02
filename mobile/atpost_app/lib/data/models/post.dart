class Post {
  final String id;
  final String authorId;
  final String? authorName;
  final String? authorAvatar;
  final String content;
  final String contentType; // 'post', 'poll', 'reel', 'video'
  final String visibility;
  final List<String> tags;
  final List<String> mediaIds;
  final int likeCount;
  final int commentCount;
  final int shareCount;
  final int? durationSeconds;
  final bool isLiked;
  final bool isBookmarked;
  final DateTime createdAt;

  const Post({
    required this.id,
    required this.authorId,
    this.authorName,
    this.authorAvatar,
    required this.content,
    this.contentType = 'post',
    this.visibility = 'public',
    this.tags = const [],
    this.mediaIds = const [],
    this.likeCount = 0,
    this.commentCount = 0,
    this.shareCount = 0,
    this.durationSeconds,
    this.isLiked = false,
    this.isBookmarked = false,
    required this.createdAt,
  });

  factory Post.fromJson(Map<String, dynamic> json) {
    return Post(
      id: json['id'] as String? ?? json['post_id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      authorName: json['author_name'] as String?,
      authorAvatar: json['author_avatar'] as String?,
      content: json['content'] as String? ?? json['text'] as String? ?? '',
      contentType: json['content_type'] as String? ?? 'post',
      visibility: json['visibility'] as String? ?? 'public',
      tags: (json['tags'] as List<dynamic>?)?.cast<String>() ?? [],
      mediaIds: (json['media_ids'] as List<dynamic>?)?.cast<String>() ?? [],
      likeCount: json['like_count'] as int? ?? json['likes'] as int? ?? 0,
      commentCount: json['comment_count'] as int? ?? json['comments'] as int? ?? 0,
      shareCount: json['share_count'] as int? ?? json['shares'] as int? ?? 0,
      durationSeconds: json['duration_seconds'] as int?,
      isLiked: json['is_liked'] as bool? ?? false,
      isBookmarked: json['is_bookmarked'] as bool? ?? false,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }

  bool get isReel => contentType == 'reel';
  bool get isVideo => contentType == 'video';
}

class Comment {
  final String id;
  final String postId;
  final String authorId;
  final String? authorName;
  final String? authorAvatar;
  final String text;
  final int likeCount;
  final DateTime createdAt;

  const Comment({
    required this.id,
    required this.postId,
    required this.authorId,
    this.authorName,
    this.authorAvatar,
    required this.text,
    this.likeCount = 0,
    required this.createdAt,
  });

  factory Comment.fromJson(Map<String, dynamic> json) {
    return Comment(
      id: json['id'] as String? ?? json['comment_id'] as String? ?? '',
      postId: json['post_id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      authorName: json['author_name'] as String?,
      authorAvatar: json['author_avatar'] as String?,
      text: json['text'] as String? ?? '',
      likeCount: json['like_count'] as int? ?? 0,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
