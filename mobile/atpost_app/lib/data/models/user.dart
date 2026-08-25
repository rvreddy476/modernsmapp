import 'package:atpost_app/core/config/environment.dart';
import 'package:freezed_annotation/freezed_annotation.dart';

part 'user.freezed.dart';

@freezed
class User with _$User {
  const factory User({
    @Default('') String id,
    @Default('user') String username,
    @Default('VChat User') String displayName,
    @Default('') String firstName,
    @Default('') String lastName,
    String? bio,
    String? pronouns,
    String? avatarMediaId,
    String? coverMediaId,
    String? location,
    String? profession,
    String? website,
    @Default(false) bool isVerified,
    @Default(0) int followerCount,
    @Default(0) int followingCount,
    @Default(0) int friendCount,
    @Default(0) int postCount,
  }) = _User;

  factory User.fromJson(Map<String, dynamic> json) {
    final first = (json['first_name'] ?? json['firstName'] ?? '').toString();
    final last = (json['last_name'] ?? json['lastName'] ?? '').toString();

    // Fallback displayName logic
    var display = (json['display_name'] ?? json['name'] ?? json['displayName'] ?? '').toString();
    if (display.isEmpty && (first.isNotEmpty || last.isNotEmpty)) {
      display = '$first $last'.trim();
    }
    if (display.isEmpty) display = 'VChat User';

    return User(
      id: (json['id'] ?? json['user_id'] ?? '').toString(),
      username: (json['username'] ?? json['user_id'] ?? 'user').toString(),
      displayName: display,
      firstName: first,
      lastName: last,
      bio: json['bio']?.toString(),
      pronouns: json['pronouns']?.toString(),
      avatarMediaId: (json['avatar_media_id'] ?? json['avatarMediaId'])?.toString(),
      coverMediaId: (json['cover_media_id'] ?? json['coverMediaId'])?.toString(),
      location: json['location']?.toString(),
      profession: json['profession']?.toString(),
      website: json['website']?.toString(),
      isVerified: json['is_verified'] ?? json['isVerified'] ?? false,
      followerCount: json['follower_count'] ?? json['followerCount'] ?? 0,
      followingCount: json['following_count'] ?? json['followingCount'] ?? 0,
      friendCount: json['friend_count'] ?? json['friendCount'] ?? 0,
      postCount: json['post_count'] ?? json['postCount'] ?? 0,
    );
  }

  const User._();

  static User empty() => const User();

  bool get hasAvatar => avatarMediaId != null && avatarMediaId!.isNotEmpty;

  String get avatarUrl => hasAvatar
      ? '${Environment.apiBaseUrl}/v1/media/$avatarMediaId/serve'
      : 'https://api.dicebear.com/7.x/avataaars/svg?seed=$id';

  bool get hasCover => coverMediaId != null && coverMediaId!.isNotEmpty;

  String? get coverUrl => hasCover
      ? '${Environment.apiBaseUrl}/v1/media/$coverMediaId/serve'
      : null;
}
