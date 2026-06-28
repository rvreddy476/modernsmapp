import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/models/group_invite.dart';
import 'package:atpost_app/data/models/group_post.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

enum _SpaceSection { myFeed, discover, mySpaces, invites }

class GroupsListScreen extends ConsumerStatefulWidget {
  const GroupsListScreen({super.key});

  @override
  ConsumerState<GroupsListScreen> createState() => _GroupsListScreenState();
}

class _GroupsListScreenState extends ConsumerState<GroupsListScreen> {
  _SpaceSection _section = _SpaceSection.myFeed;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: Column(
          children: [
            _Header(
              onCreateTap: () => context.push('/groups/create'),
            ),
            _SectionNav(
              current: _section,
              onChanged: (s) => setState(() => _section = s),
              inviteBadge: ref
                  .watch(groupInvitesProvider)
                  .valueOrNull
                  ?.length ?? 0,
            ),
            Expanded(child: _body()),
          ],
        ),
      ),
    );
  }

  Widget _body() {
    return switch (_section) {
      _SpaceSection.myFeed => const _MyFeedSection(),
      _SpaceSection.discover => const _DiscoverSection(),
      _SpaceSection.mySpaces => const _MySpacesSection(),
      _SpaceSection.invites => const _InvitesSection(),
    };
  }
}

// ─── Header ─────────────────────────────────────────────────────────────────

class _Header extends StatelessWidget {
  final VoidCallback onCreateTap;
  const _Header({required this.onCreateTap});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 12, 4),
      child: Row(
        children: [
          GestureDetector(
            onTap: () => context.pop(),
            child: const Icon(Icons.arrow_back_ios_new,
                color: AppColors.textPrimary, size: 20),
          ),
          const SizedBox(width: 12),
          Text('MySpace', style: AppTextStyles.h1),
          const Spacer(),
          GestureDetector(
            onTap: onCreateTap,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(20),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.add, color: AppColors.textPrimary, size: 16),
                  const SizedBox(width: 4),
                  Text('Create', style: AppTextStyles.labelSmall),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Section Nav ────────────────────────────────────────────────────────────

class _SectionNav extends StatelessWidget {
  final _SpaceSection current;
  final ValueChanged<_SpaceSection> onChanged;
  final int inviteBadge;

  const _SectionNav({
    required this.current,
    required this.onChanged,
    required this.inviteBadge,
  });

  @override
  Widget build(BuildContext context) {
    final items = [
      (section: _SpaceSection.myFeed, label: 'My Feed', badge: 0),
      (section: _SpaceSection.mySpaces, label: 'My Spaces', badge: 0),
      (section: _SpaceSection.discover, label: 'Discover', badge: 0),
      (section: _SpaceSection.invites, label: 'Invites', badge: inviteBadge),
    ];

    return Container(
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(color: AppColors.borderSubtle),
        ),
      ),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        child: Row(
          children: items.map((item) {
            final selected = item.section == current;
            return GestureDetector(
              onTap: () => onChanged(item.section),
              child: Container(
                padding: const EdgeInsets.fromLTRB(8, 10, 8, 0),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(
                          item.label,
                          style: AppTextStyles.label.copyWith(
                            color: selected
                                ? AppColors.textPrimary
                                : AppColors.textMuted,
                            fontWeight: selected
                                ? FontWeight.w700
                                : FontWeight.w500,
                          ),
                        ),
                        if (item.badge > 0) ...[
                          const SizedBox(width: 4),
                          Container(
                            padding: const EdgeInsets.symmetric(
                                horizontal: 5, vertical: 1),
                            decoration: BoxDecoration(
                              color: AppColors.statusSuccess,
                              borderRadius: BorderRadius.circular(10),
                            ),
                            child: Text(
                              '${item.badge}',
                              style: AppTextStyles.labelSmall.copyWith(
                                fontSize: 10,
                                color: Colors.white,
                              ),
                            ),
                          ),
                        ],
                      ],
                    ),
                    const SizedBox(height: 8),
                    AnimatedContainer(
                      duration: const Duration(milliseconds: 150),
                      height: 2,
                      width: selected ? 40 : 0,
                      decoration: BoxDecoration(
                        color: AppColors.textPrimary,
                        borderRadius: BorderRadius.circular(2),
                      ),
                    ),
                  ],
                ),
              ),
            );
          }).toList(),
        ),
      ),
    );
  }
}

// ─── My Feed ────────────────────────────────────────────────────────────────

class _MyFeedSection extends ConsumerWidget {
  const _MyFeedSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final feedAsync = ref.watch(groupFeedProvider);

    return feedAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => _EmptyState(
        icon: Icons.dynamic_feed_outlined,
        title: 'Could not load feed',
        subtitle: 'Pull down to retry',
        onAction: () => ref.invalidate(groupFeedProvider),
        actionLabel: 'Retry',
      ),
      data: (posts) {
        if (posts.isEmpty) {
          return _EmptyState(
            icon: Icons.dynamic_feed_outlined,
            title: 'Your feed is quiet',
            subtitle: 'Join spaces to see posts from their members here.',
            onAction: null,
          );
        }
        return RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () async => ref.invalidate(groupFeedProvider),
          child: ListView.separated(
            padding: const EdgeInsets.all(12),
            itemCount: posts.length,
            separatorBuilder: (_, _) => const SizedBox(height: 10),
            itemBuilder: (context, i) => _FeedPostCard(post: posts[i]),
          ),
        );
      },
    );
  }
}

class _FeedPostCard extends StatelessWidget {
  final GroupPost post;
  const _FeedPostCard({required this.post});

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: () => context.push('/comments/${post.id}'),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Author + space context
            Row(
              children: [
                CircleAvatar(
                  radius: 16,
                  backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
                  child: Text(
                    (post.authorName?.isNotEmpty == true
                            ? post.authorName![0]
                            : '?')
                        .toUpperCase(),
                    style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.postbookPrimary),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        post.authorName ?? 'Unknown',
                        style: AppTextStyles.label,
                      ),
                      if (post.channelName != null)
                        Text(
                          '# ${post.channelName}',
                          style: AppTextStyles.labelSmall
                              .copyWith(color: AppColors.textMuted),
                        ),
                    ],
                  ),
                ),
              ],
            ),
            if (post.body?.isNotEmpty == true) ...[
              const SizedBox(height: 10),
              Text(
                post.body!,
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
                style: AppTextStyles.bodySmall,
              ),
            ],
            const SizedBox(height: 10),
            Row(
              children: [
                _CountChip(
                    icon: Icons.bolt_outlined, count: post.sparkCount),
                const SizedBox(width: 12),
                _CountChip(
                    icon: Icons.chat_bubble_outline,
                    count: post.commentCount),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _CountChip extends StatelessWidget {
  final IconData icon;
  final int count;
  const _CountChip({required this.icon, required this.count});

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 14, color: AppColors.textMuted),
        const SizedBox(width: 3),
        Text('$count',
            style: AppTextStyles.labelSmall
                .copyWith(color: AppColors.textMuted)),
      ],
    );
  }
}

// ─── Discover ───────────────────────────────────────────────────────────────

class _DiscoverSection extends ConsumerWidget {
  const _DiscoverSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final discoverAsync = ref.watch(discoverGroupsProvider);

    return discoverAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => _EmptyState(
        icon: Icons.explore_outlined,
        title: 'Could not load spaces',
        subtitle: 'Pull down to retry',
        onAction: () => ref.invalidate(groupsProvider),
        actionLabel: 'Retry',
      ),
      data: (groups) {
        if (groups.isEmpty) {
          return _EmptyState(
            icon: Icons.explore_outlined,
            title: 'Nothing to discover yet',
            subtitle: 'Check back soon for new spaces.',
            onAction: null,
          );
        }
        return RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () async => ref.invalidate(groupsProvider),
          child: ListView.separated(
            padding: const EdgeInsets.all(12),
            itemCount: groups.length,
            separatorBuilder: (_, _) => const SizedBox(height: 10),
            itemBuilder: (context, i) =>
                _DiscoverCard(group: groups[i]),
          ),
        );
      },
    );
  }
}

class _DiscoverCard extends ConsumerWidget {
  final Group group;
  const _DiscoverCard({required this.group});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return GestureDetector(
      onTap: () => context.push('/groups/${group.id}'),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Cover
            ClipRRect(
              borderRadius:
                  const BorderRadius.vertical(top: Radius.circular(16)),
              child: _GroupCover(
                coverUrl: group.coverUrl,
                gradient: _categoryGradient(group.category),
                height: 80,
              ),
            ),
            Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      _GroupAvatar(
                        avatarUrl: group.avatarUrl,
                        name: group.name,
                        size: 36,
                      ),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              group.name,
                              style: AppTextStyles.label,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                            Row(
                              children: [
                                _PrivacyBadge(level: group.privacyLevel),
                                const SizedBox(width: 6),
                                Text(
                                  '${_fmtCount(group.memberCount)} members',
                                  style: AppTextStyles.labelSmall
                                      .copyWith(color: AppColors.textMuted),
                                ),
                              ],
                            ),
                          ],
                        ),
                      ),
                      _SmallJoinButton(group: group),
                    ],
                  ),
                  if (group.description.isNotEmpty) ...[
                    const SizedBox(height: 8),
                    Text(
                      group.description,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: AppTextStyles.labelSmall
                          .copyWith(color: AppColors.textSecondary),
                    ),
                  ],
                  if (group.category != null) ...[
                    const SizedBox(height: 8),
                    _ReasonChip(label: group.category!),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ReasonChip extends StatelessWidget {
  final String label;
  const _ReasonChip({required this.label});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: AppColors.postbookPrimary.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(
            color: AppColors.postbookPrimary.withValues(alpha: 0.3)),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall
            .copyWith(color: AppColors.postbookPrimary, fontSize: 10),
      ),
    );
  }
}

class _SmallJoinButton extends ConsumerWidget {
  final Group group;
  const _SmallJoinButton({required this.group});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (group.isMember) {
      return Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
        decoration: BoxDecoration(
          color: AppColors.statusSuccess.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
              color: AppColors.statusSuccess.withValues(alpha: 0.3)),
        ),
        child: Text(
          'Joined',
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.statusSuccess, fontSize: 11),
        ),
      );
    }
    return GestureDetector(
      onTap: () => ref.read(groupsProvider.notifier).toggleJoin(group.id),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: AppColors.textPrimary,
          borderRadius: BorderRadius.circular(20),
        ),
        child: Text(
          group.joinMode == 'request' ? 'Request' : 'Join',
          style: AppTextStyles.labelSmall.copyWith(
            color: AppColors.bgPrimary,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}

// ─── My Spaces ──────────────────────────────────────────────────────────────

class _MySpacesSection extends ConsumerWidget {
  const _MySpacesSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final myGroupsAsync = ref.watch(myGroupsProvider);

    return myGroupsAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => _EmptyState(
        icon: Icons.group_outlined,
        title: 'Could not load your spaces',
        onAction: () => ref.invalidate(groupsProvider),
        actionLabel: 'Retry',
      ),
      data: (groups) {
        if (groups.isEmpty) {
          return _EmptyState(
            icon: Icons.group_outlined,
            title: 'No spaces yet',
            subtitle: 'Join or create a space to get started.',
            onAction: () => context.push('/groups/create'),
            actionLabel: 'Create Space',
          );
        }
        return RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () async => ref.invalidate(groupsProvider),
          child: ListView.separated(
            padding: const EdgeInsets.all(12),
            itemCount: groups.length,
            separatorBuilder: (_, _) => const SizedBox(height: 8),
            itemBuilder: (context, i) => _SpaceTile(group: groups[i]),
          ),
        );
      },
    );
  }
}

class _SpaceTile extends StatelessWidget {
  final Group group;
  const _SpaceTile({required this.group});

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: () => context.push('/groups/${group.id}'),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            _GroupAvatar(
              avatarUrl: group.avatarUrl,
              name: group.name,
              size: 44,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          group.name,
                          style: AppTextStyles.label,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      if (group.isMature)
                        Container(
                          margin: const EdgeInsets.only(left: 6),
                          padding: const EdgeInsets.symmetric(
                              horizontal: 5, vertical: 1),
                          decoration: BoxDecoration(
                            color: AppColors.statusError.withValues(alpha: 0.15),
                            borderRadius: BorderRadius.circular(6),
                          ),
                          child: Text(
                            '18+',
                            style: AppTextStyles.labelSmall.copyWith(
                              fontSize: 9,
                              color: AppColors.statusError,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                        ),
                    ],
                  ),
                  const SizedBox(height: 2),
                  Row(
                    children: [
                      _PrivacyBadge(level: group.privacyLevel),
                      const SizedBox(width: 6),
                      Text(
                        '${_fmtCount(group.memberCount)} members',
                        style: AppTextStyles.labelSmall
                            .copyWith(color: AppColors.textMuted),
                      ),
                    ],
                  ),
                ],
              ),
            ),
            if (group.isAdminOrMod)
              Padding(
                padding: const EdgeInsets.only(left: 8),
                child: Icon(
                  Icons.shield_outlined,
                  color: AppColors.postbookPrimary.withValues(alpha: 0.7),
                  size: 16,
                ),
              ),
            const SizedBox(width: 4),
            const Icon(Icons.chevron_right_rounded,
                color: AppColors.textDim, size: 18),
          ],
        ),
      ),
    );
  }
}

// ─── Invites ────────────────────────────────────────────────────────────────

class _InvitesSection extends ConsumerWidget {
  const _InvitesSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final invitesAsync = ref.watch(groupInvitesProvider);

    return invitesAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => _EmptyState(
        icon: Icons.mail_outline,
        title: 'Could not load invites',
        onAction: () => ref.invalidate(groupInvitesProvider),
        actionLabel: 'Retry',
      ),
      data: (invites) {
        if (invites.isEmpty) {
          return _EmptyState(
            icon: Icons.mail_outline,
            title: 'No pending invites',
            subtitle: 'When someone invites you to a space it will appear here.',
            onAction: null,
          );
        }
        return ListView.separated(
          padding: const EdgeInsets.all(12),
          itemCount: invites.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, i) => _InviteTile(invite: invites[i]),
        );
      },
    );
  }
}

class _InviteTile extends ConsumerStatefulWidget {
  final GroupInvite invite;
  const _InviteTile({required this.invite});

  @override
  ConsumerState<_InviteTile> createState() => _InviteTileState();
}

class _InviteTileState extends ConsumerState<_InviteTile> {
  bool _busy = false;

  Future<void> _accept() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref.read(groupsRepositoryProvider).acceptInvite(widget.invite.id);
      ref.invalidate(groupInvitesProvider);
      ref.invalidate(groupsProvider);
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not accept invite.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _decline() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref.read(groupsRepositoryProvider).declineInvite(widget.invite.id);
      ref.invalidate(groupInvitesProvider);
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not decline invite.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final invite = widget.invite;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          _GroupAvatar(
            avatarUrl: invite.groupAvatarMediaId != null
                ? '/v1/media/${invite.groupAvatarMediaId}/serve'
                : null,
            name: invite.groupName,
            size: 44,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(invite.groupName, style: AppTextStyles.label),
                if (invite.inviterName != null)
                  Text(
                    'Invited by ${invite.inviterName}',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.textMuted),
                  ),
              ],
            ),
          ),
          if (_busy)
            const SizedBox(
              width: 24,
              height: 24,
              child: CircularProgressIndicator(
                strokeWidth: 2,
                color: AppColors.postbookPrimary,
              ),
            )
          else
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                GestureDetector(
                  onTap: _decline,
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 10, vertical: 6),
                    decoration: BoxDecoration(
                      border: Border.all(color: AppColors.borderSubtle),
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: Text(
                      'Decline',
                      style: AppTextStyles.labelSmall
                          .copyWith(color: AppColors.textMuted),
                    ),
                  ),
                ),
                const SizedBox(width: 6),
                GestureDetector(
                  onTap: _accept,
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 10, vertical: 6),
                    decoration: BoxDecoration(
                      color: AppColors.statusSuccess.withValues(alpha: 0.15),
                      borderRadius: BorderRadius.circular(10),
                      border: Border.all(
                          color:
                              AppColors.statusSuccess.withValues(alpha: 0.4)),
                    ),
                    child: Text(
                      'Accept',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.statusSuccess,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                ),
              ],
            ),
        ],
      ),
    );
  }
}

// ─── Shared helpers ─────────────────────────────────────────────────────────

class _GroupAvatar extends StatelessWidget {
  final String? avatarUrl;
  final String name;
  final double size;

  const _GroupAvatar({
    required this.avatarUrl,
    required this.name,
    required this.size,
  });

  @override
  Widget build(BuildContext context) {
    final initial =
        name.isNotEmpty ? name[0].toUpperCase() : '?';
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(size * 0.28),
        color: AppColors.bgCard,
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(size * 0.28),
        child: avatarUrl != null
            ? Image.network(
                avatarUrl!,
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => _AvatarFallback(initial: initial, size: size),
              )
            : _AvatarFallback(initial: initial, size: size),
      ),
    );
  }
}

class _AvatarFallback extends StatelessWidget {
  final String initial;
  final double size;
  const _AvatarFallback({required this.initial, required this.size});

  @override
  Widget build(BuildContext context) {
    return Container(
      color: AppColors.postbookPrimary.withValues(alpha: 0.15),
      child: Center(
        child: Text(
          initial,
          style: AppTextStyles.label.copyWith(
            color: AppColors.postbookPrimary,
            fontSize: size * 0.38,
          ),
        ),
      ),
    );
  }
}

class _GroupCover extends StatelessWidget {
  final String? coverUrl;
  final List<Color> gradient;
  final double height;

  const _GroupCover({
    required this.coverUrl,
    required this.gradient,
    required this.height,
  });

  @override
  Widget build(BuildContext context) {
    if (coverUrl != null) {
      return SizedBox(
        height: height,
        width: double.infinity,
        child: Image.network(
          coverUrl!,
          fit: BoxFit.cover,
          errorBuilder: (_, _, _) => _GradientCover(
            gradient: gradient,
            height: height,
          ),
        ),
      );
    }
    return _GradientCover(gradient: gradient, height: height);
  }
}

class _GradientCover extends StatelessWidget {
  final List<Color> gradient;
  final double height;
  const _GradientCover({required this.gradient, required this.height});

  @override
  Widget build(BuildContext context) {
    return Container(
      height: height,
      width: double.infinity,
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: gradient,
        ),
      ),
    );
  }
}

class _PrivacyBadge extends StatelessWidget {
  final String level;
  const _PrivacyBadge({required this.level});

  @override
  Widget build(BuildContext context) {
    final (icon, color) = switch (level) {
      'restricted' => (Icons.shield_outlined, AppColors.statusWarning),
      'private' => (Icons.lock_outline, AppColors.statusError),
      _ => (Icons.public, AppColors.statusSuccess),
    };
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 11, color: color),
        const SizedBox(width: 2),
        Text(
          level.substring(0, 1).toUpperCase() + level.substring(1),
          style: AppTextStyles.labelSmall.copyWith(color: color, fontSize: 10),
        ),
      ],
    );
  }
}

class _EmptyState extends StatelessWidget {
  final IconData icon;
  final String title;
  final String? subtitle;
  final VoidCallback? onAction;
  final String? actionLabel;

  const _EmptyState({
    required this.icon,
    required this.title,
    this.subtitle,
    required this.onAction,
    this.actionLabel,
  });

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 60,
              height: 60,
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(18),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Icon(icon, color: AppColors.textMuted, size: 28),
            ),
            const SizedBox(height: 16),
            Text(title,
                style: AppTextStyles.label, textAlign: TextAlign.center),
            if (subtitle != null) ...[
              const SizedBox(height: 6),
              Text(
                subtitle!,
                style: AppTextStyles.bodySmall
                    .copyWith(color: AppColors.textMuted),
                textAlign: TextAlign.center,
              ),
            ],
            if (onAction != null && actionLabel != null) ...[
              const SizedBox(height: 20),
              GestureDetector(
                onTap: onAction,
                child: Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 20, vertical: 10),
                  decoration: BoxDecoration(
                    color: AppColors.textPrimary,
                    borderRadius: BorderRadius.circular(20),
                  ),
                  child: Text(
                    actionLabel!,
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.bgPrimary,
                    ),
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

String _fmtCount(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}K';
  return '$n';
}

List<Color> _categoryGradient(String? category) {
  return switch (category?.toLowerCase()) {
    'gaming' => [const Color(0xFF7C3AED), const Color(0xFF4338CA)],
    'technology' => [const Color(0xFF0EA5E9), const Color(0xFF2563EB)],
    'music' => [const Color(0xFFEC4899), const Color(0xFFBE185D)],
    'sports' => [const Color(0xFF16A34A), const Color(0xFF059669)],
    'art' => [const Color(0xFFF59E0B), const Color(0xFFEA580C)],
    'food' => [const Color(0xFFF97316), const Color(0xFFEAB308)],
    _ => [AppColors.postbookPrimary, AppColors.accentPurple],
  };
}
