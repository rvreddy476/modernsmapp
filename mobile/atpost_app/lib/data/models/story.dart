class Story {
  final String id;
  final String authorId;
  final String authorName;
  final String? avatarMediaId;
  final List<StoryItem> items;
  final DateTime createdAt;

  const Story({
    required this.id,
    required this.authorId,
    required this.authorName,
    this.avatarMediaId,
    required this.items,
    required this.createdAt,
  });

  factory Story.fromJson(Map<String, dynamic> json) {
    final rawItems = json['items'] as List<dynamic>?;
    final items = rawItems == null
        ? [
            StoryItem.fromJson({
              'id': json['id'],
              'media_url': json['media_url'],
              'media_id': json['media_url'],
              'media_type': json['media_type'],
              'text': json['caption'],
              'expires_at': json['expires_at'],
              'interactives': json['interactives'],
            }),
          ]
        : rawItems
              .map((e) => StoryItem.fromJson(e as Map<String, dynamic>))
              .toList();

    return Story(
      id: json['id'] as String? ?? '',
      authorId: json['author_id'] as String? ?? '',
      authorName:
          (json['author_name'] ?? json['display_name'])?.toString() ?? '',
      avatarMediaId: json['avatar_media_id'] as String?,
      items: items,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

class StoryItem {
  final String id;
  final String mediaId;
  final String mediaType; // 'image' | 'video'
  final String? text;
  final DateTime expiresAt;
  final List<StoryInteractive> interactives;

  const StoryItem({
    required this.id,
    required this.mediaId,
    required this.mediaType,
    this.text,
    required this.expiresAt,
    this.interactives = const [],
  });

  factory StoryItem.fromJson(Map<String, dynamic> json) {
    final rawInteractives = json['interactives'] as List<dynamic>?;
    final interactives = (rawInteractives ?? const <dynamic>[])
        .whereType<Map<String, dynamic>>()
        .map(StoryInteractive.fromJson)
        .toList();

    return StoryItem(
      id: json['id'] as String? ?? '',
      mediaId: (json['media_url'] ?? json['media_id']) as String? ?? '',
      mediaType: json['media_type'] as String? ?? 'image',
      text: json['text'] as String?,
      expiresAt: json['expires_at'] != null
          ? DateTime.parse(json['expires_at'] as String)
          : DateTime.now().add(const Duration(hours: 24)),
      interactives: interactives,
    );
  }
}

/// Mirrors the `story_interactive` row + its draft form on the client.
///
/// Wire shape (per `010_postbook_features.sql`):
///   { id, story_id, type, question, options, correct_idx, end_time,
///     position, created_at }
///
/// `options` for poll/quiz is a JSON array of `{ id, text }`.
/// `options` for slider is a JSON object `{ emoji }`.
/// `end_time` carries the countdown target.
class StoryInteractive {
  final String id;
  final String type; // poll | quiz | countdown | question | slider
  final String question;
  final List<StoryInteractiveOption> options;
  final int? correctIdx;
  final DateTime? endTime;
  final String? emoji;
  final Map<String, dynamic>? position;

  const StoryInteractive({
    required this.id,
    required this.type,
    required this.question,
    this.options = const [],
    this.correctIdx,
    this.endTime,
    this.emoji,
    this.position,
  });

  factory StoryInteractive.fromJson(Map<String, dynamic> json) {
    final rawOptions = json['options'];
    final options = <StoryInteractiveOption>[];
    String? emoji;

    if (rawOptions is List) {
      for (final raw in rawOptions) {
        if (raw is Map) {
          options.add(StoryInteractiveOption.fromJson(
            Map<String, dynamic>.from(raw),
          ));
        }
      }
    } else if (rawOptions is Map) {
      final map = Map<String, dynamic>.from(rawOptions);
      emoji = map['emoji'] as String?;
    }

    return StoryInteractive(
      id: json['id'] as String? ?? '',
      type: json['type'] as String? ?? 'poll',
      question: json['question'] as String? ?? '',
      options: options,
      correctIdx: (json['correct_idx'] as num?)?.toInt(),
      endTime: json['end_time'] != null
          ? DateTime.tryParse(json['end_time'] as String)
          : null,
      emoji: emoji,
      position: json['position'] is Map
          ? Map<String, dynamic>.from(json['position'] as Map)
          : null,
    );
  }

  Map<String, dynamic> toCreateJson() {
    final out = <String, dynamic>{
      'type': type,
      'question': question,
    };
    if (type == 'slider') {
      out['options'] = {'emoji': emoji ?? '😍'};
    } else if (options.isNotEmpty) {
      out['options'] = options.map((o) => o.toJson()).toList();
    }
    if (correctIdx != null) out['correct_idx'] = correctIdx;
    if (endTime != null) out['end_time'] = endTime!.toUtc().toIso8601String();
    if (position != null) out['position'] = position;
    return out;
  }
}

class StoryInteractiveOption {
  final String id;
  final String text;

  const StoryInteractiveOption({required this.id, required this.text});

  factory StoryInteractiveOption.fromJson(Map<String, dynamic> json) {
    return StoryInteractiveOption(
      id: json['id'] as String? ?? '',
      text: json['text'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {'id': id, 'text': text};
}

/// Aggregated results returned to the creator viewing their own story.
///
/// Wire shape (proposed — backend currently has no handler):
///   {
///     interactive_id, type, total_responses,
///     poll_or_quiz: { votes: { option_id: count } },
///     question:     { replies: [{ user_id, display_name, text, created_at }] },
///     slider:       { average, histogram: [count, ...], count },
///     countdown:    { reminders_set: count }
///   }
class StoryInteractiveResults {
  final String interactiveId;
  final String type;
  final int totalResponses;
  final Map<String, int> votes;
  final List<StoryInteractiveReply> replies;
  final double? sliderAverage;
  final List<int> sliderHistogram;
  final int remindersSet;

  const StoryInteractiveResults({
    required this.interactiveId,
    required this.type,
    required this.totalResponses,
    this.votes = const {},
    this.replies = const [],
    this.sliderAverage,
    this.sliderHistogram = const [],
    this.remindersSet = 0,
  });

  factory StoryInteractiveResults.fromJson(Map<String, dynamic> json) {
    final votes = <String, int>{};
    final rawVotes = json['votes'];
    if (rawVotes is Map) {
      for (final entry in rawVotes.entries) {
        final v = entry.value;
        if (v is num) votes[entry.key.toString()] = v.toInt();
      }
    }

    final replies = <StoryInteractiveReply>[];
    final rawReplies = json['replies'];
    if (rawReplies is List) {
      for (final r in rawReplies) {
        if (r is Map) {
          replies.add(StoryInteractiveReply.fromJson(
            Map<String, dynamic>.from(r),
          ));
        }
      }
    }

    final histogram = <int>[];
    final rawHistogram = json['histogram'];
    if (rawHistogram is List) {
      for (final h in rawHistogram) {
        if (h is num) histogram.add(h.toInt());
      }
    }

    return StoryInteractiveResults(
      interactiveId: json['interactive_id'] as String? ?? '',
      type: json['type'] as String? ?? '',
      totalResponses: (json['total_responses'] as num?)?.toInt() ?? 0,
      votes: votes,
      replies: replies,
      sliderAverage: (json['average'] as num?)?.toDouble(),
      sliderHistogram: histogram,
      remindersSet: (json['reminders_set'] as num?)?.toInt() ?? 0,
    );
  }
}

class StoryInteractiveReply {
  final String userId;
  final String displayName;
  final String text;
  final DateTime createdAt;

  const StoryInteractiveReply({
    required this.userId,
    required this.displayName,
    required this.text,
    required this.createdAt,
  });

  factory StoryInteractiveReply.fromJson(Map<String, dynamic> json) {
    return StoryInteractiveReply(
      userId: json['user_id'] as String? ?? '',
      displayName: (json['display_name'] ?? json['author_name'])?.toString() ??
          '',
      text: json['text'] as String? ?? '',
      createdAt: json['created_at'] != null
          ? DateTime.tryParse(json['created_at'] as String) ?? DateTime.now()
          : DateTime.now(),
    );
  }
}
