import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

const _brandRed = Color(0xFFD8103F);

class UploadProgressScreen extends ConsumerStatefulWidget {
  final String videoPath;
  final String caption;
  final List<String> hashtags;
  final String visibility;

  const UploadProgressScreen({
    super.key,
    required this.videoPath,
    required this.caption,
    required this.hashtags,
    required this.visibility,
  });

  @override
  ConsumerState<UploadProgressScreen> createState() =>
      _UploadProgressScreenState();
}

class _UploadProgressScreenState extends ConsumerState<UploadProgressScreen> {
  double _progress = 0.0;
  String _statusMessage = 'Preparing...';
  bool _isDone = false;
  bool _hasError = false;
  String _errorMessage = '';
  String _postId = '';

  @override
  void initState() {
    super.initState();
    _startUpload();
  }

  Future<void> _startUpload() async {
    setState(() {
      _progress = 0;
      _hasError = false;
      _isDone = false;
      _statusMessage = 'Preparing...';
    });

    try {
      // Step 1: Upload video
      setState(() => _statusMessage = 'Uploading video...');
      final api = ref.read(apiClientProvider);

      final mediaId = await api.uploadMedia(
        XFile(widget.videoPath),
        type: 'video',
        onProgress: (int sent, int total) {
          if (mounted && total > 0) {
            setState(() => _progress = (sent / total) * 0.65);
          }
        },
      );

      // Step 2: Create post
      if (mounted) {
        setState(() {
          _progress = 0.70;
          _statusMessage = 'Creating post...';
        });
      }

      final postRes = await api.post('/v1/posts', data: {
        'content_type': 'flick',
        'media_ids': [mediaId],
        'text': widget.caption,
        'tags': widget.hashtags,
        'visibility': widget.visibility,
      });

      _postId =
          ((postRes.data['data'] ?? postRes.data)['id'] as String?) ?? '';

      // Step 3: Poll processing status
      if (mounted) {
        setState(() {
          _progress = 0.75;
          _statusMessage = 'Processing video...';
        });
      }

      for (int i = 0; i < 30; i++) {
        await Future.delayed(const Duration(seconds: 2));
        if (!mounted) break;
        try {
          final statusRes = await api.get('/v1/media/$mediaId/status');
          final payload =
              (statusRes.data['data'] ?? statusRes.data) as Map<String, dynamic>? ?? {};
          final status =
              (payload['upload_status'] as String?) ?? 'processing';
          final serverProgress = (payload['progress'] as int?) ?? 0;
          if (mounted) {
            setState(
                () => _progress = 0.75 + (serverProgress / 100.0) * 0.24);
          }
          if (status == 'ready' || status == 'failed') break;
        } catch (_) {
          // transient poll error — continue
        }
      }

      if (mounted) {
        setState(() {
          _progress = 1.0;
          _isDone = true;
          _statusMessage = 'Done!';
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _hasError = true;
          _errorMessage = 'Upload failed: $e';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      body: SafeArea(
        child: Center(
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 32),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                if (!_isDone && !_hasError) ...[
                  SizedBox(
                    width: 80,
                    height: 80,
                    child: CircularProgressIndicator(
                      value: _progress,
                      strokeWidth: 6,
                      color: _brandRed,
                      backgroundColor: Colors.white12,
                    ),
                  ),
                  const SizedBox(height: 8),
                  Text(
                    '${(_progress * 100).toInt()}%',
                    style: const TextStyle(
                      color: Colors.white,
                      fontSize: 24,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                  const SizedBox(height: 16),
                  Text(
                    _statusMessage,
                    style: const TextStyle(color: Colors.grey, fontSize: 14),
                    textAlign: TextAlign.center,
                  ),
                ],
                if (_isDone) ...[
                  const Icon(Icons.check_circle,
                      color: Colors.green, size: 60),
                  const SizedBox(height: 16),
                  const Text(
                    'Your Reel is live!',
                    style: TextStyle(
                      color: Colors.white,
                      fontSize: 24,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                  const SizedBox(height: 16),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      OutlinedButton(
                        onPressed: () =>
                            context.push('/posts/$_postId'),
                        style: OutlinedButton.styleFrom(
                          foregroundColor: Colors.white,
                          side: const BorderSide(color: Colors.white54),
                        ),
                        child: const Text('View Post'),
                      ),
                      const SizedBox(width: 12),
                      ElevatedButton(
                        onPressed: () =>
                            context.go('/reels/editor'),
                        style: ElevatedButton.styleFrom(
                          backgroundColor: _brandRed,
                          foregroundColor: Colors.white,
                        ),
                        child: const Text('Create Another'),
                      ),
                    ],
                  ),
                ],
                if (_hasError) ...[
                  const Icon(Icons.error_outline,
                      color: Colors.red, size: 60),
                  const SizedBox(height: 16),
                  Text(
                    _errorMessage,
                    style: const TextStyle(color: Colors.redAccent),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 16),
                  ElevatedButton(
                    onPressed: _startUpload,
                    style: ElevatedButton.styleFrom(
                      backgroundColor: _brandRed,
                      foregroundColor: Colors.white,
                    ),
                    child: const Text('Retry'),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
