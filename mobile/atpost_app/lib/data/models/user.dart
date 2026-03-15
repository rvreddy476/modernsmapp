import 'package:atpost_app/core/config/environment.dart';

class User {
  final String id;
  final String username;
  final String displayName;
  final String? bio;
  final String? pronouns;
  final String? avatarMediaId;
  final String? location;
  final String? profession;
  final bool isVerified;
  final int followerCount;
  final int followingCount;
  final int friendCount;

  const User({
    required this.id,
    required this.username,
    required this.displayName,
    this.bio,
    this.pronouns,
    this.avatarMediaId,
    this.location,
    this.profession,
    this.isVerified = false,
    this.followerCount = 0,
    this.followingCount = 0,
    this.friendCount = 0,
  });

  factory User.fromJson(Map<String, dynamic> json) {
    return User(
      id: json['id'] as String? ?? json['user_id'] as String? ?? '',
      username: json['username'] as String? ?? '',
      displayName: json['display_name'] as String? ?? json['name'] as String? ?? '',
      bio: json['bio'] as String?,
      pronouns: json['pronouns'] as String?,
      avatarMediaId: json['avatar_media_id'] as String?,
      location: json['location'] as String?,
      profession: json['profession'] as String?,
      isVerified: json['is_verified'] as bool? ?? false,
      followerCount: json['follower_count'] as int? ?? 0,
      followingCount: json['following_count'] as int? ?? 0,
      friendCount: json['friend_count'] as int? ?? 0,
    );
  }

  /// Whether this user has a real avatar uploaded.
  bool get hasAvatar => avatarMediaId != null && avatarMediaId!.isNotEmpty;

  /// Full URL to serve the avatar via the API gateway.
  String get avatarUrl => hasAvatar
      ? '${Environment.apiBaseUrl}/v1/media/$avatarMediaId/serve'
      : 'https://api.dicebear.com/7.x/avataaars/svg?seed=$id';
}
