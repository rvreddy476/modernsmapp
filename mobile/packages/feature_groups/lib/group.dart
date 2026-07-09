class Group {
  final String id;
  final String name;
  final String description;
  final String privacy; // legacy: 'public' | 'private' | 'secret'
  final String? coverMediaId;
  final String? avatarMediaId;
  final String? creatorId;
  final int memberCount;
  final int postCount;
  final bool isMember;
  final bool isAdmin;
  final DateTime createdAt;
  // V2 fields
  final String? handle;
  final String? category;
  final String privacyLevel; // 'public' | 'restricted' | 'private'
  final String joinMode; // 'open' | 'request' | 'invite_only'
  final String viewerRole; // 'owner'|'admin'|'moderator'|'member'|'pending'|'outsider'|'banned'
  final bool isMature;
  final int pendingRequestCount;
  final String? location;
  final String? chatConversationId;

  const Group({
    required this.id,
    required this.name,
    required this.description,
    this.privacy = 'public',
    this.coverMediaId,
    this.avatarMediaId,
    this.creatorId,
    this.memberCount = 0,
    this.postCount = 0,
    this.isMember = false,
    this.isAdmin = false,
    required this.createdAt,
    this.handle,
    this.category,
    this.privacyLevel = 'public',
    this.joinMode = 'open',
    this.viewerRole = 'outsider',
    this.isMature = false,
    this.pendingRequestCount = 0,
    this.location,
    this.chatConversationId,
  });

  String? get coverUrl =>
      coverMediaId != null ? '/v1/media/$coverMediaId/serve' : null;

  String? get avatarUrl =>
      avatarMediaId != null ? '/v1/media/$avatarMediaId/serve' : null;

  bool get isOwner => viewerRole == 'owner';
  bool get isAdminOrMod =>
      viewerRole == 'owner' ||
      viewerRole == 'admin' ||
      viewerRole == 'moderator';
  bool get canPost =>
      isMember || isAdminOrMod;

  factory Group.fromJson(Map<String, dynamic> json) {
    // Resolve privacy_level from either field, normalising 'secret' → 'private'
    final rawPrivacy = json['privacy'] as String? ?? 'public';
    final rawLevel = json['privacy_level'] as String?;
    String level;
    if (rawLevel != null) {
      level = rawLevel;
    } else {
      level = rawPrivacy == 'secret' ? 'private' : rawPrivacy;
    }

    return Group(
      id: json['id'] as String? ?? json['group_id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      description: json['description'] as String? ?? '',
      privacy: rawPrivacy,
      coverMediaId: json['cover_media_id'] as String?,
      avatarMediaId: json['avatar_media_id'] as String?,
      creatorId: json['creator_id'] as String?,
      memberCount: json['member_count'] as int? ?? 0,
      postCount: json['post_count'] as int? ?? 0,
      isMember: (json['is_member'] as bool?) ??
          (json['viewer_role'] == 'member' ||
              json['viewer_role'] == 'admin' ||
              json['viewer_role'] == 'owner' ||
              json['viewer_role'] == 'moderator'),
      isAdmin: (json['is_admin'] as bool?) ??
          (json['viewer_role'] == 'admin' || json['viewer_role'] == 'owner'),
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
      handle: json['handle'] as String?,
      category: json['category'] as String?,
      privacyLevel: level,
      joinMode: json['join_mode'] as String? ?? 'open',
      viewerRole: json['viewer_role'] as String? ?? 'outsider',
      isMature: json['is_mature'] as bool? ?? false,
      pendingRequestCount: json['pending_request_count'] as int? ?? 0,
      location: json['location'] as String?,
      chatConversationId: json['chat_conversation_id'] as String?,
    );
  }
}
