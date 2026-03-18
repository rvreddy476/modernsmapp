import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group_post.dart';
import 'package:atpost_app/data/repositories/group_posts_repository.dart';
import 'package:atpost_app/providers/group_posts_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class GroupPostComposerScreen extends ConsumerStatefulWidget {
  final String groupId;

  const GroupPostComposerScreen({super.key, required this.groupId});

  @override
  ConsumerState<GroupPostComposerScreen> createState() =>
      _GroupPostComposerScreenState();
}

class _GroupPostComposerScreenState
    extends ConsumerState<GroupPostComposerScreen> {
  final _bodyController = TextEditingController();
  final _titleController = TextEditingController();
  GroupChannel? _selectedChannel;
  bool _posting = false;

  @override
  void dispose() {
    _bodyController.dispose();
    _titleController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final body = _bodyController.text.trim();
    if (body.isEmpty || _posting) return;

    setState(() => _posting = true);

    try {
      final repo = ref.read(groupPostsRepositoryProvider);
      final title = _titleController.text.trim();
      await repo.createPost(
        widget.groupId,
        body: body,
        channelId: _selectedChannel?.id,
        title: title.isNotEmpty ? title : null,
      );
      // Invalidate the feed so it refreshes
      ref.invalidate(groupPostsProvider);
      if (mounted) context.pop();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to create post.')),
        );
      }
    } finally {
      if (mounted) setState(() => _posting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final channelsAsync = ref.watch(groupChannelsProvider(widget.groupId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.close, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('New Post', style: AppTextStyles.h2),
        actions: [
          Padding(
            padding: const EdgeInsets.only(right: 12),
            child: _posting
                ? const Center(
                    child: SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  )
                : Container(
                    decoration: BoxDecoration(
                      gradient: AppColors.postbookGradient,
                      borderRadius: BorderRadius.circular(20),
                    ),
                    child: TextButton(
                      onPressed: _submit,
                      style: TextButton.styleFrom(
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(
                            horizontal: 16, vertical: 4),
                        minimumSize: Size.zero,
                        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                      ),
                      child: Text('Post', style: AppTextStyles.label),
                    ),
                  ),
          ),
        ],
      ),
      body: SingleChildScrollView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 32),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Channel dropdown
            channelsAsync.when(
              loading: () => const SizedBox.shrink(),
              error: (_, _) => const SizedBox.shrink(),
              data: (channels) {
                if (channels.isEmpty) return const SizedBox.shrink();
                return Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusMedium),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: DropdownButtonHideUnderline(
                      child: DropdownButton<GroupChannel?>(
                        isExpanded: true,
                        value: _selectedChannel,
                        hint: Text('Select channel',
                            style: AppTextStyles.body
                                .copyWith(color: AppColors.textMuted)),
                        dropdownColor: AppColors.bgSecondary,
                        iconEnabledColor: AppColors.textMuted,
                        items: [
                          const DropdownMenuItem<GroupChannel?>(
                            value: null,
                            child: Text('All channels'),
                          ),
                          ...channels.map(
                            (ch) => DropdownMenuItem<GroupChannel?>(
                              value: ch,
                              child: Text(
                                '#${ch.name}',
                                style: AppTextStyles.body,
                              ),
                            ),
                          ),
                        ],
                        onChanged: (ch) =>
                            setState(() => _selectedChannel = ch),
                      ),
                    ),
                  ),
                );
              },
            ),

            // Title field
            TextField(
              controller: _titleController,
              style: AppTextStyles.h3,
              decoration: InputDecoration(
                hintText: 'Title (optional)',
                hintStyle:
                    AppTextStyles.h3.copyWith(color: AppColors.textDim),
                border: InputBorder.none,
                contentPadding: EdgeInsets.zero,
              ),
              maxLines: 1,
            ),
            const Divider(color: AppColors.borderSubtle, height: 16),

            // Body field
            TextField(
              controller: _bodyController,
              style: AppTextStyles.body,
              decoration: InputDecoration(
                hintText: 'What would you like to share?',
                hintStyle:
                    AppTextStyles.body.copyWith(color: AppColors.textDim),
                border: InputBorder.none,
                contentPadding: EdgeInsets.zero,
              ),
              maxLines: null,
              minLines: 8,
              keyboardType: TextInputType.multiline,
              autofocus: true,
            ),
          ],
        ),
      ),

      // Attachment bar
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          color: AppColors.bgSecondary,
          border:
              Border(top: BorderSide(color: AppColors.borderSubtle)),
        ),
        padding: EdgeInsets.only(
          left: 12,
          right: 12,
          top: 8,
          bottom: MediaQuery.of(context).viewPadding.bottom + 8,
        ),
        child: Row(
          children: [
            _AttachmentIcon(
              icon: Icons.camera_alt_outlined,
              onTap: () {},
            ),
            _AttachmentIcon(
              icon: Icons.photo_library_outlined,
              onTap: () {},
            ),
            _AttachmentIcon(
              icon: Icons.poll_outlined,
              onTap: () {},
            ),
            _AttachmentIcon(
              icon: Icons.event_outlined,
              onTap: () {},
            ),
            _AttachmentIcon(
              icon: Icons.attach_file_outlined,
              onTap: () {},
            ),
          ],
        ),
      ),
    );
  }
}

class _AttachmentIcon extends StatelessWidget {
  final IconData icon;
  final VoidCallback onTap;

  const _AttachmentIcon({required this.icon, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return IconButton(
      onPressed: onTap,
      icon: Icon(icon, color: AppColors.textSecondary, size: 22),
      splashRadius: 20,
    );
  }
}
