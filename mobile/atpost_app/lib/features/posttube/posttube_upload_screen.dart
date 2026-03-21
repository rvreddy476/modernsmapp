import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
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

  double _uploadProgress = 0.0;
  String _status = '';
  bool _isUploading = false;
  String? _error;

  @override
  void dispose() {
    _titleCtrl.dispose();
    _descCtrl.dispose();
    super.dispose();
  }

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

      // Create the actual video post
      await api.post('/v1/posts', data: {
        'content_type': 'video',
        'media_ids': [mediaId],
        'title': _titleCtrl.text.trim(),
        'text': _descCtrl.text.trim(),
        'visibility': 'public',
      });

      if (mounted) {
        setState(() {
          _isUploading = false;
          _status = 'Upload complete!';
          _uploadProgress = 1.0;
        });
      }
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
      child: Column(
        children: [
          TextField(controller: _titleCtrl, decoration: const InputDecoration(labelText: 'Title'), style: const TextStyle(color: Colors.white)),
          const SizedBox(height: 16),
          TextField(controller: _descCtrl, maxLines: 3, decoration: const InputDecoration(labelText: 'Description'), style: const TextStyle(color: Colors.white)),
          const Spacer(),
          SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              onPressed: _titleCtrl.text.isNotEmpty ? _startResilientUpload : null,
              child: const Text('Start Upload'),
            ),
          ),
        ],
      ),
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
