import 'package:atpost_app/providers/editor_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

enum _FlicksAudience { public, friends, private }

class FlicksCaptionScreen extends ConsumerStatefulWidget {
  const FlicksCaptionScreen({super.key});

  @override
  ConsumerState<FlicksCaptionScreen> createState() => _FlicksCaptionScreenState();
}

class _FlicksCaptionScreenState extends ConsumerState<FlicksCaptionScreen> {
  static const _brandRed = Color(0xFFD8103F);

  final _captionCtrl = TextEditingController();
  final _hashtagCtrl = TextEditingController();

  final List<String> _hashtags = [];
  _FlicksAudience _audience = _FlicksAudience.public;
  bool _isPosting = false;

  static const _suggestedTags = [
    '#flicks', '#viral', '#fyp', '#trending', '#explore',
    '#reels', '#video', '#creator', '#atpost',
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

      // Create the post
      await api.post('/v1/posts', data: {
        'content_type': 'flick',
        'media_ids': mediaId.isNotEmpty ? [mediaId] : [],
        'text': _captionCtrl.text.trim(),
        'tags': _hashtags,
        'visibility': _audience.name,
        'cover_frame_ms': editorState.coverFrameMs,
        'filter': editorState.activeFilter.name,
      });

      ref.read(editorProvider.notifier).deleteSession();
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
          'New Flick',
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
                'Post Flick',
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
