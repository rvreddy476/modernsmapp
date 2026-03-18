class CommunityPost {
  final String id;
  final String communityId;
  final String spaceId;
  final String authorId;
  final String contentType;
  final String status;
  final String? title;
  final String? body;
  final String? parentPostId;
  final int threadDepth;
  final int replyCount;
  final int sparkCount;
  final int commentCount;
  final int viewCount;
  final bool isPinned;
  final bool isAnnouncement;
  final bool isFeatured;
  final bool isAnswered;
  final bool isExpertAnswer;
  final String? acceptedAnswerId;
  final String? authorName;
  final String? authorAvatarUrl;
  final String? spaceName;
  final DateTime createdAt;

  const CommunityPost({
    required this.id,
    required this.communityId,
    required this.spaceId,
    required this.authorId,
    this.contentType = 'discussion',
    this.status = 'published',
    this.title,
    this.body,
    this.parentPostId,
    this.threadDepth = 0,
    this.replyCount = 0,
    this.sparkCount = 0,
    this.commentCount = 0,
    this.viewCount = 0,
    this.isPinned = false,
    this.isAnnouncement = false,
    this.isFeatured = false,
    this.isAnswered = false,
    this.isExpertAnswer = false,
    this.acceptedAnswerId,
    this.authorName,
    this.authorAvatarUrl,
    this.spaceName,
    required this.createdAt,
  });

  factory CommunityPost.fromJson(Map<String, dynamic> json) {
    return CommunityPost(
      id: json['id'] as String? ?? json['post_id'] as String? ?? '',
      communityId: json['community_id'] as String? ?? '',
      spaceId: json['space_id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      contentType: json['content_type'] as String? ?? 'discussion',
      status: json['status'] as String? ?? 'published',
      title: json['title'] as String?,
      body: json['body'] as String? ?? json['content'] as String?,
      parentPostId: json['parent_post_id'] as String?,
      threadDepth: json['thread_depth'] as int? ?? 0,
      replyCount: json['reply_count'] as int? ?? 0,
      sparkCount: json['spark_count'] as int? ?? 0,
      commentCount: json['comment_count'] as int? ?? 0,
      viewCount: json['view_count'] as int? ?? 0,
      isPinned: json['is_pinned'] as bool? ?? false,
      isAnnouncement: json['is_announcement'] as bool? ?? false,
      isFeatured: json['is_featured'] as bool? ?? false,
      isAnswered: json['is_answered'] as bool? ?? false,
      isExpertAnswer: json['is_expert_answer'] as bool? ?? false,
      acceptedAnswerId: json['accepted_answer_id'] as String?,
      authorName: json['author_name'] as String?,
      authorAvatarUrl: json['author_avatar_url'] as String?,
      spaceName: json['space_name'] as String?,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

class WikiPage {
  final String id;
  final String communityId;
  final String title;
  final String slug;
  final String content;
  final String? contentHtml;
  final String? category;
  final String? createdBy;
  final String? updatedBy;
  final bool isPinned;
  final int version;
  final DateTime createdAt;
  final DateTime updatedAt;

  const WikiPage({
    required this.id,
    required this.communityId,
    required this.title,
    this.slug = '',
    this.content = '',
    this.contentHtml,
    this.category,
    this.createdBy,
    this.updatedBy,
    this.isPinned = false,
    this.version = 1,
    required this.createdAt,
    required this.updatedAt,
  });

  factory WikiPage.fromJson(Map<String, dynamic> json) {
    return WikiPage(
      id: json['id'] as String? ?? '',
      communityId: json['community_id'] as String? ?? '',
      title: json['title'] as String? ?? '',
      slug: json['slug'] as String? ?? '',
      content: json['content'] as String? ?? '',
      contentHtml: json['content_html'] as String?,
      category: json['category'] as String?,
      createdBy: json['created_by'] as String?,
      updatedBy: json['updated_by'] as String?,
      isPinned: json['is_pinned'] as bool? ?? false,
      version: json['version'] as int? ?? 1,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
      updatedAt: json['updated_at'] != null
          ? DateTime.parse(json['updated_at'] as String)
          : DateTime.now(),
    );
  }
}
