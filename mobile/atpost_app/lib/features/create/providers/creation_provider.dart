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

enum PostVisibility { public, followers, private }

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

    state = state.copyWith(isSubmitting: true, uploadProgress: 0, error: null);

    try {
      final mediaIds = <String>[];
      final files = state.files;
      for (var i = 0; i < files.length; i++) {
        final file = files[i];
        final id = await _apiClient.uploadMedia(
          file,
          type: file.path.contains('.mp4') ? 'video' : 'image',
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
          'options': state.pollOptions.where((opt) => opt.isNotEmpty).toList(),
          'allows_multiple': state.allowsMultipleVotes,
          'duration_hours': 24,
        };
      }

      await _postRepo.createPost(
        text: state.text,
        contentType: postTypeToContentType(state.type),
        visibility: state.visibility.name,
        mediaIds: mediaIds.isEmpty ? null : mediaIds,
        tags: state.tags.isEmpty ? null : state.tags,
        feeling: state.mood,
        locationName: state.location,
        poll: pollPayload,
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
