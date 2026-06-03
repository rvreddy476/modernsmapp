import 'dart:io';
import 'dart:math' as math;
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/create/providers/creation_provider.dart';
import 'package:atpost_app/features/create/widgets/mention_field.dart';
import 'package:atpost_app/features/create/widgets/trending_hashtag_strip.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

/// A modern, elegant Post Composer designed for production scale.
/// Features: Immersive glassmorphism UI, AI enhancement animations,
/// optimized media grid, and context-aware floating toolbar.
class CreatePostScreen extends ConsumerStatefulWidget {
  const CreatePostScreen({super.key});

  @override
  ConsumerState<CreatePostScreen> createState() => _CreatePostScreenState();
}

class _CreatePostScreenState extends ConsumerState<CreatePostScreen> {
  final TextEditingController _textController = TextEditingController();
  final TextEditingController _hashtagController = TextEditingController();
  final FocusNode _focusNode = FocusNode();
  bool _showHashtagInput = false;
  bool _showBackgroundPicker = false;

  @override
  void initState() {
    super.initState();
    _focusNode.requestFocus();
    _textController.addListener(() {
      ref.read(creationProvider.notifier).setText(_textController.text);
    });
  }

  @override
  void dispose() {
    _textController.dispose();
    _hashtagController.dispose();
    _focusNode.dispose();
    super.dispose();
  }

  Future<void> _pickMedia(bool isVideo) async {
    final picker = ImagePicker();
    if (isVideo) {
      final video = await picker.pickVideo(source: ImageSource.gallery);
      if (video != null) {
        ref.read(creationProvider.notifier).addFiles([video]);
        ref.read(creationProvider.notifier).setType(PostType.video);
      }
    } else {
      final images = await picker.pickMultiImage();
      if (images.isNotEmpty) {
        ref.read(creationProvider.notifier).addFiles(images);
        ref.read(creationProvider.notifier).setType(PostType.photo);
      }
    }
  }

  Future<void> _handleCancel(CreationState state) async {
    final hasDraft = state.text.trim().isNotEmpty || state.files.isNotEmpty;
    if (!hasDraft) {
      context.pop();
      return;
    }

    final discard = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Discard post?', style: AppTextStyles.h3),
        content: Text(
          'Your current draft will be cleared.',
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Keep editing'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Discard'),
          ),
        ],
      ),
    );

    if (discard == true && mounted) {
      ref.read(creationProvider.notifier).reset();
      context.pop();
    }
  }

  void _showVisibilityPicker(CreationState state) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            _VisibilityOption(
              icon: Icons.public,
              label: 'Public',
              description: 'Anyone can see this post',
              selected: state.visibility == PostVisibility.public,
              onTap: () {
                ref
                    .read(creationProvider.notifier)
                    .setVisibility(PostVisibility.public);
                Navigator.of(ctx).pop();
              },
            ),
            _VisibilityOption(
              icon: Icons.people,
              label: 'Followers',
              description: 'Only your followers can see it',
              selected: state.visibility == PostVisibility.followers,
              onTap: () {
                ref
                    .read(creationProvider.notifier)
                    .setVisibility(PostVisibility.followers);
                Navigator.of(ctx).pop();
              },
            ),
            _VisibilityOption(
              icon: Icons.verified_user,
              label: 'Trusted Circle',
              description: 'Only your close friends can see it',
              selected: state.visibility == PostVisibility.trusted,
              onTap: () {
                ref
                    .read(creationProvider.notifier)
                    .setVisibility(PostVisibility.trusted);
                Navigator.of(ctx).pop();
              },
            ),
            _VisibilityOption(
              icon: Icons.lock,
              label: 'Private',
              description: 'Only you can see it',
              selected: state.visibility == PostVisibility.private,
              onTap: () {
                ref
                    .read(creationProvider.notifier)
                    .setVisibility(PostVisibility.private);
                Navigator.of(ctx).pop();
              },
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<CreationState>(creationProvider, (previous, next) {
      if (next.text != _textController.text) {
        _textController.value = TextEditingValue(
          text: next.text,
          selection: TextSelection.collapsed(offset: next.text.length),
        );
      }
    });
    final state = ref.watch(creationProvider);
    final user = ref.watch(currentUserProvider).valueOrNull;

    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        children: [
          // 1. Immersive Background
          _buildBackground(),

          SafeArea(
            child: Column(
              children: [
                _buildHeader(state),
                Expanded(
                  child: SingleChildScrollView(
                    physics: const BouncingScrollPhysics(),
                    padding: const EdgeInsets.symmetric(horizontal: 20),
                    child: Column(
                      children: [
                        // In poll mode, the Question lives inside the poll
                        // editor — hide the main composer textarea entirely
                        // so users aren't confused about where to type.
                        if (state.type != PostType.poll)
                          _buildComposerArea(state, user),
                        if (state.error != null)
                          _buildErrorBanner(state.error!),
                        if (state.isSubmitting && state.files.isNotEmpty)
                          _buildUploadProgress(state),
                        if (state.type == PostType.poll)
                          _buildElegantPollEditor(state),
                        if (_showHashtagInput || state.tags.isNotEmpty)
                          _buildHashtagPanel(state),
                        if (_showBackgroundPicker)
                          _buildBackgroundPicker(state),
                        if (state.files.isNotEmpty) _buildMediaGrid(state),
                        const SizedBox(height: 120), // Space for toolbar
                      ],
                    ),
                  ),
                ),
              ],
            ),
          ),

          // 2. Floating Context-Aware Toolbar
          _buildFloatingToolbar(state),
        ],
      ),
    );
  }

  Widget _buildBackground() {
    return Positioned.fill(
      child: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [Color(0xFF0F111A), Color(0xFF090A11), Color(0xFF141726)],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(CreationState state) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          TextButton(
            onPressed: state.isSubmitting ? null : () => _handleCancel(state),
            child: Text(
              'Cancel',
              style: AppTextStyles.body.copyWith(color: Colors.white70),
            ),
          ),
          ElevatedButton(
            onPressed:
                (state.text.isNotEmpty || state.files.isNotEmpty) &&
                    !state.isSubmitting
                ? () async {
                    final success = await ref
                        .read(creationProvider.notifier)
                        .submit();
                    if (success) {
                      // Drop cached feeds so the new post shows up on Home/
                      // PostTube/Reels without needing an app restart. The
                      // backend auto-classifies videos to flick/long_video by
                      // duration, so we invalidate both video surfaces.
                      ref.invalidate(videoFeedProvider);
                      ref.invalidate(reelFeedProvider);
                      try {
                        await ref
                            .read(homeFeedProvider.notifier)
                            .fetchFirstPage();
                      } catch (_) {/* non-fatal */}
                    }
                    if (success && mounted) context.pop();
                    if (!success && mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(
                          content: Text(
                            ref.read(creationProvider).error ??
                                'Could not publish post.',
                          ),
                        ),
                      );
                    }
                  }
                : null,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              disabledBackgroundColor: Colors.white10,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(24),
              ),
              padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
              elevation: 0,
            ),
            child: state.isSubmitting
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : Text(
                    'Publish',
                    style: AppTextStyles.label.copyWith(color: Colors.white),
                  ),
          ),
        ],
      ),
    );
  }

  Widget _buildComposerArea(CreationState state, dynamic user) {
    return Column(
      children: [
        Row(
          children: [
            CircleAvatar(
              radius: 22,
              backgroundColor: Colors.white10,
              backgroundImage: user?.avatarUrl != null
                  ? NetworkImage(user!.avatarUrl)
                  : null,
            ),
            const SizedBox(width: 12),
            Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(user?.displayName ?? 'Anonymous', style: AppTextStyles.h3),
                _buildVisibilityBadge(state),
              ],
            ),
            const Spacer(),
            // AI Sparkle Animation
            _buildAiMagicButton(state),
          ],
        ),
        const SizedBox(height: 20),
        // RepaintBoundary ensures smooth typing performance.
        // MentionField wraps a TextField and shows an `@username`
        // popover from `userRepository.searchUsers` whenever the
        // caret follows an unbroken `@token`. post-service already
        // parses mentions in `internal/service/posts.go`.
        RepaintBoundary(
          child: MentionField(
            controller: _textController,
            focusNode: _focusNode,
            maxLines: null,
            style: AppTextStyles.h2.copyWith(
              fontWeight: FontWeight.w400,
              color: Colors.white,
              fontSize: 20,
            ),
            hintText: "What's on your mind?",
            hintStyle: AppTextStyles.h2.copyWith(
              color: Colors.white24,
              fontWeight: FontWeight.w400,
            ),
            onChanged: (text) {
              ref.read(creationProvider.notifier).setText(text);
            },
          ),
        ),
        const SizedBox(height: 16),
        // Trending tag strip — one-tap insertion at the current caret
        // so users default to canonical tags. Tapping a chip splices
        // `#tag ` into the caption; the MentionField's existing
        // listener will resolve the inserted token like any other.
        TrendingHashtagStrip(
          onTagSelected: _insertTagAtCaret,
          excluded: _currentHashtagsInCaption(),
        ),
      ],
    );
  }

  /// Returns the set of tag chips already added, lowercased + `#`-prefixed
  /// for membership check by TrendingHashtagStrip.
  Set<String> _currentHashtagsInCaption() {
    final tags = ref.read(creationProvider).tags;
    return {for (final t in tags) '#${t.toLowerCase()}'};
  }

  /// Adds the trending strip's chip selection to the post's tag chips
  /// (NOT the main text — no stray `#` characters in the composing
  /// surface). Idempotent; dedup is handled by the provider.
  void _insertTagAtCaret(String chip) {
    ref.read(creationProvider.notifier).addTags(chip);
    setState(() => _showHashtagInput = true);
  }

  Widget _buildAiMagicButton(CreationState state) {
    return GestureDetector(
      onTap: () => ref.read(creationProvider.notifier).enhanceWithAi(),
      child:
          Container(
                padding: const EdgeInsets.all(10),
                decoration: BoxDecoration(
                  color: Colors.amber.withValues(alpha: 0.1),
                  shape: BoxShape.circle,
                ),
                child: state.isGeneratingAi
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.amber,
                        ),
                      )
                    : const Icon(
                        Icons.auto_awesome,
                        color: Colors.amber,
                        size: 20,
                      ),
              )
              .animate(target: state.isGeneratingAi ? 1 : 0)
              .shimmer(duration: 1.seconds)
              .scale(duration: 200.ms),
    );
  }

  Widget _buildVisibilityBadge(CreationState state) {
    return GestureDetector(
      onTap: () => _showVisibilityPicker(state),
      child: Container(
        margin: const EdgeInsets.only(top: 4),
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
        decoration: BoxDecoration(
          color: Colors.white.withValues(alpha: 0.05),
          borderRadius: BorderRadius.circular(4),
          border: Border.all(color: Colors.white10),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              switch (state.visibility) {
                PostVisibility.public => Icons.public,
                PostVisibility.followers => Icons.people,
                PostVisibility.trusted => Icons.verified_user,
                PostVisibility.private => Icons.lock,
              },
              size: 10,
              color: Colors.grey,
            ),
            const SizedBox(width: 4),
            Text(
              state.visibility.name.toUpperCase(),
              style: AppTextStyles.labelTiny.copyWith(color: Colors.grey),
            ),
            const Icon(Icons.keyboard_arrow_down, size: 12, color: Colors.grey),
          ],
        ),
      ),
    );
  }

  Widget _buildMediaGrid(CreationState state) {
    return Container(
      margin: const EdgeInsets.only(top: 24),
      child: GridView.builder(
        shrinkWrap: true,
        physics: const NeverScrollableScrollPhysics(),
        itemCount: state.files.length,
        gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: 2,
          crossAxisSpacing: 10,
          mainAxisSpacing: 10,
        ),
        itemBuilder: (context, index) => Stack(
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(16),
              child: Image.file(
                File(state.files[index].path),
                fit: BoxFit.cover,
                width: double.infinity,
                height: double.infinity,
              ),
            ),
            Positioned(
              right: 8,
              top: 8,
              child: GestureDetector(
                onTap: () =>
                    ref.read(creationProvider.notifier).removeFile(index),
                child: Container(
                  padding: const EdgeInsets.all(4),
                  decoration: const BoxDecoration(
                    color: Colors.black54,
                    shape: BoxShape.circle,
                  ),
                  child: const Icon(Icons.close, size: 14, color: Colors.white),
                ),
              ),
            ),
          ],
        ),
      ),
    ).animate().fadeIn().slideY(begin: 0.1, end: 0);
  }

  Widget _buildElegantPollEditor(CreationState state) {
    final qErr = state.pollQuestionError;
    return Container(
      margin: const EdgeInsets.only(top: 24),
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.03),
        borderRadius: BorderRadius.circular(24),
        border: Border.all(color: Colors.white10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Question — explicit input so users can't add options without
          // a prompt. Bound to the same `text` field used by non-poll posts.
          Text(
            'Question',
            style: AppTextStyles.label.copyWith(color: Colors.white70),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _textController,
            onChanged: (v) {
              ref.read(creationProvider.notifier).setText(v);
              ref.read(creationProvider.notifier).clearPollErrors();
            },
            style: AppTextStyles.body,
            decoration: InputDecoration(
              hintText: 'Ask a question…',
              hintStyle: AppTextStyles.body.copyWith(color: Colors.white38),
              filled: true,
              fillColor: Colors.white.withValues(alpha: 0.05),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
                borderSide: qErr != null
                    ? const BorderSide(color: Colors.redAccent)
                    : BorderSide.none,
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
                borderSide: qErr != null
                    ? const BorderSide(color: Colors.redAccent)
                    : BorderSide.none,
              ),
            ),
          ),
          if (qErr != null)
            Padding(
              padding: const EdgeInsets.only(top: 6, left: 4),
              child: Text(
                qErr,
                style: AppTextStyles.labelSmall.copyWith(color: Colors.redAccent),
              ),
            ),
          const SizedBox(height: 16),
          Text(
            'Options',
            style: AppTextStyles.label.copyWith(color: Colors.white70),
          ),
          const SizedBox(height: 8),
          ...state.pollOptions.asMap().entries.map(
            (e) {
              final err = state.pollOptionErrors[e.key];
              return Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    TextField(
                      onChanged: (v) {
                        ref
                            .read(creationProvider.notifier)
                            .updatePollOption(e.key, v);
                        // Clear inline errors as the user fixes them.
                        ref.read(creationProvider.notifier).clearPollErrors();
                      },
                      style: AppTextStyles.body,
                      decoration: InputDecoration(
                        hintText: 'Option ${e.key + 1}',
                        filled: true,
                        fillColor: Colors.white.withValues(alpha: 0.05),
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: err != null
                              ? const BorderSide(color: Colors.redAccent)
                              : BorderSide.none,
                        ),
                        enabledBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: err != null
                              ? const BorderSide(color: Colors.redAccent)
                              : BorderSide.none,
                        ),
                        suffixIcon: state.pollOptions.length > 2
                            ? IconButton(
                                icon: const Icon(
                                  Icons.remove_circle_outline,
                                  color: Colors.redAccent,
                                  size: 20,
                                ),
                                onPressed: () => ref
                                    .read(creationProvider.notifier)
                                    .removePollOption(e.key),
                              )
                            : null,
                      ),
                    ),
                    if (err != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 4, left: 4),
                        child: Text(
                          err,
                          style: AppTextStyles.labelSmall
                              .copyWith(color: Colors.redAccent),
                        ),
                      ),
                  ],
                ),
              );
            },
          ),
          if (state.pollQuestionError != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 8, left: 4),
              child: Text(
                state.pollQuestionError!,
                style: AppTextStyles.labelSmall.copyWith(color: Colors.redAccent),
              ),
            ),
          if (state.pollOptions.length < 5)
            TextButton.icon(
              onPressed: () =>
                  ref.read(creationProvider.notifier).addPollOption(),
              icon: const Icon(Icons.add_circle_outline, size: 20),
              label: const Text('Add choice'),
            ),
        ],
      ),
    ).animate().scale(begin: const Offset(0.95, 0.95), end: const Offset(1, 1));
  }

  Widget _buildFloatingToolbar(CreationState state) {
    return Positioned(
      left: 20,
      right: 20,
      bottom: 30,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: const Color(0xFF1E2130),
          borderRadius: BorderRadius.circular(30),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withValues(alpha: 0.4),
              blurRadius: 20,
              offset: const Offset(0, 10),
            ),
          ],
          border: Border.all(color: Colors.white.withValues(alpha: 0.05)),
        ),
        child: Row(
          mainAxisAlignment: MainAxisAlignment.spaceAround,
          children: [
            _ToolbarIcon(
              Icons.image_outlined,
              'Photos',
              Colors.blueAccent,
              () => _pickMedia(false),
            ),
            _ToolbarIcon(
              Icons.videocam_outlined,
              'Video',
              Colors.redAccent,
              () => _pickMedia(true),
            ),
            _ToolbarIcon(
              Icons.poll_outlined,
              'Poll',
              Colors.purpleAccent,
              () => ref.read(creationProvider.notifier).setType(PostType.poll),
            ),
            _ToolbarIcon(
              Icons.tag,
              'Tags',
              Colors.lightBlueAccent,
              () => setState(() => _showHashtagInput = !_showHashtagInput),
            ),
            _ToolbarIcon(
              Icons.palette_outlined,
              'Design',
              Colors.amberAccent,
              () => setState(() => _showBackgroundPicker = !_showBackgroundPicker),
            ),
            _ToolbarIcon(
              Icons.movie_edit,
              'Reels',
              Colors.purpleAccent,
              () => context.push('/reels/editor'),
            ),
            const VerticalDivider(color: Colors.white10),
            Text(
              '${state.text.length}/3k',
              style: AppTextStyles.monoSmall.copyWith(color: Colors.white38),
            ),
          ],
        ),
      ),
    );
  }

  /// Hashtag input panel — chips below a text field. User types, hits
  /// space/comma/enter or taps "Add" to commit a chip. Tap × on a chip
  /// to remove. The raw text area is never touched.
  Widget _buildHashtagPanel(CreationState state) {
    return Container(
      margin: const EdgeInsets.only(top: 16),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.03),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: Colors.white10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.tag, color: Colors.lightBlueAccent, size: 16),
              const SizedBox(width: 6),
              Text(
                'Hashtags',
                style: AppTextStyles.label.copyWith(color: Colors.white70),
              ),
              const Spacer(),
              IconButton(
                icon: const Icon(Icons.close, size: 18, color: Colors.white54),
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(),
                onPressed: () => setState(() => _showHashtagInput = false),
              ),
            ],
          ),
          const SizedBox(height: 10),
          TextField(
            controller: _hashtagController,
            style: AppTextStyles.body,
            textInputAction: TextInputAction.done,
            onSubmitted: (val) {
              if (val.trim().isEmpty) return;
              ref.read(creationProvider.notifier).addTags(val);
              _hashtagController.clear();
            },
            onChanged: (val) {
              // Auto-commit on space or comma so users can chain tags.
              if (val.endsWith(' ') || val.endsWith(',')) {
                final token = val.replaceAll(RegExp(r'[,\s]+$'), '');
                if (token.isNotEmpty) {
                  ref.read(creationProvider.notifier).addTags(token);
                  _hashtagController.clear();
                }
              }
            },
            decoration: InputDecoration(
              hintText: 'Enter hashtags and press Enter',
              hintStyle: AppTextStyles.body.copyWith(color: Colors.white38),
              filled: true,
              fillColor: Colors.white.withValues(alpha: 0.05),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
                borderSide: BorderSide.none,
              ),
            ),
          ),
          if (state.tags.isNotEmpty) ...[
            const SizedBox(height: 10),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: state.tags
                  .map((t) => Chip(
                        label: Text(
                          '#$t',
                          style: AppTextStyles.labelSmall.copyWith(
                            color: Colors.lightBlueAccent,
                          ),
                        ),
                        backgroundColor: Colors.lightBlueAccent.withValues(alpha: 0.1),
                        side: BorderSide(
                          color: Colors.lightBlueAccent.withValues(alpha: 0.3),
                        ),
                        deleteIcon: const Icon(Icons.close, size: 14),
                        deleteIconColor: Colors.lightBlueAccent,
                        onDeleted: () =>
                            ref.read(creationProvider.notifier).removeTag(t),
                        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        visualDensity: VisualDensity.compact,
                      ))
                  .toList(),
            ),
          ],
        ],
      ),
    ).animate().fadeIn(duration: 200.ms);
  }

  /// Rainbow half-arc background colour picker. No black / near-black.
  /// The "no background" swatch sits centred under the arc peak.
  Widget _buildBackgroundPicker(CreationState state) {
    // Disabled when media or poll is present — coloured backgrounds are text-only.
    final disabled = state.files.isNotEmpty || state.type == PostType.poll;
    const swatches = <_Swatch>[
      _Swatch(hex: '#E63946', label: 'Red', dark: true),
      _Swatch(hex: '#F58F47', label: 'Orange'),
      _Swatch(hex: '#FFD23F', label: 'Yellow'),
      _Swatch(hex: '#4ED96B', label: 'Green'),
      _Swatch(hex: '#3AC7FF', label: 'Sky'),
      _Swatch(hex: '#2563EB', label: 'Blue', dark: true),
      _Swatch(hex: '#A862FF', label: 'Indigo', dark: true),
      _Swatch(hex: '#FF4F8B', label: 'Pink', dark: true),
    ];

    return Container(
      margin: const EdgeInsets.only(top: 16),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.03),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: Colors.white10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.palette_outlined, color: Colors.amberAccent, size: 16),
              const SizedBox(width: 6),
              Text(
                'Background',
                style: AppTextStyles.label.copyWith(color: Colors.white70),
              ),
              const Spacer(),
              if (state.backgroundColor != null)
                TextButton(
                  style: TextButton.styleFrom(
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    minimumSize: Size.zero,
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  onPressed: () =>
                      ref.read(creationProvider.notifier).setBackground(null),
                  child: Text(
                    'RESET',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: Colors.lightBlueAccent,
                    ),
                  ),
                ),
              IconButton(
                icon: const Icon(Icons.close, size: 18, color: Colors.white54),
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(),
                onPressed: () =>
                    setState(() => _showBackgroundPicker = false),
              ),
            ],
          ),
          if (disabled) ...[
            const SizedBox(height: 8),
            Text(
              'Backgrounds work only on text-only posts.',
              style: AppTextStyles.labelSmall.copyWith(color: Colors.white38),
            ),
          ],
          const SizedBox(height: 12),
          SizedBox(
            height: 130,
            child: LayoutBuilder(
              builder: (context, constraints) {
                final arcWidth = constraints.maxWidth;
                final radius = (arcWidth / 2) - 22;
                const arcSpan = 160.0; // degrees
                final start = 180.0 + (180.0 - arcSpan) / 2;
                final arcHeight = 110.0;
                return Stack(
                  children: [
                    for (var i = 0; i < swatches.length; i++)
                      _arcSwatch(
                        swatches[i],
                        i: i,
                        total: swatches.length,
                        arcWidth: arcWidth,
                        arcHeight: arcHeight,
                        radius: radius,
                        start: start,
                        arcSpan: arcSpan,
                        selected: state.backgroundColor?.toUpperCase() ==
                            swatches[i].hex.toUpperCase(),
                        disabled: disabled,
                      ),
                    // No-background swatch sits centred under the arc peak.
                    Positioned(
                      left: arcWidth / 2 - 16,
                      top: arcHeight - 4,
                      child: _swatchButton(
                        const _Swatch(hex: 'transparent', label: 'No background'),
                        selected: state.backgroundColor == null,
                        disabled: disabled,
                      ),
                    ),
                  ],
                );
              },
            ),
          ),
        ],
      ),
    ).animate().fadeIn(duration: 200.ms);
  }

  Widget _arcSwatch(
    _Swatch swatch, {
    required int i,
    required int total,
    required double arcWidth,
    required double arcHeight,
    required double radius,
    required double start,
    required double arcSpan,
    required bool selected,
    required bool disabled,
  }) {
    final t = total == 1 ? 0.5 : i / (total - 1);
    final angle = start + t * arcSpan;
    final rad = angle * 3.1415926535 / 180;
    final cx = arcWidth / 2 + radius * _cos(rad);
    final cy = arcHeight + radius * _sin(rad);
    return Positioned(
      left: cx - 16,
      top: cy - 16,
      child: _swatchButton(swatch, selected: selected, disabled: disabled),
    );
  }

  double _cos(double rad) => math.cos(rad);
  double _sin(double rad) => math.sin(rad);

  /// Convert a "#RRGGBB" hex string to a Flutter Color. Returns
  /// transparent for the special "transparent" sentinel.
  Color _hex(String input) {
    if (input == 'transparent') return Colors.transparent;
    final cleaned = input.replaceFirst('#', '').toUpperCase();
    final value = int.tryParse('FF$cleaned', radix: 16);
    return value == null ? Colors.white12 : Color(value);
  }

  Widget _swatchButton(_Swatch swatch, {required bool selected, required bool disabled}) {
    final isNone = swatch.hex == 'transparent';
    return GestureDetector(
      onTap: disabled
          ? null
          : () {
              if (isNone) {
                ref.read(creationProvider.notifier).setBackground(null);
              } else {
                ref
                    .read(creationProvider.notifier)
                    .setBackground(swatch.hex, isDark: swatch.dark);
              }
              setState(() => _showBackgroundPicker = false);
            },
      child: Container(
        width: 32,
        height: 32,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          color: isNone ? Colors.white12 : _hex(swatch.hex),
          border: Border.all(
            color: selected ? Colors.lightBlueAccent : Colors.white24,
            width: selected ? 2.5 : 2,
          ),
        ),
        child: isNone
            ? const Icon(Icons.block, size: 14, color: Colors.white54)
            : null,
      ),
    );
  }

  Widget _buildErrorBanner(String error) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(top: 16),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Colors.redAccent.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.redAccent.withValues(alpha: 0.35)),
      ),
      child: Text(
        error,
        style: AppTextStyles.bodySmall.copyWith(color: Colors.redAccent),
      ),
    );
  }

  Widget _buildUploadProgress(CreationState state) {
    final progress = state.uploadProgress.clamp(0, 1).toDouble();
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(top: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          LinearProgressIndicator(
            value: progress == 0 ? null : progress,
            color: AppColors.postbookPrimary,
            backgroundColor: Colors.white10,
          ),
          const SizedBox(height: 8),
          Text(
            progress == 0
                ? 'Preparing upload'
                : 'Uploading ${(progress * 100).round()}%',
            style: AppTextStyles.labelSmall.copyWith(color: Colors.white54),
          ),
        ],
      ),
    );
  }
}

class _ToolbarIcon extends StatelessWidget {
  final IconData icon;
  final String label;
  final Color color;
  final VoidCallback onTap;

  const _ToolbarIcon(this.icon, this.label, this.color, this.onTap);

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(10),
        child: Icon(icon, color: color, size: 24),
      ),
    );
  }
}

class _VisibilityOption extends StatelessWidget {
  const _VisibilityOption({
    required this.icon,
    required this.label,
    required this.description,
    required this.selected,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final String description;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: Icon(
        icon,
        color: selected ? AppColors.postbookPrimary : Colors.white70,
      ),
      title: Text(label, style: AppTextStyles.body),
      subtitle: Text(
        description,
        style: AppTextStyles.bodySmall.copyWith(color: Colors.white54),
      ),
      trailing: selected
          ? const Icon(Icons.check, color: AppColors.postbookPrimary)
          : null,
      onTap: onTap,
    );
  }
}

/// Background-picker swatch — `dark: true` flips post text to white so
/// it stays legible on saturated/dark fills.
class _Swatch {
  final String hex;
  final String label;
  final bool dark;
  const _Swatch({required this.hex, required this.label, this.dark = false});
}
