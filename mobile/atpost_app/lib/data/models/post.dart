import 'package:atpost_app/core/utils/app_logger.dart';

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
    try {
      final counts = json['counts'] as Map<String, dynamic>? ?? {};
      return Post(
        id: (json['id'] ?? json['post_id'] ?? '').toString(),
        authorId: (json['author_id'] ?? '').toString(),
        authorName: json['author_name']?.toString(),
        authorAvatar: json['author_avatar']?.toString(),
        content: (json['content'] ?? json['text'] ?? '').toString(),
        contentType: (json['content_type'] ?? 'post').toString(),
        visibility: (json['visibility'] ?? 'public').toString(),
        tags: _parseList<String>(json['tags']),
        mediaIds: _parseList<String>(json['media_ids']),
        likeCount: _toInt(json['like_count'] ?? counts['likes']),
        commentCount: _toInt(json['comment_count'] ?? counts['comments']),
        shareCount: _toInt(json['share_count'] ?? counts['shares']),
        durationSeconds: _toIntNullable(json['duration_seconds']),
        isLiked: _toBool(json['is_liked']),
        isBookmarked: _toBool(json['is_bookmarked']),
        createdAt: _parseDate(json['created_at']),
        feeling: json['feeling']?.toString(),
        activity: json['activity']?.toString(),
        activityDetail: json['activity_detail']?.toString(),
        locationName: json['location_name']?.toString(),
        poll: json['poll'] != null
            ? PollData.fromJson(Map<String, dynamic>.from(json['poll']))
            : null,
      );
    } catch (e, st) {
      AppLogger.error('Post.fromJson failed: $e', error: e, stackTrace: st);
      return Post.empty();
    }
  }

  static Post empty() => Post(
        id: 'error_${DateTime.now().millisecondsSinceEpoch}',
        authorId: '',
        content: 'Content unavailable',
        createdAt: DateTime.now(),
      );

  bool get isReel => contentType == 'reel';
  bool get isVideo => contentType == 'video';
  bool get isPoll => contentType == 'poll';

  String get firstMediaUrl {
    if (mediaIds.isEmpty) return '';
    return '/v1/media/${mediaIds.first}/serve';
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'author_id': authorId,
      'author_name': authorName,
      'author_avatar': authorAvatar,
      'content': content,
      'content_type': contentType,
      'visibility': visibility,
      'tags': tags,
      'media_ids': mediaIds,
      'like_count': likeCount,
      'comment_count': commentCount,
      'share_count': shareCount,
      'duration_seconds': durationSeconds,
      'is_liked': isLiked,
      'is_bookmarked': isBookmarked,
      'created_at': createdAt.toIso8601String(),
      'feeling': feeling,
      'activity': activity,
      'activity_detail': activityDetail,
      'location_name': locationName,
      'poll': poll?.toJson(),
    };
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
      question: (json['question'] ?? '').toString(),
      options: (json['options'] as List? ?? [])
          .map((e) => PollOption.fromJson(Map<String, dynamic>.from(e)))
          .toList(),
      allowsMultiple: _toBool(json['allows_multiple']),
      endsAt: _parseDateNullable(json['ends_at']),
      totalVotes: _toInt(json['total_votes']),
      hasEnded: _toBool(json['has_ended']),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'question': question,
      'options': options.map((e) => e.toJson()).toList(),
      'allows_multiple': allowsMultiple,
      'ends_at': endsAt?.toIso8601String(),
      'total_votes': totalVotes,
      'has_ended': hasEnded,
    };
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
      id: (json['id'] ?? '').toString(),
      label: (json['label'] ?? '').toString(),
      voteCount: _toInt(json['vote_count']),
      percentage: _toDouble(json['percentage']),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'label': label,
      'vote_count': voteCount,
      'percentage': percentage,
    };
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
      id: (json['id'] ?? json['comment_id'] ?? '').toString(),
      postId: (json['post_id'] ?? '').toString(),
      authorId: (json['user_id'] ?? json['author_id'] ?? '').toString(),
      authorName: (json['user_display_name'] ?? json['author_name'])?.toString(),
      authorAvatar: (json['user_avatar_url'] ?? json['author_avatar'])?.toString(),
      text: (json['text'] ?? '').toString(),
      likeCount: _toInt(json['like_count']),
      createdAt: _parseDate(json['created_at']),
    );
  }
}

// --- Total Resilience Helper Methods ---

List<T> _parseList<T>(dynamic data) {
  if (data is List) return data.cast<T>();
  return const [];
}

int _toInt(dynamic data) {
  if (data is int) return data;
  if (data is double) return data.toInt();
  if (data is String) return int.tryParse(data) ?? 0;
  return 0;
}

int? _toIntNullable(dynamic data) {
  if (data == null) return null;
  return _toInt(data);
}

double _toDouble(dynamic data) {
  if (data is double) return data;
  if (data is int) return data.toDouble();
  if (data is String) return double.tryParse(data) ?? 0.0;
  return 0.0;
}

bool _toBool(dynamic data) {
  if (data is bool) return data;
  if (data is int) return data == 1;
  if (data is String) return data.toLowerCase() == 'true';
  return false;
}

DateTime _parseDate(dynamic data) {
  if (data is String) return DateTime.tryParse(data) ?? DateTime.now();
  return DateTime.now();
}

DateTime? _parseDateNullable(dynamic data) {
  if (data == null) return null;
  return _parseDate(data);
}
