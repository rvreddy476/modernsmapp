class PostMatchSession {
  final String userId;
  final String onboardingStatus;

  const PostMatchSession({
    required this.userId,
    required this.onboardingStatus,
  });

  PostMatchSession copyWith({String? userId, String? onboardingStatus}) {
    return PostMatchSession(
      userId: userId ?? this.userId,
      onboardingStatus: onboardingStatus ?? this.onboardingStatus,
    );
  }

  Map<String, dynamic> toJson() {
    return {'user_id': userId, 'onboarding_status': onboardingStatus};
  }

  factory PostMatchSession.fromJson(Map<String, dynamic> json) {
    return PostMatchSession(
      userId: (json['user_id'] ?? json['id'] ?? '').toString(),
      onboardingStatus: (json['onboarding_status'] ?? 'new').toString(),
    );
  }
}

class PostMatchProfile {
  final String userId;
  final String firstName;
  final String dateOfBirth;
  final String gender;
  final String lookingFor;
  final String relationshipIntent;
  final String? bio;
  final String? occupation;
  final String? company;
  final String? education;
  final String? city;
  final String? state;
  final String? country;
  final double? latitude;
  final double? longitude;
  final int? heightCm;
  final String? religion;
  final String? community;
  final String? drinking;
  final String? smoking;
  final String? exercise;
  final String? diet;
  final String? wantsChildren;
  final String? familyPlans;
  final bool visibleToPublic;
  final bool blurModeEnabled;
  final int profileCompletionPercent;

  const PostMatchProfile({
    required this.userId,
    required this.firstName,
    required this.dateOfBirth,
    required this.gender,
    required this.lookingFor,
    required this.relationshipIntent,
    this.bio,
    this.occupation,
    this.company,
    this.education,
    this.city,
    this.state,
    this.country,
    this.latitude,
    this.longitude,
    this.heightCm,
    this.religion,
    this.community,
    this.drinking,
    this.smoking,
    this.exercise,
    this.diet,
    this.wantsChildren,
    this.familyPlans,
    this.visibleToPublic = true,
    this.blurModeEnabled = false,
    this.profileCompletionPercent = 0,
  });

  factory PostMatchProfile.fromJson(Map<String, dynamic> json) {
    return PostMatchProfile(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      dateOfBirth: (json['date_of_birth'] ?? '').toString(),
      gender: (json['gender'] ?? '').toString(),
      lookingFor: (json['looking_for'] ?? '').toString(),
      relationshipIntent: (json['relationship_intent'] ?? '').toString(),
      bio: json['bio'] as String?,
      occupation: json['occupation'] as String?,
      company: json['company'] as String?,
      education: json['education'] as String?,
      city: json['city'] as String?,
      state: json['state'] as String?,
      country: json['country'] as String?,
      latitude: (json['latitude'] as num?)?.toDouble(),
      longitude: (json['longitude'] as num?)?.toDouble(),
      heightCm: json['height_cm'] as int?,
      religion: json['religion'] as String?,
      community: json['community'] as String?,
      drinking: json['drinking'] as String?,
      smoking: json['smoking'] as String?,
      exercise: json['exercise'] as String?,
      diet: json['diet'] as String?,
      wantsChildren: json['wants_children'] as String?,
      familyPlans: json['family_plans'] as String?,
      visibleToPublic: json['visible_to_public'] as bool? ?? true,
      blurModeEnabled: json['blur_mode_enabled'] as bool? ?? false,
      profileCompletionPercent: json['profile_completion_percent'] as int? ?? 0,
    );
  }
}

class PostMatchPreferences {
  final int minAge;
  final int maxAge;
  final int distanceKm;
  final String interestedInGender;
  final String? relationshipIntent;
  final bool blurModePreference;

  const PostMatchPreferences({
    required this.minAge,
    required this.maxAge,
    required this.distanceKm,
    required this.interestedInGender,
    this.relationshipIntent,
    this.blurModePreference = false,
  });

  factory PostMatchPreferences.fromJson(Map<String, dynamic> json) {
    return PostMatchPreferences(
      minAge: json['min_age'] as int? ?? 18,
      maxAge: json['max_age'] as int? ?? 35,
      distanceKm: json['distance_km'] as int? ?? 50,
      interestedInGender: (json['interested_in_gender'] ?? 'everyone')
          .toString(),
      relationshipIntent: json['relationship_intent'] as String?,
      blurModePreference: json['blur_mode_preference'] as bool? ?? false,
    );
  }
}

class PostMatchPhoto {
  final String id;
  final String userId;
  final String mediaKey;
  final String? mediaUrl;
  final String? thumbnailUrl;
  final int sortOrder;
  final bool isPrimary;
  final String visibility;
  final String moderationStatus;

  const PostMatchPhoto({
    required this.id,
    required this.userId,
    required this.mediaKey,
    this.mediaUrl,
    this.thumbnailUrl,
    required this.sortOrder,
    required this.isPrimary,
    required this.visibility,
    required this.moderationStatus,
  });

  factory PostMatchPhoto.fromJson(Map<String, dynamic> json) {
    return PostMatchPhoto(
      id: (json['id'] ?? '').toString(),
      userId: (json['user_id'] ?? '').toString(),
      mediaKey: (json['media_key'] ?? '').toString(),
      mediaUrl: json['media_url'] as String? ?? json['url'] as String?,
      thumbnailUrl:
          json['thumbnail_url'] as String? ?? json['thumbnail'] as String?,
      sortOrder: json['sort_order'] as int? ?? 0,
      isPrimary: json['is_primary'] as bool? ?? false,
      visibility: (json['visibility'] ?? 'public').toString(),
      moderationStatus: (json['moderation_status'] ?? 'pending').toString(),
    );
  }
}

class PostMatchInitUpload {
  final String mediaId;
  final String uploadUrl;
  final String mediaKey;

  const PostMatchInitUpload({
    required this.mediaId,
    required this.uploadUrl,
    required this.mediaKey,
  });

  factory PostMatchInitUpload.fromJson(Map<String, dynamic> json) {
    return PostMatchInitUpload(
      mediaId: (json['media_id'] ?? '').toString(),
      uploadUrl: (json['upload_url'] ?? '').toString(),
      mediaKey: (json['media_key'] ?? '').toString(),
    );
  }
}

class PostMatchPrimaryPhoto {
  final String url;
  final bool blurred;

  const PostMatchPrimaryPhoto({required this.url, required this.blurred});

  factory PostMatchPrimaryPhoto.fromJson(Map<String, dynamic> json) {
    return PostMatchPrimaryPhoto(
      url: (json['url'] ?? '').toString(),
      blurred: json['blurred'] as bool? ?? false,
    );
  }
}

class PostMatchFeedItem {
  final String userId;
  final String firstName;
  final int age;
  final String? city;
  final String? bioPreview;
  final int compatibilityScore;
  final String trustLevel;
  final PostMatchPrimaryPhoto? primaryPhoto;
  final String? relationshipIntent;
  final String? occupation;

  const PostMatchFeedItem({
    required this.userId,
    required this.firstName,
    required this.age,
    this.city,
    this.bioPreview,
    required this.compatibilityScore,
    required this.trustLevel,
    this.primaryPhoto,
    this.relationshipIntent,
    this.occupation,
  });

  factory PostMatchFeedItem.fromJson(Map<String, dynamic> json) {
    return PostMatchFeedItem(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      age: json['age'] as int? ?? 18,
      city: json['city'] as String?,
      bioPreview: json['bio_preview'] as String?,
      compatibilityScore: json['compatibility_score'] as int? ?? 0,
      trustLevel: (json['trust_level'] ?? 'low').toString(),
      primaryPhoto: json['primary_photo'] is Map<String, dynamic>
          ? PostMatchPrimaryPhoto.fromJson(
              json['primary_photo'] as Map<String, dynamic>,
            )
          : null,
      relationshipIntent: json['relationship_intent'] as String?,
      occupation: json['occupation'] as String?,
    );
  }
}

class PostMatchDecisionResult {
  final String result;
  final String? matchId;
  final String? conversationId;

  const PostMatchDecisionResult({
    required this.result,
    this.matchId,
    this.conversationId,
  });

  factory PostMatchDecisionResult.fromJson(Map<String, dynamic> json) {
    return PostMatchDecisionResult(
      result: (json['result'] ?? '').toString(),
      matchId: json['match_id'] as String?,
      conversationId: json['conversation_id'] as String?,
    );
  }
}

class PostMatchOtherUser {
  final String userId;
  final String firstName;
  final String? photoUrl;

  const PostMatchOtherUser({
    required this.userId,
    required this.firstName,
    this.photoUrl,
  });

  factory PostMatchOtherUser.fromJson(Map<String, dynamic> json) {
    return PostMatchOtherUser(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      photoUrl: json['photo_url'] as String?,
    );
  }
}

class PostMatchMatch {
  final String id;
  final String status;
  final String? conversationId;
  final PostMatchOtherUser? otherUser;

  const PostMatchMatch({
    required this.id,
    required this.status,
    this.conversationId,
    this.otherUser,
  });

  factory PostMatchMatch.fromJson(Map<String, dynamic> json) {
    return PostMatchMatch(
      id: (json['id'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      conversationId: json['conversation_id'] as String?,
      otherUser: json['other_user'] is Map<String, dynamic>
          ? PostMatchOtherUser.fromJson(
              json['other_user'] as Map<String, dynamic>,
            )
          : null,
    );
  }
}

class PostMatchLikeReceived {
  final String userId;
  final String firstName;
  final String? photoUrl;
  final String likedAt;

  const PostMatchLikeReceived({
    required this.userId,
    required this.firstName,
    this.photoUrl,
    required this.likedAt,
  });

  factory PostMatchLikeReceived.fromJson(Map<String, dynamic> json) {
    return PostMatchLikeReceived(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      photoUrl: json['photo_url'] as String?,
      likedAt: (json['liked_at'] ?? '').toString(),
    );
  }
}

class PostMatchMessage {
  final String id;
  final String conversationId;
  final String senderUserId;
  final String messageType;
  final String? bodyText;
  final String? mediaKey;
  final String moderationStatus;
  final String createdAt;

  const PostMatchMessage({
    required this.id,
    required this.conversationId,
    required this.senderUserId,
    required this.messageType,
    this.bodyText,
    this.mediaKey,
    required this.moderationStatus,
    required this.createdAt,
  });

  factory PostMatchMessage.fromJson(Map<String, dynamic> json) {
    return PostMatchMessage(
      id: (json['id'] ?? '').toString(),
      conversationId: (json['conversation_id'] ?? '').toString(),
      senderUserId: (json['sender_user_id'] ?? '').toString(),
      messageType: (json['message_type'] ?? 'text').toString(),
      bodyText: json['body_text'] as String?,
      mediaKey: json['media_key'] as String?,
      moderationStatus: (json['moderation_status'] ?? 'approved').toString(),
      createdAt: (json['created_at'] ?? '').toString(),
    );
  }
}

class PostMatchConversation {
  final String id;
  final String type;
  final String status;
  final PostMatchMessage? lastMessage;
  final PostMatchOtherUser? otherUser;

  const PostMatchConversation({
    required this.id,
    required this.type,
    required this.status,
    this.lastMessage,
    this.otherUser,
  });

  factory PostMatchConversation.fromJson(Map<String, dynamic> json) {
    return PostMatchConversation(
      id: (json['id'] ?? '').toString(),
      type: (json['type'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      lastMessage: json['last_message'] is Map<String, dynamic>
          ? PostMatchMessage.fromJson(
              json['last_message'] as Map<String, dynamic>,
            )
          : null,
      otherUser: json['other_user'] is Map<String, dynamic>
          ? PostMatchOtherUser.fromJson(
              json['other_user'] as Map<String, dynamic>,
            )
          : null,
    );
  }
}

const postMatchGenderOptions = ['male', 'female', 'non_binary', 'other'];

const postMatchLookingForOptions = ['everyone', 'male', 'female'];

const postMatchIntentOptions = [
  'long_term',
  'marriage',
  'casual',
  'figuring_out',
];
