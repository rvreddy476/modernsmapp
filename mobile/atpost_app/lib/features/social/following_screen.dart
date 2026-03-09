import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
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
    final followingAsync = ref.watch(followingProvider(widget.userId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: followingAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: _InlineStateCard(
              icon: Icons.person_search_outlined,
              message: 'Could not load following list.',
              action: 'Retry',
              onTap: () => ref.invalidate(followingProvider(widget.userId)),
            ),
          ),
          data: (users) {
            final filtered = _filter(users);

            return Column(
              children: [
                Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 10),
                  child: _HeaderCard(
                    followingCount: users.length,
                    onBack: () => context.pop(),
                    onRefresh: () =>
                        ref.invalidate(followingProvider(widget.userId)),
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
                        hintText: 'Search following',
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
                        ref.invalidate(followingProvider(widget.userId)),
                    child: filtered.isEmpty
                        ? ListView(
                            physics: const AlwaysScrollableScrollPhysics(),
                            children: [
                              SizedBox(
                                height: 300,
                                child: Center(
                                  child: _InlineStateCard(
                                    icon: Icons.group_outlined,
                                    message: users.isEmpty
                                        ? 'Not following anyone yet.'
                                        : 'No matches for your search.',
                                    action: users.isEmpty
                                        ? 'Discover'
                                        : 'Clear',
                                    onTap: users.isEmpty
                                        ? () => context.push('/discover')
                                        : () {
                                            _searchController.clear();
                                            setState(() {});
                                          },
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
                            itemBuilder: (context, index) => _FollowingTile(
                              user: filtered[index],
                              onUpdated: () => ref.invalidate(
                                followingProvider(widget.userId),
                              ),
                            ),
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

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({
    required this.followingCount,
    required this.onBack,
    required this.onRefresh,
  });

  final int followingCount;
  final VoidCallback onBack;
  final VoidCallback onRefresh;

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
      child: Column(
        children: [
          Row(
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
                child: Text(
                  'Following',
                  style: AppTextStyles.h1.copyWith(fontSize: 30),
                ),
              ),
              IconButton(
                onPressed: onRefresh,
                icon: const Icon(
                  Icons.refresh_rounded,
                  color: AppColors.textPrimary,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              _Pill(label: 'Accounts', value: '$followingCount'),
              const SizedBox(width: 8),
              const Expanded(
                child: _Pill(label: 'Feed', value: 'Curated'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(value, style: AppTextStyles.h3),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _FollowingTile extends ConsumerStatefulWidget {
  const _FollowingTile({required this.user, required this.onUpdated});

  final User user;
  final VoidCallback onUpdated;

  @override
  ConsumerState<_FollowingTile> createState() => _FollowingTileState();
}

class _FollowingTileState extends ConsumerState<_FollowingTile> {
  bool _following = true;
  bool _loading = false;

  Future<void> _toggleFollow() async {
    if (_loading) return;

    final previous = _following;
    setState(() {
      _loading = true;
      _following = !previous;
    });

    try {
      final repo = ref.read(userRepositoryProvider);
      if (previous) {
        await repo.unfollowUser(widget.user.id);
      } else {
        await repo.followUser(widget.user.id);
      }
      widget.onUpdated();
    } catch (_) {
      if (!mounted) return;
      setState(() => _following = previous);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update follow status.')),
      );
    } finally {
      if (mounted) {
        setState(() => _loading = false);
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
                radius: 22,
                backgroundColor: AppColors.postbookPrimary.withValues(
                  alpha: 0.2,
                ),
                backgroundImage: user.hasAvatar
                    ? NetworkImage(user.avatarUrl)
                    : null,
                child: user.hasAvatar
                    ? null
                    : Text(
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
                onPressed: _loading ? null : _toggleFollow,
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
                child: _loading
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
      margin: AppSpacing.pagePadding,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
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
