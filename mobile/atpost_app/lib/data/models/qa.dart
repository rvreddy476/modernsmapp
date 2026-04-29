import 'package:atpost_app/core/utils/app_logger.dart';

/// Production-ready Question model for the Q&A system.
class Question {
  final String id;
  final String authorId;
  final String? authorName;
  final String? authorAvatar;
  final String title;
  final String body;
  final String bodyHtml;
  final List<String> topics;
  final List<QaTopic> topicObjects;
  final String? communityId;
  final SimpleCommunity? community;
  final int upvoteCount;
  final int downvoteCount;
  final int answerCount;
  final int viewCount;
  final bool isPinned;
  final bool isAnswered;
  final bool isAnonymous;
  final DateTime createdAt;
  final bool? viewerVote; // true = up, false = down, null = none

  const Question({
    required this.id,
    required this.authorId,
    this.authorName,
    this.authorAvatar,
    required this.title,
    required this.body,
    this.bodyHtml = '',
    this.topics = const [],
    this.topicObjects = const [],
    this.communityId,
    this.community,
    this.upvoteCount = 0,
    this.downvoteCount = 0,
    this.answerCount = 0,
    this.viewCount = 0,
    this.isPinned = false,
    this.isAnswered = false,
    this.isAnonymous = false,
    required this.createdAt,
    this.viewerVote,
  });

  factory Question.fromJson(Map<String, dynamic> json) {
    try {
      return Question(
        id: (json['id'] ?? '').toString(),
        authorId: (json['author_id'] ?? '').toString(),
        authorName: json['author_name']?.toString(),
        authorAvatar: json['author_avatar']?.toString(),
        title: (json['title'] ?? '').toString(),
        body: (json['body'] ?? '').toString(),
        bodyHtml: json['body_html']?.toString() ?? '',
        topics: _parseList<String>(json['topics']),
        topicObjects: (json['topic_objects'] as List?)
                ?.map((e) => QaTopic.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        communityId: json['community_id']?.toString(),
        community: json['community'] != null
            ? SimpleCommunity.fromJson(
                Map<String, dynamic>.from(json['community']))
            : null,
        upvoteCount: _toInt(json['upvote_count']),
        downvoteCount: _toInt(json['downvote_count']),
        answerCount: _toInt(json['answer_count']),
        viewCount: _toInt(json['view_count']),
        isPinned: json['is_pinned'] == true,
        isAnswered: json['is_answered'] == true,
        isAnonymous: json['is_anonymous'] == true,
        createdAt: _parseDate(json['created_at']),
        viewerVote: json['viewer_vote'] as bool?,
      );
    } catch (e, st) {
      AppLogger.error('Question.fromJson failed', error: e, stackTrace: st);
      return Question.empty();
    }
  }

  static Question empty() => Question(
    id: 'error',
    authorId: '',
    title: 'Content unavailable',
    body: '',
    createdAt: DateTime.now(),
  );

  int get voteScore => upvoteCount - downvoteCount;
  String get status => isAnswered ? 'Answered' : 'Open';

  QaQuestionSummary toSummary() {
    return QaQuestionSummary(
      id: id,
      title: title,
      authorName: authorName ?? 'Anonymous',
      answerCount: answerCount,
      upvoteCount: upvoteCount,
      viewCount: viewCount,
      createdAt: createdAt,
      excerpt: body.length > 150 ? '${body.substring(0, 147)}...' : body,
      isAnswered: isAnswered,
      isPinned: isPinned,
      community: community,
    );
  }

  Question copyWith({
    String? id,
    String? authorId,
    String? authorName,
    String? authorAvatar,
    String? title,
    String? body,
    List<String>? topics,
    String? communityId,
    int? upvoteCount,
    int? downvoteCount,
    int? answerCount,
    int? viewCount,
    bool? isPinned,
    bool? isAnswered,
    bool? isAnonymous,
    DateTime? createdAt,
    bool? viewerVote,
  }) {
    return Question(
      id: id ?? this.id,
      authorId: authorId ?? this.authorId,
      authorName: authorName ?? this.authorName,
      authorAvatar: authorAvatar ?? this.authorAvatar,
      title: title ?? this.title,
      body: body ?? this.body,
      topics: topics ?? this.topics,
      communityId: communityId ?? this.communityId,
      upvoteCount: upvoteCount ?? this.upvoteCount,
      downvoteCount: downvoteCount ?? this.downvoteCount,
      answerCount: answerCount ?? this.answerCount,
      viewCount: viewCount ?? this.viewCount,
      isPinned: isPinned ?? this.isPinned,
      isAnswered: isAnswered ?? this.isAnswered,
      isAnonymous: isAnonymous ?? this.isAnonymous,
      createdAt: createdAt ?? this.createdAt,
      viewerVote: viewerVote ?? this.viewerVote,
    );
  }
}

class QaTopic {
  final String id;
  final String name;
  final String slug;
  final String description;
  final int questionCount;
  final int followerCount;
  final bool isFeatured;

  const QaTopic({
    required this.id,
    required this.name,
    required this.slug,
    this.description = '',
    this.questionCount = 0,
    this.followerCount = 0,
    this.isFeatured = false,
  });

  factory QaTopic.fromJson(Map<String, dynamic> json) {
    return QaTopic(
      id: json['id']?.toString() ?? '',
      name: json['name']?.toString() ?? '',
      slug: json['slug']?.toString() ?? '',
      description: json['description']?.toString() ?? '',
      questionCount: _toInt(json['question_count']),
      followerCount: _toInt(json['follower_count']),
      isFeatured: json['is_featured'] == true,
    );
  }
}

class QaTopicOption {
  final String name;
  final String slug;

  const QaTopicOption({required this.name, required this.slug});

  factory QaTopicOption.fromJson(Map<String, dynamic> json) {
    return QaTopicOption(
      name: json['name']?.toString() ?? '',
      slug: json['slug']?.toString() ?? '',
    );
  }
}

class QaQuestionSummary {
  final String id;
  final String title;
  final String authorName;
  final int answerCount;
  final int upvoteCount;
  final int viewCount;
  final DateTime createdAt;
  final String excerpt;
  final bool isAnswered;
  final bool isPinned;
  final SimpleCommunity? community;

  const QaQuestionSummary({
    required this.id,
    required this.title,
    required this.authorName,
    this.answerCount = 0,
    this.upvoteCount = 0,
    this.viewCount = 0,
    required this.createdAt,
    this.excerpt = '',
    this.isAnswered = false,
    this.isPinned = false,
    this.community,
  });

  factory QaQuestionSummary.fromJson(Map<String, dynamic> json) {
    return QaQuestionSummary(
      id: json['id']?.toString() ?? '',
      title: json['title']?.toString() ?? '',
      authorName: json['author_name']?.toString() ?? 'Anonymous',
      answerCount: _toInt(json['answer_count']),
      upvoteCount: _toInt(json['upvote_count']),
      viewCount: _toInt(json['view_count']),
      createdAt: _parseDate(json['created_at']),
      excerpt: json['excerpt']?.toString() ?? '',
      isAnswered: json['is_answered'] == true,
      isPinned: json['is_pinned'] == true,
      community: json['community'] != null
          ? SimpleCommunity.fromJson(Map<String, dynamic>.from(json['community']))
          : null,
    );
  }

  int get voteScore => upvoteCount;
}

class SimpleCommunity {
  final String id;
  final String name;

  const SimpleCommunity({required this.id, required this.name});

  factory SimpleCommunity.fromJson(Map<String, dynamic> json) {
    return SimpleCommunity(
      id: json['id']?.toString() ?? '',
      name: json['name']?.toString() ?? '',
    );
  }
}

class QuestionDetail {
  final Question question;
  final List<Answer> answers;

  const QuestionDetail({required this.question, required this.answers});
}

class QAPage {
  final List<Question> items;
  final String? nextCursor;

  const QAPage({required this.items, this.nextCursor});
}

class CommunityQaSettings {
  final bool qaEnabled;
  final String welcomeMessage;
  final int totalQuestions;
  final int totalAnswers;
  final int uniqueContributors;
  final String askPermission;
  final String answerPermission;
  final bool autoSuggestTopics;
  final bool requireApproval;
  final bool anonymityEnabled;

  const CommunityQaSettings({
    this.qaEnabled = true,
    this.welcomeMessage = '',
    this.totalQuestions = 0,
    this.totalAnswers = 0,
    this.uniqueContributors = 0,
    this.askPermission = 'members',
    this.answerPermission = 'members',
    this.autoSuggestTopics = false,
    this.requireApproval = false,
    this.anonymityEnabled = false,
  });

  factory CommunityQaSettings.fromJson(Map<String, dynamic> json) {
    return CommunityQaSettings(
      qaEnabled: json['qa_enabled'] != false,
      welcomeMessage: json['welcome_message']?.toString() ?? '',
      totalQuestions: _toInt(json['total_questions']),
      totalAnswers: _toInt(json['total_answers']),
      uniqueContributors: _toInt(json['unique_contributors']),
      askPermission: json['ask_permission']?.toString() ?? 'members',
      answerPermission: json['answer_permission']?.toString() ?? 'members',
      autoSuggestTopics: json['auto_suggest_topics'] == true,
      requireApproval: json['require_approval'] == true,
      anonymityEnabled: json['anonymity_enabled'] == true,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'qa_enabled': qaEnabled,
      'welcome_message': welcomeMessage,
      'ask_permission': askPermission,
      'answer_permission': answerPermission,
      'auto_suggest_topics': autoSuggestTopics,
      'require_approval': requireApproval,
      'anonymity_enabled': anonymityEnabled,
    };
  }

  CommunityQaSettings copyWith({
    bool? qaEnabled,
    String? welcomeMessage,
    int? totalQuestions,
    int? totalAnswers,
    int? uniqueContributors,
    String? askPermission,
    String? answerPermission,
    bool? autoSuggestTopics,
    bool? requireApproval,
    bool? anonymityEnabled,
  }) {
    return CommunityQaSettings(
      qaEnabled: qaEnabled ?? this.qaEnabled,
      welcomeMessage: welcomeMessage ?? this.welcomeMessage,
      totalQuestions: totalQuestions ?? this.totalQuestions,
      totalAnswers: totalAnswers ?? this.totalAnswers,
      uniqueContributors: uniqueContributors ?? this.uniqueContributors,
      askPermission: askPermission ?? this.askPermission,
      answerPermission: answerPermission ?? this.answerPermission,
      autoSuggestTopics: autoSuggestTopics ?? this.autoSuggestTopics,
      requireApproval: requireApproval ?? this.requireApproval,
      anonymityEnabled: anonymityEnabled ?? this.anonymityEnabled,
    );
  }
}

/// Production-ready Answer model.
class Answer {
  final String id;
  final String questionId;
  final String authorId;
  final String? authorName;
  final String? authorAvatar;
  final String body;
  final String bodyHtml;
  final int upvoteCount;
  final int downvoteCount;
  final int commentCount;
  final bool isAccepted;
  final bool isAnonymous;
  final DateTime createdAt;
  final bool? viewerVote;

  const Answer({
    required this.id,
    required this.questionId,
    required this.authorId,
    this.authorName,
    this.authorAvatar,
    required this.body,
    this.bodyHtml = '',
    this.upvoteCount = 0,
    this.downvoteCount = 0,
    this.commentCount = 0,
    this.isAccepted = false,
    this.isAnonymous = false,
    required this.createdAt,
    this.viewerVote,
  });

  factory Answer.fromJson(Map<String, dynamic> json) {
    try {
      return Answer(
        id: (json['id'] ?? '').toString(),
        questionId: (json['question_id'] ?? '').toString(),
        authorId: (json['author_id'] ?? '').toString(),
        authorName: json['author_name']?.toString(),
        authorAvatar: json['author_avatar']?.toString(),
        body: (json['body'] ?? '').toString(),
        bodyHtml: json['body_html']?.toString() ?? '',
        upvoteCount: _toInt(json['upvote_count']),
        downvoteCount: _toInt(json['downvote_count']),
        commentCount: _toInt(json['comment_count']),
        isAccepted: json['is_accepted'] == true,
        isAnonymous: json['is_anonymous'] == true,
        createdAt: _parseDate(json['created_at']),
        viewerVote: json['viewer_vote'] as bool?,
      );
    } catch (e, st) {
      AppLogger.error('Answer.fromJson failed', error: e, stackTrace: st);
      return Answer.empty();
    }
  }

  int get voteScore => upvoteCount - downvoteCount;

  static Answer empty() => Answer(
    id: 'error',
    questionId: '',
    authorId: '',
    body: 'Unavailable',
    createdAt: DateTime.now(),
  );
}

// --- Resilience Helpers ---
List<T> _parseList<T>(dynamic data) {
  if (data is List) return data.cast<T>();
  return const [];
}

int _toInt(dynamic data) {
  if (data is int) return data;
  if (data is String) return int.tryParse(data) ?? 0;
  return 0;
}

DateTime _parseDate(dynamic data) {
  if (data is String) return DateTime.tryParse(data) ?? DateTime.now();
  return DateTime.now();
}

/// Anonymous author placeholder ID returned by backend when is_anonymous=true.
const String anonymousAuthorId = '00000000-0000-0000-0000-000000000000';

/// Comment on an Answer.
class AnswerComment {
  final String id;
  final String answerId;
  final String authorId;
  final String? authorName;
  final String? authorAvatar;
  final String body;
  final int voteScore;
  final int viewerVote; // 1 | -1 | 0
  final DateTime createdAt;
  final DateTime updatedAt;

  const AnswerComment({
    required this.id,
    required this.answerId,
    required this.authorId,
    this.authorName,
    this.authorAvatar,
    required this.body,
    this.voteScore = 0,
    this.viewerVote = 0,
    required this.createdAt,
    required this.updatedAt,
  });

  factory AnswerComment.fromJson(Map<String, dynamic> json) {
    return AnswerComment(
      id: (json['id'] ?? '').toString(),
      answerId: (json['answer_id'] ?? '').toString(),
      authorId: (json['author_id'] ?? '').toString(),
      authorName: json['author_name']?.toString(),
      authorAvatar: json['author_avatar']?.toString(),
      body: (json['body'] ?? '').toString(),
      voteScore: _toInt(json['vote_score']),
      viewerVote: _toInt(json['viewer_vote']),
      createdAt: _parseDate(json['created_at']),
      updatedAt: _parseDate(json['updated_at'] ?? json['created_at']),
    );
  }
}

/// Q&A profile for a user (separate from main social profile).
class QaProfile {
  final String userId;
  final String displayName;
  final String? avatarUrl;
  final String bio;
  final List<String> expertiseAreas;
  final int reputationScore;
  final int questionCount;
  final int answerCount;
  final int bestAnswerCount;
  final bool isVerified;

  const QaProfile({
    required this.userId,
    required this.displayName,
    this.avatarUrl,
    this.bio = '',
    this.expertiseAreas = const [],
    this.reputationScore = 0,
    this.questionCount = 0,
    this.answerCount = 0,
    this.bestAnswerCount = 0,
    this.isVerified = false,
  });

  factory QaProfile.fromJson(Map<String, dynamic> json) {
    return QaProfile(
      userId: (json['user_id'] ?? json['id'] ?? '').toString(),
      displayName: (json['display_name'] ?? '').toString(),
      avatarUrl: json['avatar_url']?.toString(),
      bio: json['bio']?.toString() ?? '',
      expertiseAreas: (json['expertise_areas'] as List?)
              ?.map((e) => e.toString())
              .toList() ??
          const [],
      reputationScore: _toInt(json['reputation_score']),
      questionCount: _toInt(json['question_count']),
      answerCount: _toInt(json['answer_count']),
      bestAnswerCount: _toInt(json['best_answer_count']),
      isVerified: json['is_verified'] == true,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'display_name': displayName,
      'bio': bio,
      'expertise_areas': expertiseAreas,
    };
  }
}

class ReputationEvent {
  final String id;
  final String eventType;
  final int points;
  final DateTime occurredAt;

  const ReputationEvent({
    required this.id,
    required this.eventType,
    required this.points,
    required this.occurredAt,
  });

  factory ReputationEvent.fromJson(Map<String, dynamic> json) {
    return ReputationEvent(
      id: (json['id'] ?? '').toString(),
      eventType: (json['event_type'] ?? '').toString(),
      points: _toInt(json['points']),
      occurredAt: _parseDate(json['occurred_at'] ?? json['created_at']),
    );
  }
}

class ContributorBadge {
  final String id;
  final String badgeType;
  final String? title;
  final String? description;
  final DateTime awardedAt;

  const ContributorBadge({
    required this.id,
    required this.badgeType,
    this.title,
    this.description,
    required this.awardedAt,
  });

  factory ContributorBadge.fromJson(Map<String, dynamic> json) {
    return ContributorBadge(
      id: (json['id'] ?? '').toString(),
      badgeType: (json['badge_type'] ?? '').toString(),
      title: json['title']?.toString(),
      description: json['description']?.toString(),
      awardedAt: _parseDate(json['awarded_at'] ?? json['created_at']),
    );
  }
}

class LeaderboardEntry {
  final String userId;
  final String displayName;
  final String? avatarUrl;
  final int reputationScore;
  final int rank;

  const LeaderboardEntry({
    required this.userId,
    required this.displayName,
    this.avatarUrl,
    required this.reputationScore,
    required this.rank,
  });

  factory LeaderboardEntry.fromJson(Map<String, dynamic> json) {
    return LeaderboardEntry(
      userId: (json['user_id'] ?? json['id'] ?? '').toString(),
      displayName: (json['display_name'] ?? '').toString(),
      avatarUrl: json['avatar_url']?.toString(),
      reputationScore: _toInt(json['reputation_score']),
      rank: _toInt(json['rank']),
    );
  }
}

class QuestionDraft {
  final String? id;
  final String? communityId;
  final String title;
  final String body;
  final List<String> tags;
  final List<String> topicIds;
  final bool isAnonymous;
  final DateTime updatedAt;

  const QuestionDraft({
    this.id,
    this.communityId,
    this.title = '',
    this.body = '',
    this.tags = const [],
    this.topicIds = const [],
    this.isAnonymous = false,
    required this.updatedAt,
  });

  factory QuestionDraft.fromJson(Map<String, dynamic> json) {
    return QuestionDraft(
      id: json['id']?.toString(),
      communityId: json['community_id']?.toString(),
      title: json['title']?.toString() ?? '',
      body: json['body']?.toString() ?? '',
      tags: (json['tags'] as List?)?.map((e) => e.toString()).toList() ??
          const [],
      topicIds:
          (json['topic_ids'] as List?)?.map((e) => e.toString()).toList() ??
              const [],
      isAnonymous: json['is_anonymous'] == true,
      updatedAt: _parseDate(json['updated_at'] ?? json['created_at']),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      if (id != null) 'id': id,
      if (communityId != null) 'community_id': communityId,
      'title': title,
      'body': body,
      'tags': tags,
      'topic_ids': topicIds,
      'is_anonymous': isAnonymous,
    };
  }
}

class AnswerDraft {
  final String? id;
  final String questionId;
  final String body;
  final bool isAnonymous;
  final DateTime updatedAt;

  const AnswerDraft({
    this.id,
    required this.questionId,
    this.body = '',
    this.isAnonymous = false,
    required this.updatedAt,
  });

  factory AnswerDraft.fromJson(Map<String, dynamic> json) {
    return AnswerDraft(
      id: json['id']?.toString(),
      questionId: (json['question_id'] ?? '').toString(),
      body: json['body']?.toString() ?? '',
      isAnonymous: json['is_anonymous'] == true,
      updatedAt: _parseDate(json['updated_at'] ?? json['created_at']),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      if (id != null) 'id': id,
      'question_id': questionId,
      'body': body,
      'is_anonymous': isAnonymous,
    };
  }
}

/// Lightweight summary returned by community Q&A endpoints.
class CommunityQuestionsResponse {
  final List<Question> questions;
  final List<QaTopicOption> availableTopics;
  final CommunityQaSettings settings;

  const CommunityQuestionsResponse({
    required this.questions,
    required this.availableTopics,
    required this.settings,
  });
}
