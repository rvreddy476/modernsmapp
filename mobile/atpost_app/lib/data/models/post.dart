class Post {
  final String id;
  final String authorId;
  final String? authorName;
  final String? authorAvatar;
  final String content;
  final String contentType; // 'post', 'poll', 'reel', 'video'
  final String visibility;
  final List<String> tags;
  final List<String> mediaIds;
  final int likeCount;
  final int commentCount;
  final int shareCount;
  final int? durationSeconds;
  final bool isLiked;
  final bool isBookmarked;
  final DateTime createdAt;
  final String? feeling;
  final String? activity;
  final String? activityDetail;
  final String? locationName;
  final PollData? poll;

  const Post({
    required this.id,
    required this.authorId,
    this.authorName,
    this.authorAvatar,
    required this.content,
    this.contentType = 'post',
    this.visibility = 'public',
    this.tags = const [],
    this.mediaIds = const [],
    this.likeCount = 0,
    this.commentCount = 0,
    this.shareCount = 0,
    this.durationSeconds,
    this.isLiked = false,
    this.isBookmarked = false,
    required this.createdAt,
    this.feeling,
    this.activity,
    this.activityDetail,
    this.locationName,
    this.poll,
  });

  factory Post.fromJson(Map<String, dynamic> json) {
    // Handle nested counts object: {"counts": {"likes": 0, "comments": 0, "shares": 0}}
    final counts = json['counts'] as Map<String, dynamic>? ?? {};
    return Post(
      id: json['id'] as String? ?? json['post_id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      authorName: json['author_name'] as String?,
      authorAvatar: json['author_avatar'] as String?,
      content: json['content'] as String? ?? json['text'] as String? ?? '',
      contentType: json['content_type'] as String? ?? 'post',
      visibility: json['visibility'] as String? ?? 'public',
      tags: (json['tags'] as List<dynamic>?)?.cast<String>() ?? [],
      mediaIds: (json['media_ids'] as List<dynamic>?)?.cast<String>() ?? [],
      likeCount: json['like_count'] as int? ?? counts['likes'] as int? ?? 0,
      commentCount: json['comment_count'] as int? ?? counts['comments'] as int? ?? 0,
      shareCount: json['share_count'] as int? ?? counts['shares'] as int? ?? 0,
      durationSeconds: json['duration_seconds'] as int?,
      isLiked: json['is_liked'] as bool? ?? false,
      isBookmarked: json['is_bookmarked'] as bool? ?? false,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
      feeling: json['feeling'] as String?,
      activity: json['activity'] as String?,
      activityDetail: json['activity_detail'] as String?,
      locationName: json['location_name'] as String?,
      poll: json['poll'] != null
          ? PollData.fromJson(json['poll'] as Map<String, dynamic>)
          : null,
    );
  }

  bool get isReel => contentType == 'reel';
  bool get isVideo => contentType == 'video';
  bool get isPoll => contentType == 'poll';

  /// Returns the serve URL for the first media item, or empty string if none.
  String get firstMediaUrl {
    if (mediaIds.isEmpty) return '';
    return '/v1/media/${mediaIds.first}/serve';
  }
}

class PollData {
  final String question;
  final List<PollOption> options;
  final bool allowsMultiple;
  final DateTime? endsAt;
  final int totalVotes;
  final bool hasEnded;

  const PollData({
    required this.question,
    required this.options,
    this.allowsMultiple = false,
    this.endsAt,
    this.totalVotes = 0,
    this.hasEnded = false,
  });

  factory PollData.fromJson(Map<String, dynamic> json) {
    return PollData(
      question: json['question'] as String? ?? '',
      options: (json['options'] as List<dynamic>?)
              ?.map((e) => PollOption.fromJson(e as Map<String, dynamic>))
              .toList() ??
          [],
      allowsMultiple: json['allows_multiple'] as bool? ?? false,
      endsAt: json['ends_at'] != null
          ? DateTime.parse(json['ends_at'] as String)
          : null,
      totalVotes: json['total_votes'] as int? ?? 0,
      hasEnded: json['has_ended'] as bool? ?? false,
    );
  }
}

class PollOption {
  final String id;
  final String label;
  final int voteCount;
  final double percentage;

  const PollOption({
    required this.id,
    required this.label,
    this.voteCount = 0,
    this.percentage = 0,
  });

  factory PollOption.fromJson(Map<String, dynamic> json) {
    return PollOption(
      id: json['id'] as String? ?? '',
      label: json['label'] as String? ?? '',
      voteCount: json['vote_count'] as int? ?? 0,
      percentage: (json['percentage'] as num?)?.toDouble() ?? 0,
    );
  }
}

class Comment {
  final String id;
  final String postId;
  final String authorId;
  final String? authorName;
  final String? authorAvatar;
  final String text;
  final int likeCount;
  final DateTime createdAt;

  const Comment({
    required this.id,
    required this.postId,
    required this.authorId,
    this.authorName,
    this.authorAvatar,
    required this.text,
    this.likeCount = 0,
    required this.createdAt,
  });

  factory Comment.fromJson(Map<String, dynamic> json) {
    return Comment(
      id: json['id'] as String? ?? json['comment_id'] as String? ?? '',
      postId: json['post_id'] as String? ?? '',
      authorId: json['user_id'] as String? ?? json['author_id'] as String? ?? '',
      authorName: json['user_display_name'] as String? ?? json['author_name'] as String?,
      authorAvatar: json['user_avatar_url'] as String? ?? json['author_avatar'] as String?,
      text: json['text'] as String? ?? '',
      likeCount: json['like_count'] as int? ?? 0,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
