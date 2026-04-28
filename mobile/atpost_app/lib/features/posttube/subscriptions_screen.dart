import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Posttube subscriptions feed: long-form videos posted by creators the
/// current user follows, newest first. Backend already supports
/// `following_only=true` on `/v1/feed/watch` so we just thread the flag
/// through `FeedRepository.getVideoFeedPage`.
final _subscriptionsFutureProvider = FutureProvider.autoDispose<List<Post>>((
  ref,
) async {
  final repo = ref.watch(feedRepositoryProvider);
  final page = await repo.getVideoFeedPage(followingOnly: true);
  return page.items;
});

class PosttubeSubscriptionsScreen extends ConsumerWidget {
  const PosttubeSubscriptionsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(_subscriptionsFutureProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: const Text('Subscriptions'),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
      ),
      body: RefreshIndicator(
        onRefresh: () async => ref.refresh(_subscriptionsFutureProvider),
        child: async.when(
          data: (videos) {
            if (videos.isEmpty) {
              return ListView(
                children: [
                  const SizedBox(height: 80),
                  Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        "You don't follow any video creators yet.\nFollow someone and their uploads will show up here.",
                        textAlign: TextAlign.center,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textDim,
                        ),
                      ),
                    ),
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: AppSpacing.pagePadding,
              itemCount: videos.length,
              separatorBuilder: (_, _) => const SizedBox(height: 12),
              itemBuilder: (_, i) => _RowCard(post: videos[i]),
            );
          },
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                'Could not load subscriptions.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textDim,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

// Wider thumbnail row, optimized for vertical scanning of recent uploads.
class _RowCard extends StatelessWidget {
  const _RowCard({required this.post});
  final Post post;

  @override
  Widget build(BuildContext context) {
    final thumb = post.firstMediaUrl.isNotEmpty
        ? '${Environment.apiBaseUrl}${post.firstMediaUrl}'
        : null;
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          ClipRRect(
            borderRadius: const BorderRadius.horizontal(
              left: Radius.circular(20),
            ),
            child: SizedBox(
              width: 130,
              height: 80,
              child: thumb != null
                  ? Image.network(
                      thumb,
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) => Container(color: AppColors.bgTertiary),
                    )
                  : Container(color: AppColors.bgTertiary),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 4),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(
                    post.content.isEmpty ? 'Untitled' : post.content,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.label,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    '${post.authorName ?? 'Creator'} • ${post.likeCount} likes',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.monoSmall.copyWith(
                      color: AppColors.textDim,
                    ),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(width: 12),
        ],
      ),
    );
  }
}
