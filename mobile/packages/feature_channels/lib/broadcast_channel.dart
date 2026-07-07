class BroadcastChannel {
  final String id;
  final String name;
  final String handle;
  final String description;
  final String channelType;
  final String status;
  final String? avatarMediaId;
  final String? bannerMediaId;
  final int subscriberCount;
  final int updateCount;
  final bool isVerified;
  final String? viewerRole;
  final DateTime createdAt;

  const BroadcastChannel({
    required this.id,
    required this.name,
    required this.handle,
    required this.description,
    required this.channelType,
    required this.status,
    this.avatarMediaId,
    this.bannerMediaId,
    this.subscriberCount = 0,
    this.updateCount = 0,
    this.isVerified = false,
    this.viewerRole,
    required this.createdAt,
  });

  factory BroadcastChannel.fromJson(Map<String, dynamic> json) {
    return BroadcastChannel(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      handle: json['handle'] as String? ?? '',
      description: json['description'] as String? ?? '',
      channelType: json['channel_type'] as String? ?? 'general',
      status: json['status'] as String? ?? 'active',
      avatarMediaId: json['avatar_media_id'] as String?,
      bannerMediaId: json['banner_media_id'] as String?,
      subscriberCount: json['subscriber_count'] as int? ?? 0,
      updateCount: json['update_count'] as int? ?? 0,
      isVerified: json['is_verified'] as bool? ?? false,
      viewerRole: json['viewer_role'] as String?,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

class ChannelUpdate {
  final String id;
  final String channelId;
  final String authorId;
  final String updateType;
  final String body;
  final String status;
  final String? title;
  final List<String> mediaIds;
  final int viewCount;
  final int reactionCount;
  final int commentCount;
  final DateTime createdAt;

  const ChannelUpdate({
    required this.id,
    required this.channelId,
    required this.authorId,
    required this.updateType,
    required this.body,
    required this.status,
    this.title,
    this.mediaIds = const [],
    this.viewCount = 0,
    this.reactionCount = 0,
    this.commentCount = 0,
    required this.createdAt,
  });

  factory ChannelUpdate.fromJson(Map<String, dynamic> json) {
    return ChannelUpdate(
      id: json['id'] as String? ?? '',
      channelId: json['channel_id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      updateType: json['update_type'] as String? ?? 'text',
      body: json['body'] as String? ?? '',
      status: json['status'] as String? ?? 'published',
      title: json['title'] as String?,
      mediaIds: (json['media_ids'] as List<dynamic>?)
              ?.map((e) => e as String)
              .toList() ??
          [],
      viewCount: json['view_count'] as int? ?? 0,
      reactionCount: json['reaction_count'] as int? ?? 0,
      commentCount: json['comment_count'] as int? ?? 0,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
