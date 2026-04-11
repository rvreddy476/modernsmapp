class Community {
  final String id;
  final String name;
  final String handle;
  final String description;
  final String communityType;
  final String status;
  final String? avatarMediaId;
  final String? bannerMediaId;
  final int memberCount;
  final int spaceCount;
  final bool isVerified;
  final String? viewerRole;
  final List<String> topicTags;
  final DateTime createdAt;

  const Community({
    required this.id,
    required this.name,
    required this.handle,
    required this.description,
    required this.communityType,
    required this.status,
    this.avatarMediaId,
    this.bannerMediaId,
    this.memberCount = 0,
    this.spaceCount = 0,
    this.isVerified = false,
    this.viewerRole,
    this.topicTags = const [],
    required this.createdAt,
  });

  factory Community.fromJson(Map<String, dynamic> json) {
    final topicTagsJson = json['topic_tags'] as List<dynamic>? ?? const [];
    return Community(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      handle: json['handle'] as String? ?? '',
      description: json['description'] as String? ?? '',
      communityType: json['community_type'] as String? ?? 'public',
      status: json['status'] as String? ?? 'active',
      avatarMediaId: json['avatar_media_id'] as String?,
      bannerMediaId: json['banner_media_id'] as String?,
      memberCount: _asInt(json['member_count']),
      spaceCount: _asInt(json['space_count']),
      isVerified: json['is_verified'] as bool? ?? false,
      viewerRole: json['viewer_role'] as String?,
      topicTags: topicTagsJson
          .map((item) => item.toString().trim())
          .where((item) => item.isNotEmpty)
          .toList(),
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

int _asInt(dynamic value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  return int.tryParse(value?.toString() ?? '') ?? 0;
}

class CommunitySpace {
  final String id;
  final String communityId;
  final String spaceType;
  final String name;
  final String description;
  final String? linkedGroupId;
  final String? linkedChannelId;
  final bool isQuarantined;

  const CommunitySpace({
    required this.id,
    required this.communityId,
    required this.spaceType,
    required this.name,
    required this.description,
    this.linkedGroupId,
    this.linkedChannelId,
    this.isQuarantined = false,
  });

  factory CommunitySpace.fromJson(Map<String, dynamic> json) {
    return CommunitySpace(
      id: json['id'] as String? ?? '',
      communityId: json['community_id'] as String? ?? '',
      spaceType: json['space_type'] as String? ?? 'discussion',
      name: json['name'] as String? ?? '',
      description: json['description'] as String? ?? '',
      linkedGroupId: json['linked_group_id'] as String?,
      linkedChannelId: json['linked_channel_id'] as String?,
      isQuarantined: json['is_quarantined'] as bool? ?? false,
    );
  }
}
