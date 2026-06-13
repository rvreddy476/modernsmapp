import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

enum PostType { text, photo, video, article, poll }

/// Maps PostType to backend content_type value
String postTypeToContentType(PostType type) {
  switch (type) {
    case PostType.poll:
      return 'poll';
    default:
      return 'post';
  }
}

const _videoExtensions = {
  '.mp4', '.mov', '.m4v', '.webm', '.avi', '.mkv', '.3gp', '.hevc',
};

/// Detects video files by extension. Covers iOS (.mov), Android (.mp4),
/// and the common alternatives users hand the picker.
bool _isVideoFile(String path) {
  final lower = path.toLowerCase();
  for (final ext in _videoExtensions) {
    if (lower.endsWith(ext)) return true;
  }
  return false;
}

/// Post audience. The enum name is sent verbatim as the `visibility`
/// value to post-service (e.g. `trusted` → close-friends-only).
enum PostVisibility { public, followers, trusted, private }

class CreationState {
  final PostType type;
  final String text;
  final List<XFile> files;
  final PostVisibility visibility;
  final List<String> tags;
  final String? location;
  final String? mood;
  final List<String> pollOptions;
  final bool allowsMultipleVotes;
  final bool isSubmitting;
  final bool isGeneratingAi;
  final double uploadProgress;
  final String? error;
  /// Optional solid background for text-only posts (matches web composer).
  /// Sent as `rich_text.background` + a derived `text_color`.
  final String? backgroundColor;
  final bool backgroundIsDark;
  /// Per-option errors keyed by option index. Cleared on edit.
  final Map<int, String> pollOptionErrors;
  final String? pollQuestionError;

  const CreationState({
    this.type = PostType.text,
    this.text = '',
    this.files = const [],
    this.visibility = PostVisibility.public,
    this.tags = const [],
    this.location,
    this.mood,
    this.pollOptions = const ['', ''],
    this.allowsMultipleVotes = false,
    this.isSubmitting = false,
    this.isGeneratingAi = false,
    this.uploadProgress = 0,
    this.error,
    this.backgroundColor,
    this.backgroundIsDark = false,
    this.pollOptionErrors = const {},
    this.pollQuestionError,
  });

  CreationState copyWith({
    PostType? type,
    String? text,
    List<XFile>? files,
    PostVisibility? visibility,
    List<String>? tags,
    String? location,
    String? mood,
    List<String>? pollOptions,
    bool? allowsMultipleVotes,
    bool? isSubmitting,
    bool? isGeneratingAi,
    double? uploadProgress,
    String? error,
    String? backgroundColor,
    bool clearBackground = false,
    bool? backgroundIsDark,
    Map<int, String>? pollOptionErrors,
    String? pollQuestionError,
    bool clearPollQuestionError = false,
  }) {
    return CreationState(
      type: type ?? this.type,
      text: text ?? this.text,
      files: files ?? this.files,
      visibility: visibility ?? this.visibility,
      tags: tags ?? this.tags,
      location: location ?? this.location,
      mood: mood ?? this.mood,
      pollOptions: pollOptions ?? this.pollOptions,
      allowsMultipleVotes: allowsMultipleVotes ?? this.allowsMultipleVotes,
      isSubmitting: isSubmitting ?? this.isSubmitting,
      isGeneratingAi: isGeneratingAi ?? this.isGeneratingAi,
      uploadProgress: uploadProgress ?? this.uploadProgress,
      error: error,
      backgroundColor: clearBackground ? null : (backgroundColor ?? this.backgroundColor),
      backgroundIsDark: clearBackground ? false : (backgroundIsDark ?? this.backgroundIsDark),
      pollOptionErrors: pollOptionErrors ?? this.pollOptionErrors,
      pollQuestionError: clearPollQuestionError ? null : (pollQuestionError ?? this.pollQuestionError),
    );
  }
}

class CreationNotifier extends StateNotifier<CreationState> {
  final PostRepository _postRepo;
  final ApiClient _apiClient;

  CreationNotifier(this._postRepo, this._apiClient)
    : super(const CreationState());

  void setType(PostType type) => state = state.copyWith(type: type);
  void setText(String text) => state = state.copyWith(text: text);
  void setVisibility(PostVisibility visibility) =>
      state = state.copyWith(visibility: visibility);
  void setMood(String? mood) => state = state.copyWith(mood: mood);
  void setLocation(String? location) =>
      state = state.copyWith(location: location);

  void addFiles(List<XFile> newFiles) =>
      state = state.copyWith(files: [...state.files, ...newFiles]);
  void removeFile(int index) {
    final newFiles = List<XFile>.from(state.files)..removeAt(index);
    state = state.copyWith(files: newFiles);
  }

  void updatePollOption(int index, String value) {
    final options = List<String>.from(state.pollOptions);
    options[index] = value;
    state = state.copyWith(pollOptions: options);
  }

  void addPollOption() {
    if (state.pollOptions.length < 5) {
      state = state.copyWith(pollOptions: [...state.pollOptions, '']);
    }
  }

  void removePollOption(int index) {
    if (state.pollOptions.length <= 2) return;
    final options = List<String>.from(state.pollOptions)..removeAt(index);
    state = state.copyWith(pollOptions: options);
  }

  void setAllowsMultipleVotes(bool value) =>
      state = state.copyWith(allowsMultipleVotes: value);

  /// Background colour for text-only posts. `null` = no background.
  /// `isDark` flips the rendered text colour to white for contrast.
  void setBackground(String? hex, {bool isDark = false}) {
    if (hex == null) {
      state = state.copyWith(clearBackground: true);
    } else {
      state = state.copyWith(backgroundColor: hex, backgroundIsDark: isDark);
    }
  }

  /// Normalize a raw hashtag string:
  ///   "  #Design ! " → "design".
  static String _normalizeTag(String raw) {
    final stripped = raw.trim().replaceFirst(RegExp(r'^#+'), '');
    return stripped.replaceAll(RegExp(r'[^\p{L}\p{N}_]', unicode: true), '').toLowerCase();
  }

  /// Accept user input with or without #, split on whitespace/commas,
  /// dedupe, lowercase. Caps at 30 tags total.
  void addTags(String input) {
    final parts = input
        .split(RegExp(r'[\s,]+'))
        .map(_normalizeTag)
        .where((t) => t.isNotEmpty)
        .toList();
    if (parts.isEmpty) return;
    final seen = state.tags.toSet();
    final next = List<String>.from(state.tags);
    for (final t in parts) {
      if (!seen.contains(t)) {
        next.add(t);
        seen.add(t);
        if (next.length >= 30) break;
      }
    }
    state = state.copyWith(tags: next);
  }

  void removeTag(String tag) {
    state = state.copyWith(tags: state.tags.where((t) => t != tag).toList());
  }

  /// Validates a poll before allowing submit. Returns true if valid,
  /// false otherwise (with errors set on state).
  /// - text.trim() is the question, must be non-empty.
  /// - at least 2 non-empty, non-duplicate options required.
  bool _validatePoll() {
    if (state.type != PostType.poll) return true;
    final optionErrors = <int, String>{};
    final trimmed = state.pollOptions.map((o) => o.trim()).toList();
    final firstIndexFor = <String, int>{};
    for (var i = 0; i < trimmed.length; i++) {
      final t = trimmed[i];
      if (t.isEmpty) {
        optionErrors[i] = 'Please enter this option.';
      } else {
        final lower = t.toLowerCase();
        if (firstIndexFor.containsKey(lower)) {
          optionErrors[i] = 'Duplicate option — please change or remove.';
          optionErrors[firstIndexFor[lower]!] =
              'Duplicate option — please change or remove.';
        } else {
          firstIndexFor[lower] = i;
        }
      }
    }
    final validCount = trimmed.where((t) => t.isNotEmpty && optionErrors[trimmed.indexOf(t)] == null).length;
    final hasQuestion = state.text.trim().isNotEmpty;

    final missingQuestion = !hasQuestion;
    final missingOptions = validCount < 2;

    if (!missingQuestion && !missingOptions && optionErrors.isEmpty) {
      state = state.copyWith(
        pollOptionErrors: const {},
        clearPollQuestionError: true,
        error: null,
      );
      return true;
    }

    String banner;
    if (missingQuestion && missingOptions) {
      banner = 'Please provide the poll question and at least two poll options.';
    } else if (missingQuestion) {
      banner = 'Please enter poll question.';
    } else if (missingOptions) {
      banner = 'Please provide at least two poll options.';
    } else {
      banner = 'Please fix the highlighted poll fields.';
    }
    state = state.copyWith(
      pollOptionErrors: optionErrors,
      pollQuestionError: missingQuestion ? 'Please enter poll question.' : null,
      clearPollQuestionError: !missingQuestion,
      error: banner,
    );
    return false;
  }

  void clearPollErrors() {
    if (state.pollOptionErrors.isEmpty && state.pollQuestionError == null) return;
    state = state.copyWith(
      pollOptionErrors: const {},
      clearPollQuestionError: true,
      error: null,
    );
  }

  /// AI content enhancement (The "Sparkle" feature)
  Future<void> enhanceWithAi() async {
    if (state.text.isEmpty || state.isGeneratingAi) return;

    state = state.copyWith(isGeneratingAi: true, error: null);
    try {
      final result = await _postRepo.generateAiSuggestions(
        text: state.text,
        context: 'Improve this social media post',
      );

      final enhancedText = result['enhanced_text'] as String?;
      final suggestedTags = (result['suggested_tags'] as List?)?.cast<String>();

      if (enhancedText != null) {
        state = state.copyWith(
          text: enhancedText,
          tags: {...state.tags, ...?suggestedTags}.toList(),
          isGeneratingAi: false,
        );
      }
    } catch (e) {
      state = state.copyWith(
        isGeneratingAi: false,
        error: 'AI enhancement failed',
      );
    }
  }

  /// Submit the post with background media uploads
  Future<bool> submit() async {
    if (state.isSubmitting) return false;

    // Frontend validation gate — never let an invalid poll reach the backend.
    if (!_validatePoll()) return false;

    state = state.copyWith(isSubmitting: true, uploadProgress: 0, error: null);

    try {
      final mediaIds = <String>[];
      final files = state.files;
      for (var i = 0; i < files.length; i++) {
        final file = files[i];
        final id = await _apiClient.uploadMedia(
          file,
          type: _isVideoFile(file.path) ? 'video' : 'image',
          onProgress: (sent, total) {
            if (total <= 0 || files.isEmpty) return;
            final fileProgress = sent / total;
            final overall = (i + fileProgress) / files.length;
            state = state.copyWith(
              uploadProgress: overall.clamp(0, 1).toDouble(),
            );
          },
        );
        if (id.isNotEmpty) mediaIds.add(id);
      }
      if (files.isEmpty) {
        state = state.copyWith(uploadProgress: 1);
      }

      Map<String, dynamic>? pollPayload;
      if (state.type == PostType.poll) {
        pollPayload = {
          'question': state.text.trim(),
          'options': state.pollOptions
              .map((o) => o.trim())
              .where((opt) => opt.isNotEmpty)
              .toList(),
          'allows_multiple': state.allowsMultipleVotes,
          'duration_hours': 24,
        };
      }

      // Backend extracts hashtags from the post body via regex.
      // Append chip-entered tags to the wire text so they land in
      // posts.hashtags[]. The user's composing surface stays clean.
      final tagSuffix = state.tags.isEmpty
          ? ''
          : ' ${state.tags.map((t) => '#$t').join(' ')}';
      final wireText = (state.text.trim() + tagSuffix).trim();

      Map<String, dynamic>? richText;
      if (state.backgroundColor != null && state.files.isEmpty && state.type != PostType.poll) {
        richText = {
          'background': state.backgroundColor,
          'text_color': state.backgroundIsDark ? '#ffffff' : '#111111',
        };
      }

      await _postRepo.createPost(
        text: wireText,
        contentType: postTypeToContentType(state.type),
        visibility: state.visibility.name,
        mediaIds: mediaIds.isEmpty ? null : mediaIds,
        tags: state.tags.isEmpty ? null : state.tags,
        feeling: state.mood,
        locationName: state.location,
        poll: pollPayload,
        richText: richText,
      );

      reset();
      return true;
    } catch (e) {
      state = state.copyWith(
        isSubmitting: false,
        uploadProgress: 0,
        error: e.toString(),
      );
      return false;
    }
  }

  void reset() => state = const CreationState();
}

final creationProvider =
    StateNotifierProvider.autoDispose<CreationNotifier, CreationState>((ref) {
      return CreationNotifier(
        ref.watch(postRepositoryProvider),
        ref.watch(apiClientProvider),
      );
    });
