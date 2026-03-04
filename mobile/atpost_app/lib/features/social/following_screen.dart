import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class FollowingScreen extends ConsumerStatefulWidget {
  const FollowingScreen({super.key, required this.userId});

  final String userId;

  @override
  ConsumerState<FollowingScreen> createState() => _FollowingScreenState();
}

class _FollowingScreenState extends ConsumerState<FollowingScreen> {
  @override
  Widget build(BuildContext context) {
    final asyncFollowing = ref.watch(followingProvider(widget.userId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Following', style: AppTextStyles.h2),
        leading: BackButton(
          color: AppColors.textPrimary,
          onPressed: () => context.pop(),
        ),
      ),
      body: asyncFollowing.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (err, _) => Center(
          child: Text(
            'Failed to load following',
            style: AppTextStyles.body.copyWith(color: AppColors.textDim),
          ),
        ),
        data: (users) {
          if (users.isEmpty) {
            return Center(
              child: Text(
                'Not following anyone yet',
                style: AppTextStyles.body.copyWith(color: AppColors.textDim),
              ),
            );
          }
          return ListView.separated(
            itemCount: users.length,
            separatorBuilder: (_, _) =>
                Divider(height: 1, color: AppColors.borderSubtle),
            itemBuilder: (context, index) => _UserListTile(
              user: users[index],
              onTap: () => context.push('/profile/${users[index].id}'),
            ),
          );
        },
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Private widgets
// ---------------------------------------------------------------------------

class _UserListTile extends ConsumerStatefulWidget {
  const _UserListTile({
    required this.user,
    required this.onTap,
  });

  final User user;
  final VoidCallback onTap;

  @override
  ConsumerState<_UserListTile> createState() => _UserListTileState();
}

class _UserListTileState extends ConsumerState<_UserListTile> {
  bool _following = true;
  bool _loading = false;

  Future<void> _toggleFollow() async {
    final repo = ref.read(userRepositoryProvider);
    setState(() => _loading = true);
    try {
      if (_following) {
        await repo.unfollowUser(widget.user.id);
      } else {
        await repo.followUser(widget.user.id);
      }
      setState(() => _following = !_following);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 18, vertical: 4),
      onTap: widget.onTap,
      leading: CircleAvatar(
        backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
        child: Text(
          widget.user.displayName.isNotEmpty
              ? widget.user.displayName[0].toUpperCase()
              : '?',
          style: AppTextStyles.h3.copyWith(color: AppColors.postbookPrimary),
        ),
      ),
      title: Text(
        widget.user.displayName,
        style: AppTextStyles.body.copyWith(
          fontWeight: FontWeight.bold,
          color: AppColors.textPrimary,
        ),
      ),
      subtitle: Text(
        '@${widget.user.username}',
        style: AppTextStyles.bodySmall,
      ),
      trailing: _loading
          ? const SizedBox(
              width: 24,
              height: 24,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : OutlinedButton(
              onPressed: _toggleFollow,
              style: OutlinedButton.styleFrom(
                foregroundColor: _following
                    ? AppColors.textSecondary
                    : AppColors.postbookPrimary,
                side: BorderSide(
                  color: _following
                      ? AppColors.borderSubtle
                      : AppColors.postbookPrimary,
                ),
                padding: const EdgeInsets.symmetric(horizontal: 14),
                minimumSize: const Size(80, 34),
              ),
              child: Text(_following ? 'Following' : 'Follow'),
            ),
    );
  }
}
