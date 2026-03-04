import 'dart:io';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/providers/user_provider.dart';
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
  final List<XFile> _selectedMedia = [];
  String _visibility = 'public';
  String _postType = 'post';
  bool _posting = false;

  @override
  void dispose() {
    _textController.dispose();
    super.dispose();
  }

  Future<void> _pickImages() async {
    final picker = ImagePicker();
    final images = await picker.pickMultiImage();
    if (images.isNotEmpty) setState(() => _selectedMedia.addAll(images));
  }

  Future<void> _pickVideo() async {
    final picker = ImagePicker();
    final video = await picker.pickVideo(source: ImageSource.gallery);
    if (video != null) setState(() => _selectedMedia.add(video));
  }

  Future<void> _pickFromCamera() async {
    final picker = ImagePicker();
    final photo = await picker.pickImage(source: ImageSource.camera);
    if (photo != null) setState(() => _selectedMedia.add(photo));
  }

  Future<void> _submitPost() async {
    setState(() => _posting = true);
    try {
      // NOTE: Media upload requires multipart/form-data Dio setup beyond the
      // typed ApiClient. Media picker UI is fully wired; actual file upload
      // is a future improvement. Posts are created with text content only.
      await ref.read(postRepositoryProvider).createPost(
            content: _textController.text.trim(),
            contentType: _postType,
            visibility: _visibility,
          );
      if (mounted) context.pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Failed to create post. Please try again.'),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _posting = false);
    }
  }

  void _removeMedia(int index) {
    setState(() => _selectedMedia.removeAt(index));
  }

  Widget _buildPostTypeChip(String label, String value) {
    final isSelected = _postType == value;
    return GestureDetector(
      onTap: () => setState(() => _postType = value),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 200),
        margin: const EdgeInsets.only(right: AppSpacing.m),
        padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.l,
          vertical: AppSpacing.s,
        ),
        decoration: BoxDecoration(
          gradient: isSelected ? AppColors.ctaGradient : null,
          color: isSelected ? null : AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          border: Border.all(
            color: isSelected
                ? Colors.transparent
                : AppColors.borderMedium,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: isSelected ? Colors.white : AppColors.textSecondary,
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final currentUserAsync = ref.watch(currentUserProvider);
    final user = currentUserAsync.valueOrNull;
    final canPost = !_posting && _textController.text.trim().isNotEmpty;

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgSecondary,
        elevation: 0,
        leading: TextButton(
          onPressed: () => context.pop(),
          child: Text(
            'Cancel',
            style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
          ),
        ),
        leadingWidth: 80,
        title: Text('Create Post', style: AppTextStyles.h3),
        centerTitle: true,
        actions: [
          Padding(
            padding: const EdgeInsets.symmetric(
              horizontal: AppSpacing.xxl,
              vertical: AppSpacing.m,
            ),
            child: _posting
                ? const SizedBox(
                    width: 24,
                    height: 24,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      valueColor: AlwaysStoppedAnimation<Color>(
                        AppColors.postbookPrimary,
                      ),
                    ),
                  )
                : GestureDetector(
                    onTap: canPost ? _submitPost : null,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: AppSpacing.l,
                        vertical: AppSpacing.s,
                      ),
                      decoration: BoxDecoration(
                        gradient: canPost ? AppColors.ctaGradient : null,
                        color: canPost ? null : AppColors.bgTertiary,
                        borderRadius:
                            BorderRadius.circular(AppSpacing.radiusFull),
                      ),
                      child: Text(
                        'Post',
                        style: AppTextStyles.label.copyWith(
                          color: canPost
                              ? Colors.white
                              : AppColors.textMuted,
                        ),
                      ),
                    ),
                  ),
          ),
        ],
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(1),
          child: Container(
            height: 1,
            color: AppColors.borderSubtle,
          ),
        ),
      ),
      body: Column(
        children: [
          Expanded(
            child: SingleChildScrollView(
              padding: AppSpacing.pagePadding.copyWith(
                top: AppSpacing.xxl,
                bottom: AppSpacing.xxl,
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  // User avatar + display name row
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.center,
                    children: [
                      CircleAvatar(
                        radius: 22,
                        backgroundColor: AppColors.bgTertiary,
                        backgroundImage: user?.avatarMediaId != null
                            ? NetworkImage(user!.avatarUrl)
                            : null,
                        child: user?.avatarMediaId == null
                            ? Text(
                                (user?.displayName.isNotEmpty == true)
                                    ? user!.displayName[0].toUpperCase()
                                    : '?',
                                style: AppTextStyles.h3.copyWith(
                                  color: AppColors.postbookPrimary,
                                ),
                              )
                            : null,
                      ),
                      const SizedBox(width: AppSpacing.l),
                      Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            user?.displayName ?? 'You',
                            style: AppTextStyles.h3,
                          ),
                          const SizedBox(height: AppSpacing.xs),
                          // Visibility dropdown
                          Container(
                            height: 28,
                            padding: const EdgeInsets.symmetric(
                              horizontal: AppSpacing.m,
                            ),
                            decoration: BoxDecoration(
                              color: AppColors.bgTertiary,
                              borderRadius: BorderRadius.circular(
                                AppSpacing.radiusFull,
                              ),
                              border: Border.all(color: AppColors.borderMedium),
                            ),
                            child: DropdownButtonHideUnderline(
                              child: DropdownButton<String>(
                                value: _visibility,
                                isDense: true,
                                dropdownColor: AppColors.bgTertiary,
                                icon: const Icon(
                                  Icons.keyboard_arrow_down_rounded,
                                  size: 16,
                                  color: AppColors.textMuted,
                                ),
                                style: AppTextStyles.labelSmall,
                                onChanged: (v) {
                                  if (v != null) {
                                    setState(() => _visibility = v);
                                  }
                                },
                                items: const [
                                  DropdownMenuItem(
                                    value: 'public',
                                    child: Text('Public'),
                                  ),
                                  DropdownMenuItem(
                                    value: 'followers',
                                    child: Text('Followers'),
                                  ),
                                  DropdownMenuItem(
                                    value: 'friends',
                                    child: Text('Friends'),
                                  ),
                                ],
                              ),
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),

                  const SizedBox(height: AppSpacing.xxl),

                  // Post content text field
                  TextField(
                    controller: _textController,
                    maxLines: null,
                    minLines: 4,
                    style: AppTextStyles.body.copyWith(
                      fontSize: 16,
                      color: AppColors.textPrimary,
                    ),
                    decoration: InputDecoration(
                      hintText: "What's on your mind?",
                      hintStyle: AppTextStyles.body.copyWith(
                        fontSize: 16,
                        color: AppColors.textMuted,
                      ),
                      border: InputBorder.none,
                      enabledBorder: InputBorder.none,
                      focusedBorder: InputBorder.none,
                      contentPadding: EdgeInsets.zero,
                    ),
                    onChanged: (_) => setState(() {}),
                  ),

                  const SizedBox(height: AppSpacing.xxl),

                  // Post type chips
                  Text(
                    'Post type',
                    style: AppTextStyles.labelSmall,
                  ),
                  const SizedBox(height: AppSpacing.m),
                  SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: Row(
                      children: [
                        _buildPostTypeChip('Post', 'post'),
                        _buildPostTypeChip('Reel', 'reel'),
                        _buildPostTypeChip('Video', 'video'),
                      ],
                    ),
                  ),

                  // Media preview
                  if (_selectedMedia.isNotEmpty) ...[
                    const SizedBox(height: AppSpacing.xxl),
                    Text(
                      'Media',
                      style: AppTextStyles.labelSmall,
                    ),
                    const SizedBox(height: AppSpacing.m),
                    SizedBox(
                      height: 80,
                      child: ListView.builder(
                        scrollDirection: Axis.horizontal,
                        itemCount: _selectedMedia.length,
                        itemBuilder: (context, index) {
                          final xfile = _selectedMedia[index];
                          return Container(
                            margin: const EdgeInsets.only(right: AppSpacing.m),
                            width: 80,
                            height: 80,
                            child: Stack(
                              children: [
                                ClipRRect(
                                  borderRadius: BorderRadius.circular(
                                    AppSpacing.radiusSmall,
                                  ),
                                  child: Image.file(
                                    File(xfile.path),
                                    width: 80,
                                    height: 80,
                                    fit: BoxFit.cover,
                                    errorBuilder: (_, _, _) => Container(
                                      width: 80,
                                      height: 80,
                                      color: AppColors.bgTertiary,
                                      child: const Icon(
                                        Icons.videocam_rounded,
                                        color: AppColors.textMuted,
                                        size: 32,
                                      ),
                                    ),
                                  ),
                                ),
                                Positioned(
                                  top: 2,
                                  right: 2,
                                  child: GestureDetector(
                                    onTap: () => _removeMedia(index),
                                    child: Container(
                                      width: 20,
                                      height: 20,
                                      decoration: const BoxDecoration(
                                        color: Colors.black54,
                                        shape: BoxShape.circle,
                                      ),
                                      child: const Icon(
                                        Icons.close_rounded,
                                        color: Colors.white,
                                        size: 14,
                                      ),
                                    ),
                                  ),
                                ),
                              ],
                            ),
                          );
                        },
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ),

          // Bottom media toolbar
          Container(
            padding: EdgeInsets.only(
              left: AppSpacing.xxl,
              right: AppSpacing.xxl,
              top: AppSpacing.l,
              bottom:
                  MediaQuery.of(context).viewInsets.bottom + AppSpacing.l,
            ),
            decoration: const BoxDecoration(
              color: AppColors.bgSecondary,
              border: Border(
                top: BorderSide(color: AppColors.borderSubtle),
              ),
            ),
            child: Row(
              children: [
                IconButton(
                  icon: const Icon(
                    Icons.image_outlined,
                    color: AppColors.textSecondary,
                  ),
                  tooltip: 'Add photos',
                  onPressed: _pickImages,
                ),
                IconButton(
                  icon: const Icon(
                    Icons.videocam_outlined,
                    color: AppColors.textSecondary,
                  ),
                  tooltip: 'Add video',
                  onPressed: _pickVideo,
                ),
                IconButton(
                  icon: const Icon(
                    Icons.camera_alt_outlined,
                    color: AppColors.textSecondary,
                  ),
                  tooltip: 'Take photo',
                  onPressed: _pickFromCamera,
                ),
                const Spacer(),
                if (_selectedMedia.isNotEmpty)
                  Text(
                    '${_selectedMedia.length} file${_selectedMedia.length == 1 ? '' : 's'}',
                    style: AppTextStyles.labelSmall,
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
