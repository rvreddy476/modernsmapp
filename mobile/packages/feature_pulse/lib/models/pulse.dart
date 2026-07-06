// formerly PostMatchSession
class PulseSession {
  final String userId;
  final String onboardingStatus;

  const PulseSession({required this.userId, required this.onboardingStatus});

  PulseSession copyWith({String? userId, String? onboardingStatus}) {
    return PulseSession(
      userId: userId ?? this.userId,
      onboardingStatus: onboardingStatus ?? this.onboardingStatus,
    );
  }

  Map<String, dynamic> toJson() {
    return {'user_id': userId, 'onboarding_status': onboardingStatus};
  }

  factory PulseSession.fromJson(Map<String, dynamic> json) {
    return PulseSession(
      userId: (json['user_id'] ?? json['id'] ?? '').toString(),
      onboardingStatus: (json['onboarding_status'] ?? 'new').toString(),
    );
  }
}

// formerly PostMatchProfile
class PulseProfile {
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

  const PulseProfile({
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

  factory PulseProfile.fromJson(Map<String, dynamic> json) {
    return PulseProfile(
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

// formerly PostMatchPreferences
class PulsePreferences {
  final int minAge;
  final int maxAge;
  final int distanceKm;
  final String interestedInGender;
  final String? relationshipIntent;
  final bool blurModePreference;

  const PulsePreferences({
    required this.minAge,
    required this.maxAge,
    required this.distanceKm,
    required this.interestedInGender,
    this.relationshipIntent,
    this.blurModePreference = false,
  });

  factory PulsePreferences.fromJson(Map<String, dynamic> json) {
    return PulsePreferences(
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

// formerly PostMatchPhoto
class PulsePhoto {
  final String id;
  final String userId;
  final String mediaKey;
  final String? mediaUrl;
  final String? thumbnailUrl;
  final int sortOrder;
  final bool isPrimary;
  final String visibility;
  final String moderationStatus;

  const PulsePhoto({
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

  factory PulsePhoto.fromJson(Map<String, dynamic> json) {
    return PulsePhoto(
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

// formerly PostMatchInitUpload
class PulseInitUpload {
  final String mediaId;
  final String uploadUrl;
  final String mediaKey;

  const PulseInitUpload({
    required this.mediaId,
    required this.uploadUrl,
    required this.mediaKey,
  });

  factory PulseInitUpload.fromJson(Map<String, dynamic> json) {
    return PulseInitUpload(
      mediaId: (json['media_id'] ?? '').toString(),
      uploadUrl: (json['upload_url'] ?? '').toString(),
      mediaKey: (json['media_key'] ?? '').toString(),
    );
  }
}

// formerly PostMatchPrimaryPhoto
class PulsePrimaryPhoto {
  final String url;
  final bool blurred;

  const PulsePrimaryPhoto({required this.url, required this.blurred});

  factory PulsePrimaryPhoto.fromJson(Map<String, dynamic> json) {
    return PulsePrimaryPhoto(
      url: (json['url'] ?? '').toString(),
      blurred: json['blurred'] as bool? ?? false,
    );
  }
}

// formerly PostMatchFeedItem
class PulseFeedItem {
  final String userId;
  final String firstName;
  final int age;
  final String? city;
  final String? bioPreview;
  final int compatibilityScore;
  final String trustLevel;
  final PulsePrimaryPhoto? primaryPhoto;
  final String? relationshipIntent;
  final String? occupation;

  const PulseFeedItem({
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

  factory PulseFeedItem.fromJson(Map<String, dynamic> json) {
    return PulseFeedItem(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      age: json['age'] as int? ?? 18,
      city: json['city'] as String?,
      bioPreview: json['bio_preview'] as String?,
      compatibilityScore: json['compatibility_score'] as int? ?? 0,
      trustLevel: (json['trust_level'] ?? 'low').toString(),
      primaryPhoto: json['primary_photo'] is Map<String, dynamic>
          ? PulsePrimaryPhoto.fromJson(
              json['primary_photo'] as Map<String, dynamic>,
            )
          : null,
      relationshipIntent: json['relationship_intent'] as String?,
      occupation: json['occupation'] as String?,
    );
  }
}

// formerly PostMatchDecisionResult
class PulseDecisionResult {
  final String result;
  final String? matchId;
  final String? conversationId;

  const PulseDecisionResult({
    required this.result,
    this.matchId,
    this.conversationId,
  });

  factory PulseDecisionResult.fromJson(Map<String, dynamic> json) {
    return PulseDecisionResult(
      result: (json['result'] ?? '').toString(),
      matchId: json['match_id'] as String?,
      conversationId: json['conversation_id'] as String?,
    );
  }
}

// formerly PostMatchOtherUser
class PulseOtherUser {
  final String userId;
  final String firstName;
  final String? photoUrl;

  const PulseOtherUser({
    required this.userId,
    required this.firstName,
    this.photoUrl,
  });

  factory PulseOtherUser.fromJson(Map<String, dynamic> json) {
    return PulseOtherUser(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      photoUrl: json['photo_url'] as String?,
    );
  }
}

// formerly PostMatchMatch
class PulseMatch {
  final String id;
  final String status;
  final String? conversationId;
  final PulseOtherUser? otherUser;

  const PulseMatch({
    required this.id,
    required this.status,
    this.conversationId,
    this.otherUser,
  });

  factory PulseMatch.fromJson(Map<String, dynamic> json) {
    return PulseMatch(
      id: (json['id'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      conversationId: json['conversation_id'] as String?,
      otherUser: json['other_user'] is Map<String, dynamic>
          ? PulseOtherUser.fromJson(json['other_user'] as Map<String, dynamic>)
          : null,
    );
  }
}

// formerly PostMatchLikeReceived
class PulseLikeReceived {
  final String userId;
  final String firstName;
  final String? photoUrl;
  final String likedAt;

  const PulseLikeReceived({
    required this.userId,
    required this.firstName,
    this.photoUrl,
    required this.likedAt,
  });

  factory PulseLikeReceived.fromJson(Map<String, dynamic> json) {
    return PulseLikeReceived(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      photoUrl: json['photo_url'] as String?,
      likedAt: (json['liked_at'] ?? '').toString(),
    );
  }
}

// formerly PostMatchMessage
class PulseMessage {
  final String id;
  final String conversationId;
  final String senderUserId;
  final String messageType;
  final String? bodyText;
  final String? mediaKey;
  final String moderationStatus;
  final String createdAt;

  /// Sprint 6: moderation verdict attached by dating-service when strict
  /// mode is on. `null` in shadow mode (the backend either omits the
  /// field or sets `action_taken: 'shadow'` — both map to no UI change).
  final Map<String, dynamic>? moderation;

  const PulseMessage({
    required this.id,
    required this.conversationId,
    required this.senderUserId,
    required this.messageType,
    this.bodyText,
    this.mediaKey,
    required this.moderationStatus,
    required this.createdAt,
    this.moderation,
  });

  factory PulseMessage.fromJson(Map<String, dynamic> json) {
    Map<String, dynamic>? mod;
    final raw = json['moderation'];
    if (raw is Map<String, dynamic>) {
      mod = raw;
    } else if (raw is Map) {
      mod = Map<String, dynamic>.from(raw);
    }
    return PulseMessage(
      id: (json['id'] ?? '').toString(),
      conversationId: (json['conversation_id'] ?? '').toString(),
      senderUserId: (json['sender_user_id'] ?? '').toString(),
      messageType: (json['message_type'] ?? 'text').toString(),
      bodyText: json['body_text'] as String?,
      mediaKey: json['media_key'] as String?,
      moderationStatus: (json['moderation_status'] ?? 'approved').toString(),
      createdAt: (json['created_at'] ?? '').toString(),
      moderation: mod,
    );
  }
}

// formerly PostMatchConversation
class PulseConversation {
  final String id;
  final String type;
  final String status;
  final PulseMessage? lastMessage;
  final PulseOtherUser? otherUser;

  const PulseConversation({
    required this.id,
    required this.type,
    required this.status,
    this.lastMessage,
    this.otherUser,
  });

  factory PulseConversation.fromJson(Map<String, dynamic> json) {
    return PulseConversation(
      id: (json['id'] ?? '').toString(),
      type: (json['type'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      lastMessage: json['last_message'] is Map<String, dynamic>
          ? PulseMessage.fromJson(json['last_message'] as Map<String, dynamic>)
          : null,
      otherUser: json['other_user'] is Map<String, dynamic>
          ? PulseOtherUser.fromJson(json['other_user'] as Map<String, dynamic>)
          : null,
    );
  }
}

const pulseGenderOptions = ['male', 'female', 'non_binary', 'other'];

const pulseLookingForOptions = ['everyone', 'male', 'female'];

const pulseIntentOptions = ['long_term', 'marriage', 'casual', 'figuring_out'];

// ---------------------------------------------------------------------------
// Sprint 2: New Pulse contract types.
//
// Backed by `GET /v1/dating/pulse/today` and `GET /v1/dating/pulse/nebula`.
// See PULSE_DATING_SPEC.md §6.2. These coexist with the legacy `PulseProfile`
// / `PulseFeedItem` swipe-deck types from S1; once S3 lands the new orbital
// surface as the only experience, the legacy types can be removed.
// ---------------------------------------------------------------------------

/// Why we recommended this candidate. The `kind` enum mirrors the backend.
enum MatchReasonKind {
  tune,
  community,
  qaTopic,
  content,
  recency,
  geo,
  trust,
  diversity,
  unknown;

  static MatchReasonKind fromString(String? raw) {
    switch (raw) {
      case 'tune':
        return MatchReasonKind.tune;
      case 'community':
        return MatchReasonKind.community;
      case 'qa_topic':
        return MatchReasonKind.qaTopic;
      case 'content':
        return MatchReasonKind.content;
      case 'recency':
        return MatchReasonKind.recency;
      case 'geo':
        return MatchReasonKind.geo;
      case 'trust':
        return MatchReasonKind.trust;
      case 'diversity':
        return MatchReasonKind.diversity;
      default:
        return MatchReasonKind.unknown;
    }
  }

  String toJsonValue() {
    switch (this) {
      case MatchReasonKind.tune:
        return 'tune';
      case MatchReasonKind.community:
        return 'community';
      case MatchReasonKind.qaTopic:
        return 'qa_topic';
      case MatchReasonKind.content:
        return 'content';
      case MatchReasonKind.recency:
        return 'recency';
      case MatchReasonKind.geo:
        return 'geo';
      case MatchReasonKind.trust:
        return 'trust';
      case MatchReasonKind.diversity:
        return 'diversity';
      case MatchReasonKind.unknown:
        return 'unknown';
    }
  }
}

class MatchReason {
  final MatchReasonKind kind;
  final String summary;

  const MatchReason({required this.kind, required this.summary});

  factory MatchReason.fromJson(Map<String, dynamic> json) {
    return MatchReason(
      kind: MatchReasonKind.fromString(json['kind'] as String?),
      summary: (json['summary'] ?? '').toString(),
    );
  }

  Map<String, dynamic> toJson() => {
    'kind': kind.toJsonValue(),
    'summary': summary,
  };
}

/// Lightweight tune snapshot served alongside a Pulse card.
///
/// Spec keeps this small on purpose — full tune is fetched via the profile
/// endpoint when needed.
class PulseTuneSummary {
  final int? lifestyleRhythm; // 1..5
  final String? conversationStyle;
  final int? faithFamilyWeight;
  final List<String> languages;

  const PulseTuneSummary({
    this.lifestyleRhythm,
    this.conversationStyle,
    this.faithFamilyWeight,
    this.languages = const [],
  });

  factory PulseTuneSummary.fromJson(Map<String, dynamic> json) {
    final rawLanguages = json['languages'];
    return PulseTuneSummary(
      lifestyleRhythm: (json['lifestyle_rhythm'] as num?)?.toInt(),
      conversationStyle: json['conversation_style'] as String?,
      faithFamilyWeight: (json['faith_family_weight'] as num?)?.toInt(),
      languages: rawLanguages is List
          ? rawLanguages.whereType<String>().toList()
          : const [],
    );
  }

  Map<String, dynamic> toJson() => {
    if (lifestyleRhythm != null) 'lifestyle_rhythm': lifestyleRhythm,
    if (conversationStyle != null) 'conversation_style': conversationStyle,
    if (faithFamilyWeight != null) 'faith_family_weight': faithFamilyWeight,
    if (languages.isNotEmpty) 'languages': languages,
  };
}

/// Profile preview embedded in a Pulse card.
///
/// NOTE(reviewer): the spec called this `PulseProfile` but a class with that
/// name already exists in this file (S1 onboarding profile, ~30 fields). To
/// avoid renaming an in-flight class, this preview type is `PulseCardProfile`.
/// If you'd rather rename the legacy one, do it in S3 with the chat refactor.
class PulseCardProfile {
  final String userId;
  final String firstName;
  final int age;
  final String intent; // casual | serious | marriage
  final String? city;
  final int? distanceKm;
  final String? primaryPhotoUrl;
  final bool primaryPhotoBlurred;
  final PulseTuneSummary? tuneSummary;
  final String? trustTier; // none | email | phone | aadhaar | vouched

  const PulseCardProfile({
    required this.userId,
    required this.firstName,
    required this.age,
    required this.intent,
    this.city,
    this.distanceKm,
    this.primaryPhotoUrl,
    this.primaryPhotoBlurred = false,
    this.tuneSummary,
    this.trustTier,
  });

  factory PulseCardProfile.fromJson(Map<String, dynamic> json) {
    return PulseCardProfile(
      userId: (json['user_id'] ?? '').toString(),
      firstName: (json['first_name'] ?? '').toString(),
      age: (json['age'] as num?)?.toInt() ?? 0,
      intent: (json['intent'] ?? 'serious').toString(),
      city: json['city'] as String?,
      distanceKm: (json['distance_km'] as num?)?.toInt(),
      primaryPhotoUrl: json['primary_photo_url'] as String?,
      primaryPhotoBlurred: json['primary_photo_blurred'] as bool? ?? false,
      tuneSummary: json['tune_summary'] is Map<String, dynamic>
          ? PulseTuneSummary.fromJson(
              json['tune_summary'] as Map<String, dynamic>,
            )
          : null,
      trustTier: json['trust_tier'] as String?,
    );
  }

  Map<String, dynamic> toJson() => {
    'user_id': userId,
    'first_name': firstName,
    'age': age,
    'intent': intent,
    if (city != null) 'city': city,
    if (distanceKm != null) 'distance_km': distanceKm,
    if (primaryPhotoUrl != null) 'primary_photo_url': primaryPhotoUrl,
    'primary_photo_blurred': primaryPhotoBlurred,
    if (tuneSummary != null) 'tune_summary': tuneSummary!.toJson(),
    if (trustTier != null) 'trust_tier': trustTier,
  };
}

/// Optional cross-product hooks ("echoes") that prove this isn't just a
/// faceless dating profile. All four ids may be null.
class PulseEchoes {
  final String? topQaAnswerId;
  final String? topReelId;
  final String? topCommunity;
  final String? recentPostId;

  const PulseEchoes({
    this.topQaAnswerId,
    this.topReelId,
    this.topCommunity,
    this.recentPostId,
  });

  bool get isEmpty =>
      topQaAnswerId == null &&
      topReelId == null &&
      topCommunity == null &&
      recentPostId == null;

  factory PulseEchoes.fromJson(Map<String, dynamic> json) {
    return PulseEchoes(
      topQaAnswerId: json['top_qa_answer_id'] as String?,
      topReelId: json['top_reel_id'] as String?,
      topCommunity: json['top_community'] as String?,
      recentPostId: json['recent_post_id'] as String?,
    );
  }

  Map<String, dynamic> toJson() => {
    'top_qa_answer_id': topQaAnswerId,
    'top_reel_id': topReelId,
    'top_community': topCommunity,
    'recent_post_id': recentPostId,
  };
}

/// One row of `GET /v1/dating/pulse/today`.
class PulseCard {
  final String candidateId;
  final double score;
  final List<MatchReason> matchReasons;
  final PulseCardProfile profile;
  final PulseEchoes echoes;

  const PulseCard({
    required this.candidateId,
    required this.score,
    required this.matchReasons,
    required this.profile,
    required this.echoes,
  });

  factory PulseCard.fromJson(Map<String, dynamic> json) {
    final reasonsRaw = json['match_reasons'];
    final reasons = reasonsRaw is List
        ? reasonsRaw
              .whereType<Map>()
              .map((m) => MatchReason.fromJson(Map<String, dynamic>.from(m)))
              .toList()
        : <MatchReason>[];
    final profileRaw = json['profile'];
    final echoesRaw = json['echoes'];
    return PulseCard(
      candidateId: (json['candidate_id'] ?? '').toString(),
      score: (json['score'] as num?)?.toDouble() ?? 0.0,
      matchReasons: reasons,
      profile: profileRaw is Map<String, dynamic>
          ? PulseCardProfile.fromJson(profileRaw)
          : profileRaw is Map
          ? PulseCardProfile.fromJson(Map<String, dynamic>.from(profileRaw))
          : PulseCardProfile.fromJson(const {}),
      echoes: echoesRaw is Map<String, dynamic>
          ? PulseEchoes.fromJson(echoesRaw)
          : echoesRaw is Map
          ? PulseEchoes.fromJson(Map<String, dynamic>.from(echoesRaw))
          : const PulseEchoes(),
    );
  }

  Map<String, dynamic> toJson() => {
    'candidate_id': candidateId,
    'score': score,
    'match_reasons': matchReasons.map((r) => r.toJson()).toList(),
    'profile': profile.toJson(),
    'echoes': echoes.toJson(),
  };
}

/// One page of Pulse cards plus envelope metadata.
class PulsePage {
  final List<PulseCard> items;
  final DateTime? generatedAt;
  final int size;

  /// Sprint 6 — true when the soft-launch cohort gate excludes this user.
  /// UI shows the same "coming soon" empty state as the city gate.
  final bool cohortGated;

  const PulsePage({
    required this.items,
    required this.generatedAt,
    required this.size,
    this.cohortGated = false,
  });

  const PulsePage.empty()
      : items = const [],
        generatedAt = null,
        size = 0,
        cohortGated = false;

  factory PulsePage.fromJson(Map<String, dynamic> json) {
    final dataRaw = json['data'];
    final metaRaw = json['meta'];
    final items = dataRaw is List
        ? dataRaw
              .whereType<Map>()
              .map((m) => PulseCard.fromJson(Map<String, dynamic>.from(m)))
              .toList()
        : <PulseCard>[];

    DateTime? generatedAt;
    int size = items.length;
    if (metaRaw is Map) {
      final meta = Map<String, dynamic>.from(metaRaw);
      final ts = meta['generated_at'] as String?;
      if (ts != null && ts.isNotEmpty) {
        generatedAt = DateTime.tryParse(ts);
      }
      size = (meta['size'] as num?)?.toInt() ?? size;
    }
    final gated = json['cohort_gated'] == true;
    return PulsePage(
      items: items,
      generatedAt: generatedAt,
      size: size,
      cohortGated: gated,
    );
  }

  Map<String, dynamic> toJson() => {
    'data': items.map((c) => c.toJson()).toList(),
    'meta': {'generated_at': generatedAt?.toIso8601String(), 'size': size},
    if (cohortGated) 'cohort_gated': true,
  };
}

// ---------------------------------------------------------------------------
// Sprint 3 — Sparks, Stash, Matches, conversation context.
//
// Backed by:
//   POST   /v1/dating/sparks
//   GET    /v1/dating/sparks/incoming
//   DELETE /v1/dating/sparks/:id
//   GET    /v1/dating/stash
//   POST   /v1/dating/stash
//   DELETE /v1/dating/stash/:candidate_id
//   GET    /v1/dating/matches
//   GET    /v1/dating/matches/:id
//   POST   /v1/dating/matches/:id/close
//   POST   /v1/dating/matches/:id/extend
// ---------------------------------------------------------------------------

/// A "sparkable item" inside someone's profile. The user picks one of these
/// in the Spark target picker; that selection becomes `target_kind` +
/// `target_ref` on the POST /sparks payload.
enum SparkTargetKind {
  photo,
  prompt,
  tuneAxis,
  echoQa,
  echoReel,
  echoCommunity,
  echoPost;

  static SparkTargetKind fromString(String? raw) {
    switch (raw) {
      case 'photo':
        return SparkTargetKind.photo;
      case 'prompt':
        return SparkTargetKind.prompt;
      case 'tune_axis':
        return SparkTargetKind.tuneAxis;
      case 'echo_qa':
        return SparkTargetKind.echoQa;
      case 'echo_reel':
        return SparkTargetKind.echoReel;
      case 'echo_community':
        return SparkTargetKind.echoCommunity;
      case 'echo_post':
        return SparkTargetKind.echoPost;
      default:
        return SparkTargetKind.photo;
    }
  }

  String toJsonValue() {
    switch (this) {
      case SparkTargetKind.photo:
        return 'photo';
      case SparkTargetKind.prompt:
        return 'prompt';
      case SparkTargetKind.tuneAxis:
        return 'tune_axis';
      case SparkTargetKind.echoQa:
        return 'echo_qa';
      case SparkTargetKind.echoReel:
        return 'echo_reel';
      case SparkTargetKind.echoCommunity:
        return 'echo_community';
      case SparkTargetKind.echoPost:
        return 'echo_post';
    }
  }
}

/// Lightweight record of "where on a profile did the spark land?" Used by
/// both the picker (to render) and the conversation banner (to summarise
/// "you both Sparked the photo: ...").
class SparkContext {
  final SparkTargetKind targetKind;
  final String targetRef;
  final String summary; // human-friendly label for UI ("Their cooking photo")

  const SparkContext({
    required this.targetKind,
    required this.targetRef,
    required this.summary,
  });

  factory SparkContext.fromJson(Map<String, dynamic> json) {
    return SparkContext(
      targetKind: SparkTargetKind.fromString(json['target_kind'] as String?),
      targetRef: (json['target_ref'] ?? '').toString(),
      summary: (json['summary'] ?? '').toString(),
    );
  }

  Map<String, dynamic> toJson() => {
    'target_kind': targetKind.toJsonValue(),
    'target_ref': targetRef,
    'summary': summary,
  };
}

/// Response shape from POST /v1/dating/sparks. The backend always returns
/// `spark_id`; when both users have sparked each other it also flips
/// `match_formed` to true and embeds the freshly-created `Match`.
class SparkResult {
  final String sparkId;
  final bool matchFormed;
  final MatchDetail? match;

  const SparkResult({
    required this.sparkId,
    required this.matchFormed,
    this.match,
  });

  factory SparkResult.fromJson(Map<String, dynamic> json) {
    final matchRaw = json['match'];
    return SparkResult(
      sparkId: (json['spark_id'] ?? json['id'] ?? '').toString(),
      matchFormed: json['match_formed'] as bool? ?? false,
      match: matchRaw is Map<String, dynamic>
          ? MatchDetail.fromJson(matchRaw)
          : matchRaw is Map
          ? MatchDetail.fromJson(Map<String, dynamic>.from(matchRaw))
          : null,
    );
  }
}

/// One row from GET /v1/dating/sparks/incoming.
class IncomingSpark {
  final String id;
  final String fromUserId;
  final String fromFirstName;
  final String? fromAvatarUrl;
  final SparkTargetKind targetKind;
  final String targetRef;
  final String? note;
  final DateTime createdAt;

  const IncomingSpark({
    required this.id,
    required this.fromUserId,
    required this.fromFirstName,
    this.fromAvatarUrl,
    required this.targetKind,
    required this.targetRef,
    this.note,
    required this.createdAt,
  });

  factory IncomingSpark.fromJson(Map<String, dynamic> json) {
    return IncomingSpark(
      id: (json['id'] ?? '').toString(),
      fromUserId: (json['from_user_id'] ?? '').toString(),
      fromFirstName: (json['from_first_name'] ?? '').toString(),
      fromAvatarUrl: json['from_avatar_url'] as String?,
      targetKind: SparkTargetKind.fromString(json['target_kind'] as String?),
      targetRef: (json['target_ref'] ?? '').toString(),
      note: json['note'] as String?,
      createdAt:
          DateTime.tryParse((json['created_at'] ?? '').toString()) ??
          DateTime.now(),
    );
  }
}

/// One row from GET /v1/dating/matches. Lighter than `MatchDetail` — the
/// inbox only needs name/preview/expiry.
class MatchSummary {
  final String id;
  final String otherUserId;
  final String otherFirstName;
  final String? otherAvatarUrl;
  final String? otherIntent; // casual | serious | marriage
  final String? lastMessagePreview;
  final DateTime? lastMessageAt;
  final DateTime? expiresAt;
  final String status; // active | quiet | sparks_waiting | closed | expired
  final String? conversationId;
  final SparkContext? sparkContext;

  const MatchSummary({
    required this.id,
    required this.otherUserId,
    required this.otherFirstName,
    this.otherAvatarUrl,
    this.otherIntent,
    this.lastMessagePreview,
    this.lastMessageAt,
    this.expiresAt,
    required this.status,
    this.conversationId,
    this.sparkContext,
  });

  factory MatchSummary.fromJson(Map<String, dynamic> json) {
    final ctxRaw = json['spark_context'];
    return MatchSummary(
      id: (json['id'] ?? '').toString(),
      otherUserId: (json['other_user_id'] ?? '').toString(),
      otherFirstName: (json['other_first_name'] ?? '').toString(),
      otherAvatarUrl: json['other_avatar_url'] as String?,
      otherIntent: json['other_intent'] as String?,
      lastMessagePreview: json['last_message_preview'] as String?,
      lastMessageAt: DateTime.tryParse(
        (json['last_message_at'] ?? '').toString(),
      ),
      expiresAt: DateTime.tryParse((json['expires_at'] ?? '').toString()),
      status: (json['status'] ?? 'active').toString(),
      conversationId: json['conversation_id'] as String?,
      sparkContext: ctxRaw is Map<String, dynamic>
          ? SparkContext.fromJson(ctxRaw)
          : ctxRaw is Map
          ? SparkContext.fromJson(Map<String, dynamic>.from(ctxRaw))
          : null,
    );
  }

  /// Hours until match expiry, negative if already expired. `null` if there
  /// is no expiry on this match.
  Duration? get timeUntilExpiry {
    final exp = expiresAt;
    if (exp == null) return null;
    return exp.difference(DateTime.now());
  }
}

/// Full match detail returned by GET /v1/dating/matches/:id and embedded in
/// SparkResult when a match is freshly formed.
class MatchDetail {
  final String id;
  final String otherUserId;
  final String otherFirstName;
  final String? otherAvatarUrl;
  final String? otherIntent;
  final String status;
  final String? conversationId;
  final DateTime? expiresAt;
  final DateTime? createdAt;
  final SparkContext? sparkContext;
  final String? authorUserId; // who created (sparked first) — for extend gate

  const MatchDetail({
    required this.id,
    required this.otherUserId,
    required this.otherFirstName,
    this.otherAvatarUrl,
    this.otherIntent,
    required this.status,
    this.conversationId,
    this.expiresAt,
    this.createdAt,
    this.sparkContext,
    this.authorUserId,
  });

  factory MatchDetail.fromJson(Map<String, dynamic> json) {
    final ctxRaw = json['spark_context'];
    return MatchDetail(
      id: (json['id'] ?? '').toString(),
      otherUserId: (json['other_user_id'] ?? '').toString(),
      otherFirstName: (json['other_first_name'] ?? '').toString(),
      otherAvatarUrl: json['other_avatar_url'] as String?,
      otherIntent: json['other_intent'] as String?,
      status: (json['status'] ?? 'active').toString(),
      conversationId: json['conversation_id'] as String?,
      expiresAt: DateTime.tryParse((json['expires_at'] ?? '').toString()),
      createdAt: DateTime.tryParse((json['created_at'] ?? '').toString()),
      sparkContext: ctxRaw is Map<String, dynamic>
          ? SparkContext.fromJson(ctxRaw)
          : ctxRaw is Map
          ? SparkContext.fromJson(Map<String, dynamic>.from(ctxRaw))
          : null,
      authorUserId: json['author_user_id'] as String?,
    );
  }

  Duration? get timeUntilExpiry {
    final exp = expiresAt;
    if (exp == null) return null;
    return exp.difference(DateTime.now());
  }
}

/// A single sparkable item drawn from a candidate's profile. Synthesised
/// client-side from `PulseCard` data plus, eventually, a profile-detail
/// response. Not a wire type — hence no `fromJson`.
class SparkableItem {
  final SparkTargetKind kind;
  final String ref; // photo url, prompt id, axis name, echo id...
  final String label; // primary label (e.g., prompt question, axis name)
  final String? secondary; // answer text, value, etc.
  final String? imageUrl; // for photo cards

  const SparkableItem({
    required this.kind,
    required this.ref,
    required this.label,
    this.secondary,
    this.imageUrl,
  });
}

class AadhaarFlowStart {
  final String authorizeUrl;
  final String state;
  final DateTime? expiresAt;

  const AadhaarFlowStart({
    required this.authorizeUrl,
    required this.state,
    this.expiresAt,
  });

  factory AadhaarFlowStart.fromJson(Map<String, dynamic> json) {
    return AadhaarFlowStart(
      authorizeUrl:
          (json['digilocker_authorize_url'] ??
                  json['authorize_url'] ??
                  json['url'] ??
                  '')
              .toString(),
      state: (json['state'] ?? '').toString(),
      expiresAt: DateTime.tryParse((json['expires_at'] ?? '').toString()),
    );
  }
}

class AadhaarFlowResult {
  final String trustTier;
  final bool success;
  final DateTime? verifiedAt;
  final String? errorMessage;

  const AadhaarFlowResult({
    required this.trustTier,
    required this.success,
    this.verifiedAt,
    this.errorMessage,
  });

  factory AadhaarFlowResult.fromJson(Map<String, dynamic> json) {
    return AadhaarFlowResult(
      trustTier: (json['trust_tier'] ?? json['tier'] ?? 'none').toString(),
      success: json['success'] as bool? ?? true,
      verifiedAt: DateTime.tryParse(
        (json['verified_at'] ?? '').toString(),
      ),
      errorMessage: json['error_message'] as String?,
    );
  }
}

class SelfieFlowResult {
  final String trustTier;
  final bool success;
  final double? livenessScore;
  final String? errorMessage;

  const SelfieFlowResult({
    required this.trustTier,
    required this.success,
    this.livenessScore,
    this.errorMessage,
  });

  factory SelfieFlowResult.fromJson(Map<String, dynamic> json) {
    return SelfieFlowResult(
      trustTier: (json['trust_tier'] ?? json['tier'] ?? 'none').toString(),
      success: json['success'] as bool? ?? true,
      livenessScore:
          (json['liveness_score'] as num?)?.toDouble() ??
              (json['similarity'] as num?)?.toDouble(),
      errorMessage: json['error_message'] as String?,
    );
  }

  /// Spec alias: API returns either `liveness_score` or `similarity`.
  double? get similarity => livenessScore;
}

class Vouch {
  final String id;
  final String voucherUserId;
  final String? voucherName;
  final String? voucherAvatarUrl;
  final String voucheeId;
  final String? voucheeName;
  final String relationship;
  final String? communityId;
  final String? communityName;
  final String? note;
  final String status;
  final DateTime? createdAt;
  final DateTime? decidedAt;
  final DateTime? updatedAt;

  const Vouch({
    required this.id,
    required this.voucherUserId,
    this.voucherName,
    this.voucherAvatarUrl,
    required this.voucheeId,
    this.voucheeName,
    required this.relationship,
    this.communityId,
    this.communityName,
    this.note,
    required this.status,
    this.createdAt,
    this.decidedAt,
    this.updatedAt,
  });

  factory Vouch.fromJson(Map<String, dynamic> json) {
    return Vouch(
      id: (json['id'] ?? '').toString(),
      voucherUserId: (json['voucher_user_id'] ?? '').toString(),
      voucherName: json['voucher_name'] as String?,
      voucherAvatarUrl: json['voucher_avatar_url'] as String?,
      voucheeId: (json['vouchee_id'] ?? '').toString(),
      voucheeName: json['vouchee_name'] as String?,
      relationship: (json['relationship'] ?? '').toString(),
      communityId: json['community_id'] as String?,
      communityName: json['community_name'] as String?,
      note: json['note'] as String?,
      status: (json['status'] ?? 'pending').toString(),
      createdAt: DateTime.tryParse((json['created_at'] ?? '').toString()),
      decidedAt: DateTime.tryParse((json['decided_at'] ?? '').toString()),
      updatedAt: DateTime.tryParse((json['updated_at'] ?? '').toString()),
    );
  }

  bool get isPending => status == 'pending';
  bool get isAccepted => status == 'accepted' || status == 'active';
}

class SafeMeet {
  final String id;
  final String withUserId;
  final DateTime? when;
  final double? lat;
  final double? lng;
  final String venueName;
  final String status;
  final DateTime? createdAt;

  const SafeMeet({
    required this.id,
    required this.withUserId,
    this.when,
    this.lat,
    this.lng,
    required this.venueName,
    required this.status,
    this.createdAt,
  });

  factory SafeMeet.fromJson(Map<String, dynamic> json) {
    return SafeMeet(
      id: (json['id'] ?? '').toString(),
      withUserId: (json['with_user_id'] ?? '').toString(),
      when: DateTime.tryParse((json['when'] ?? '').toString()),
      lat: (json['lat'] as num?)?.toDouble(),
      lng: (json['lng'] as num?)?.toDouble(),
      venueName: (json['venue_name'] ?? '').toString(),
      status: (json['status'] ?? 'scheduled').toString(),
      createdAt: DateTime.tryParse((json['created_at'] ?? '').toString()),
    );
  }
}

// ---------------------------------------------------------------------------
// Phase 1 — "My Reports" list (status of reports the viewer filed).
//
// `endpointAvailable` is false when dating-service hasn't shipped
// `GET /v1/dating/safety/reports/me` yet — the UI uses it to show a
// pending-endpoint banner instead of an empty state.
// ---------------------------------------------------------------------------

class MyReportEntry {
  final String id;
  final String targetUserId;
  final String? targetName;
  final String category;
  final String status; // submitted | under_review | actioned | dismissed
  final String? details;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final String? resolutionNote;

  const MyReportEntry({
    required this.id,
    required this.targetUserId,
    this.targetName,
    required this.category,
    required this.status,
    this.details,
    this.createdAt,
    this.updatedAt,
    this.resolutionNote,
  });

  factory MyReportEntry.fromJson(Map<String, dynamic> json) {
    return MyReportEntry(
      id: (json['id'] ?? '').toString(),
      targetUserId: (json['target_user_id'] ?? '').toString(),
      targetName: json['target_name'] as String?,
      category: (json['category'] ?? '').toString(),
      status: (json['status'] ?? 'submitted').toString(),
      details: json['details'] as String?,
      createdAt: DateTime.tryParse((json['created_at'] ?? '').toString()),
      updatedAt: DateTime.tryParse((json['updated_at'] ?? '').toString()),
      resolutionNote: json['resolution_note'] as String?,
    );
  }
}

class MyReportsResult {
  final List<MyReportEntry> items;
  final bool endpointAvailable;

  const MyReportsResult({
    required this.items,
    required this.endpointAvailable,
  });
}

// ---------------------------------------------------------------------------
// Sprint 5 — Premium tier, data export.
//
// Spec §14 (Premium tier) and §13 (India-first / DPDP).
// All amounts are in paise to mirror the backend (Razorpay convention).
// ---------------------------------------------------------------------------

/// A single Premium plan returned by `GET /v1/dating/premium/plans`.
class PremiumPlan {
  final String id;
  final String name;
  final String tagline;
  final int amountInrPaise;
  final int durationDays;
  final String? badge;
  final List<String> features;
  final bool isOneShot;

  const PremiumPlan({
    required this.id,
    required this.name,
    required this.tagline,
    required this.amountInrPaise,
    required this.durationDays,
    this.badge,
    this.features = const [],
    this.isOneShot = false,
  });

  /// Display price in `₹X,YYY` style (rupees, no decimal because plans are
  /// whole-rupee for v1).
  String get displayPriceInr {
    final rupees = amountInrPaise ~/ 100;
    return '₹${_formatRupees(rupees)}';
  }

  static String _formatRupees(int rupees) {
    final s = rupees.toString();
    if (s.length <= 3) return s;
    final last3 = s.substring(s.length - 3);
    final rest = s.substring(0, s.length - 3);
    final buf = StringBuffer();
    for (int i = 0; i < rest.length; i++) {
      if (i != 0 && (rest.length - i) % 2 == 0) buf.write(',');
      buf.write(rest[i]);
    }
    buf.write(',');
    buf.write(last3);
    return buf.toString();
  }

  factory PremiumPlan.fromJson(Map<String, dynamic> json) {
    return PremiumPlan(
      id: (json['id'] ?? '').toString(),
      name: (json['name'] ?? '').toString(),
      tagline: (json['tagline'] ?? '').toString(),
      amountInrPaise:
          (json['amount_inr_paise'] ?? json['amount_paise'] ?? 0) as int,
      durationDays: (json['duration_days'] ?? 0) as int,
      badge: json['badge'] as String?,
      features: (json['features'] is List)
          ? List<String>.from(
              (json['features'] as List).whereType<String>(),
            )
          : const [],
      isOneShot: (json['is_one_shot'] ?? false) as bool,
    );
  }
}

/// Response envelope from `POST /v1/dating/premium/checkout`.
class PremiumCheckoutOrder {
  final String razorpayOrderId;
  final String razorpayKeyId;
  final int amountInrPaise;
  final String planName;
  final String planId;

  const PremiumCheckoutOrder({
    required this.razorpayOrderId,
    required this.razorpayKeyId,
    required this.amountInrPaise,
    required this.planName,
    required this.planId,
  });

  factory PremiumCheckoutOrder.fromJson(Map<String, dynamic> json) {
    return PremiumCheckoutOrder(
      razorpayOrderId: (json['razorpay_order_id'] ?? '').toString(),
      razorpayKeyId: (json['razorpay_key_id'] ?? '').toString(),
      amountInrPaise: (json['amount_inr_paise'] ?? 0) as int,
      planName: (json['plan_name'] ?? '').toString(),
      planId: (json['plan_id'] ?? '').toString(),
    );
  }
}

/// Response envelope from `GET /v1/dating/premium/me`.
class PremiumState {
  final bool active;
  final String? planId;
  final String? planName;
  final DateTime? expiresAt;
  final bool autoRenew;
  final int boostsRemaining;
  final int sparksRemainingToday;
  final bool unlimitedSparks;

  const PremiumState({
    required this.active,
    this.planId,
    this.planName,
    this.expiresAt,
    this.autoRenew = false,
    this.boostsRemaining = 0,
    this.sparksRemainingToday = 0,
    this.unlimitedSparks = false,
  });

  factory PremiumState.fromJson(Map<String, dynamic> json) {
    return PremiumState(
      active: (json['active'] ?? false) as bool,
      planId: json['plan_id'] as String?,
      planName: json['plan_name'] as String?,
      expiresAt:
          DateTime.tryParse((json['expires_at'] ?? '').toString()),
      autoRenew: (json['auto_renew'] ?? false) as bool,
      boostsRemaining: (json['boosts_remaining'] ?? 0) as int,
      sparksRemainingToday: (json['sparks_remaining_today'] ?? 0) as int,
      unlimitedSparks: (json['unlimited_sparks'] ?? false) as bool,
    );
  }

  static const PremiumState free = PremiumState(active: false);
}

/// One row in the user's data export history (`GET /v1/dating/data-export/me`).
class DataExportRecord {
  final String id;
  final String status;
  final DateTime? requestedAt;
  final DateTime? readyAt;
  final DateTime? expiresAt;
  final String? downloadUrl;

  const DataExportRecord({
    required this.id,
    required this.status,
    this.requestedAt,
    this.readyAt,
    this.expiresAt,
    this.downloadUrl,
  });

  factory DataExportRecord.fromJson(Map<String, dynamic> json) {
    return DataExportRecord(
      id: (json['id'] ?? '').toString(),
      status: (json['status'] ?? 'pending').toString(),
      requestedAt:
          DateTime.tryParse((json['requested_at'] ?? '').toString()),
      readyAt: DateTime.tryParse((json['ready_at'] ?? '').toString()),
      expiresAt:
          DateTime.tryParse((json['expires_at'] ?? '').toString()),
      downloadUrl: json['download_url'] as String?,
    );
  }

  bool get isReady => status == 'ready' && (downloadUrl ?? '').isNotEmpty;
}
