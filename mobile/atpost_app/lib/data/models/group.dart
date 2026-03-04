class Group {
  final String id;
  final String name;
  final String description;
  final String privacy; // 'public', 'private', 'secret'
  final String? coverMediaId;
  final String? creatorId;
  final int memberCount;
  final int postCount;
  final bool isMember;
  final bool isAdmin;
  final DateTime createdAt;

  const Group({
    required this.id,
    required this.name,
    required this.description,
    required this.privacy,
    this.coverMediaId,
    this.creatorId,
    this.memberCount = 0,
    this.postCount = 0,
    this.isMember = false,
    this.isAdmin = false,
    required this.createdAt,
  });

  factory Group.fromJson(Map<String, dynamic> json) {
    return Group(
      id: json['id'] as String? ?? json['group_id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      description: json['description'] as String? ?? '',
      privacy: json['privacy'] as String? ?? 'public',
      coverMediaId: json['cover_media_id'] as String?,
      creatorId: json['creator_id'] as String?,
      memberCount: json['member_count'] as int? ?? 0,
      postCount: json['post_count'] as int? ?? 0,
      isMember: json['is_member'] as bool? ?? false,
      isAdmin: json['is_admin'] as bool? ?? false,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
