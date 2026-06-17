class GroupMember {
  final String groupId;
  final String userId;
  final String role; // 'owner' | 'admin' | 'moderator' | 'member'
  final DateTime joinedAt;
  final String? displayName;
  final String? username;
  final String? avatarMediaId;
  final String status; // 'active' | 'left' | 'removed' | 'banned'
  final String? removalReason;

  const GroupMember({
    required this.groupId,
    required this.userId,
    this.role = 'member',
    required this.joinedAt,
    this.displayName,
    this.username,
    this.avatarMediaId,
    this.status = 'active',
    this.removalReason,
  });

  String get displayLabel =>
      displayName?.isNotEmpty == true ? displayName! : username ?? userId;

  String get avatarInitial =>
      displayLabel.isNotEmpty ? displayLabel[0].toUpperCase() : '?';

  bool get isOwner => role == 'owner';
  bool get isAdmin => role == 'admin' || role == 'owner';
  bool get isMod => role == 'moderator';
  bool get isAdminOrMod => isAdmin || isMod;

  factory GroupMember.fromJson(Map<String, dynamic> json) {
    return GroupMember(
      groupId: json['group_id'] as String? ?? '',
      userId: json['user_id'] as String? ?? json['id'] as String? ?? '',
      role: json['role'] as String? ?? 'member',
      joinedAt: json['joined_at'] != null
          ? DateTime.parse(json['joined_at'] as String)
          : DateTime.now(),
      displayName: json['display_name'] as String?,
      username: json['username'] as String?,
      avatarMediaId: json['avatar_media_id'] as String?,
      status: json['status'] as String? ?? 'active',
      removalReason: json['removal_reason'] as String?,
    );
  }
}
