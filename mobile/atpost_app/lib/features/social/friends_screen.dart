import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
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
    final friendsAsync = ref.watch(friendsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: friendsAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: _InlineStateCard(
              icon: Icons.people_alt_outlined,
              message: 'Could not load friends list.',
              action: 'Retry',
              onTap: () => ref.invalidate(friendsProvider),
            ),
          ),
          data: (users) {
            final filtered = _filter(users);

            return Column(
              children: [
                Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 10),
                  child: _HeaderCard(
                    friendCount: users.length,
                    onBack: () => context.pop(),
                    onRefresh: () => ref.invalidate(friendsProvider),
                    onRequests: () => context.push('/friend-requests'),
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
                        hintText: 'Search friends',
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
                    onRefresh: () async => ref.invalidate(friendsProvider),
                    child: filtered.isEmpty
                        ? ListView(
                            physics: const AlwaysScrollableScrollPhysics(),
                            children: [
                              SizedBox(
                                height: 300,
                                child: Center(
                                  child: _InlineStateCard(
                                    icon: Icons.person_off_outlined,
                                    message: users.isEmpty
                                        ? 'No friends yet. Send a few requests.'
                                        : 'No friends match your search.',
                                    action: users.isEmpty
                                        ? 'Requests'
                                        : 'Clear',
                                    onTap: users.isEmpty
                                        ? () => context.push('/friend-requests')
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
                            itemBuilder: (context, index) => _FriendTile(
                              user: filtered[index],
                              onUnfriend: () => ref.invalidate(friendsProvider),
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
    required this.friendCount,
    required this.onBack,
    required this.onRefresh,
    required this.onRequests,
  });

  final int friendCount;
  final VoidCallback onBack;
  final VoidCallback onRefresh;
  final VoidCallback onRequests;

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
                  'Friends',
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
              _Pill(label: 'Friends', value: '$friendCount'),
              const SizedBox(width: 8),
              const Expanded(
                child: _Pill(label: 'Circle', value: 'Trusted'),
              ),
              const SizedBox(width: 8),
              ElevatedButton.icon(
                onPressed: onRequests,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10),
                  ),
                ),
                icon: const Icon(Icons.mail_outline_rounded, size: 16),
                label: const Text('Requests'),
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

class _FriendTile extends ConsumerStatefulWidget {
  const _FriendTile({required this.user, required this.onUnfriend});

  final User user;
  final VoidCallback onUnfriend;

  @override
  ConsumerState<_FriendTile> createState() => _FriendTileState();
}

class _FriendTileState extends ConsumerState<_FriendTile> {
  bool _loading = false;

  Future<void> _unfriend() async {
    if (_loading) return;

    setState(() => _loading = true);
    try {
      await ref
          .read(apiClientProvider)
          .delete('/v1/graph/friends/${widget.user.id}');
      widget.onUnfriend();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not remove friend.')));
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
                backgroundColor: AppColors.posttubePrimary.withValues(
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
                          color: AppColors.posttubePrimary,
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
              _loading
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : TextButton(
                      onPressed: _unfriend,
                      style: TextButton.styleFrom(
                        foregroundColor: AppColors.postgramPrimary,
                      ),
                      child: const Text('Unfriend'),
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
