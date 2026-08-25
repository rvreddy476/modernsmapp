import 'dart:async';
import 'package:dio/dio.dart';
import 'dart:io';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/stories_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

class CreateStoryScreen extends ConsumerStatefulWidget {
  const CreateStoryScreen({super.key});

  @override
  ConsumerState<CreateStoryScreen> createState() => _CreateStoryScreenState();
}

class _CreateStoryScreenState extends ConsumerState<CreateStoryScreen> {
  final ImagePicker _picker = ImagePicker();
  XFile? _picked;

  /// Who this story goes to. Defaults to followers, not everyone.
  ///
  /// The old client hardcoded 'public' and showed no chooser at all, so every
  /// story was published to everyone whatever the creator assumed. Defaulting
  /// to the narrower audience means an unmade choice under-shares rather than
  /// over-shares.
  StoryAudience _audience = StoryAudience.followers;

  /// Stable across retries of one share, so a retry after a network failure
  /// returns the same story instead of creating a second one.
  final String _idempotencyKey = DateTime.now().microsecondsSinceEpoch.toString();
  bool _isVideo = false;
  bool _uploading = false;
  final TextEditingController _textController = TextEditingController();

  @override
  void dispose() {
    _textController.dispose();
    super.dispose();
  }

  Future<void> _pickImage(ImageSource source) async {
    final file = await _picker.pickImage(source: source, imageQuality: 85);
    if (file != null) {
      setState(() {
        _picked = file;
        _isVideo = false;
      });
    }
  }

  Future<void> _pickVideo() async {
    final file = await _picker.pickVideo(source: ImageSource.gallery);
    if (file != null) {
      setState(() {
        _picked = file;
        _isVideo = true;
      });
    }
  }

  Future<void> _share() async {
    if (_picked == null || _uploading) return;
    setState(() => _uploading = true);
    try {
      final api = ref.read(apiClientProvider);
      final storiesRepo = ref.read(storiesRepositoryProvider);
      final mediaId =
          await api.uploadMedia(_picked!, type: _isVideo ? 'video' : 'image');
      final text = _textController.text.trim();
      // Audit H7: if createStory fails after upload, drop the orphan
      // media right away rather than waiting for the 24h server sweep.
      try {
        await storiesRepo.createStory(
          mediaId: mediaId,
          mediaType: _isVideo ? 'video' : 'image',
          audience: _audience,
          text: text.isEmpty ? null : text,
          idempotencyKey: _idempotencyKey,
        );
      } catch (_) {
        unawaited(api.tryDeleteMedia(mediaId));
        rethrow;
      }
      if (mounted) {
        // The story is accepted, NOT published. Saying "shared" here would be
        // the false-success state: the story sits in review and the creator
        // would go looking for it on their profile and not find it.
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text(
              'Story sent for review. It goes live once it is approved.',
            ),
          ),
        );
        context.pop();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_shareFailureMessage(e))),
        );
      }
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final hasPicked = _picked != null;

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        leading: TextButton(
          onPressed: () => context.pop(),
          child: Text('Cancel', style: AppTextStyles.label),
        ),
        title: Text('New Story', style: AppTextStyles.h3),
        centerTitle: true,
        actions: [
          _uploading
              ? const Padding(
                  padding: EdgeInsets.symmetric(horizontal: 16),
                  child: SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                )
              : TextButton(
                  onPressed: hasPicked ? _share : null,
                  child: Text(
                    'Share',
                    style: AppTextStyles.label.copyWith(
                      color: hasPicked
                          ? AppColors.postbookPrimary
                          : AppColors.textMuted,
                    ),
                  ),
                ),
        ],
      ),
      body: Column(
        children: [
          // Audience chooser. The creator has to be able to see and change who
          // a story goes to before sharing it — the previous screen offered no
          // control at all and always published to everyone.
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Row(
              children: [
                const Icon(Icons.group_outlined,
                    size: 18, color: AppColors.textMuted),
                const SizedBox(width: 8),
                Text('Share with', style: AppTextStyles.label),
                const Spacer(),
                DropdownButton<StoryAudience>(
                  value: _audience,
                  underline: const SizedBox.shrink(),
                  dropdownColor: AppColors.bgSecondary,
                  onChanged: _uploading
                      ? null
                      : (value) {
                          if (value != null) {
                            setState(() => _audience = value);
                          }
                        },
                  items: StoryAudience.values
                      .map(
                        (a) => DropdownMenuItem<StoryAudience>(
                          value: a,
                          child: Text(a.label, style: AppTextStyles.body),
                        ),
                      )
                      .toList(),
                ),
              ],
            ),
          ),
          // Media preview area
          Expanded(
            child: hasPicked
                ? Stack(
                    fit: StackFit.expand,
                    children: [
                      if (!_isVideo)
                        Image.file(
                          File(_picked!.path),
                          fit: BoxFit.cover,
                        )
                      else
                        Container(
                          color: AppColors.bgTertiary,
                          child: const Center(
                            child: Icon(
                              Icons.videocam,
                              color: AppColors.textMuted,
                              size: 64,
                            ),
                          ),
                        ),
                      // Text overlay input
                      Positioned(
                        left: 16,
                        right: 16,
                        bottom: 16,
                        child: Container(
                          decoration: BoxDecoration(
                            color: Colors.black45,
                            borderRadius: BorderRadius.circular(12),
                          ),
                          padding: const EdgeInsets.symmetric(horizontal: 12),
                          child: TextField(
                            controller: _textController,
                            style: AppTextStyles.body.copyWith(color: Colors.white),
                            decoration: InputDecoration(
                              hintText: 'Add text...',
                              hintStyle: AppTextStyles.body.copyWith(color: Colors.white54),
                              border: InputBorder.none,
                            ),
                            maxLines: 3,
                            minLines: 1,
                          ),
                        ),
                      ),
                    ],
                  )
                : Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(
                          Icons.add_photo_alternate_outlined,
                          color: AppColors.textMuted,
                          size: 64,
                        ),
                        const SizedBox(height: 12),
                        Text(
                          'Tap to add photo or video',
                          style: AppTextStyles.body,
                        ),
                      ],
                    ),
                  ),
          ),

          // Bottom toolbar
          Container(
            color: AppColors.bgSecondary,
            padding: EdgeInsets.only(
              left: 24,
              right: 24,
              top: 12,
              bottom: MediaQuery.of(context).padding.bottom + 12,
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceEvenly,
              children: [
                _ToolbarButton(
                  icon: Icons.camera_alt_outlined,
                  label: 'Camera',
                  onTap: () => _pickImage(ImageSource.camera),
                ),
                _ToolbarButton(
                  icon: Icons.photo_library_outlined,
                  label: 'Gallery',
                  onTap: () => _pickImage(ImageSource.gallery),
                ),
                _ToolbarButton(
                  icon: Icons.videocam_outlined,
                  label: 'Video',
                  onTap: _pickVideo,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _ToolbarButton extends StatelessWidget {
  const _ToolbarButton({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, color: AppColors.textSecondary, size: 28),
          const SizedBox(height: 4),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

/// Maps a share failure onto something a creator can act on.
///
/// The previous screen showed one generic message for every failure, so a
/// signed-out session, a rejected upload and a transient outage were
/// indistinguishable — and the only advice was "try again", which is wrong for
/// two of the three.
String _shareFailureMessage(Object error) {
  final status = error is DioException ? error.response?.statusCode : null;
  switch (status) {
    case 401:
      return 'Please sign in again to share a story.';
    case 400:
      return 'That media cannot be used for a story. Try picking it again.';
    case 503:
      return 'We could not check who this story is for. Please retry shortly.';
    default:
      return 'Failed to share story. Please try again.';
  }
}
