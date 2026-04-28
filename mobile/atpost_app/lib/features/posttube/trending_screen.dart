import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Trending screen: long-form videos sorted by the engagement-score formula
/// (like + comment*2 + share*3 + bookmark*2 + views*0.1 - reports*5 +
/// freshness boost) computed by post-service `/v1/posts/trending`.
///
/// Same content_type filter the backend uses, so this screen only shows
/// long_video. A future Reels trending tab can hit the same endpoint with
/// content_type=flick.
final _trendingFutureProvider = FutureProvider.autoDispose<List<Post>>((
  ref,
) async {
  final api = ref.watch(apiClientProvider);
  final res = await api.get(
    '/v1/posts/trending',
    queryParameters: {'content_type': 'long_video', 'limit': 30},
  );
  final raw = res.data;
  final list = (raw is Map && raw['data'] is Map && raw['data']['items'] is List)
      ? raw['data']['items'] as List
      : (raw is Map && raw['data'] is List ? raw['data'] as List : const []);
  return list
      .whereType<Map>()
      .map((e) => Post.fromJson(Map<String, dynamic>.from(e)))
      .toList();
});

class PosttubeTrendingScreen extends ConsumerWidget {
  const PosttubeTrendingScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(_trendingFutureProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: const Text('Trending'),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
      ),
      body: RefreshIndicator(
        onRefresh: () async => ref.refresh(_trendingFutureProvider),
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
                        'Nothing trending yet — check back once people start engaging with new uploads.',
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
              itemBuilder: (_, i) => _TrendingCard(post: videos[i], rank: i + 1),
            );
          },
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                'Could not load trending videos.',
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

class _TrendingCard extends StatelessWidget {
  const _TrendingCard({required this.post, required this.rank});
  final Post post;
  final int rank;

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
          // Rank indicator on the left, gradient-tinted, so users can see at
          // a glance where this video sits in the trending list.
          Container(
            width: 40,
            alignment: Alignment.center,
            child: Text(
              '#$rank',
              style: AppTextStyles.h2.copyWith(
                color: rank <= 3
                    ? AppColors.posttubePrimary
                    : AppColors.textDim,
                fontWeight: FontWeight.w800,
              ),
            ),
          ),
          ClipRRect(
            borderRadius: BorderRadius.circular(12),
            child: SizedBox(
              width: 110,
              height: 70,
              child: thumb != null
                  ? Image.network(
                      thumb,
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) =>
                          Container(color: AppColors.bgTertiary),
                    )
                  : Container(color: AppColors.bgTertiary),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 10),
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
                    '${post.authorName ?? 'Creator'} • ${post.likeCount} likes • ${post.commentCount} comments',
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
