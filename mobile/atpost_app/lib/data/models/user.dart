import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';

/// Production-ready User model with "Total Resilience" parsing.
/// Designed to prevent app crashes from malformed backend JSON.
class User {
  final String id;
  final String username;
  final String displayName;
  final String? bio;
  final String? pronouns;
  final String? avatarMediaId;
  final String? coverMediaId;
  final String? location;
  final String? profession;
  final String? website;
  final bool isVerified;
  final int followerCount;
  final int followingCount;
  final int friendCount;
  final int postCount;

  const User({
    required this.id,
    required this.username,
    required this.displayName,
    this.bio,
    this.pronouns,
    this.avatarMediaId,
    this.coverMediaId,
    this.location,
    this.profession,
    this.website,
    this.isVerified = false,
    this.followerCount = 0,
    this.followingCount = 0,
    this.friendCount = 0,
    this.postCount = 0,
  });

  factory User.fromJson(Map<String, dynamic> json) {
    try {
      return User(
        id: (json['id'] ?? json['user_id'] ?? '').toString(),
        username: (json['username'] ?? '').toString(),
        displayName:
            (json['display_name'] ?? json['name'] ?? 'User').toString(),
        bio: json['bio']?.toString(),
        pronouns: json['pronouns']?.toString(),
        avatarMediaId: json['avatar_media_id']?.toString(),
        coverMediaId: json['cover_media_id']?.toString(),
        location: json['location']?.toString(),
        profession: json['profession']?.toString(),
        website: json['website']?.toString(),
        isVerified: _toBool(json['is_verified']),
        followerCount: _toInt(json['follower_count']),
        followingCount: _toInt(json['following_count']),
        friendCount: _toInt(json['friend_count']),
        postCount: _toInt(json['post_count']),
      );
    } catch (e, st) {
      AppLogger.error('User.fromJson failed', error: e, stackTrace: st);
      return User.empty();
    }
  }

  static User empty() => const User(
        id: '',
        username: 'unknown',
        displayName: 'User',
      );

  /// Whether this user has a real avatar uploaded.
  bool get hasAvatar => avatarMediaId != null && avatarMediaId!.isNotEmpty;

  /// Full URL to serve the avatar via the API gateway.
  String get avatarUrl => hasAvatar
      ? '${Environment.apiBaseUrl}/v1/media/$avatarMediaId/serve'
      : 'https://api.dicebear.com/7.x/avataaars/svg?seed=$id';

  /// Whether this user has a cover photo uploaded.
  bool get hasCover => coverMediaId != null && coverMediaId!.isNotEmpty;

  /// Full URL to serve the cover photo via the API gateway.
  String? get coverUrl => hasCover
      ? '${Environment.apiBaseUrl}/v1/media/$coverMediaId/serve'
      : null;
}

// --- Resilience Helpers ---

int _toInt(dynamic data) {
  if (data is int) return data;
  if (data is double) return data.toInt();
  if (data is String) return int.tryParse(data) ?? 0;
  return 0;
}

bool _toBool(dynamic data) {
  if (data is bool) return data;
  if (data is int) return data == 1;
  if (data is String) return data.toLowerCase() == 'true';
  return false;
}
