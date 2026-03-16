import 'dart:io';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/create/providers/creation_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

class CreatePostScreen extends ConsumerStatefulWidget {
  const CreatePostScreen({super.key});

  @override
  ConsumerState<CreatePostScreen> createState() => _CreatePostScreenState();
}

class _CreatePostScreenState extends ConsumerState<CreatePostScreen> {
  final TextEditingController _textController = TextEditingController();
  bool _loadingCaptions = false;
  bool _loadingHashtags = false;
  List<String> _captionSuggestions = [];
  List<String> _hashtagSuggestions = [];

  @override
  void initState() {
    super.initState();
    // Sync local controller with provider state if needed
    _textController.addListener(() {
      ref.read(creationProvider.notifier).setText(_textController.text);
    });
  }

  @override
  void dispose() {
    _textController.dispose();
    super.dispose();
  }

  Future<void> _pickImages() async {
    final images = await ImagePicker().pickMultiImage();
    if (images.isNotEmpty) {
      ref.read(creationProvider.notifier).addFiles(images);
      ref.read(creationProvider.notifier).setType(PostType.photo);
    }
  }

  Future<void> _pickVideo() async {
    final video = await ImagePicker().pickVideo(source: ImageSource.gallery);
    if (video != null) {
      ref.read(creationProvider.notifier).addFiles([video]);
      ref.read(creationProvider.notifier).setType(PostType.video);
    }
  }

  void _handlePost() async {
    final success = await ref.read(creationProvider.notifier).submit();
    if (success && mounted) {
      context.pop();
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(creationProvider);
    final notifier = ref.read(creationProvider.notifier);
    final user = ref.watch(currentUserProvider).valueOrNull;

    // Sync external changes (like AI enhancement) back to text controller
    if (_textController.text != state.text) {
      _textController.text = state.text;
    }

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            colors: [Color(0xFF090A14), Color(0xFF08080F), Color(0xFF0B0D18)],
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
          ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              _buildHeader(state),
              _buildTypeSelector(state, notifier),
              Expanded(
                child: SingleChildScrollView(
                  padding: AppSpacing.pagePadding,
                  child: Column(
                    children: [
                      _buildComposerCard(state, user),
                      if (state.type == PostType.poll) _buildPollEditor(state, notifier),
                      if (state.files.isNotEmpty) _buildMediaPreview(state, notifier),
                      if (state.error != null)
                        Padding(
                          padding: const EdgeInsets.only(top: 12),
                          child: Text(state.error!, style: const TextStyle(color: Colors.redAccent)),
                        ),
                    ],
                  ),
                ),
              ),
              _buildBottomToolbar(state, notifier),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(CreationState state) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          IconButton(
            icon: const Icon(Icons.close, color: Colors.white),
            onPressed: () => context.pop(),
          ),
          const Spacer(),
          if (state.isSubmitting)
            const CircularProgressIndicator(strokeWidth: 2)
          else
            ElevatedButton(
              onPressed: (state.text.isNotEmpty || state.files.isNotEmpty) ? _handlePost : null,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
              ),
              child: const Text('Post', style: TextStyle(color: Colors.white)),
            ),
        ],
      ),
    );
  }

  Widget _buildTypeSelector(CreationState state, CreationNotifier notifier) {
    return SizedBox(
      height: 50,
      child: ListView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        children: PostType.values.map((type) {
          final isSelected = state.type == type;
          return Padding(
            padding: const EdgeInsets.only(right: 8),
            child: ChoiceChip(
              label: Text(type.name.toUpperCase()),
              selected: isSelected,
              onSelected: (_) => notifier.setType(type),
              selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
              labelStyle: TextStyle(
                color: isSelected ? AppColors.postbookPrimary : Colors.grey,
                fontWeight: FontWeight.bold,
                fontSize: 12,
              ),
            ),
          );
        }).toList(),
      ),
    );
  }

  Widget _buildComposerCard(CreationState state, dynamic user) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Row(
            children: [
              CircleAvatar(radius: 20, backgroundImage: user?.avatarUrl != null ? NetworkImage(user!.avatarUrl) : null),
              const SizedBox(width: 12),
              Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(user?.displayName ?? 'User', style: AppTextStyles.h3),
                  _buildVisibilityButton(state),
                ],
              ),
              const Spacer(),
              // AI Sparkle Button
              IconButton(
                icon: state.isGeneratingAi
                  ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                  : const Icon(Icons.auto_awesome, color: Colors.amber),
                onPressed: () => ref.read(creationProvider.notifier).enhanceWithAi(),
                tooltip: 'Enhance with AI',
              ),
            ],
          ),
          const SizedBox(height: 16),
          TextField(
            controller: _textController,
            maxLines: null,
            style: const TextStyle(color: Colors.white, fontSize: 18),
            decoration: const InputDecoration(
              hintText: "What's happening?",
              hintStyle: TextStyle(color: Colors.grey),
              border: InputBorder.none,
            ),
          ),
          const SizedBox(height: 8),
          // AI suggestion buttons
          Row(
            children: [
              TextButton.icon(
                onPressed: _loadingCaptions ? null : _fetchCaptionSuggestions,
                icon: _loadingCaptions
                    ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.auto_awesome, size: 14, color: Colors.amber),
                label: const Text('Captions', style: TextStyle(fontSize: 12)),
              ),
              TextButton.icon(
                onPressed: _loadingHashtags ? null : _fetchHashtagSuggestions,
                icon: _loadingHashtags
                    ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.tag, size: 14, color: Colors.blue),
                label: const Text('Hashtags', style: TextStyle(fontSize: 12)),
              ),
              if (_captionSuggestions.isNotEmpty || _hashtagSuggestions.isNotEmpty)
                IconButton(
                  icon: const Icon(Icons.close, size: 16),
                  onPressed: () => setState(() { _captionSuggestions = []; _hashtagSuggestions = []; }),
                  tooltip: 'Clear suggestions',
                ),
            ],
          ),
          if (_captionSuggestions.isNotEmpty) ...[
            const Divider(),
            ...(_captionSuggestions.map((s) => ListTile(
              dense: true,
              contentPadding: EdgeInsets.zero,
              title: Text(s, style: const TextStyle(fontSize: 13)),
              trailing: TextButton(
                onPressed: () { _textController.text = s; setState(() => _captionSuggestions = []); },
                child: const Text('Use'),
              ),
            ))),
          ],
          if (_hashtagSuggestions.isNotEmpty) ...[
            const Divider(),
            Wrap(
              spacing: 6,
              children: _hashtagSuggestions.map((tag) => ActionChip(
                label: Text(tag, style: const TextStyle(fontSize: 12)),
                onPressed: () {
                  final current = _textController.text;
                  _textController.text = current.isEmpty ? tag : '$current $tag';
                },
              )).toList(),
            ),
          ],
        ],
      ),
    );
  }

  Future<void> _fetchCaptionSuggestions() async {
    setState(() { _loadingCaptions = true; _captionSuggestions = []; });
    try {
      final res = await ref.read(apiClientProvider).post('/v1/ai/caption-suggestions', data: {
        'ref_id': 'new', 'ref_type': 'post', 'context_text': _textController.text,
      });
      final data = res.data['data'] ?? res.data;
      setState(() => _captionSuggestions = List<String>.from(data['captions'] ?? []));
    } catch (_) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Could not load suggestions')));
    } finally {
      if (mounted) setState(() => _loadingCaptions = false);
    }
  }

  Future<void> _fetchHashtagSuggestions() async {
    setState(() { _loadingHashtags = true; _hashtagSuggestions = []; });
    try {
      final res = await ref.read(apiClientProvider).post('/v1/ai/hashtag-suggestions', data: {
        'ref_id': 'new', 'ref_type': 'post', 'context_text': _textController.text,
      });
      final data = res.data['data'] ?? res.data;
      setState(() => _hashtagSuggestions = List<String>.from(data['hashtags'] ?? []));
    } catch (_) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Could not load hashtags')));
    } finally {
      if (mounted) setState(() => _loadingHashtags = false);
    }
  }

  Widget _buildVisibilityButton(CreationState state) {
    return PopupMenuButton<PostVisibility>(
      initialValue: state.visibility,
      onSelected: ref.read(creationProvider.notifier).setVisibility,
      itemBuilder: (context) => PostVisibility.values.map((v) =>
        PopupMenuItem(value: v, child: Text(v.name.toUpperCase()))
      ).toList(),
      child: Row(
        children: [
          Text(state.visibility.name.toUpperCase(), style: AppTextStyles.labelSmall),
          const Icon(Icons.arrow_drop_down, size: 16, color: Colors.grey),
        ],
      ),
    );
  }

  Widget _buildPollEditor(CreationState state, CreationNotifier notifier) {
    return Container(
      margin: const EdgeInsets.only(top: 16),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(color: AppColors.bgCard, borderRadius: BorderRadius.circular(20)),
      child: Column(
        children: [
          ...state.pollOptions.asMap().entries.map((e) => Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: TextField(
              onChanged: (val) => notifier.updatePollOption(e.key, val),
              style: const TextStyle(color: Colors.white),
              decoration: InputDecoration(
                hintText: 'Option ${e.key + 1}',
                suffixIcon: state.pollOptions.length > 2 ? IconButton(
                  icon: const Icon(Icons.remove_circle, color: Colors.red),
                  onPressed: () => notifier.removePollOption(e.key),
                ) : null,
              ),
            ),
          )),
          if (state.pollOptions.length < 5)
            TextButton.icon(
              onPressed: notifier.addPollOption,
              icon: const Icon(Icons.add),
              label: const Text('Add Option'),
            ),
        ],
      ),
    );
  }

  Widget _buildMediaPreview(CreationState state, CreationNotifier notifier) {
    return Container(
      height: 120,
      margin: const EdgeInsets.only(top: 16),
      child: ListView.builder(
        scrollDirection: Axis.horizontal,
        itemCount: state.files.length,
        itemBuilder: (context, index) => Stack(
          children: [
            Container(
              width: 100,
              margin: const EdgeInsets.only(right: 8),
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(12),
                image: DecorationImage(image: FileImage(File(state.files[index].path)), fit: BoxFit.cover),
              ),
            ),
            Positioned(
              right: 4, top: 4,
              child: GestureDetector(
                onTap: () => notifier.removeFile(index),
                child: const CircleAvatar(radius: 10, backgroundColor: Colors.black54, child: Icon(Icons.close, size: 12, color: Colors.white)),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildBottomToolbar(CreationState state, CreationNotifier notifier) {
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 16),
      decoration: const BoxDecoration(border: Border(top: BorderSide(color: AppColors.borderSubtle))),
      child: Row(
        children: [
          IconButton(icon: const Icon(Icons.image, color: Colors.blue), onPressed: _pickImages),
          IconButton(icon: const Icon(Icons.videocam, color: Colors.red), onPressed: _pickVideo),
          IconButton(
            icon: const Icon(Icons.movie_edit, color: Colors.amber),
            tooltip: 'Flicks Editor',
            onPressed: () => context.push('/flicks/editor'),
          ),
          IconButton(icon: const Icon(Icons.poll, color: Colors.purple), onPressed: () => notifier.setType(PostType.poll)),
          IconButton(icon: const Icon(Icons.location_on, color: Colors.green), onPressed: () {}),
          const Spacer(),
          Text('${state.text.length}/3000', style: AppTextStyles.labelTiny),
        ],
      ),
    );
  }
}
