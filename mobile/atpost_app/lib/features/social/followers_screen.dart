import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class FollowersScreen extends ConsumerStatefulWidget {
  const FollowersScreen({super.key, required this.userId});

  final String userId;

  @override
  ConsumerState<FollowersScreen> createState() => _FollowersScreenState();
}

class _FollowersScreenState extends ConsumerState<FollowersScreen> {
  final TextEditingController _searchController = TextEditingController();

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  List<User> _filter(List<User> users) {
    final query = _searchController.text.trim().toLowerCase();
    if (query.isEmpty) return users;

    return users.where((user) {
      return user.displayName.toLowerCase().contains(query) ||
          user.username.toLowerCase().contains(query);
    }).toList();
  }

  @override
  Widget build(BuildContext context) {
    final followersAsync = ref.watch(followersProvider(widget.userId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: followersAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: _InlineStateCard(
              message: 'Failed to load followers.',
              onRetry: () => ref.invalidate(followersProvider(widget.userId)),
            ),
          ),
          data: (users) {
            final filtered = _filter(users);

            return Column(
              children: [
                Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 10),
                  child: _Header(
                    title: 'Followers',
                    subtitle: '${users.length} people follow this account',
                    onBack: () => context.pop(),
                  ),
                ),
                const SizedBox(height: 12),
                Padding(
                  padding: AppSpacing.pagePadding,
                  child: Container(
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge,
                      ),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: TextField(
                      controller: _searchController,
                      onChanged: (_) => setState(() {}),
                      style: AppTextStyles.body,
                      decoration: InputDecoration(
                        border: InputBorder.none,
                        hintText: 'Search followers',
                        hintStyle: AppTextStyles.bodySmall,
                        prefixIcon: const Icon(
                          Icons.search,
                          color: AppColors.textMuted,
                        ),
                        suffixIcon: _searchController.text.isEmpty
                            ? null
                            : IconButton(
                                onPressed: () {
                                  _searchController.clear();
                                  setState(() {});
                                },
                                icon: const Icon(
                                  Icons.close,
                                  color: AppColors.textMuted,
                                ),
                              ),
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 8),
                Expanded(
                  child: RefreshIndicator(
                    color: AppColors.postbookPrimary,
                    onRefresh: () async =>
                        ref.invalidate(followersProvider(widget.userId)),
                    child: filtered.isEmpty
                        ? ListView(
                            physics: const AlwaysScrollableScrollPhysics(),
                            children: [
                              SizedBox(
                                height: 280,
                                child: Center(
                                  child: _InlineStateCard(
                                    message: users.isEmpty
                                        ? 'No followers yet.'
                                        : 'No followers match your search.',
                                    onRetry: () {},
                                  ),
                                ),
                              ),
                            ],
                          )
                        : ListView.separated(
                            physics: const AlwaysScrollableScrollPhysics(),
                            padding: AppSpacing.pagePadding.copyWith(
                              top: 6,
                              bottom: 110,
                            ),
                            itemCount: filtered.length,
                            separatorBuilder: (_, _) =>
                                const SizedBox(height: 8),
                            itemBuilder: (context, index) {
                              final user = filtered[index];
                              return _FollowerTile(user: user);
                            },
                          ),
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.title,
    required this.subtitle,
    required this.onBack,
  });

  final String title;
  final String subtitle;
  final VoidCallback onBack;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          colors: [Color(0x33FF6B35), Color(0x334ECDC4), Color(0x337B68EE)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Row(
        children: [
          IconButton(
            onPressed: onBack,
            icon: const Icon(
              Icons.arrow_back_ios_new_rounded,
              size: 18,
              color: AppColors.textPrimary,
            ),
          ),
          const SizedBox(width: 4),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppTextStyles.h1.copyWith(fontSize: 30)),
                Text(subtitle, style: AppTextStyles.bodySmall),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _FollowerTile extends ConsumerStatefulWidget {
  const _FollowerTile({required this.user});

  final User user;

  @override
  ConsumerState<_FollowerTile> createState() => _FollowerTileState();
}

class _FollowerTileState extends ConsumerState<_FollowerTile> {
  bool _following = false;
  bool _busy = false;

  Future<void> _toggleFollow() async {
    if (_busy) return;

    final currentlyFollowing = _following;
    setState(() {
      _busy = true;
      _following = !currentlyFollowing;
    });

    try {
      final repo = ref.read(userRepositoryProvider);
      if (currentlyFollowing) {
        await repo.unfollowUser(widget.user.id);
      } else {
        await repo.followUser(widget.user.id);
      }
    } catch (_) {
      if (!mounted) return;
      setState(() => _following = currentlyFollowing);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update follow status.')),
      );
    } finally {
      if (mounted) {
        setState(() => _busy = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final user = widget.user;
    final initial = user.displayName.isEmpty
        ? 'U'
        : user.displayName.substring(0, 1).toUpperCase();

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: () => context.push('/profile/${user.id}'),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: Ink(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              CircleAvatar(
                radius: 20,
                backgroundColor: AppColors.postbookPrimary.withValues(
                  alpha: 0.2,
                ),
                child: Text(
                  initial,
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Flexible(
                          child: Text(
                            user.displayName,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: AppTextStyles.label,
                          ),
                        ),
                        if (user.isVerified) ...[
                          const SizedBox(width: 4),
                          const Icon(
                            Icons.verified,
                            size: 14,
                            color: AppColors.posttubePrimary,
                          ),
                        ],
                      ],
                    ),
                    Text('@${user.username}', style: AppTextStyles.labelSmall),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              OutlinedButton(
                onPressed: _busy ? null : _toggleFollow,
                style: OutlinedButton.styleFrom(
                  foregroundColor: _following
                      ? AppColors.textSecondary
                      : AppColors.postbookPrimary,
                  side: BorderSide(
                    color: _following
                        ? AppColors.borderSubtle
                        : AppColors.postbookPrimary,
                  ),
                ),
                child: _busy
                    ? const SizedBox(
                        width: 12,
                        height: 12,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : Text(_following ? 'Following' : 'Follow'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: AppSpacing.pagePadding,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          const Icon(Icons.group_outlined, color: AppColors.textMuted),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
          TextButton(
            onPressed: onRetry,
            child: Text(
              'Retry',
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
