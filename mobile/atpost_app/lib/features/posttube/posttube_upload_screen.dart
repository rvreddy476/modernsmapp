import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

const _brandRed = Color(0xFFD8103F);

const _categories = [
  'Education',
  'Entertainment',
  'News',
  'Sports',
  'Music',
  'Gaming',
  'Technology',
  'Other',
];

class PosttubeUploadScreen extends ConsumerStatefulWidget {
  const PosttubeUploadScreen({super.key});

  @override
  ConsumerState<PosttubeUploadScreen> createState() =>
      _PosttubeUploadScreenState();
}

class _PosttubeUploadScreenState
    extends ConsumerState<PosttubeUploadScreen> {
  int _step = 0;
  XFile? _videoFile;

  // Step 1 trim fields
  final TextEditingController _trimStartCtrl = TextEditingController();
  final TextEditingController _trimEndCtrl = TextEditingController();

  // Step 2 metadata
  final TextEditingController _titleCtrl = TextEditingController();
  final TextEditingController _descCtrl = TextEditingController();
  final TextEditingController _tagsCtrl = TextEditingController();
  String _category = 'Entertainment';
  bool _schedule = false;
  DateTime? _scheduleTime;
  int _selectedThumbnail = 0; // 0=auto1, 1=auto2, 2=auto3, 3=custom

  // Step 3 upload
  double _uploadProgress = 0.0;
  String _uploadStatus = '';
  bool _uploadDone = false;
  bool _uploadError = false;
  String _errorMessage = '';
  String _postId = '';

  @override
  void dispose() {
    _trimStartCtrl.dispose();
    _trimEndCtrl.dispose();
    _titleCtrl.dispose();
    _descCtrl.dispose();
    _tagsCtrl.dispose();
    super.dispose();
  }

  Future<void> _pickVideo() async {
    final picked =
        await ImagePicker().pickVideo(source: ImageSource.gallery);
    if (picked != null && mounted) {
      setState(() => _videoFile = picked);
    }
  }

  Future<void> _pickCustomThumbnail() async {
    final picked = await ImagePicker().pickImage(source: ImageSource.gallery);
    if (picked != null && mounted) {
      setState(() => _selectedThumbnail = 3);
    }
  }

  Future<void> _selectScheduleDateTime() async {
    final date = await showDatePicker(
      context: context,
      initialDate: DateTime.now().add(const Duration(hours: 1)),
      firstDate: DateTime.now(),
      lastDate: DateTime.now().add(const Duration(days: 365)),
    );
    if (date == null || !mounted) return;

    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.now(),
    );
    if (time == null || !mounted) return;

    setState(() {
      _scheduleTime = DateTime(
          date.year, date.month, date.day, time.hour, time.minute);
    });
  }

  Future<void> _startUpload() async {
    setState(() {
      _uploadProgress = 0;
      _uploadError = false;
      _uploadDone = false;
      _uploadStatus = 'Preparing...';
    });

    try {
      setState(() => _uploadStatus = 'Uploading video...');
      final api = ref.read(apiClientProvider);

      final mediaId = await api.uploadMedia(
        _videoFile!,
        type: 'video',
        onProgress: (int sent, int total) {
          if (mounted && total > 0) {
            setState(() => _uploadProgress = (sent / total) * 0.7);
          }
        },
      );

      if (mounted) {
        setState(() {
          _uploadProgress = 0.75;
          _uploadStatus = 'Creating post...';
        });
      }

      final tags = _tagsCtrl.text
          .split(',')
          .map((t) => t.trim())
          .where((t) => t.isNotEmpty)
          .toList();

      final body = <String, dynamic>{
        'content_type': 'video',
        'media_ids': [mediaId],
        'title': _titleCtrl.text.trim(),
        'text': _descCtrl.text.trim(),
        'tags': tags,
        'category': _category,
      };

      if (_schedule && _scheduleTime != null) {
        body['scheduled_at'] = _scheduleTime!.toIso8601String();
      }

      final postRes = await api.post('/v1/posts', data: body);
      _postId =
          ((postRes.data['data'] ?? postRes.data)['id'] as String?) ?? '';

      if (mounted) {
        setState(() {
          _uploadProgress = 1.0;
          _uploadDone = true;
          _uploadStatus = 'Done!';
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _uploadError = true;
          _errorMessage = 'Upload failed: $e';
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
        foregroundColor: Colors.white,
        title: const Text('Upload to Posttube'),
        leading: IconButton(
          icon: const Icon(Icons.close),
          onPressed: () => context.pop(),
        ),
      ),
      body: SafeArea(
        child: _step == 0
            ? _buildStepSelectVideo()
            : _step == 1
                ? _buildStepDetails()
                : _buildStepUpload(),
      ),
    );
  }

  // ─── Step 1: Select Video ───────────────────────────────────────────────────

  Widget _buildStepSelectVideo() {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _buildStepIndicator(0),
          const SizedBox(height: 24),
          const Text(
            'Select Video',
            style: TextStyle(
                color: Colors.white, fontSize: 22, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 24),
          ElevatedButton.icon(
            icon: const Icon(Icons.video_library),
            label: const Text('Choose Video'),
            style: ElevatedButton.styleFrom(
              backgroundColor: _brandRed,
              foregroundColor: Colors.white,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            onPressed: _pickVideo,
          ),
          if (_videoFile != null) ...[
            const SizedBox(height: 16),
            Container(
              padding: const EdgeInsets.all(16),
              decoration: BoxDecoration(
                color: Colors.white10,
                borderRadius: BorderRadius.circular(12),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    _videoFile!.name,
                    style: const TextStyle(
                        color: Colors.white, fontWeight: FontWeight.w600),
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 4),
                  const Text(
                    'Video selected',
                    style: TextStyle(color: Colors.grey, fontSize: 12),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 24),
            const Text(
              'Or trim before upload',
              style: TextStyle(color: Colors.white70, fontSize: 14),
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _trimStartCtrl,
                    style: const TextStyle(color: Colors.white),
                    keyboardType: TextInputType.number,
                    decoration: _inputDecoration('Start (sec)'),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: TextField(
                    controller: _trimEndCtrl,
                    style: const TextStyle(color: Colors.white),
                    keyboardType: TextInputType.number,
                    decoration: _inputDecoration('End (sec)'),
                  ),
                ),
              ],
            ),
          ],
          const SizedBox(height: 32),
          ElevatedButton(
            onPressed: _videoFile != null
                ? () => setState(() => _step = 1)
                : null,
            style: ElevatedButton.styleFrom(
              backgroundColor: _brandRed,
              foregroundColor: Colors.white,
              padding: const EdgeInsets.symmetric(vertical: 14),
              disabledBackgroundColor: Colors.white12,
            ),
            child: const Text('Next: Add Details'),
          ),
        ],
      ),
    );
  }

  // ─── Step 2: Metadata ───────────────────────────────────────────────────────

  Widget _buildStepDetails() {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _buildStepIndicator(1),
          const SizedBox(height: 24),
          const Text(
            'Add Details',
            style: TextStyle(
                color: Colors.white, fontSize: 22, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 20),
          TextField(
            controller: _titleCtrl,
            style: const TextStyle(color: Colors.white),
            decoration: _inputDecoration('Title (required)'),
          ),
          const SizedBox(height: 16),
          TextField(
            controller: _descCtrl,
            style: const TextStyle(color: Colors.white),
            maxLines: 4,
            decoration: _inputDecoration('Description'),
          ),
          const SizedBox(height: 16),
          DropdownButtonFormField<String>(
            initialValue: _category,
            dropdownColor: const Color(0xFF1A1A2E),
            style: const TextStyle(color: Colors.white),
            decoration: _inputDecoration('Category'),
            items: _categories
                .map((c) => DropdownMenuItem(value: c, child: Text(c)))
                .toList(),
            onChanged: (v) {
              if (v != null) setState(() => _category = v);
            },
          ),
          const SizedBox(height: 16),
          TextField(
            controller: _tagsCtrl,
            style: const TextStyle(color: Colors.white),
            decoration: _inputDecoration('Tags (comma-separated)'),
          ),
          const SizedBox(height: 16),
          Row(
            children: [
              const Text('Schedule?',
                  style: TextStyle(color: Colors.white70)),
              const Spacer(),
              Switch(
                value: _schedule,
                onChanged: (v) => setState(() => _schedule = v),
                activeThumbColor: _brandRed,
              ),
            ],
          ),
          if (_schedule) ...[
            const SizedBox(height: 8),
            GestureDetector(
              onTap: _selectScheduleDateTime,
              child: Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 16, vertical: 12),
                decoration: BoxDecoration(
                  color: Colors.white10,
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(color: Colors.white24),
                ),
                child: Row(
                  children: [
                    const Icon(Icons.calendar_today,
                        color: Colors.white54, size: 18),
                    const SizedBox(width: 8),
                    Text(
                      _scheduleTime != null
                          ? '${_scheduleTime!.year}-${_scheduleTime!.month.toString().padLeft(2, '0')}-${_scheduleTime!.day.toString().padLeft(2, '0')} '
                              '${_scheduleTime!.hour.toString().padLeft(2, '0')}:${_scheduleTime!.minute.toString().padLeft(2, '0')}'
                          : 'Pick date & time',
                      style: const TextStyle(color: Colors.white70),
                    ),
                  ],
                ),
              ),
            ),
          ],
          const SizedBox(height: 24),
          const Text('Thumbnail',
              style: TextStyle(color: Colors.white70, fontSize: 14)),
          const SizedBox(height: 12),
          Row(
            children: [
              for (int i = 0; i < 3; i++) ...[
                _buildThumbnailOption(i, 'Auto ${i + 1}'),
                const SizedBox(width: 8),
              ],
              _buildThumbnailOption(3, 'Custom',
                  onTap: _pickCustomThumbnail),
            ],
          ),
          const SizedBox(height: 32),
          Row(
            children: [
              OutlinedButton(
                onPressed: () => setState(() => _step = 0),
                style: OutlinedButton.styleFrom(
                  foregroundColor: Colors.white,
                  side: const BorderSide(color: Colors.white54),
                  padding: const EdgeInsets.symmetric(
                      horizontal: 24, vertical: 14),
                ),
                child: const Text('Back'),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: ElevatedButton(
                  onPressed: _titleCtrl.text.trim().isNotEmpty
                      ? () {
                          setState(() => _step = 2);
                          _startUpload();
                        }
                      : null,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: _brandRed,
                    foregroundColor: Colors.white,
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    disabledBackgroundColor: Colors.white12,
                  ),
                  child: const Text('Upload'),
                ),
              ),
            ],
          ),
          const SizedBox(height: 16),
        ],
      ),
    );
  }

  Widget _buildThumbnailOption(int index, String label,
      {VoidCallback? onTap}) {
    final isSelected = _selectedThumbnail == index;
    return GestureDetector(
      onTap: onTap ?? () => setState(() => _selectedThumbnail = index),
      child: Container(
        width: 72,
        height: 48,
        decoration: BoxDecoration(
          color: isSelected ? _brandRed.withValues(alpha: 0.3) : Colors.white10,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isSelected ? _brandRed : Colors.white24,
            width: isSelected ? 2 : 1,
          ),
        ),
        child: Center(
          child: Text(
            label,
            style: TextStyle(
              color: isSelected ? Colors.white : Colors.white54,
              fontSize: 10,
              fontWeight: FontWeight.w600,
            ),
            textAlign: TextAlign.center,
          ),
        ),
      ),
    );
  }

  // ─── Step 3: Upload Progress ────────────────────────────────────────────────

  Widget _buildStepUpload() {
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 32),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            if (!_uploadDone && !_uploadError) ...[
              SizedBox(
                width: 80,
                height: 80,
                child: CircularProgressIndicator(
                  value: _uploadProgress,
                  strokeWidth: 6,
                  color: _brandRed,
                  backgroundColor: Colors.white12,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                '${(_uploadProgress * 100).toInt()}%',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 24,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 16),
              Text(
                _uploadStatus,
                style: const TextStyle(color: Colors.grey, fontSize: 14),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 24),
              const Text(
                'Video processing can take 5-10 minutes.\nYou can close this screen and we\'ll notify you.',
                style: TextStyle(color: Colors.white38, fontSize: 12),
                textAlign: TextAlign.center,
              ),
            ],
            if (_uploadDone) ...[
              const Icon(Icons.check_circle, color: Colors.green, size: 60),
              const SizedBox(height: 16),
              const Text(
                'Your video is live!',
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
                    onPressed: () => context.push('/posts/$_postId'),
                    style: OutlinedButton.styleFrom(
                      foregroundColor: Colors.white,
                      side: const BorderSide(color: Colors.white54),
                    ),
                    child: const Text('View Post'),
                  ),
                  const SizedBox(width: 12),
                  ElevatedButton(
                    onPressed: () => context.go('/posttube'),
                    style: ElevatedButton.styleFrom(
                      backgroundColor: _brandRed,
                      foregroundColor: Colors.white,
                    ),
                    child: const Text('Go to Posttube'),
                  ),
                ],
              ),
            ],
            if (_uploadError) ...[
              const Icon(Icons.error_outline, color: Colors.red, size: 60),
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
    );
  }

  // ─── Step indicator ─────────────────────────────────────────────────────────

  Widget _buildStepIndicator(int activeStep) {
    final labels = ['Select Video', 'Add Details', 'Upload'];
    return Row(
      children: List.generate(labels.length, (i) {
        final isActive = i == activeStep;
        final isDone = i < activeStep;
        return Expanded(
          child: Row(
            children: [
              Container(
                width: 28,
                height: 28,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: isDone
                      ? Colors.green
                      : isActive
                          ? _brandRed
                          : Colors.white12,
                ),
                child: Center(
                  child: isDone
                      ? const Icon(Icons.check, color: Colors.white, size: 14)
                      : Text(
                          '${i + 1}',
                          style: TextStyle(
                            color: isActive ? Colors.white : Colors.white38,
                            fontSize: 12,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                ),
              ),
              const SizedBox(width: 6),
              Flexible(
                child: Text(
                  labels[i],
                  style: TextStyle(
                    color: isActive ? Colors.white : Colors.white38,
                    fontSize: 12,
                    fontWeight:
                        isActive ? FontWeight.w600 : FontWeight.normal,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (i < labels.length - 1)
                Expanded(
                  child: Container(
                    height: 1,
                    color: Colors.white12,
                    margin: const EdgeInsets.symmetric(horizontal: 4),
                  ),
                ),
            ],
          ),
        );
      }),
    );
  }

  // ─── Helper ─────────────────────────────────────────────────────────────────

  InputDecoration _inputDecoration(String hint) {
    return InputDecoration(
      hintText: hint,
      hintStyle: const TextStyle(color: Colors.white38),
      filled: true,
      fillColor: Colors.white10,
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(8),
        borderSide: const BorderSide(color: Colors.white24),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(8),
        borderSide: const BorderSide(color: Colors.white24),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(8),
        borderSide: const BorderSide(color: _brandRed),
      ),
    );
  }
}
