class AiJob {
  final String id;
  final String jobType;
  final String status; // queued | processing | completed | failed
  final String refType;
  final String refId;
  final Map<String, dynamic>? resultJson;
  final String? errorMessage;
  final DateTime createdAt;

  const AiJob({
    required this.id,
    required this.jobType,
    required this.status,
    required this.refType,
    required this.refId,
    this.resultJson,
    this.errorMessage,
    required this.createdAt,
  });

  factory AiJob.fromJson(Map<String, dynamic> json) => AiJob(
        id: json['id'] as String,
        jobType: json['job_type'] as String,
        status: json['status'] as String,
        refType: json['ref_type'] as String,
        refId: json['ref_id'] as String,
        resultJson: json['result_json'] as Map<String, dynamic>?,
        errorMessage: json['error_message'] as String?,
        createdAt: DateTime.parse(json['created_at'] as String),
      );
}

class CaptionSuggestions {
  final List<String> captions;

  const CaptionSuggestions({required this.captions});

  factory CaptionSuggestions.fromJson(Map<String, dynamic> json) =>
      CaptionSuggestions(
        captions: (json['captions'] as List<dynamic>?)?.cast<String>() ?? [],
      );
}

class HashtagSuggestions {
  final List<String> hashtags;

  const HashtagSuggestions({required this.hashtags});

  factory HashtagSuggestions.fromJson(Map<String, dynamic> json) =>
      HashtagSuggestions(
        hashtags: (json['hashtags'] as List<dynamic>?)?.cast<String>() ?? [],
      );
}

class SmartReplies {
  final List<String> replies;

  const SmartReplies({required this.replies});

  factory SmartReplies.fromJson(Map<String, dynamic> json) => SmartReplies(
        replies: (json['replies'] as List<dynamic>?)?.cast<String>() ?? [],
      );
}
