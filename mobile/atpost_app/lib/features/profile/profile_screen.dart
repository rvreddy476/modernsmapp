import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final _myPostsProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/posts',
    queryParameters: {'author_id': 'me', 'limit': 30},
  );
  final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
  return items.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();
});

class ProfileScreen extends ConsumerWidget {
  const ProfileScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final userAsync = ref.watch(currentUserProvider);

    return SafeArea(
      child: Padding(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
        child: userAsync.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Text('Could not load profile', style: AppTextStyles.bodySmall),
          ),
          data: (user) => Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    width: 64,
                    height: 64,
                    decoration: BoxDecoration(
                      gradient: AppColors.postbookGradient,
                      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    ),
                    child: Center(
                      child: Text(
                        user.displayName.isNotEmpty
                            ? user.displayName[0].toUpperCase()
                            : 'U',
                        style: AppTextStyles.h2.copyWith(color: Colors.white),
                      ),
                    ),
                  ),
                  const SizedBox(width: 14),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(user.displayName, style: AppTextStyles.h2),
                        const SizedBox(height: 2),
                        Text('@${user.username}', style: AppTextStyles.bodySmall),
                        if (user.profession != null && user.profession!.isNotEmpty) ...[
                          const SizedBox(height: 2),
                          Text(
                            user.profession!,
                            style: AppTextStyles.bodySmall.copyWith(
                              color: AppColors.textDim,
                            ),
                          ),
                        ],
                      ],
                    ),
                  ),
                  GestureDetector(
                    onTap: () => context.push('/settings/profile'),
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                        border: Border.all(color: AppColors.borderSubtle),
                      ),
                      child: Text('Edit Profile', style: AppTextStyles.label),
                    ),
                  ),
                  const SizedBox(width: 4),
                  IconButton(
                    icon: const Icon(Icons.settings_outlined, color: AppColors.textSecondary),
                    onPressed: () => context.push('/settings'),
                    tooltip: 'Settings',
                  ),
                ],
              ),
              if (user.bio != null && user.bio!.isNotEmpty) ...[
                const SizedBox(height: 12),
                Text(
                  user.bio!,
                  style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
                ),
              ],
              const SizedBox(height: 16),
              Row(
                children: [
                  _StatBadge(label: 'Followers', count: user.followerCount),
                  const SizedBox(width: 10),
                  _StatBadge(label: 'Following', count: user.followingCount),
                  const SizedBox(width: 10),
                  _StatBadge(label: 'Friends', count: user.friendCount),
                ],
              ),
              const SizedBox(height: 12),
              GestureDetector(
                onTap: () => context.push('/bookmarks'),
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.bookmark_outline, color: AppColors.textSecondary, size: 16),
                      const SizedBox(width: 6),
                      Text('Bookmarks', style: AppTextStyles.label),
                    ],
                  ),
                ),
              ),
              const SizedBox(height: 12),
              Expanded(
                child: ref.watch(_myPostsProvider).when(
                  loading: () => const Center(child: CircularProgressIndicator()),
                  error: (_, _) => Center(
                    child: Text('Could not load posts', style: AppTextStyles.bodySmall),
                  ),
                  data: (posts) => posts.isEmpty
                      ? Center(
                          child: Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              const Icon(Icons.grid_on_outlined, color: AppColors.textMuted, size: 32),
                              const SizedBox(height: 8),
                              Text('Share your first post!', style: AppTextStyles.bodySmall),
                            ],
                          ),
                        )
                      : GridView.builder(
                          gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                            crossAxisCount: 3,
                            mainAxisSpacing: 2,
                            crossAxisSpacing: 2,
                          ),
                          itemCount: posts.length,
                          itemBuilder: (_, i) {
                            final post = posts[i];
                            return Container(
                              color: AppColors.bgCard,
                              child: Center(
                                child: Icon(
                                  post.isReel
                                      ? Icons.play_circle_outline
                                      : post.isVideo
                                          ? Icons.videocam_outlined
                                          : Icons.image_outlined,
                                  color: AppColors.textMuted,
                                ),
                              ),
                            );
                          },
                        ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _StatBadge extends StatelessWidget {
  const _StatBadge({required this.label, required this.count});

  final String label;
  final int count;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Text(_formatCount(count), style: AppTextStyles.h3),
          const SizedBox(height: 2),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }
}
