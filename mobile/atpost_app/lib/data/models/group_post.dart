class GroupPost {
  final String id;
  final String groupId;
  final String authorId;
  final String contentType;
  final String status;
  final String? channelId;
  final String? title;
  final String? body;
  final String? channelName;
  final String? authorName;
  final String? authorAvatarUrl;
  final bool needsApproval;
  final bool isPinned;
  final bool isAnnouncement;
  final int sparkCount;
  final int commentCount;
  final int echoCount;
  final int viewCount;
  final DateTime createdAt;

  const GroupPost({
    required this.id,
    required this.groupId,
    required this.authorId,
    this.contentType = 'post',
    this.status = 'published',
    this.channelId,
    this.title,
    this.body,
    this.channelName,
    this.authorName,
    this.authorAvatarUrl,
    this.needsApproval = false,
    this.isPinned = false,
    this.isAnnouncement = false,
    this.sparkCount = 0,
    this.commentCount = 0,
    this.echoCount = 0,
    this.viewCount = 0,
    required this.createdAt,
  });

  factory GroupPost.fromJson(Map<String, dynamic> json) {
    return GroupPost(
      id: json['id'] as String? ?? json['post_id'] as String? ?? '',
      groupId: json['group_id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      contentType: json['content_type'] as String? ?? 'post',
      status: json['status'] as String? ?? 'published',
      channelId: json['channel_id'] as String?,
      title: json['title'] as String?,
      body: json['body'] as String? ?? json['content'] as String?,
      channelName: json['channel_name'] as String?,
      authorName: json['author_name'] as String?,
      authorAvatarUrl: json['author_avatar_url'] as String?,
      needsApproval: json['needs_approval'] as bool? ?? false,
      isPinned: json['is_pinned'] as bool? ?? false,
      isAnnouncement: json['is_announcement'] as bool? ?? false,
      sparkCount: json['spark_count'] as int? ?? 0,
      commentCount: json['comment_count'] as int? ?? 0,
      echoCount: json['echo_count'] as int? ?? 0,
      viewCount: json['view_count'] as int? ?? 0,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

class GroupChannel {
  final String id;
  final String groupId;
  final String name;
  final String type;
  final String description;
  final String whoCanPost;
  final String createdBy;
  final bool isDefault;
  final bool isArchived;
  final int sortOrder;
  final int postCount;

  const GroupChannel({
    required this.id,
    required this.groupId,
    required this.name,
    this.type = 'discussion',
    this.description = '',
    this.whoCanPost = 'members',
    this.createdBy = '',
    this.isDefault = false,
    this.isArchived = false,
    this.sortOrder = 0,
    this.postCount = 0,
  });

  factory GroupChannel.fromJson(Map<String, dynamic> json) {
    return GroupChannel(
      id: json['id'] as String? ?? json['channel_id'] as String? ?? '',
      groupId: json['group_id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      type: json['type'] as String? ?? 'discussion',
      description: json['description'] as String? ?? '',
      whoCanPost: json['who_can_post'] as String? ?? 'members',
      createdBy: json['created_by'] as String? ?? '',
      isDefault: json['is_default'] as bool? ?? false,
      isArchived: json['is_archived'] as bool? ?? false,
      sortOrder: json['sort_order'] as int? ?? 0,
      postCount: json['post_count'] as int? ?? 0,
    );
  }
}
