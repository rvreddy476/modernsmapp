import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/providers/comments_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CommentsScreen extends ConsumerStatefulWidget {
  const CommentsScreen({super.key, required this.postId});

  final String postId;

  @override
  ConsumerState<CommentsScreen> createState() => _CommentsScreenState();
}

class _CommentsScreenState extends ConsumerState<CommentsScreen> {
  final TextEditingController _commentController = TextEditingController();
  bool _submitting = false;

  @override
  void dispose() {
    _commentController.dispose();
    super.dispose();
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inSeconds < 60) return '${diff.inSeconds}s';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    return '${diff.inDays}d';
  }

  Future<void> _submitComment() async {
    final text = _commentController.text.trim();
    if (text.isEmpty) return;
    setState(() => _submitting = true);
    try {
      await ref.read(postRepositoryProvider).addComment(widget.postId, text);
      _commentController.clear();
      ref.invalidate(commentsProvider(widget.postId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to post comment.')),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  Future<void> _confirmDelete(Comment comment) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Delete comment?', style: AppTextStyles.h3),
        content: Text(
          'This action cannot be undone.',
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(
              'Cancel',
              style: AppTextStyles.body.copyWith(
                color: AppColors.textSecondary,
              ),
            ),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(
              'Delete',
              style: AppTextStyles.body.copyWith(color: AppColors.liveRed),
            ),
          ),
        ],
      ),
    );
    if (confirmed == true) {
      try {
        await ref.read(postRepositoryProvider).deleteComment(comment.id);
        ref.invalidate(commentsProvider(widget.postId));
      } catch (_) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Failed to delete comment.')),
          );
        }
      }
    }
  }

  Future<void> _toggleCommentLike(Comment comment) async {
    try {
      await ref.read(postRepositoryProvider).toggleCommentLike(comment.id);
      ref.invalidate(commentsProvider(widget.postId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to update comment reaction.')),
        );
      }
    }
  }

  Widget _buildCommentTile(Comment comment) {
    final currentUser = ref.read(currentUserProvider).valueOrNull;
    final isOwn = currentUser != null && comment.authorId == currentUser.id;

    final initials = (comment.authorName?.isNotEmpty == true)
        ? comment.authorName![0].toUpperCase()
        : '?';

    return GestureDetector(
      onLongPress: isOwn ? () => _confirmDelete(comment) : null,
      child: Padding(
        padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.xxl,
          vertical: AppSpacing.l,
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Avatar
            comment.authorAvatar != null
                ? CircleAvatar(
                    radius: 18,
                    backgroundImage: NetworkImage(comment.authorAvatar!),
                    backgroundColor: AppColors.bgTertiary,
                  )
                : CircleAvatar(
                    radius: 18,
                    backgroundColor: AppColors.bgTertiary,
                    child: Text(
                      initials,
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  ),

            const SizedBox(width: AppSpacing.l),

            // Content
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  // Username + time
                  Row(
                    children: [
                      Flexible(
                        child: Text(
                          comment.authorName ?? 'Unknown',
                          style: AppTextStyles.h3.copyWith(fontSize: 13),
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      const SizedBox(width: AppSpacing.m),
                      Text(
                        _timeAgo(comment.createdAt),
                        style: AppTextStyles.labelSmall,
                      ),
                    ],
                  ),

                  const SizedBox(height: AppSpacing.xs),

                  // Comment text
                  Text(
                    comment.text,
                    style: AppTextStyles.body.copyWith(
                      color: AppColors.textSecondary,
                    ),
                  ),

                  const SizedBox(height: AppSpacing.s),

                  // Like count
                  InkWell(
                    onTap: () => _toggleCommentLike(comment),
                    borderRadius: BorderRadius.circular(999),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 2,
                        vertical: 4,
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(
                            Icons.favorite_border_rounded,
                            size: 14,
                            color: AppColors.textMuted,
                          ),
                          if (comment.likeCount > 0) ...[
                            const SizedBox(width: AppSpacing.xs),
                            Text(
                              '${comment.likeCount}',
                              style: AppTextStyles.labelSmall,
                            ),
                          ],
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final commentsAsync = ref.watch(commentsProvider(widget.postId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgSecondary,
        elevation: 0,
        title: Text('Comments', style: AppTextStyles.h3),
        centerTitle: true,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textSecondary,
            size: 20,
          ),
          onPressed: () => Navigator.of(context).maybePop(),
        ),
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(1),
          child: Container(height: 1, color: AppColors.borderSubtle),
        ),
      ),
      body: Column(
        children: [
          // Comments list
          Expanded(
            child: commentsAsync.when(
              loading: () => const Center(
                child: CircularProgressIndicator(
                  valueColor: AlwaysStoppedAnimation<Color>(
                    AppColors.postbookPrimary,
                  ),
                ),
              ),
              error: (_, _) => Center(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(
                      Icons.error_outline_rounded,
                      color: AppColors.textMuted,
                      size: 40,
                    ),
                    const SizedBox(height: AppSpacing.l),
                    Text(
                      'Failed to load comments.',
                      style: AppTextStyles.body.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                    const SizedBox(height: AppSpacing.l),
                    TextButton(
                      onPressed: () =>
                          ref.invalidate(commentsProvider(widget.postId)),
                      child: Text(
                        'Retry',
                        style: AppTextStyles.label.copyWith(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
              data: (comments) {
                if (comments.isEmpty) {
                  return Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(
                          Icons.chat_bubble_outline_rounded,
                          color: AppColors.textMuted,
                          size: 40,
                        ),
                        const SizedBox(height: AppSpacing.l),
                        Text(
                          'No comments yet.',
                          style: AppTextStyles.body.copyWith(
                            color: AppColors.textMuted,
                          ),
                        ),
                        const SizedBox(height: AppSpacing.xs),
                        Text(
                          'Be the first to comment!',
                          style: AppTextStyles.labelSmall,
                        ),
                      ],
                    ),
                  );
                }

                return ListView.separated(
                  itemCount: comments.length,
                  separatorBuilder: (_, _) => Container(
                    height: 1,
                    margin: const EdgeInsets.symmetric(
                      horizontal: AppSpacing.xxl,
                    ),
                    color: AppColors.borderSubtle,
                  ),
                  itemBuilder: (context, index) =>
                      _buildCommentTile(comments[index]),
                );
              },
            ),
          ),

          // Input bar
          Container(
            padding: EdgeInsets.only(
              left: AppSpacing.xxl,
              right: AppSpacing.m,
              top: AppSpacing.l,
              bottom: MediaQuery.of(context).viewInsets.bottom + AppSpacing.l,
            ),
            decoration: const BoxDecoration(
              color: AppColors.bgSecondary,
              border: Border(top: BorderSide(color: AppColors.borderSubtle)),
            ),
            child: Row(
              children: [
                Expanded(
                  child: Container(
                    decoration: BoxDecoration(
                      color: AppColors.bgTertiary,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge,
                      ),
                      border: Border.all(color: AppColors.borderMedium),
                    ),
                    child: TextField(
                      controller: _commentController,
                      style: AppTextStyles.body,
                      maxLines: 4,
                      minLines: 1,
                      textCapitalization: TextCapitalization.sentences,
                      decoration: InputDecoration(
                        hintText: 'Add a comment...',
                        hintStyle: AppTextStyles.body.copyWith(
                          color: AppColors.textMuted,
                        ),
                        border: InputBorder.none,
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: AppSpacing.l,
                          vertical: AppSpacing.m,
                        ),
                      ),
                    ),
                  ),
                ),
                const SizedBox(width: AppSpacing.s),
                _submitting
                    ? const SizedBox(
                        width: 40,
                        height: 40,
                        child: Padding(
                          padding: EdgeInsets.all(8),
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            valueColor: AlwaysStoppedAnimation<Color>(
                              AppColors.postbookPrimary,
                            ),
                          ),
                        ),
                      )
                    : IconButton(
                        icon: const Icon(
                          Icons.send_rounded,
                          color: AppColors.postbookPrimary,
                        ),
                        onPressed: _submitComment,
                        tooltip: 'Post comment',
                      ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
