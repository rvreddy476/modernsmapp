import 'package:atpost_app/providers/editor_provider.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

enum _FlicksAudience { public, friends, private }

class ReelsCaptionScreen extends ConsumerStatefulWidget {
  const ReelsCaptionScreen({super.key});

  @override
  ConsumerState<ReelsCaptionScreen> createState() => _ReelsCaptionScreenState();
}

class _ReelsCaptionScreenState extends ConsumerState<ReelsCaptionScreen> {
  static const _brandRed = Color(0xFFD8103F);

  final _captionCtrl = TextEditingController();
  final _hashtagCtrl = TextEditingController();

  final List<String> _hashtags = [];
  _FlicksAudience _audience = _FlicksAudience.public;
  bool _isPosting = false;
  // Tier 2c — when non-null, the post is queued for cmd/scheduler to
  // promote at this moment. UTC-aware: we send RFC3339 to the
  // backend; the picker shows local time to the creator.
  DateTime? _scheduleAt;

  static const _suggestedTags = [
    '#reels', '#viral', '#fyp', '#trending', '#explore',
    '#video', '#creator', '#atpost',
  ];

  @override
  void dispose() {
    _captionCtrl.dispose();
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

  Future<void> _submit() async {
    final editorState = ref.read(editorProvider);
    if (editorState.clips.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Add at least one clip before posting.')),
      );
      return;
    }

    setState(() => _isPosting = true);
    try {
      final api = ref.read(apiClientProvider);

      // Upload the first clip as video media
      String mediaId = '';
      final firstClip = editorState.clips.first;
      mediaId = await api.uploadMedia(
        XFile(firstClip.filePath),
        type: 'video',
      );

      // Embed hashtags into the caption text so the backend's
      // extractHashtags() indexes them into posts.hashtags[]. Sending them in
      // the `tags` field would land in reel-metadata categories, not the
      // hashtag index → trending/hashtag-feed wouldn't see them.
      final caption = _captionCtrl.text.trim();
      final tagLine = _hashtags.join(' ');
      final fullText = [caption, tagLine].where((s) => s.isNotEmpty).join('\n\n');

      // Create the post. Include audio_track_id when the editor selected
      // background music — post-service AttachAudioToPost links it to the
      // new post on the server side.
      final body = <String, dynamic>{
        'content_type': 'flick',
        'media_ids': mediaId.isNotEmpty ? [mediaId] : [],
        'text': fullText,
        'visibility': _audience.name,
        'cover_frame_ms': editorState.coverFrameMs,
        'filter': editorState.activeFilter.name,
      };
      final audio = editorState.backgroundAudio;
      if (audio != null && audio.id.isNotEmpty) {
        body['audio_track_id'] = audio.id;
      }
      // Tier 2c: when a future schedule is set, save as draft +
      // schedule_at (the cmd/scheduler worker promotes it). When
      // unset, publish immediately via /v1/posts.
      if (_scheduleAt != null && _scheduleAt!.isAfter(DateTime.now())) {
        final draftBody = Map<String, dynamic>.from(body)
          ..['schedule_at'] = _scheduleAt!.toUtc().toIso8601String();
        await api.post('/v1/drafts', data: draftBody);
      } else {
        await api.post('/v1/posts', data: body);
      }

      ref.read(editorProvider.notifier).deleteSession();

      // Drop cached video/reel/home feeds so the new flick shows up immediately
      // when the user lands on /reels or /. Without this, autoDispose's first
      // build is served from the prior cached future.
      ref.invalidate(reelFeedProvider);
      ref.invalidate(videoFeedProvider);
      try {
        await ref.read(homeFeedProvider.notifier).fetchFirstPage();
      } catch (_) {/* non-fatal */}

      if (mounted) context.go('/');
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to post: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _isPosting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios, color: Colors.white),
          onPressed: () => context.pop(),
        ),
        title: const Text(
          'New Reel',
          style: TextStyle(color: Colors.white, fontWeight: FontWeight.w700),
        ),
        centerTitle: true,
      ),
      body: GestureDetector(
        onTap: () => FocusScope.of(context).unfocus(),
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _buildCaption(),
              const SizedBox(height: 20),
              _buildHashtagsSection(),
              const SizedBox(height: 20),
              _buildAudienceSelector(),
              const SizedBox(height: 20),
              _buildScheduleRow(),
              const SizedBox(height: 20),
              _buildCrossPostRow(),
              const SizedBox(height: 32),
              _buildPostButton(),
              const SizedBox(height: 16),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildCaption() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text(
          'Caption',
          style: TextStyle(color: Colors.white54, fontSize: 12, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 8),
        Container(
          decoration: BoxDecoration(
            color: Colors.white10,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: Colors.white12),
          ),
          child: TextField(
            controller: _captionCtrl,
            style: const TextStyle(color: Colors.white, fontSize: 15),
            maxLines: 4,
            maxLength: 500,
            decoration: const InputDecoration(
              hintText: 'Write a caption…',
              hintStyle: TextStyle(color: Colors.white38),
              border: InputBorder.none,
              contentPadding: EdgeInsets.all(14),
              counterStyle: TextStyle(color: Colors.white24),
            ),
          ),
        ),
      ],
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
        // Existing hashtag chips
        if (_hashtags.isNotEmpty)
          Wrap(
            spacing: 8,
            runSpacing: 4,
            children: _hashtags.map((tag) {
              return Chip(
                label: Text(tag, style: const TextStyle(color: Colors.white, fontSize: 12)),
                backgroundColor: _brandRed.withValues(alpha: 0.25),
                deleteIcon: const Icon(Icons.close, size: 14, color: Colors.white54),
                onDeleted: () => _removeHashtag(tag),
                side: BorderSide.none,
                visualDensity: VisualDensity.compact,
              );
            }).toList(),
          ),
        const SizedBox(height: 8),
        // Tag input
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
                  color: _brandRed,
                  borderRadius: BorderRadius.circular(10),
                ),
                child: const Text('Add', style: TextStyle(color: Colors.white, fontWeight: FontWeight.w600, fontSize: 13)),
              ),
            ),
          ],
        ),
        const SizedBox(height: 10),
        // Suggested tags
        Wrap(
          spacing: 6,
          runSpacing: 4,
          children: _suggestedTags.where((t) => !_hashtags.contains(t)).map((tag) {
            return GestureDetector(
              onTap: () => _addHashtag(tag),
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
                decoration: BoxDecoration(
                  color: Colors.white10,
                  borderRadius: BorderRadius.circular(14),
                  border: Border.all(color: Colors.white12),
                ),
                child: Text(tag, style: const TextStyle(color: Colors.white54, fontSize: 11)),
              ),
            );
          }).toList(),
        ),
      ],
    );
  }

  Widget _buildAudienceSelector() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text(
          'Audience',
          style: TextStyle(color: Colors.white54, fontSize: 12, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 8),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 14),
          decoration: BoxDecoration(
            color: Colors.white10,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: Colors.white12),
          ),
          child: DropdownButton<_FlicksAudience>(
            value: _audience,
            isExpanded: true,
            dropdownColor: const Color(0xFF1A1A2E),
            underline: const SizedBox.shrink(),
            style: const TextStyle(color: Colors.white),
            icon: const Icon(Icons.keyboard_arrow_down, color: Colors.white54),
            items: _FlicksAudience.values.map((a) {
              final label = switch (a) {
                _FlicksAudience.public => 'Public',
                _FlicksAudience.friends => 'Friends',
                _FlicksAudience.private => 'Private',
              };
              final icon = switch (a) {
                _FlicksAudience.public => Icons.public,
                _FlicksAudience.friends => Icons.people,
                _FlicksAudience.private => Icons.lock,
              };
              return DropdownMenuItem(
                value: a,
                child: Row(
                  children: [
                    Icon(icon, color: Colors.white54, size: 18),
                    const SizedBox(width: 10),
                    Text(label),
                  ],
                ),
              );
            }).toList(),
            onChanged: (v) {
              if (v != null) setState(() => _audience = v);
            },
          ),
        ),
      ],
    );
  }

  Future<void> _pickScheduleAt() async {
    final now = DateTime.now();
    // Default to 1 hour out so the picker doesn't immediately fail
    // the past-time validator on the backend.
    final initial = _scheduleAt ?? now.add(const Duration(hours: 1));

    final pickedDate = await showDatePicker(
      context: context,
      initialDate: initial,
      firstDate: now,
      lastDate: now.add(const Duration(days: 365)),
    );
    if (pickedDate == null || !mounted) return;
    final pickedTime = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(initial),
    );
    if (pickedTime == null || !mounted) return;

    final combined = DateTime(
      pickedDate.year, pickedDate.month, pickedDate.day,
      pickedTime.hour, pickedTime.minute,
    );
    if (combined.isBefore(now)) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Scheduled time must be in the future.')),
        );
      }
      return;
    }
    setState(() => _scheduleAt = combined);
  }

  Widget _buildScheduleRow() {
    final hasSchedule = _scheduleAt != null;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            const Text(
              'Schedule',
              style: TextStyle(color: Colors.white54, fontSize: 12, fontWeight: FontWeight.w600),
            ),
            const Spacer(),
            Switch(
              value: hasSchedule,
              activeThumbColor: _brandRed,
              onChanged: (on) {
                if (on) {
                  _pickScheduleAt();
                } else {
                  setState(() => _scheduleAt = null);
                }
              },
            ),
          ],
        ),
        if (hasSchedule) ...[
          const SizedBox(height: 4),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
            decoration: BoxDecoration(
              color: Colors.white10,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: Colors.white12),
            ),
            child: Row(
              children: [
                const Icon(Icons.schedule, color: Colors.white54, size: 18),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(
                    _formatScheduledLocal(_scheduleAt!),
                    style: const TextStyle(color: Colors.white),
                  ),
                ),
                TextButton(
                  onPressed: _pickScheduleAt,
                  child: const Text('Change'),
                ),
              ],
            ),
          ),
          const SizedBox(height: 4),
          const Text(
            'The post will be published automatically at this time.',
            style: TextStyle(color: Colors.white38, fontSize: 11),
          ),
        ],
      ],
    );
  }

  String _formatScheduledLocal(DateTime dt) {
    // Tight, non-locale-y format so it matches across devices and is
    // unambiguous about timezone (always local).
    String two(int n) => n.toString().padLeft(2, '0');
    return '${dt.year}-${two(dt.month)}-${two(dt.day)} '
        '${two(dt.hour)}:${two(dt.minute)}';
  }

  Widget _buildCrossPostRow() {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: BoxDecoration(
        color: Colors.white10,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.white12),
      ),
      child: Row(
        children: [
          const Icon(Icons.share, color: Colors.white38, size: 20),
          const SizedBox(width: 12),
          const Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Cross-post', style: TextStyle(color: Colors.white, fontSize: 14, fontWeight: FontWeight.w600)),
                Text('Share to other feeds', style: TextStyle(color: Colors.white38, fontSize: 12)),
              ],
            ),
          ),
          Switch(
            value: false,
            onChanged: null, // disabled for now
            activeThumbColor: _brandRed,
          ),
        ],
      ),
    );
  }

  Widget _buildPostButton() {
    return SizedBox(
      width: double.infinity,
      height: 52,
      child: ElevatedButton(
        style: ElevatedButton.styleFrom(
          backgroundColor: _brandRed,
          disabledBackgroundColor: _brandRed.withValues(alpha: 0.4),
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          elevation: 0,
        ),
        onPressed: _isPosting ? null : _submit,
        child: _isPosting
            ? const SizedBox(
                width: 22,
                height: 22,
                child: CircularProgressIndicator(color: Colors.white, strokeWidth: 2),
              )
            : const Text(
                'Post Reel',
                style: TextStyle(
                  color: Colors.white,
                  fontSize: 16,
                  fontWeight: FontWeight.w700,
                ),
              ),
      ),
    );
  }
}
