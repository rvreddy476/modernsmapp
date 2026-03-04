import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/providers/bookmarks_provider.dart';
import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class BookmarksScreen extends ConsumerWidget {
  const BookmarksScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final bookmarksAsync = ref.watch(bookmarksProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        title: Text('Bookmarks', style: AppTextStyles.h3),
        leading: const BackButton(color: AppColors.textSecondary),
      ),
      body: bookmarksAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, _) => Center(
          child: Text('Could not load bookmarks', style: AppTextStyles.bodySmall),
        ),
        data: (posts) {
          if (posts.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(32),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(
                      Icons.bookmark_border,
                      color: AppColors.textMuted,
                      size: 56,
                    ),
                    const SizedBox(height: 16),
                    Text(
                      'No bookmarks yet. Start saving posts!',
                      style: AppTextStyles.body,
                      textAlign: TextAlign.center,
                    ),
                  ],
                ),
              ),
            );
          }

          return GridView.builder(
            padding: const EdgeInsets.all(2),
            gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
              crossAxisCount: 3,
              crossAxisSpacing: 2,
              mainAxisSpacing: 2,
            ),
            itemCount: posts.length,
            itemBuilder: (context, index) {
              final post = posts[index];
              return _BookmarkCell(
                post: post,
                onUnbookmark: () async {
                  final confirmed = await showDialog<bool>(
                    context: context,
                    builder: (ctx) => AlertDialog(
                      backgroundColor: AppColors.bgSecondary,
                      title: Text('Remove bookmark?', style: AppTextStyles.h3),
                      content: Text(
                        'This post will be removed from your bookmarks.',
                        style: AppTextStyles.body,
                      ),
                      actions: [
                        TextButton(
                          onPressed: () => Navigator.of(ctx).pop(false),
                          child: Text('Cancel', style: AppTextStyles.label),
                        ),
                        TextButton(
                          onPressed: () => Navigator.of(ctx).pop(true),
                          child: Text(
                            'Remove',
                            style: AppTextStyles.label.copyWith(
                              color: Colors.redAccent,
                            ),
                          ),
                        ),
                      ],
                    ),
                  );
                  if (confirmed == true) {
                    await ref.read(postRepositoryProvider).toggleBookmark(post.id);
                    ref.invalidate(bookmarksProvider);
                  }
                },
              );
            },
          );
        },
      ),
    );
  }
}

class _BookmarkCell extends StatelessWidget {
  const _BookmarkCell({required this.post, required this.onUnbookmark});

  final Post post;
  final VoidCallback onUnbookmark;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: () {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(post.content, maxLines: 2)),
        );
      },
      onLongPress: onUnbookmark,
      child: AspectRatio(
        aspectRatio: 1,
        child: post.mediaIds.isNotEmpty
            ? CachedNetworkImage(
                imageUrl:
                    '${Environment.apiBaseUrl}/v1/media/${post.mediaIds.first}',
                fit: BoxFit.cover,
                placeholder: (_, _) => _PlaceholderCell(post: post),
                errorWidget: (_, _, _) => _PlaceholderCell(post: post),
              )
            : _PlaceholderCell(post: post),
      ),
    );
  }
}

class _PlaceholderCell extends StatelessWidget {
  const _PlaceholderCell({required this.post});

  final Post post;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            AppColors.bgTertiary,
            AppColors.bgSecondary,
          ],
        ),
      ),
      child: Stack(
        fit: StackFit.expand,
        children: [
          Padding(
            padding: const EdgeInsets.all(8),
            child: Text(
              post.content,
              style: AppTextStyles.labelSmall,
              maxLines: 4,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          Positioned(
            bottom: 6,
            right: 6,
            child: Icon(
              post.isVideo
                  ? Icons.videocam
                  : post.isReel
                      ? Icons.loop
                      : Icons.article_outlined,
              color: AppColors.textMuted,
              size: 14,
            ),
          ),
        ],
      ),
    );
  }
}
