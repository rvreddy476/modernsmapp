import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/monetization/widgets/tier_picker_sheet.dart';
import 'package:atpost_app/features/monetization/widgets/tip_sheet.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ProfileDetailScreen extends ConsumerStatefulWidget {
  const ProfileDetailScreen({super.key, required this.userId});

  final String userId;

  @override
  ConsumerState<ProfileDetailScreen> createState() =>
      _ProfileDetailScreenState();
}

class _ProfileDetailScreenState extends ConsumerState<ProfileDetailScreen> {
  bool _following = false;
  bool _subscribed = false;
  bool _openingConversation = false;

  // Opens (or creates) a 1:1 conversation with this profile's user and
  // navigates to it. DM gating is enforced server-side: a non-connection is
  // routed to a Message Request, a blocked pair is rejected (spec §4).
  Future<void> _openConversation() async {
    if (_openingConversation) return;
    setState(() => _openingConversation = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      final conversation = await ref
          .read(chatRepositoryProvider)
          .createDirectConversation(widget.userId);
      if (!mounted) return;
      context.push('/chat/${conversation.id}');
    } catch (_) {
      if (!mounted) return;
      messenger.showSnackBar(
        const SnackBar(content: Text("Couldn't open the conversation")),
      );
    } finally {
      if (mounted) setState(() => _openingConversation = false);
    }
  }

  void _showProfileOptions(
    BuildContext context,
    WidgetRef ref,
    String userId,
  ) {
    final isMuted = ref.read(muteStateProvider(userId));
    final isBlocked = ref.read(blockStateProvider(userId));

    showModalBottomSheet(
      context: context,
      backgroundColor: AppColors.bgCard,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 36,
                height: 4,
                margin: const EdgeInsets.only(bottom: 12),
                decoration: BoxDecoration(
                  color: AppColors.textMuted,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              ListTile(
                leading: Icon(
                  isMuted ? Icons.volume_up_outlined : Icons.volume_off_outlined,
                  color: AppColors.textSecondary,
                ),
                title: Text(
                  isMuted ? 'Unmute' : 'Mute',
                  style: AppTextStyles.label,
                ),
                subtitle: Text(
                  isMuted
                      ? 'Show their posts in your feed again'
                      : 'Hide their posts from your feed',
                  style: AppTextStyles.labelSmall,
                ),
                onTap: () async {
                  Navigator.of(ctx).pop();
                  final repo = ref.read(userRepositoryProvider);
                  try {
                    if (isMuted) {
                      await repo.unmuteUser(userId);
                    } else {
                      await repo.muteUser(userId);
                    }
                    ref.read(muteStateProvider(userId).notifier).state =
                        !isMuted;
                    if (context.mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(
                          content: Text(
                            isMuted
                                ? 'User unmuted.'
                                : 'User muted.',
                          ),
                        ),
                      );
                    }
                  } catch (_) {
                    if (context.mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(
                          content: Text('Could not update mute status.'),
                        ),
                      );
                    }
                  }
                },
              ),
              ListTile(
                leading: Icon(
                  isBlocked ? Icons.check_circle_outline : Icons.block,
                  color: isBlocked ? AppColors.textSecondary : Colors.red,
                ),
                title: Text(
                  isBlocked ? 'Unblock' : 'Block',
                  style: AppTextStyles.label.copyWith(
                    color: isBlocked ? null : Colors.red,
                  ),
                ),
                subtitle: Text(
                  isBlocked
                      ? 'Allow this user to see your profile again'
                      : 'Prevent this user from seeing your profile',
                  style: AppTextStyles.labelSmall,
                ),
                onTap: () async {
                  Navigator.of(ctx).pop();
                  final repo = ref.read(userRepositoryProvider);
                  try {
                    if (isBlocked) {
                      await repo.unblockUser(userId);
                    } else {
                      await repo.blockUser(userId);
                    }
                    ref.read(blockStateProvider(userId).notifier).state =
                        !isBlocked;
                    if (context.mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(
                          content: Text(
                            isBlocked
                                ? 'User unblocked.'
                                : 'User blocked.',
                          ),
                        ),
                      );
                    }
                  } catch (_) {
                    if (context.mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(
                          content: Text('Could not update block status.'),
                        ),
                      );
                    }
                  }
                },
              ),
              ListTile(
                leading: const Icon(
                  Icons.flag_outlined,
                  color: AppColors.textSecondary,
                ),
                title: Text('Report', style: AppTextStyles.label),
                subtitle: Text(
                  'Report this profile for violating guidelines',
                  style: AppTextStyles.labelSmall,
                ),
                onTap: () {
                  Navigator.of(ctx).pop();
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(
                      content: Text(
                        'Report submitted. We will review this profile.',
                      ),
                    ),
                  );
                },
              ),
            ],
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final userAsync = ref.watch(userProfileProvider(widget.userId));

    return Scaffold(
      body: SafeArea(
        child: userAsync.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('Could not load profile', style: AppTextStyles.bodySmall),
                const SizedBox(height: 12),
                TextButton(
                  onPressed: () => context.pop(),
                  child: const Text('Go back'),
                ),
              ],
            ),
          ),
          data: (user) => CustomScrollView(
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 0),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          IconButton(
                            onPressed: () => context.pop(),
                            icon: const Icon(Icons.arrow_back),
                            color: AppColors.textSecondary,
                          ),
                          const Spacer(),
                          IconButton(
                            onPressed: () => _showProfileOptions(
                              context,
                              ref,
                              widget.userId,
                            ),
                            icon: const Icon(
                              Icons.more_horiz,
                              color: AppColors.textMuted,
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 8),
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          ClipRRect(
                            borderRadius: BorderRadius.circular(
                              AppSpacing.radiusLarge,
                            ),
                            child: user.hasAvatar
                                ? Image.network(
                                    user.avatarUrl,
                                    width: 72,
                                    height: 72,
                                    fit: BoxFit.cover,
                                    errorBuilder: (_, _, _) => _AvatarFallback(
                                      size: 72,
                                      initial: user.displayName,
                                      style: AppTextStyles.h1,
                                    ),
                                  )
                                : _AvatarFallback(
                                    size: 72,
                                    initial: user.displayName,
                                    style: AppTextStyles.h1,
                                  ),
                          ),
                          const SizedBox(width: 16),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Row(
                                  children: [
                                    Flexible(
                                      child: Text(
                                        user.displayName,
                                        style: AppTextStyles.h2,
                                      ),
                                    ),
                                    if (user.isVerified) ...[
                                      const SizedBox(width: 4),
                                      const Icon(
                                        Icons.verified,
                                        size: 16,
                                        color: AppColors.postbookPrimary,
                                      ),
                                    ],
                                  ],
                                ),
                                const SizedBox(height: 2),
                                Text(
                                  '@${user.username}',
                                  style: AppTextStyles.bodySmall,
                                ),
                                if (user.profession != null &&
                                    user.profession!.isNotEmpty) ...[
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
                        ],
                      ),
                      if (user.bio != null && user.bio!.isNotEmpty) ...[
                        const SizedBox(height: 12),
                        Text(
                          user.bio!,
                          style: AppTextStyles.bodySmall.copyWith(
                            color: AppColors.textSecondary,
                          ),
                        ),
                      ],
                      const SizedBox(height: 16),
                      Row(
                        children: [
                          _StatBadge(
                            label: 'Followers',
                            count: user.followerCount,
                          ),
                          const SizedBox(width: 10),
                          _StatBadge(
                            label: 'Following',
                            count: user.followingCount,
                          ),
                          const SizedBox(width: 10),
                          _StatBadge(label: 'Friends', count: user.friendCount),
                        ],
                      ),
                      const SizedBox(height: 16),
                      Row(
                        children: [
                          Expanded(
                            child: GestureDetector(
                              onTap: () async {
                                final repo = ref.read(userRepositoryProvider);
                                try {
                                  if (_following) {
                                    await repo.unfollowUser(widget.userId);
                                  } else {
                                    await repo.followUser(widget.userId);
                                  }
                                  if (mounted) {
                                    setState(() => _following = !_following);
                                  }
                                } catch (_) {
                                  if (!context.mounted) return;
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    const SnackBar(
                                      content: Text(
                                        'Could not update follow status.',
                                      ),
                                    ),
                                  );
                                }
                              },
                              child: AnimatedContainer(
                                duration: const Duration(milliseconds: 200),
                                padding: const EdgeInsets.symmetric(
                                  vertical: 12,
                                ),
                                decoration: BoxDecoration(
                                  gradient: _following
                                      ? null
                                      : AppColors.postbookGradient,
                                  color: _following ? AppColors.bgCard : null,
                                  borderRadius: BorderRadius.circular(
                                    AppSpacing.radiusLarge,
                                  ),
                                  border: Border.all(
                                    color: _following
                                        ? AppColors.borderSubtle
                                        : AppColors.postbookPrimary.withValues(
                                            alpha: 0.4,
                                          ),
                                  ),
                                ),
                                child: Center(
                                  child: Text(
                                    _following ? 'Following' : 'Follow',
                                    style: AppTextStyles.label.copyWith(
                                      color: _following
                                          ? AppColors.textSecondary
                                          : Colors.white,
                                    ),
                                  ),
                                ),
                              ),
                            ),
                          ),
                          const SizedBox(width: 8),
                          Expanded(
                            child: _ConnectionButton(userId: widget.userId),
                          ),
                          if (user.isVerified) ...[
                            const SizedBox(width: 8),
                            GestureDetector(
                              onTap: () async {
                                // Tier 3c: open the tier picker. The
                                // sheet returns the chosen tier ID on
                                // success.
                                final pickedTierId =
                                    await TierPickerSheet.show(
                                  context,
                                  creatorId: widget.userId,
                                );
                                if (pickedTierId != null && mounted) {
                                  setState(() => _subscribed = true);
                                }
                              },
                              child: Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 14,
                                  vertical: 12,
                                ),
                                decoration: BoxDecoration(
                                  gradient: _subscribed
                                      ? null
                                      : AppColors.ctaGradient,
                                  color: _subscribed ? AppColors.bgCard : null,
                                  borderRadius: BorderRadius.circular(
                                    AppSpacing.radiusLarge,
                                  ),
                                  border: Border.all(
                                    color: _subscribed
                                        ? AppColors.borderSubtle
                                        : Colors.transparent,
                                  ),
                                ),
                                child: Text(
                                  _subscribed ? 'Member' : 'Subscribe',
                                  style: AppTextStyles.label.copyWith(
                                    color: _subscribed
                                        ? AppColors.textSecondary
                                        : Colors.white,
                                  ),
                                ),
                              ),
                            ),
                            const SizedBox(width: 8),
                            // Tier 3d: tip button next to Subscribe.
                            GestureDetector(
                              onTap: () {
                                TipSheet.show(
                                  context,
                                  creatorId: widget.userId,
                                );
                              },
                              child: Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 14,
                                  vertical: 12,
                                ),
                                decoration: BoxDecoration(
                                  color: AppColors.bgCard,
                                  borderRadius: BorderRadius.circular(
                                    AppSpacing.radiusLarge,
                                  ),
                                  border: Border.all(
                                    color: AppColors.borderSubtle,
                                  ),
                                ),
                                child: Text('Tip', style: AppTextStyles.label),
                              ),
                            ),
                          ],
                          const SizedBox(width: 8),
                          GestureDetector(
                            onTap: _openConversation,
                            child: Container(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 18,
                                vertical: 12,
                              ),
                              decoration: BoxDecoration(
                                color: AppColors.bgCard,
                                borderRadius: BorderRadius.circular(
                                  AppSpacing.radiusLarge,
                                ),
                                border: Border.all(
                                  color: AppColors.borderSubtle,
                                ),
                              ),
                              child: Text(
                                'Message',
                                style: AppTextStyles.label,
                              ),
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 20),
                    ],
                  ),
                ),
              ),
              SliverFillRemaining(
                child: Container(
                  margin: AppSpacing.pagePadding.copyWith(top: 0, bottom: 20),
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(
                          Icons.grid_on_outlined,
                          color: AppColors.textMuted,
                          size: 32,
                        ),
                        const SizedBox(height: 8),
                        Text('No posts yet', style: AppTextStyles.bodySmall),
                      ],
                    ),
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

/// Status-aware connection ("Add Friend") button shown on another user's
/// profile. Reflects the live connection state and drives send / cancel /
/// accept against graph-service.
class _ConnectionButton extends ConsumerStatefulWidget {
  const _ConnectionButton({required this.userId});

  final String userId;

  @override
  ConsumerState<_ConnectionButton> createState() => _ConnectionButtonState();
}

class _ConnectionButtonState extends ConsumerState<_ConnectionButton> {
  // Local override applied once the user acts, so the button updates
  // immediately. Null means "use connectionStatusProvider's value".
  String? _status;
  bool _busy = false;

  Future<void> _act(String status) async {
    final repo = ref.read(userRepositoryProvider);
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _busy = true);
    try {
      if (status == 'none') {
        await repo.sendConnectionRequest(widget.userId);
        _status = 'pending_sent';
      } else if (status == 'pending_sent') {
        await repo.cancelConnectionRequest(widget.userId);
        _status = 'none';
      } else if (status == 'pending_received') {
        await repo.acceptConnectionRequest(widget.userId);
        _status = 'accepted';
      }
    } catch (_) {
      messenger.showSnackBar(
        const SnackBar(content: Text("Couldn't update connection")),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final statusAsync = ref.watch(connectionStatusProvider(widget.userId));
    final status = _status ?? statusAsync.valueOrNull ?? 'none';

    final label = switch (status) {
      'accepted' => 'Connected',
      'pending_sent' => 'Requested',
      'pending_received' => 'Accept',
      _ => 'Add Friend',
    };
    // Gradient (primary) for the actionable states "Add Friend" / "Accept";
    // neutral for "Requested" / "Connected".
    final filled = status == 'none' || status == 'pending_received';
    // "Connected" is a status indicator, not an action.
    final tappable = status != 'accepted';

    return GestureDetector(
      onTap: (_busy || !tappable) ? null : () => _act(status),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 200),
        padding: const EdgeInsets.symmetric(vertical: 12),
        decoration: BoxDecoration(
          gradient: filled ? AppColors.postbookGradient : null,
          color: filled ? null : AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(
            color: filled
                ? AppColors.postbookPrimary.withValues(alpha: 0.4)
                : AppColors.borderSubtle,
          ),
        ),
        child: Center(
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: AppTextStyles.label.copyWith(
              color: filled ? Colors.white : AppColors.textSecondary,
            ),
          ),
        ),
      ),
    );
  }
}
