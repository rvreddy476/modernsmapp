import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class FriendsScreen extends ConsumerStatefulWidget {
  const FriendsScreen({super.key});

  @override
  ConsumerState<FriendsScreen> createState() => _FriendsScreenState();
}

class _FriendsScreenState extends ConsumerState<FriendsScreen> {
  @override
  Widget build(BuildContext context) {
    final asyncFriends = ref.watch(friendsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Friends', style: AppTextStyles.h2),
        leading: BackButton(
          color: AppColors.textPrimary,
          onPressed: () => context.pop(),
        ),
      ),
      body: RefreshIndicator(
        color: AppColors.postbookPrimary,
        onRefresh: () async => ref.invalidate(friendsProvider),
        child: asyncFriends.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (err, _) => Center(
            child: Text(
              'Failed to load friends',
              style: AppTextStyles.body.copyWith(color: AppColors.textDim),
            ),
          ),
          data: (users) {
            if (users.isEmpty) {
              return Center(
                child: Text(
                  'No friends yet',
                  style:
                      AppTextStyles.body.copyWith(color: AppColors.textDim),
                ),
              );
            }
            return ListView.separated(
              itemCount: users.length,
              separatorBuilder: (_, _) =>
                  Divider(height: 1, color: AppColors.borderSubtle),
              itemBuilder: (context, index) => _FriendTile(
                user: users[index],
                onTap: () => context.push('/profile/${users[index].id}'),
                onUnfriend: () => ref.invalidate(friendsProvider),
              ),
            );
          },
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Private widgets
// ---------------------------------------------------------------------------

class _FriendTile extends ConsumerStatefulWidget {
  const _FriendTile({
    required this.user,
    required this.onTap,
    required this.onUnfriend,
  });

  final User user;
  final VoidCallback onTap;
  final VoidCallback onUnfriend;

  @override
  ConsumerState<_FriendTile> createState() => _FriendTileState();
}

class _FriendTileState extends ConsumerState<_FriendTile> {
  bool _loading = false;

  Future<void> _unfriend() async {
    setState(() => _loading = true);
    try {
      await ref
          .read(apiClientProvider)
          .delete('/v1/graph/friends/${widget.user.id}');
      widget.onUnfriend();
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
          : TextButton(
              onPressed: _unfriend,
              style: TextButton.styleFrom(
                foregroundColor: AppColors.postgramPrimary,
              ),
              child: const Text('Unfriend'),
            ),
    );
  }
}
