class QaTopic {
  final String id;
  final String name;
  final String slug;
  final String description;
  final String iconUrl;
  final int questionCount;
  final int followerCount;
  final bool isFeatured;
  final bool? isFollowing;

  const QaTopic({
    required this.id,
    required this.name,
    required this.slug,
    required this.description,
    this.iconUrl = '',
    this.questionCount = 0,
    this.followerCount = 0,
    this.isFeatured = false,
    this.isFollowing,
  });

  factory QaTopic.fromJson(Map<String, dynamic> json) {
    return QaTopic(
      id: (json['id'] ?? '').toString(),
      name: (json['name'] ?? '').toString(),
      slug: (json['slug'] ?? '').toString(),
      description: (json['description'] ?? '').toString(),
      iconUrl: (json['icon_url'] ?? '').toString(),
      questionCount: _asInt(json['question_count']),
      followerCount: _asInt(json['follower_count']),
      isFeatured: json['is_featured'] as bool? ?? false,
      isFollowing: json['is_following'] as bool?,
    );
  }
}

class QaCommunityScope {
  final String id;
  final String name;
  final String visibility;
  final String communityType;

  const QaCommunityScope({
    required this.id,
    required this.name,
    required this.visibility,
    required this.communityType,
  });

  factory QaCommunityScope.fromJson(Map<String, dynamic> json) {
    return QaCommunityScope(
      id: (json['id'] ?? '').toString(),
      name: (json['name'] ?? '').toString(),
      visibility: (json['visibility'] ?? 'public').toString(),
      communityType: (json['community_type'] ?? 'public').toString(),
    );
  }
}

class QaQuestionSummary {
  final String id;
  final String authorId;
  final String title;
  final String slug;
  final String status;
  final int voteScore;
  final int answerCount;
  final int viewCount;
  final bool isAnswered;
  final DateTime createdAt;
  final String excerpt;
  final bool isPinned;
  final QaCommunityScope? community;

  const QaQuestionSummary({
    required this.id,
    required this.authorId,
    required this.title,
    required this.slug,
    required this.status,
    this.voteScore = 0,
    this.answerCount = 0,
    this.viewCount = 0,
    this.isAnswered = false,
    required this.createdAt,
    this.excerpt = '',
    this.isPinned = false,
    this.community,
  });

  factory QaQuestionSummary.fromJson(Map<String, dynamic> json) {
    final communityJson = json['community'];
    return QaQuestionSummary(
      id: (json['id'] ?? '').toString(),
      authorId: (json['author_id'] ?? '').toString(),
      title: (json['title'] ?? '').toString(),
      slug: (json['slug'] ?? '').toString(),
      status: (json['status'] ?? 'open').toString(),
      voteScore: _asInt(json['vote_score']),
      answerCount: _asInt(json['answer_count']),
      viewCount: _asInt(json['view_count']),
      isAnswered: json['is_answered'] as bool? ?? false,
      createdAt: _asDateTime(json['created_at']),
      excerpt: (json['excerpt'] ?? '').toString(),
      isPinned: json['is_pinned'] as bool? ?? false,
      community: communityJson is Map<String, dynamic>
          ? QaCommunityScope.fromJson(communityJson)
          : null,
    );
  }
}

class QaQuestion {
  final String id;
  final String authorId;
  final String title;
  final String body;
  final String bodyHtml;
  final String slug;
  final String status;
  final String visibility;
  final String language;
  final int voteScore;
  final int upvoteCount;
  final int downvoteCount;
  final int answerCount;
  final int viewCount;
  final int followCount;
  final bool isAnswered;
  final DateTime createdAt;
  final List<QaTopic> topics;
  final List<String> tags;
  final QaCommunityScope? community;
  final bool isPinned;

  const QaQuestion({
    required this.id,
    required this.authorId,
    required this.title,
    required this.body,
    required this.bodyHtml,
    required this.slug,
    required this.status,
    required this.visibility,
    required this.language,
    this.voteScore = 0,
    this.upvoteCount = 0,
    this.downvoteCount = 0,
    this.answerCount = 0,
    this.viewCount = 0,
    this.followCount = 0,
    this.isAnswered = false,
    required this.createdAt,
    this.topics = const [],
    this.tags = const [],
    this.community,
    this.isPinned = false,
  });

  factory QaQuestion.fromJson(Map<String, dynamic> json) {
    final topicsJson = json['topics'] as List<dynamic>? ?? const [];
    final tagsJson = json['tags'] as List<dynamic>? ?? const [];
    final communityJson = json['community'];
    return QaQuestion(
      id: (json['id'] ?? '').toString(),
      authorId: (json['author_id'] ?? '').toString(),
      title: (json['title'] ?? '').toString(),
      body: (json['body'] ?? '').toString(),
      bodyHtml: (json['body_html'] ?? '').toString(),
      slug: (json['slug'] ?? '').toString(),
      status: (json['status'] ?? 'open').toString(),
      visibility: (json['visibility'] ?? 'public').toString(),
      language: (json['language'] ?? 'en').toString(),
      voteScore: _asInt(json['vote_score']),
      upvoteCount: _asInt(json['upvote_count']),
      downvoteCount: _asInt(json['downvote_count']),
      answerCount: _asInt(json['answer_count']),
      viewCount: _asInt(json['view_count']),
      followCount: _asInt(json['follow_count']),
      isAnswered: json['is_answered'] as bool? ?? false,
      createdAt: _asDateTime(json['created_at']),
      topics: topicsJson
          .whereType<Map>()
          .map((item) => QaTopic.fromJson(Map<String, dynamic>.from(item)))
          .toList(),
      tags: tagsJson.map((item) => item.toString()).toList(),
      community: communityJson is Map<String, dynamic>
          ? QaCommunityScope.fromJson(communityJson)
          : null,
      isPinned: json['is_pinned'] as bool? ?? false,
    );
  }
}

class QaAnswer {
  final String id;
  final String questionId;
  final String authorId;
  final String body;
  final String bodyHtml;
  final int voteScore;
  final int upvoteCount;
  final int downvoteCount;
  final bool isBest;
  final bool isAccepted;
  final int commentCount;
  final int referenceCount;
  final DateTime createdAt;
  final DateTime updatedAt;

  const QaAnswer({
    required this.id,
    required this.questionId,
    required this.authorId,
    required this.body,
    required this.bodyHtml,
    this.voteScore = 0,
    this.upvoteCount = 0,
    this.downvoteCount = 0,
    this.isBest = false,
    this.isAccepted = false,
    this.commentCount = 0,
    this.referenceCount = 0,
    required this.createdAt,
    required this.updatedAt,
  });

  factory QaAnswer.fromJson(Map<String, dynamic> json) {
    return QaAnswer(
      id: (json['id'] ?? '').toString(),
      questionId: (json['question_id'] ?? '').toString(),
      authorId: (json['author_id'] ?? '').toString(),
      body: (json['body'] ?? '').toString(),
      bodyHtml: (json['body_html'] ?? '').toString(),
      voteScore: _asInt(json['vote_score']),
      upvoteCount: _asInt(json['upvote_count']),
      downvoteCount: _asInt(json['downvote_count']),
      isBest: json['is_best'] as bool? ?? false,
      isAccepted: json['is_accepted'] as bool? ?? false,
      commentCount: _asInt(json['comment_count']),
      referenceCount: _asInt(json['reference_count']),
      createdAt: _asDateTime(json['created_at']),
      updatedAt: _asDateTime(json['updated_at']),
    );
  }
}

class QaTopicOption {
  final String id;
  final String name;
  final String slug;
  final String description;
  final int questionCount;

  const QaTopicOption({
    required this.id,
    required this.name,
    required this.slug,
    required this.description,
    this.questionCount = 0,
  });

  factory QaTopicOption.fromJson(Map<String, dynamic> json) {
    return QaTopicOption(
      id: (json['id'] ?? '').toString(),
      name: (json['name'] ?? '').toString(),
      slug: (json['slug'] ?? '').toString(),
      description: (json['description'] ?? '').toString(),
      questionCount: _asInt(json['question_count']),
    );
  }
}

class CommunityQaSettings {
  final bool qaEnabled;
  final String askPermission;
  final String answerPermission;
  final bool autoSuggestTopics;
  final bool requireApproval;
  final String welcomeMessage;
  final int totalQuestions;
  final int totalAnswers;
  final int uniqueContributors;

  const CommunityQaSettings({
    this.qaEnabled = true,
    this.askPermission = 'members',
    this.answerPermission = 'everyone',
    this.autoSuggestTopics = true,
    this.requireApproval = false,
    this.welcomeMessage = '',
    this.totalQuestions = 0,
    this.totalAnswers = 0,
    this.uniqueContributors = 0,
  });

  factory CommunityQaSettings.fromJson(Map<String, dynamic> json) {
    return CommunityQaSettings(
      qaEnabled: json['qa_enabled'] as bool? ?? true,
      askPermission: (json['ask_permission'] ?? 'members').toString(),
      answerPermission: (json['answer_permission'] ?? 'everyone').toString(),
      autoSuggestTopics: json['auto_suggest_topics'] as bool? ?? true,
      requireApproval: json['require_approval'] as bool? ?? false,
      welcomeMessage: (json['welcome_message'] ?? '').toString(),
      totalQuestions: _asInt(json['total_questions']),
      totalAnswers: _asInt(json['total_answers']),
      uniqueContributors: _asInt(json['unique_contributors']),
    );
  }
}

class CommunityQuestionsResult {
  final List<QaQuestionSummary> questions;
  final List<QaTopicOption> availableTopics;
  final CommunityQaSettings settings;

  const CommunityQuestionsResult({
    this.questions = const [],
    this.availableTopics = const [],
    this.settings = const CommunityQaSettings(),
  });

  factory CommunityQuestionsResult.fromJson(Map<String, dynamic> json) {
    final questionsJson = json['questions'] as List<dynamic>? ?? const [];
    final topicsJson = json['available_topics'] as List<dynamic>? ?? const [];
    final settingsJson = json['community_qa_settings'];
    return CommunityQuestionsResult(
      questions: questionsJson
          .whereType<Map>()
          .map(
            (item) =>
                QaQuestionSummary.fromJson(Map<String, dynamic>.from(item)),
          )
          .toList(),
      availableTopics: topicsJson
          .whereType<Map>()
          .map(
            (item) => QaTopicOption.fromJson(Map<String, dynamic>.from(item)),
          )
          .toList(),
      settings: settingsJson is Map<String, dynamic>
          ? CommunityQaSettings.fromJson(settingsJson)
          : const CommunityQaSettings(),
    );
  }
}

int _asInt(dynamic value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  return int.tryParse(value?.toString() ?? '') ?? 0;
}

DateTime _asDateTime(dynamic value) {
  if (value is String && value.isNotEmpty) {
    return DateTime.tryParse(value)?.toLocal() ?? DateTime.now();
  }
  return DateTime.now();
}
