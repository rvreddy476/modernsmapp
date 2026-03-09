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

class ProfileScreen extends ConsumerStatefulWidget {
  const ProfileScreen({super.key});

  @override
  ConsumerState<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends ConsumerState<ProfileScreen> {
  _PostFilter _activeFilter = _PostFilter.all;

  Future<void> _refresh() async {
    ref.invalidate(currentUserProvider);
    ref.invalidate(_myPostsProvider);
  }

  List<Post> _filtered(List<Post> posts) {
    return posts.where((post) {
      return switch (_activeFilter) {
        _PostFilter.all => true,
        _PostFilter.posts => !post.isReel && !post.isVideo,
        _PostFilter.reels => post.isReel,
        _PostFilter.videos => post.isVideo,
      };
    }).toList();
  }

  @override
  Widget build(BuildContext context) {
    final userAsync = ref.watch(currentUserProvider);
    final postsAsync = ref.watch(_myPostsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: userAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(
                  Icons.person_off_outlined,
                  color: AppColors.textMuted,
                ),
                const SizedBox(height: 8),
                Text('Could not load profile', style: AppTextStyles.bodySmall),
                const SizedBox(height: 6),
                TextButton(
                  onPressed: _refresh,
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
          data: (user) {
            final filteredPosts = postsAsync.value != null
                ? _filtered(postsAsync.value!)
                : const <Post>[];

            return RefreshIndicator(
              color: AppColors.postbookPrimary,
              onRefresh: _refresh,
              child: CustomScrollView(
                physics: const AlwaysScrollableScrollPhysics(),
                slivers: [
                  SliverToBoxAdapter(
                    child: Padding(
                      padding: AppSpacing.pagePadding.copyWith(top: 12),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Container(
                            width: double.infinity,
                            padding: const EdgeInsets.all(16),
                            decoration: BoxDecoration(
                              borderRadius: BorderRadius.circular(
                                AppSpacing.radiusXL,
                              ),
                              border: Border.all(color: AppColors.borderMedium),
                              gradient: const LinearGradient(
                                colors: [
                                  Color(0x3325B2FF),
                                  Color(0x334ECDC4),
                                  Color(0x33FF6B35),
                                ],
                                begin: Alignment.topLeft,
                                end: Alignment.bottomRight,
                              ),
                            ),
                            child: Column(
                              children: [
                                Row(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    ClipRRect(
                                      borderRadius: BorderRadius.circular(20),
                                      child: user.hasAvatar
                                          ? Image.network(
                                              user.avatarUrl,
                                              width: 72,
                                              height: 72,
                                              fit: BoxFit.cover,
                                              errorBuilder: (_, _, _) =>
                                                  _AvatarFallback(
                                                    size: 72,
                                                    initial: user.displayName,
                                                    style: AppTextStyles.h2,
                                                  ),
                                            )
                                          : _AvatarFallback(
                                              size: 72,
                                              initial: user.displayName,
                                              style: AppTextStyles.h2,
                                            ),
                                    ),
                                    const SizedBox(width: 12),
                                    Expanded(
                                      child: Column(
                                        crossAxisAlignment:
                                            CrossAxisAlignment.start,
                                        children: [
                                          Row(
                                            children: [
                                              Expanded(
                                                child: Text(
                                                  user.displayName,
                                                  maxLines: 1,
                                                  overflow:
                                                      TextOverflow.ellipsis,
                                                  style: AppTextStyles.h1
                                                      .copyWith(fontSize: 28),
                                                ),
                                              ),
                                              if (user.isVerified)
                                                const Icon(
                                                  Icons.verified,
                                                  size: 18,
                                                  color:
                                                      AppColors.posttubePrimary,
                                                ),
                                            ],
                                          ),
                                          const SizedBox(height: 2),
                                          Text(
                                            '@${user.username}',
                                            style: AppTextStyles.bodySmall,
                                          ),
                                          if ((user.profession ?? '')
                                              .trim()
                                              .isNotEmpty)
                                            Padding(
                                              padding: const EdgeInsets.only(
                                                top: 2,
                                              ),
                                              child: Text(
                                                user.profession!,
                                                style: AppTextStyles.labelSmall,
                                              ),
                                            ),
                                          if ((user.location ?? '')
                                              .trim()
                                              .isNotEmpty)
                                            Padding(
                                              padding: const EdgeInsets.only(
                                                top: 2,
                                              ),
                                              child: Text(
                                                user.location!,
                                                style: AppTextStyles.labelSmall,
                                              ),
                                            ),
                                        ],
                                      ),
                                    ),
                                  ],
                                ),
                                if ((user.bio ?? '').trim().isNotEmpty) ...[
                                  const SizedBox(height: 12),
                                  Container(
                                    width: double.infinity,
                                    padding: const EdgeInsets.all(10),
                                    decoration: BoxDecoration(
                                      color: AppColors.bgCard,
                                      borderRadius: BorderRadius.circular(12),
                                      border: Border.all(
                                        color: AppColors.borderSubtle,
                                      ),
                                    ),
                                    child: Text(
                                      user.bio!,
                                      style: AppTextStyles.bodySmall.copyWith(
                                        color: AppColors.textSecondary,
                                      ),
                                    ),
                                  ),
                                ],
                                const SizedBox(height: 12),
                                Row(
                                  children: [
                                    Expanded(
                                      child: _StatChip(
                                        label: 'Followers',
                                        value: user.followerCount,
                                        onTap: () => context.push(
                                          '/followers/${user.id}',
                                        ),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _StatChip(
                                        label: 'Following',
                                        value: user.followingCount,
                                        onTap: () => context.push(
                                          '/following/${user.id}',
                                        ),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _StatChip(
                                        label: 'Friends',
                                        value: user.friendCount,
                                        onTap: () => context.push('/friends'),
                                      ),
                                    ),
                                  ],
                                ),
                                const SizedBox(height: 12),
                                Row(
                                  children: [
                                    Expanded(
                                      child: _ActionButton(
                                        icon: Icons.edit_outlined,
                                        label: 'Edit Profile',
                                        onTap: () =>
                                            context.push('/settings/profile'),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _ActionButton(
                                        icon: Icons.bookmark_border,
                                        label: 'Bookmarks',
                                        onTap: () => context.push('/bookmarks'),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _ActionButton(
                                        icon: Icons.settings_outlined,
                                        label: 'Settings',
                                        onTap: () => context.push('/settings'),
                                      ),
                                    ),
                                  ],
                                ),
                              ],
                            ),
                          ),
                          const SizedBox(height: 16),
                          Row(
                            children: [
                              Text('Your Content', style: AppTextStyles.h2),
                              const Spacer(),
                              TextButton(
                                onPressed: () => context.push('/create'),
                                child: Text(
                                  'Create',
                                  style: AppTextStyles.label.copyWith(
                                    color: AppColors.postbookPrimary,
                                  ),
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 8),
                          Wrap(
                            spacing: 8,
                            runSpacing: 8,
                            children: [
                              _FilterChip(
                                label: 'All',
                                selected: _activeFilter == _PostFilter.all,
                                onTap: () => setState(
                                  () => _activeFilter = _PostFilter.all,
                                ),
                              ),
                              _FilterChip(
                                label: 'Posts',
                                selected: _activeFilter == _PostFilter.posts,
                                onTap: () => setState(
                                  () => _activeFilter = _PostFilter.posts,
                                ),
                              ),
                              _FilterChip(
                                label: 'Reels',
                                selected: _activeFilter == _PostFilter.reels,
                                onTap: () => setState(
                                  () => _activeFilter = _PostFilter.reels,
                                ),
                              ),
                              _FilterChip(
                                label: 'Videos',
                                selected: _activeFilter == _PostFilter.videos,
                                onTap: () => setState(
                                  () => _activeFilter = _PostFilter.videos,
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 10),
                        ],
                      ),
                    ),
                  ),
                  if (postsAsync.isLoading)
                    const SliverToBoxAdapter(
                      child: Padding(
                        padding: EdgeInsets.symmetric(vertical: 24),
                        child: Center(
                          child: CircularProgressIndicator(
                            color: AppColors.postbookPrimary,
                          ),
                        ),
                      ),
                    )
                  else if (postsAsync.hasError)
                    SliverToBoxAdapter(
                      child: Padding(
                        padding: AppSpacing.pagePadding,
                        child: _InlineStateCard(
                          icon: Icons.grid_off_outlined,
                          message: 'Could not load posts.',
                          action: 'Retry',
                          onTap: () => ref.invalidate(_myPostsProvider),
                        ),
                      ),
                    )
                  else if (filteredPosts.isEmpty)
                    SliverToBoxAdapter(
                      child: Padding(
                        padding: AppSpacing.pagePadding,
                        child: _InlineStateCard(
                          icon: Icons.photo_library_outlined,
                          message: 'No posts in this filter yet.',
                          action: 'Create one',
                          onTap: () => context.push('/create'),
                        ),
                      ),
                    )
                  else
                    SliverPadding(
                      padding: AppSpacing.pagePadding.copyWith(bottom: 110),
                      sliver: SliverGrid(
                        gridDelegate:
                            const SliverGridDelegateWithMaxCrossAxisExtent(
                              maxCrossAxisExtent: 180,
                              mainAxisSpacing: 8,
                              crossAxisSpacing: 8,
                              childAspectRatio: 0.86,
                            ),
                        delegate: SliverChildBuilderDelegate((context, index) {
                          final post = filteredPosts[index];
                          return _PostTile(
                            post: post,
                            onTap: () => context.push('/comments/${post.id}'),
                          );
                        }, childCount: filteredPosts.length),
                      ),
                    ),
                ],
              ),
            );
          },
        ),
      ),
    );
  }
}

enum _PostFilter { all, posts, reels, videos }

class _AvatarFallback extends StatelessWidget {
  const _AvatarFallback({
    required this.size,
    required this.initial,
    required this.style,
  });

  final double size;
  final String initial;
  final TextStyle style;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: const BoxDecoration(gradient: AppColors.postbookGradient),
      child: Center(
        child: Text(
          initial.isNotEmpty ? initial[0].toUpperCase() : 'U',
          style: style.copyWith(color: Colors.white),
        ),
      ),
    );
  }
}

class _StatChip extends StatelessWidget {
  const _StatChip({
    required this.label,
    required this.value,
    required this.onTap,
  });

  final String label;
  final int value;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(12),
      child: Ink(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          children: [
            Text(_format(value), style: AppTextStyles.h3),
            const SizedBox(height: 2),
            Text(label, style: AppTextStyles.labelSmall),
          ],
        ),
      ),
    );
  }

  String _format(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }
}

class _ActionButton extends StatelessWidget {
  const _ActionButton({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      onPressed: onTap,
      style: OutlinedButton.styleFrom(
        foregroundColor: AppColors.textSecondary,
        side: const BorderSide(color: AppColors.borderSubtle),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      ),
      icon: Icon(icon, size: 16),
      label: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
    );
  }
}

class _FilterChip extends StatelessWidget {
  const _FilterChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: (_) => onTap(),
      selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
      backgroundColor: AppColors.bgCard,
      side: BorderSide(
        color: selected ? AppColors.postbookPrimary : AppColors.borderSubtle,
      ),
      labelStyle: AppTextStyles.label.copyWith(
        color: selected ? AppColors.postbookPrimary : AppColors.textSecondary,
      ),
    );
  }
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({
    required this.icon,
    required this.message,
    required this.action,
    required this.onTap,
  });

  final IconData icon;
  final String message;
  final String action;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textMuted),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
          TextButton(
            onPressed: onTap,
            child: Text(
              action,
              style: AppTextStyles.label.copyWith(
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _PostTile extends StatelessWidget {
  const _PostTile({required this.post, required this.onTap});

  final Post post;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final icon = switch (post.contentType) {
      'reel' => Icons.play_circle_fill_rounded,
      'video' => Icons.videocam_rounded,
      'poll' => Icons.poll_outlined,
      _ => Icons.image_outlined,
    };

    final badge = switch (post.contentType) {
      'reel' => 'REEL',
      'video' => 'VIDEO',
      'poll' => 'POLL',
      _ => 'POST',
    };

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(14),
        child: Ink(
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(color: AppColors.borderSubtle),
            gradient: const LinearGradient(
              colors: [Color(0x1A4ECDC4), Color(0x1AFF6B35), Color(0x1A7B68EE)],
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
            ),
          ),
          child: Padding(
            padding: const EdgeInsets.all(10),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 7,
                        vertical: 3,
                      ),
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: 0.35),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Text(
                        badge,
                        style: AppTextStyles.labelSmall.copyWith(
                          color: Colors.white,
                        ),
                      ),
                    ),
                    const Spacer(),
                    Icon(icon, color: AppColors.textPrimary, size: 20),
                  ],
                ),
                const Spacer(),
                Text(
                  post.content.isEmpty ? '(No caption)' : post.content,
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.label,
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    const Icon(
                      Icons.favorite_border,
                      size: 14,
                      color: AppColors.textMuted,
                    ),
                    const SizedBox(width: 4),
                    Text('${post.likeCount}', style: AppTextStyles.labelSmall),
                    const SizedBox(width: 10),
                    const Icon(
                      Icons.chat_bubble_outline,
                      size: 14,
                      color: AppColors.textMuted,
                    ),
                    const SizedBox(width: 4),
                    Text(
                      '${post.commentCount}',
                      style: AppTextStyles.labelSmall,
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
