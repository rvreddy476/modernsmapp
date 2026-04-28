import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

/// Production-ready video upload screen with 3-step resilient upload.
class PosttubeUploadScreen extends ConsumerStatefulWidget {
  const PosttubeUploadScreen({super.key});

  @override
  ConsumerState<PosttubeUploadScreen> createState() => _PosttubeUploadScreenState();
}

class _PosttubeUploadScreenState extends ConsumerState<PosttubeUploadScreen> {
  int _step = 0; // 0: Select, 1: Details, 2: Progress
  XFile? _videoFile;
  final TextEditingController _titleCtrl = TextEditingController();
  final TextEditingController _descCtrl = TextEditingController();
  final TextEditingController _hashtagCtrl = TextEditingController();
  final List<String> _hashtags = [];

  double _uploadProgress = 0.0;
  String _status = '';
  bool _isUploading = false;
  String? _error;

  @override
  void dispose() {
    _titleCtrl.dispose();
    _descCtrl.dispose();
    _hashtagCtrl.dispose();
    super.dispose();
  }

  void _addHashtag(String tag) {
    final cleaned = tag.trim().toLowerCase();
    if (cleaned.isEmpty) return;
    final withHash = cleaned.startsWith('#') ? cleaned : '#$cleaned';
    if (!_hashtags.contains(withHash)) {
      setState(() => _hashtags.add(withHash));
    }
    _hashtagCtrl.clear();
  }

  void _removeHashtag(String tag) => setState(() => _hashtags.remove(tag));

  /// Refactored 3-Step Upload (Init -> Presigned -> Confirm)
  Future<void> _startResilientUpload() async {
    if (_videoFile == null) return;

    setState(() {
      _step = 2;
      _isUploading = true;
      _status = 'Preparing secure upload...';
      _uploadProgress = 0.0;
      _error = null;
    });

    try {
      final api = ref.read(apiClientProvider);

      // Executes the 3-step orchestration defined in the new ApiClient
      final mediaId = await api.uploadMedia(
        _videoFile!,
        type: 'video',
        onProgress: (sent, total) {
          if (mounted) setState(() => _uploadProgress = sent / total);
        },
      );

      if (mounted) setState(() => _status = 'Finalizing post...');

      // Embed hashtags into the description so post-service's extractHashtags
      // picks them up and writes posts.hashtags[]. Sending them in the `tags`
      // field would land in reel-metadata categories, not the hashtag index.
      final desc = _descCtrl.text.trim();
      final tagLine = _hashtags.join(' ');
      final fullText = [desc, tagLine].where((s) => s.isNotEmpty).join('\n\n');

      // Create the actual video post. Use canonical 'long_video' rather than
      // legacy 'video' (backend normalizes either, but this is the modern name).
      await api.post('/v1/posts', data: {
        'content_type': 'long_video',
        'media_ids': [mediaId],
        'title': _titleCtrl.text.trim(),
        'text': fullText,
        'visibility': 'public',
      });

      if (mounted) {
        setState(() {
          _isUploading = false;
          _status = 'Upload complete!';
          _uploadProgress = 1.0;
        });
      }

      // Drop cached PostTube and home feeds so the new long-video shows up
      // immediately when the user taps "Finish" and lands on /posttube.
      ref.invalidate(videoFeedProvider);
      ref.invalidate(reelFeedProvider);
      try {
        await ref.read(homeFeedProvider.notifier).fetchFirstPage();
      } catch (_) {/* non-fatal */}
    } catch (e, st) {
      final exception = ErrorHandler.handle(e, st);
      if (mounted) {
        setState(() {
          _isUploading = false;
          _error = exception.userMessage;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        title: const Text('New Video'),
        leading: IconButton(icon: const Icon(Icons.close), onPressed: () => context.pop()),
      ),
      body: _buildCurrentStep(),
    );
  }

  Widget _buildCurrentStep() {
    return switch (_step) {
      0 => _buildStepSelect(),
      1 => _buildStepDetails(),
      2 => _buildStepProgress(),
      _ => const SizedBox.shrink(),
    };
  }

  Widget _buildStepSelect() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.cloud_upload_outlined, size: 80, color: Colors.white24),
          const SizedBox(height: 24),
          ElevatedButton.icon(
            onPressed: () async {
              final picked = await ImagePicker().pickVideo(source: ImageSource.gallery);
              if (picked != null) setState(() { _videoFile = picked; _step = 1; });
            },
            icon: const Icon(Icons.video_library),
            label: const Text('Select Video'),
            style: ElevatedButton.styleFrom(backgroundColor: AppColors.posttubePrimary),
          ),
        ],
      ),
    );
  }

  Widget _buildStepDetails() {
    return Padding(
      padding: const EdgeInsets.all(20),
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TextField(
              controller: _titleCtrl,
              decoration: const InputDecoration(labelText: 'Title'),
              style: const TextStyle(color: Colors.white),
              onChanged: (_) => setState(() {}),
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _descCtrl,
              maxLines: 3,
              decoration: const InputDecoration(labelText: 'Description'),
              style: const TextStyle(color: Colors.white),
            ),
            const SizedBox(height: 20),
            _buildHashtagsSection(),
            const SizedBox(height: 32),
            ElevatedButton(
              onPressed: _titleCtrl.text.trim().isNotEmpty ? _startResilientUpload : null,
              child: const Text('Start Upload'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildHashtagsSection() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text(
          'Hashtags',
          style: TextStyle(color: Colors.white54, fontSize: 12, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 8),
        if (_hashtags.isNotEmpty)
          Wrap(
            spacing: 8,
            runSpacing: 4,
            children: _hashtags.map((tag) {
              return Chip(
                label: Text(tag, style: const TextStyle(color: Colors.white, fontSize: 12)),
                backgroundColor: AppColors.posttubePrimary.withValues(alpha: 0.25),
                deleteIcon: const Icon(Icons.close, size: 14, color: Colors.white54),
                onDeleted: () => _removeHashtag(tag),
                side: BorderSide.none,
                visualDensity: VisualDensity.compact,
              );
            }).toList(),
          ),
        const SizedBox(height: 8),
        Row(
          children: [
            Expanded(
              child: TextField(
                controller: _hashtagCtrl,
                style: const TextStyle(color: Colors.white, fontSize: 14),
                onSubmitted: _addHashtag,
                decoration: InputDecoration(
                  hintText: 'Add a hashtag',
                  hintStyle: const TextStyle(color: Colors.white38),
                  filled: true,
                  fillColor: Colors.white10,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(10),
                    borderSide: BorderSide.none,
                  ),
                  contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                  prefixIcon: const Icon(Icons.tag, color: Colors.white38, size: 18),
                ),
              ),
            ),
            const SizedBox(width: 8),
            GestureDetector(
              onTap: () => _addHashtag(_hashtagCtrl.text),
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                decoration: BoxDecoration(
                  color: AppColors.posttubePrimary,
                  borderRadius: BorderRadius.circular(10),
                ),
                child: const Text('Add', style: TextStyle(color: Colors.white, fontWeight: FontWeight.w600, fontSize: 13)),
              ),
            ),
          ],
        ),
      ],
    );
  }

  Widget _buildStepProgress() {
    return Padding(
      padding: const EdgeInsets.all(40),
      child: Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            if (_error != null) ...[
              const Icon(Icons.error_outline, color: Colors.red, size: 60),
              const SizedBox(height: 16),
              Text(_error!, style: const TextStyle(color: Colors.redAccent), textAlign: TextAlign.center),
              const SizedBox(height: 24),
              ElevatedButton(onPressed: _startResilientUpload, child: const Text('Retry')),
            ] else ...[
              CircularProgressIndicator(value: _uploadProgress, color: AppColors.posttubePrimary, strokeWidth: 8),
              const SizedBox(height: 24),
              Text('${(_uploadProgress * 100).toInt()}%', style: AppTextStyles.h1),
              const SizedBox(height: 8),
              Text(_status, style: AppTextStyles.bodySmall),
              if (!_isUploading) ...[
                const SizedBox(height: 32),
                ElevatedButton(onPressed: () => context.go('/posttube'), child: const Text('Finish')),
              ],
            ],
          ],
        ),
      ),
    );
  }
}
