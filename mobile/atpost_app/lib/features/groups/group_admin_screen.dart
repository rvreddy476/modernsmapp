import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/models/group_member.dart';
import 'package:atpost_app/data/models/group_post.dart';
import 'package:atpost_app/data/models/group_rule.dart';
import 'package:atpost_app/data/repositories/group_posts_repository.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:atpost_app/providers/group_posts_provider.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

enum _AdminSection {
  details,
  memberRequests,
  pendingPosts,
  banned,
  rules,
}

// ─────────────────────────────────────────────────────────────────────────────

class GroupAdminScreen extends ConsumerStatefulWidget {
  final String groupId;
  const GroupAdminScreen({super.key, required this.groupId});

  @override
  ConsumerState<GroupAdminScreen> createState() => _GroupAdminScreenState();
}

class _GroupAdminScreenState extends ConsumerState<GroupAdminScreen> {
  _AdminSection _section = _AdminSection.details;

  @override
  Widget build(BuildContext context) {
    final groupAsync = ref.watch(groupDetailProvider(widget.groupId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded,
              color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Space Settings', style: AppTextStyles.h2),
      ),
      body: groupAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Text('Failed to load space.',
              style: AppTextStyles.body),
        ),
        data: (group) => _AdminBody(
          groupId: widget.groupId,
          group: group,
          section: _section,
          onSectionChanged: (s) => setState(() => _section = s),
        ),
      ),
    );
  }
}

// ─── Admin body ──────────────────────────────────────────────────────────────

class _AdminBody extends ConsumerWidget {
  final String groupId;
  final Group group;
  final _AdminSection section;
  final ValueChanged<_AdminSection> onSectionChanged;

  const _AdminBody({
    required this.groupId,
    required this.group,
    required this.section,
    required this.onSectionChanged,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final requestsAsync = ref.watch(joinRequestsProvider(groupId));
    final pendingAsync = ref.watch(pendingPostsProvider(groupId));

    final requestBadge = requestsAsync.valueOrNull?.length ?? 0;
    final pendingBadge = pendingAsync.valueOrNull?.length ?? 0;

    final menuItems = [
      (
        section: _AdminSection.details,
        icon: Icons.tune_outlined,
        label: 'Space details',
        badge: 0,
      ),
      (
        section: _AdminSection.memberRequests,
        icon: Icons.person_add_outlined,
        label: 'Member requests',
        badge: requestBadge,
      ),
      (
        section: _AdminSection.pendingPosts,
        icon: Icons.pending_actions_outlined,
        label: 'Pending posts',
        badge: pendingBadge,
      ),
      (
        section: _AdminSection.banned,
        icon: Icons.block_outlined,
        label: 'Banned',
        badge: 0,
      ),
      (
        section: _AdminSection.rules,
        icon: Icons.gavel_outlined,
        label: 'Rules',
        badge: 0,
      ),
    ];

    return Row(
      children: [
        // Left menu rail
        Container(
          width: 200,
          decoration: BoxDecoration(
            border: Border(
              right: BorderSide(color: AppColors.borderSubtle),
            ),
          ),
          child: ListView(
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: menuItems.map((item) {
              final isSelected = item.section == section;
              return GestureDetector(
                onTap: () => onSectionChanged(item.section),
                child: AnimatedContainer(
                  duration: const Duration(milliseconds: 120),
                  margin:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  padding: const EdgeInsets.symmetric(
                      horizontal: 12, vertical: 10),
                  decoration: BoxDecoration(
                    color: isSelected
                        ? AppColors.textPrimary.withValues(alpha: 0.08)
                        : Colors.transparent,
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: Row(
                    children: [
                      Icon(
                        item.icon,
                        size: 18,
                        color: isSelected
                            ? AppColors.textPrimary
                            : AppColors.textMuted,
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          item.label,
                          style: AppTextStyles.labelSmall.copyWith(
                            color: isSelected
                                ? AppColors.textPrimary
                                : AppColors.textMuted,
                            fontWeight: isSelected
                                ? FontWeight.w700
                                : FontWeight.w500,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      if (item.badge > 0)
                        Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 5, vertical: 1),
                          decoration: BoxDecoration(
                            color: AppColors.postbookPrimary,
                            borderRadius: BorderRadius.circular(8),
                          ),
                          child: Text(
                            '${item.badge}',
                            style: const TextStyle(
                              color: Colors.white,
                              fontSize: 9,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              );
            }).toList(),
          ),
        ),

        // Right content
        Expanded(
          child: switch (section) {
            _AdminSection.details => _DetailsPanel(
                groupId: groupId,
                group: group,
              ),
            _AdminSection.memberRequests => _MemberRequestsPanel(
                groupId: groupId,
              ),
            _AdminSection.pendingPosts => _PendingPostsPanel(
                groupId: groupId,
              ),
            _AdminSection.banned => _BannedPanel(groupId: groupId),
            _AdminSection.rules => _RulesPanel(groupId: groupId),
          },
        ),
      ],
    );
  }
}

// ─── Details panel ───────────────────────────────────────────────────────────

class _DetailsPanel extends ConsumerStatefulWidget {
  final String groupId;
  final Group group;
  const _DetailsPanel({required this.groupId, required this.group});

  @override
  ConsumerState<_DetailsPanel> createState() => _DetailsPanelState();
}

class _DetailsPanelState extends ConsumerState<_DetailsPanel> {
  late final TextEditingController _name;
  late final TextEditingController _desc;
  late String _privacy;
  late bool _isMature;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    _name = TextEditingController(text: widget.group.name);
    _desc = TextEditingController(text: widget.group.description);
    _privacy = widget.group.privacyLevel;
    _isMature = widget.group.isMature;
  }

  @override
  void dispose() {
    _name.dispose();
    _desc.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    if (_saving) return;
    setState(() => _saving = true);
    try {
      await ref.read(groupsRepositoryProvider).updateGroup(
            widget.groupId,
            name: _name.text.trim(),
            description: _desc.text.trim(),
            privacy: _privacy,
            isMature: _isMature,
          );
      ref.invalidate(groupDetailProvider(widget.groupId));
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Space updated.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save changes.')),
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _delete(BuildContext context) async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Delete Space', style: AppTextStyles.h3),
        content: Text(
          'This will permanently delete "${widget.group.name}" and all its posts. This cannot be undone.',
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(
              'Delete',
              style: TextStyle(color: AppColors.statusError),
            ),
          ),
        ],
      ),
    );
    if (confirm != true) return;
    try {
      await ref.read(groupsRepositoryProvider).deleteGroup(widget.groupId);
      if (!mounted) return;
      context.go('/groups');
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not delete space.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Space details', style: AppTextStyles.h3),
          const SizedBox(height: 14),

          Text('Name', style: AppTextStyles.label),
          const SizedBox(height: 6),
          TextField(
            controller: _name,
            maxLength: 60,
            style: AppTextStyles.body,
          ),

          const SizedBox(height: 10),

          Text('Description', style: AppTextStyles.label),
          const SizedBox(height: 6),
          TextField(
            controller: _desc,
            maxLength: 240,
            minLines: 3,
            maxLines: 5,
            style: AppTextStyles.body,
          ),

          const SizedBox(height: 10),

          Text('Privacy', style: AppTextStyles.label),
          const SizedBox(height: 8),
          ...[
            _AdminPrivacyOption(
              title: 'Public',
              value: 'public',
              selected: _privacy,
              onTap: (v) => setState(() => _privacy = v),
            ),
            const SizedBox(height: 6),
            _AdminPrivacyOption(
              title: 'Restricted',
              value: 'restricted',
              selected: _privacy,
              onTap: (v) => setState(() => _privacy = v),
            ),
            const SizedBox(height: 6),
            _AdminPrivacyOption(
              title: 'Private',
              value: 'private',
              selected: _privacy,
              onTap: (v) => setState(() => _privacy = v),
            ),
          ],

          const SizedBox(height: 12),

          // Mature toggle
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('Mature content (18+)',
                          style: AppTextStyles.label),
                      Text('Marks space as adult content',
                          style: AppTextStyles.labelSmall
                              .copyWith(color: AppColors.textMuted)),
                    ],
                  ),
                ),
                Switch(
                  value: _isMature,
                  onChanged: (v) => setState(() => _isMature = v),
                  activeColor: AppColors.statusError,
                ),
              ],
            ),
          ),

          const SizedBox(height: 20),

          SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              onPressed: _saving ? null : _save,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.textPrimary,
                foregroundColor: AppColors.bgPrimary,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
              child: _saving
                  ? const SizedBox(
                      width: 14,
                      height: 14,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: AppColors.bgPrimary,
                      ),
                    )
                  : const Text('Save changes'),
            ),
          ),

          const SizedBox(height: 32),

          // Danger zone
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: AppColors.statusError.withValues(alpha: 0.05),
              borderRadius: BorderRadius.circular(12),
              border: Border.all(
                  color: AppColors.statusError.withValues(alpha: 0.25)),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Danger zone',
                  style: AppTextStyles.label
                      .copyWith(color: AppColors.statusError),
                ),
                const SizedBox(height: 6),
                Text(
                  'Deleting a space is permanent and cannot be undone.',
                  style: AppTextStyles.labelSmall
                      .copyWith(color: AppColors.textMuted),
                ),
                const SizedBox(height: 12),
                OutlinedButton.icon(
                  onPressed: () => _delete(context),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.statusError,
                    side:
                        BorderSide(color: AppColors.statusError),
                  ),
                  icon: const Icon(Icons.delete_outline, size: 16),
                  label: const Text('Delete Space'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _AdminPrivacyOption extends StatelessWidget {
  final String title;
  final String value;
  final String selected;
  final ValueChanged<String> onTap;

  const _AdminPrivacyOption({
    required this.title,
    required this.value,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isSelected = value == selected;
    return GestureDetector(
      onTap: () => onTap(value),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        decoration: BoxDecoration(
          color: isSelected
              ? AppColors.textPrimary.withValues(alpha: 0.06)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: isSelected ? AppColors.textPrimary : AppColors.borderSubtle,
          ),
        ),
        child: Row(
          children: [
            Expanded(
              child: Text(title, style: AppTextStyles.labelSmall),
            ),
            if (isSelected)
              const Icon(Icons.check_rounded,
                  size: 14, color: AppColors.textPrimary),
          ],
        ),
      ),
    );
  }
}

// ─── Member requests ─────────────────────────────────────────────────────────

class _MemberRequestsPanel extends ConsumerWidget {
  final String groupId;
  const _MemberRequestsPanel({required this.groupId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final requestsAsync = ref.watch(joinRequestsProvider(groupId));

    return requestsAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Text('Could not load requests.',
            style: AppTextStyles.bodySmall),
      ),
      data: (requests) {
        if (requests.isEmpty) {
          return _AdminEmptyState(
            icon: Icons.person_add_outlined,
            message: 'No pending member requests.',
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 80),
          itemCount: requests.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, i) {
            final r = requests[i];
            final userId = r['user_id'] as String? ?? '';
            final name = r['display_name'] as String? ??
                r['username'] as String? ??
                userId;
            return _JoinRequestTile(
              groupId: groupId,
              userId: userId,
              displayName: name,
            );
          },
        );
      },
    );
  }
}

class _JoinRequestTile extends ConsumerStatefulWidget {
  final String groupId;
  final String userId;
  final String displayName;

  const _JoinRequestTile({
    required this.groupId,
    required this.userId,
    required this.displayName,
  });

  @override
  ConsumerState<_JoinRequestTile> createState() => _JoinRequestTileState();
}

class _JoinRequestTileState extends ConsumerState<_JoinRequestTile> {
  bool _busy = false;

  Future<void> _approve() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(groupPostsRepositoryProvider)
          .approveJoinRequest(widget.groupId, widget.userId);
      ref.invalidate(joinRequestsProvider(widget.groupId));
      ref.invalidate(groupMembersProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to approve request.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 16,
            backgroundColor:
                AppColors.postbookPrimary.withValues(alpha: 0.2),
            child: Text(
              widget.displayName.isNotEmpty
                  ? widget.displayName[0].toUpperCase()
                  : '?',
              style: AppTextStyles.labelSmall
                  .copyWith(color: AppColors.postbookPrimary),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(widget.displayName, style: AppTextStyles.label),
          ),
          if (_busy)
            const SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(
                  strokeWidth: 2, color: AppColors.postbookPrimary),
            )
          else
            IconButton(
              icon: const Icon(Icons.check_circle_outline,
                  color: AppColors.statusSuccess, size: 20),
              onPressed: _approve,
            ),
        ],
      ),
    );
  }
}

// ─── Pending posts ───────────────────────────────────────────────────────────

class _PendingPostsPanel extends ConsumerWidget {
  final String groupId;
  const _PendingPostsPanel({required this.groupId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final postsAsync = ref.watch(pendingPostsProvider(groupId));

    return postsAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Text('Could not load pending posts.',
            style: AppTextStyles.bodySmall),
      ),
      data: (posts) {
        if (posts.isEmpty) {
          return _AdminEmptyState(
            icon: Icons.pending_actions_outlined,
            message: 'No posts awaiting approval.',
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 80),
          itemCount: posts.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, i) =>
              _PendingPostTile(groupId: groupId, post: posts[i]),
        );
      },
    );
  }
}

class _PendingPostTile extends ConsumerStatefulWidget {
  final String groupId;
  final GroupPost post;
  const _PendingPostTile({required this.groupId, required this.post});

  @override
  ConsumerState<_PendingPostTile> createState() => _PendingPostTileState();
}

class _PendingPostTileState extends ConsumerState<_PendingPostTile> {
  bool _busy = false;

  Future<void> _approve() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(groupPostsRepositoryProvider)
          .approvePost(widget.groupId, widget.post.id);
      ref.invalidate(pendingPostsProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to approve.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _reject() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(groupPostsRepositoryProvider)
          .rejectPost(widget.groupId, widget.post.id);
      ref.invalidate(pendingPostsProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to reject.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            widget.post.body ?? widget.post.title ?? '(no content)',
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 4),
          Text(
            'by ${widget.post.authorName ?? 'Unknown'}',
            style: AppTextStyles.labelSmall
                .copyWith(color: AppColors.textMuted),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              if (_busy)
                const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: AppColors.postbookPrimary,
                  ),
                )
              else ...[
                _SmallBtn(
                  label: 'Approve',
                  color: AppColors.statusSuccess,
                  onTap: _approve,
                ),
                const SizedBox(width: 8),
                _SmallBtn(
                  label: 'Reject',
                  color: AppColors.statusError,
                  onTap: _reject,
                ),
              ],
            ],
          ),
        ],
      ),
    );
  }
}

// ─── Banned members ──────────────────────────────────────────────────────────

class _BannedPanel extends ConsumerWidget {
  final String groupId;
  const _BannedPanel({required this.groupId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final bannedAsync = ref.watch(groupBannedProvider(groupId));

    return bannedAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Text('Could not load bans.', style: AppTextStyles.bodySmall),
      ),
      data: (members) {
        if (members.isEmpty) {
          return _AdminEmptyState(
            icon: Icons.block_outlined,
            message: 'No banned members.',
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 80),
          itemCount: members.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, i) =>
              _BannedTile(groupId: groupId, member: members[i]),
        );
      },
    );
  }
}

class _BannedTile extends ConsumerStatefulWidget {
  final String groupId;
  final GroupMember member;
  const _BannedTile({required this.groupId, required this.member});

  @override
  ConsumerState<_BannedTile> createState() => _BannedTileState();
}

class _BannedTileState extends ConsumerState<_BannedTile> {
  bool _busy = false;

  Future<void> _unban() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(groupsRepositoryProvider)
          .unbanMember(widget.groupId, widget.member.userId);
      ref.invalidate(groupBannedProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not unban member.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final m = widget.member;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 16,
            backgroundColor:
                AppColors.statusError.withValues(alpha: 0.15),
            child: Text(
              m.avatarInitial,
              style: AppTextStyles.labelSmall
                  .copyWith(color: AppColors.statusError),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(m.displayLabel, style: AppTextStyles.label),
                if (m.removalReason != null)
                  Text(
                    'Reason: ${m.removalReason}',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.textMuted),
                  ),
              ],
            ),
          ),
          if (_busy)
            const SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(
                  strokeWidth: 2, color: AppColors.postbookPrimary),
            )
          else
            _SmallBtn(
              label: 'Unban',
              color: AppColors.statusSuccess,
              onTap: _unban,
            ),
        ],
      ),
    );
  }
}

// ─── Rules panel ─────────────────────────────────────────────────────────────

class _RulesPanel extends ConsumerWidget {
  final String groupId;
  const _RulesPanel({required this.groupId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rulesAsync = ref.watch(groupRulesProvider(groupId));

    return rulesAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Text('Could not load rules.', style: AppTextStyles.bodySmall),
      ),
      data: (rules) => Column(
        children: [
          Expanded(
            child: rules.isEmpty
                ? _AdminEmptyState(
                    icon: Icons.gavel_outlined,
                    message: 'No rules yet. Add one below.',
                  )
                : ListView.separated(
                    padding: AppSpacing.pagePadding
                        .copyWith(top: 12, bottom: 80),
                    itemCount: rules.length,
                    separatorBuilder: (_, _) => const SizedBox(height: 8),
                    itemBuilder: (context, i) =>
                        _RuleAdminTile(
                          groupId: groupId,
                          rule: rules[i],
                          index: i,
                        ),
                  ),
          ),
          Padding(
            padding: AppSpacing.pagePadding.copyWith(bottom: 12),
            child: SizedBox(
              width: double.infinity,
              child: OutlinedButton.icon(
                onPressed: () =>
                    _addRuleDialog(context, ref, groupId),
                style: OutlinedButton.styleFrom(
                  foregroundColor: AppColors.textSecondary,
                  side: const BorderSide(color: AppColors.borderSubtle),
                ),
                icon: const Icon(Icons.add, size: 16),
                label: const Text('Add Rule'),
              ),
            ),
          ),
        ],
      ),
    );
  }

  void _addRuleDialog(
      BuildContext context, WidgetRef ref, String groupId) async {
    final titleCtrl = TextEditingController();
    final descCtrl = TextEditingController();

    await showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Add Rule', style: AppTextStyles.h3),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              controller: titleCtrl,
              autofocus: true,
              style: AppTextStyles.body,
              decoration: const InputDecoration(labelText: 'Rule title'),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: descCtrl,
              style: AppTextStyles.body,
              decoration: const InputDecoration(
                  labelText: 'Description (optional)'),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () async {
              final title = titleCtrl.text.trim();
              if (title.isEmpty) return;
              try {
                await ref.read(groupsRepositoryProvider).createRule(
                      groupId,
                      title: title,
                      description: descCtrl.text.trim(),
                    );
                ref.invalidate(groupRulesProvider(groupId));
                if (ctx.mounted) Navigator.of(ctx).pop();
              } catch (_) {
                if (ctx.mounted) {
                  ScaffoldMessenger.of(ctx).showSnackBar(
                    const SnackBar(content: Text('Could not add rule.')),
                  );
                }
              }
            },
            child: const Text('Add'),
          ),
        ],
      ),
    );

    titleCtrl.dispose();
    descCtrl.dispose();
  }
}

class _RuleAdminTile extends ConsumerStatefulWidget {
  final String groupId;
  final GroupRule rule;
  final int index;
  const _RuleAdminTile(
      {required this.groupId, required this.rule, required this.index});

  @override
  ConsumerState<_RuleAdminTile> createState() => _RuleAdminTileState();
}

class _RuleAdminTileState extends ConsumerState<_RuleAdminTile> {
  bool _deleting = false;

  Future<void> _delete() async {
    if (_deleting) return;
    setState(() => _deleting = true);
    try {
      await ref
          .read(groupsRepositoryProvider)
          .deleteRule(widget.groupId, widget.rule.id);
      ref.invalidate(groupRulesProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not delete rule.')),
        );
      }
    } finally {
      if (mounted) setState(() => _deleting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 22,
            height: 22,
            decoration: BoxDecoration(
              color: AppColors.postbookPrimary.withValues(alpha: 0.15),
              shape: BoxShape.circle,
            ),
            child: Center(
              child: Text(
                '${widget.index + 1}',
                style: TextStyle(
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(widget.rule.title, style: AppTextStyles.label),
                if (widget.rule.description.isNotEmpty)
                  Text(widget.rule.description,
                      style: AppTextStyles.labelSmall
                          .copyWith(color: AppColors.textMuted)),
              ],
            ),
          ),
          if (_deleting)
            const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(
                  strokeWidth: 2, color: AppColors.statusError),
            )
          else
            IconButton(
              icon: const Icon(Icons.delete_outline,
                  color: AppColors.statusError, size: 18),
              onPressed: _delete,
            ),
        ],
      ),
    );
  }
}

// ─── Shared helpers ──────────────────────────────────────────────────────────

class _AdminEmptyState extends StatelessWidget {
  final IconData icon;
  final String message;
  const _AdminEmptyState({required this.icon, required this.message});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 40, color: AppColors.textMuted),
          const SizedBox(height: 12),
          Text(message,
              style: AppTextStyles.bodySmall
                  .copyWith(color: AppColors.textMuted)),
        ],
      ),
    );
  }
}

class _SmallBtn extends StatelessWidget {
  final String label;
  final Color color;
  final VoidCallback onTap;
  const _SmallBtn(
      {required this.label, required this.color, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: color.withValues(alpha: 0.3)),
        ),
        child: Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: color,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}
