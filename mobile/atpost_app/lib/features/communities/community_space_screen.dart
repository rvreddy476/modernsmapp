import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/repositories/community_posts_repository.dart';
import 'package:atpost_app/providers/community_posts_provider.dart';
import 'package:atpost_app/shared/widgets/community_post_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CommunitySpaceScreen extends ConsumerStatefulWidget {
  final String communityId;
  final String spaceId;

  const CommunitySpaceScreen({
    super.key,
    required this.communityId,
    required this.spaceId,
  });

  @override
  ConsumerState<CommunitySpaceScreen> createState() =>
      _CommunitySpaceScreenState();
}

class _CommunitySpaceScreenState
    extends ConsumerState<CommunitySpaceScreen> {
  final _composerController = TextEditingController();
  bool _posting = false;

  @override
  void dispose() {
    _composerController.dispose();
    super.dispose();
  }

  Future<void> _submitPost() async {
    final body = _composerController.text.trim();
    if (body.isEmpty || _posting) return;

    setState(() => _posting = true);
    try {
      await ref.read(communityPostsRepositoryProvider).createPost(
            widget.communityId,
            widget.spaceId,
            body: body,
          );
      _composerController.clear();
      ref.invalidate(communityPostsProvider(CommunityPostsParams(
        communityId: widget.communityId,
        spaceId: widget.spaceId,
      )));
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

  IconData _iconForType(String type) {
    return switch (type) {
      'group' => Icons.group,
      'channel' => Icons.campaign,
      'discussion' => Icons.forum,
      'events' => Icons.event,
      'resources' => Icons.folder_open,
      _ => Icons.space_dashboard,
    };
  }

  @override
  Widget build(BuildContext context) {
    final spacesAsync =
        ref.watch(communitySpacesListProvider(widget.communityId));
    final postsAsync = ref.watch(communityPostsProvider(
      CommunityPostsParams(
        communityId: widget.communityId,
        spaceId: widget.spaceId,
      ),
    ));

    // Find the current space from the list
    CommunitySpace? currentSpace;
    spacesAsync.whenData((spaces) {
      for (final s in spaces) {
        if (s.id == widget.spaceId) {
          currentSpace = s;
          break;
        }
      }
    });

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Row(
          children: [
            if (currentSpace != null)
              Icon(
                _iconForType(currentSpace!.spaceType),
                color: AppColors.postbookPrimary,
                size: 20,
              ),
            if (currentSpace != null) const SizedBox(width: 8),
            Expanded(
              child: Text(
                currentSpace?.name ?? 'Space',
                style: AppTextStyles.h2,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
      body: postsAsync.when(
        loading: () => const Center(
          child:
              CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.error_outline,
                  color: AppColors.textDim, size: 40),
              const SizedBox(height: 12),
              Text('Failed to load posts', style: AppTextStyles.body),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () => ref.invalidate(
                  communityPostsProvider(CommunityPostsParams(
                    communityId: widget.communityId,
                    spaceId: widget.spaceId,
                  )),
                ),
                child: Text('Retry',
                    style: AppTextStyles.label
                        .copyWith(color: AppColors.postbookPrimary)),
              ),
            ],
          ),
        ),
        data: (posts) {
          if (posts.isEmpty) {
            return Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.forum_outlined,
                      color: AppColors.textDim, size: 48),
                  const SizedBox(height: 12),
                  Text(
                    'No posts in this space yet.',
                    style: AppTextStyles.body
                        .copyWith(color: AppColors.textSecondary),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'Be the first to start a discussion!',
                    style: AppTextStyles.bodySmall
                        .copyWith(color: AppColors.textMuted),
                  ),
                ],
              ),
            );
          }

          return ListView.separated(
            padding:
                AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
            itemCount: posts.length,
            separatorBuilder: (_, _) => const SizedBox(height: 10),
            itemBuilder: (context, index) {
              final post = posts[index];
              return CommunityPostCard(
                post: post,
                onSpark: () {
                  ref.read(communityPostsRepositoryProvider).sparkPost(
                        widget.communityId,
                        widget.spaceId,
                        post.id,
                      );
                },
              );
            },
          );
        },
      ),

      // Composer input at bottom
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          color: AppColors.bgSecondary,
          border: Border(top: BorderSide(color: AppColors.borderSubtle)),
        ),
        padding: EdgeInsets.only(
          left: 12,
          right: 8,
          top: 8,
          bottom: MediaQuery.of(context).viewPadding.bottom + 8,
        ),
        child: Row(
          children: [
            Expanded(
              child: TextField(
                controller: _composerController,
                style: AppTextStyles.body,
                decoration: InputDecoration(
                  hintText: 'Write something...',
                  hintStyle: AppTextStyles.body
                      .copyWith(color: AppColors.textDim),
                  filled: true,
                  fillColor: AppColors.bgTertiary,
                  contentPadding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 10),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(24),
                    borderSide: BorderSide.none,
                  ),
                ),
                maxLines: 3,
                minLines: 1,
              ),
            ),
            const SizedBox(width: 6),
            _posting
                ? const Padding(
                    padding: EdgeInsets.all(8),
                    child: SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: AppColors.postbookPrimary),
                    ),
                  )
                : IconButton(
                    onPressed: _submitPost,
                    icon: const Icon(Icons.send_rounded,
                        color: AppColors.postbookPrimary),
                    splashRadius: 20,
                  ),
          ],
        ),
      ),
    );
  }
}
