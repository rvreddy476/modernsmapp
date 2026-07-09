class GroupInvite {
  final String id;
  final String groupId;
  final String groupName;
  final String? groupAvatarMediaId;
  final String inviterId;
  final String? inviterName;
  final String status; // 'pending' | 'accepted' | 'rejected'
  final DateTime createdAt;

  const GroupInvite({
    required this.id,
    required this.groupId,
    required this.groupName,
    this.groupAvatarMediaId,
    required this.inviterId,
    this.inviterName,
    this.status = 'pending',
    required this.createdAt,
  });

  factory GroupInvite.fromJson(Map<String, dynamic> json) {
    return GroupInvite(
      id: json['id'] as String? ?? '',
      groupId: json['group_id'] as String? ?? '',
      groupName: json['group_name'] as String? ?? '',
      groupAvatarMediaId: json['group_avatar_media_id'] as String?,
      inviterId: json['inviter_id'] as String? ?? '',
      inviterName: json['inviter_name'] as String?,
      status: json['status'] as String? ?? 'pending',
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
