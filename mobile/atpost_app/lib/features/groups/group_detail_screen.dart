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
import 'package:atpost_app/shared/widgets/group_post_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

enum _GroupTab { about, discussion, people, media, rules, manage }

// ─────────────────────────────────────────────────────────────────────────────

class GroupDetailScreen extends ConsumerStatefulWidget {
  const GroupDetailScreen({super.key, required this.groupId});
  final String groupId;

  @override
  ConsumerState<GroupDetailScreen> createState() => _GroupDetailScreenState();
}

class _GroupDetailScreenState extends ConsumerState<GroupDetailScreen> {
  @override
  Widget build(BuildContext context) {
    final groupAsync = ref.watch(groupDetailProvider(widget.groupId));

    return groupAsync.when(
      loading: () => Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
      ),
      error: (_, _) => Scaffold(
        backgroundColor: AppColors.bgPrimary,
        appBar: AppBar(
          backgroundColor: AppColors.bgPrimary,
          leading: IconButton(
            onPressed: () => context.pop(),
            icon: const Icon(Icons.arrow_back_rounded),
          ),
        ),
        body: Center(
          child: _RetryCard(
            message: 'Failed to load this space.',
            onRetry: () =>
                ref.invalidate(groupDetailProvider(widget.groupId)),
          ),
        ),
      ),
      data: (group) =>
          _GroupDetailBody(group: group, groupId: widget.groupId),
    );
  }
}

// ─────────────────────────────────────────────────────────────────────────────

class _GroupDetailBody extends ConsumerStatefulWidget {
  const _GroupDetailBody({required this.group, required this.groupId});
  final Group group;
  final String groupId;

  @override
  ConsumerState<_GroupDetailBody> createState() => _GroupDetailBodyState();
}

class _GroupDetailBodyState extends ConsumerState<_GroupDetailBody>
    with SingleTickerProviderStateMixin {
  late TabController _tabController;
  late bool _joined;
  late int _memberCount;
  bool _joinBusy = false;
  String? _selectedChannelId;

  List<_GroupTab> get _tabs {
    final base = [
      _GroupTab.about,
      _GroupTab.discussion,
      _GroupTab.people,
      _GroupTab.media,
      _GroupTab.rules,
    ];
    if (widget.group.isAdminOrMod) base.add(_GroupTab.manage);
    return base;
  }

  @override
  void initState() {
    super.initState();
    _joined = widget.group.isMember;
    _memberCount = widget.group.memberCount;
    _tabController = TabController(length: _tabs.length, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  Future<void> _toggleJoin() async {
    if (_joinBusy) return;
    final wasJoined = _joined;
    setState(() {
      _joinBusy = true;
      _joined = !wasJoined;
      _memberCount += _joined ? 1 : -1;
      if (_memberCount < 0) _memberCount = 0;
    });
    try {
      final repo = ref.read(groupsRepositoryProvider);
      if (wasJoined) {
        await repo.leaveGroup(widget.groupId);
      } else {
        await repo.joinGroup(widget.groupId);
      }
      ref.invalidate(groupDetailProvider(widget.groupId));
      ref.invalidate(groupsProvider);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _joined = wasJoined;
        _memberCount += wasJoined ? 1 : -1;
        if (_memberCount < 0) _memberCount = 0;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(wasJoined
              ? 'Could not leave space.'
              : 'Could not join space.'),
        ),
      );
    } finally {
      if (mounted) setState(() => _joinBusy = false);
    }
  }

  String _tabLabel(_GroupTab t) => switch (t) {
        _GroupTab.about => 'About',
        _GroupTab.discussion => 'Discussion',
        _GroupTab.people => 'People',
        _GroupTab.media => 'Media',
        _GroupTab.rules => 'Rules',
        _GroupTab.manage => 'Manage',
      };

  @override
  Widget build(BuildContext context) {
    final group = widget.group;
    final tabs = _tabs;

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: NestedScrollView(
        headerSliverBuilder: (context, innerBoxIsScrolled) => [
          // Cover + back button
          SliverAppBar(
            backgroundColor: AppColors.bgPrimary,
            expandedHeight: 200,
            pinned: true,
            elevation: 0,
            leading: IconButton(
              onPressed: () => context.pop(),
              icon: const Icon(Icons.arrow_back_rounded),
            ),
            actions: [
              IconButton(
                icon: const Icon(Icons.more_horiz),
                onPressed: () => _showOverflow(context, group),
              ),
            ],
            flexibleSpace: FlexibleSpaceBar(
              collapseMode: CollapseMode.pin,
              background: Stack(
                fit: StackFit.expand,
                children: [
                  _buildCover(group),
                  // gradient overlay
                  const DecoratedBox(
                    decoration: BoxDecoration(
                      gradient: LinearGradient(
                        begin: Alignment.topCenter,
                        end: Alignment.bottomCenter,
                        colors: [Colors.transparent, Color(0xCC000000)],
                        stops: [0.5, 1.0],
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),

          // Identity block (avatar, name, badges, actions)
          SliverToBoxAdapter(
            child: _IdentityBlock(
              group: group,
              memberCount: _memberCount,
              joined: _joined,
              joinBusy: _joinBusy,
              onToggleJoin: _toggleJoin,
              onInvite: () => _showInviteDialog(context),
              onCreatePost: () =>
                  context.push('/groups/${widget.groupId}/post'),
            ),
          ),

          // Sticky tab bar
          SliverPersistentHeader(
            pinned: true,
            delegate: _TabBarDelegate(
              TabBar(
                controller: _tabController,
                isScrollable: true,
                tabAlignment: TabAlignment.start,
                labelColor: AppColors.textPrimary,
                unselectedLabelColor: AppColors.textMuted,
                indicatorColor: AppColors.textPrimary,
                indicatorWeight: 2,
                labelStyle: AppTextStyles.label.copyWith(
                  fontWeight: FontWeight.w700,
                ),
                unselectedLabelStyle: AppTextStyles.label.copyWith(
                  fontWeight: FontWeight.w500,
                ),
                dividerColor: AppColors.borderSubtle,
                tabs: tabs.map((t) => Tab(text: _tabLabel(t))).toList(),
              ),
            ),
          ),
        ],
        body: TabBarView(
          controller: _tabController,
          children: tabs.map((t) => _buildTabContent(t, group)).toList(),
        ),
      ),
    );
  }

  Widget _buildCover(Group group) {
    if (group.coverUrl != null) {
      return Image.network(
        group.coverUrl!,
        fit: BoxFit.cover,
        errorBuilder: (_, _, _) => _gradientCover(group),
      );
    }
    return _gradientCover(group);
  }

  Widget _gradientCover(Group group) {
    final colors = _categoryColors(group.category);
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: colors,
        ),
      ),
    );
  }

  Widget _buildTabContent(_GroupTab tab, Group group) {
    return switch (tab) {
      _GroupTab.about => _AboutTab(group: group),
      _GroupTab.discussion => _DiscussionTab(
          groupId: widget.groupId,
          channelId: _selectedChannelId,
          isMember: _joined,
          onChannelChanged: (id) =>
              setState(() => _selectedChannelId = id),
        ),
      _GroupTab.people => _PeopleTab(groupId: widget.groupId, group: group),
      _GroupTab.media => _MediaTab(groupId: widget.groupId),
      _GroupTab.rules => _RulesTab(groupId: widget.groupId, isAdmin: group.isAdminOrMod),
      _GroupTab.manage => _ManageTab(groupId: widget.groupId, group: group),
    };
  }

  void _showInviteDialog(BuildContext context) async {
    final controller = TextEditingController();
    final userId = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Invite to Space', style: AppTextStyles.h3),
        content: TextField(
          controller: controller,
          autofocus: true,
          style: AppTextStyles.body,
          decoration: const InputDecoration(
            labelText: 'User ID',
            hintText: 'Paste the user ID to invite',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(controller.text.trim()),
            child: const Text('Invite'),
          ),
        ],
      ),
    );
    controller.dispose();
    if (userId == null || userId.isEmpty) return;
    try {
      await ref.read(groupsRepositoryProvider).inviteUser(widget.groupId, userId);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Invite sent.')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Could not send invite.')));
    }
  }

  void _showOverflow(BuildContext context, Group group) {
    showModalBottomSheet(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (_) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.share_outlined,
                  color: AppColors.textSecondary),
              title: Text('Share Space', style: AppTextStyles.body),
              onTap: () => Navigator.of(context).pop(),
            ),
            if (_joined)
              ListTile(
                leading: const Icon(Icons.logout,
                    color: AppColors.statusError),
                title: Text('Leave Space',
                    style: AppTextStyles.body
                        .copyWith(color: AppColors.statusError)),
                onTap: () {
                  Navigator.of(context).pop();
                  _toggleJoin();
                },
              ),
          ],
        ),
      ),
    );
  }
}

// ─── Identity block ──────────────────────────────────────────────────────────

class _IdentityBlock extends StatelessWidget {
  final Group group;
  final int memberCount;
  final bool joined;
  final bool joinBusy;
  final VoidCallback onToggleJoin;
  final VoidCallback onInvite;
  final VoidCallback onCreatePost;

  const _IdentityBlock({
    required this.group,
    required this.memberCount,
    required this.joined,
    required this.joinBusy,
    required this.onToggleJoin,
    required this.onInvite,
    required this.onCreatePost,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Avatar + name row
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              _GroupAvatar(group: group, size: 56),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      group.name,
                      style: AppTextStyles.h2,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (group.handle != null)
                      Text(
                        '@${group.handle}',
                        style: AppTextStyles.labelSmall
                            .copyWith(color: AppColors.textMuted),
                      ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          // Badges row
          Wrap(
            spacing: 6,
            runSpacing: 4,
            children: [
              _PrivacyChip(level: group.privacyLevel),
              _MetaChip(
                icon: Icons.people_outline,
                label: '$memberCount members',
              ),
              if (group.isMature)
                _MetaChip(
                  icon: Icons.warning_amber_outlined,
                  label: '18+',
                  color: AppColors.statusError,
                ),
              if (group.category != null)
                _MetaChip(
                  icon: Icons.tag_outlined,
                  label: group.category!,
                ),
            ],
          ),
          if (group.description.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              group.description,
              maxLines: 3,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.bodySmall
                  .copyWith(color: AppColors.textSecondary),
            ),
          ],
          const SizedBox(height: 12),
          // Action buttons row
          _ActionButtons(
            group: group,
            joined: joined,
            joinBusy: joinBusy,
            onToggleJoin: onToggleJoin,
            onInvite: onInvite,
            onCreatePost: onCreatePost,
          ),
        ],
      ),
    );
  }
}

class _GroupAvatar extends StatelessWidget {
  final Group group;
  final double size;
  const _GroupAvatar({required this.group, required this.size});

  @override
  Widget build(BuildContext context) {
    final colors = _categoryColors(group.category);
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(size * 0.25),
        border: Border.all(color: AppColors.borderMedium, width: 2),
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(size * 0.25 - 2),
        child: group.avatarUrl != null
            ? Image.network(
                group.avatarUrl!,
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => _AvatarPlaceholder(
                  name: group.name,
                  colors: colors,
                  size: size,
                ),
              )
            : _AvatarPlaceholder(
                name: group.name,
                colors: colors,
                size: size,
              ),
      ),
    );
  }
}

class _AvatarPlaceholder extends StatelessWidget {
  final String name;
  final List<Color> colors;
  final double size;
  const _AvatarPlaceholder(
      {required this.name, required this.colors, required this.size});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(colors: colors),
      ),
      child: Center(
        child: Text(
          name.isNotEmpty ? name[0].toUpperCase() : '?',
          style: AppTextStyles.h1.copyWith(
            fontSize: size * 0.45,
            color: Colors.white,
          ),
        ),
      ),
    );
  }
}

class _PrivacyChip extends StatelessWidget {
  final String level;
  const _PrivacyChip({required this.level});

  @override
  Widget build(BuildContext context) {
    final (icon, label, color) = switch (level) {
      'restricted' => (
          Icons.shield_outlined,
          'Restricted',
          AppColors.statusWarning
        ),
      'private' => (Icons.lock_outline, 'Private', AppColors.statusError),
      _ => (Icons.public, 'Public', AppColors.statusSuccess),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: color.withValues(alpha: 0.3)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 11, color: color),
          const SizedBox(width: 3),
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(
              color: color,
              fontSize: 10,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }
}

class _MetaChip extends StatelessWidget {
  final IconData icon;
  final String label;
  final Color? color;

  const _MetaChip({required this.icon, required this.label, this.color});

  @override
  Widget build(BuildContext context) {
    final c = color ?? AppColors.textMuted;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 11, color: c),
        const SizedBox(width: 3),
        Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(color: c, fontSize: 11),
        ),
      ],
    );
  }
}

class _ActionButtons extends StatelessWidget {
  final Group group;
  final bool joined;
  final bool joinBusy;
  final VoidCallback onToggleJoin;
  final VoidCallback onInvite;
  final VoidCallback onCreatePost;

  const _ActionButtons({
    required this.group,
    required this.joined,
    required this.joinBusy,
    required this.onToggleJoin,
    required this.onInvite,
    required this.onCreatePost,
  });

  @override
  Widget build(BuildContext context) {
    if (!joined) {
      // Non-member CTA
      final isInviteOnly = group.joinMode == 'invite_only' ||
          group.privacyLevel == 'private';
      if (isInviteOnly) {
        return Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.lock_outline,
                  size: 14, color: AppColors.textMuted),
              const SizedBox(width: 6),
              Text('Invite only',
                  style: AppTextStyles.label
                      .copyWith(color: AppColors.textMuted)),
            ],
          ),
        );
      }
      return SizedBox(
        width: double.infinity,
        child: ElevatedButton.icon(
          onPressed: joinBusy ? null : onToggleJoin,
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.textPrimary,
            foregroundColor: AppColors.bgPrimary,
            shape:
                RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
            padding: const EdgeInsets.symmetric(vertical: 12),
          ),
          icon: joinBusy
              ? const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(
                      strokeWidth: 2, color: AppColors.bgPrimary),
                )
              : Icon(
                  group.joinMode == 'request'
                      ? Icons.hourglass_empty_outlined
                      : Icons.group_add_outlined,
                  size: 16,
                ),
          label: Text(
            joinBusy
                ? 'Joining…'
                : group.joinMode == 'request'
                    ? 'Request to join'
                    : 'Join Space',
          ),
        ),
      );
    }

    // Member action row
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        _ActionBtn(
          label: 'Create Post',
          icon: Icons.edit_outlined,
          onTap: onCreatePost,
          filled: true,
        ),
        _ActionBtn(
          label: 'Invite',
          icon: Icons.person_add_alt_1_outlined,
          onTap: onInvite,
        ),
        _ActionBtn(
          label: joined ? 'Joined' : 'Join',
          icon: joined ? Icons.check_circle_outline : Icons.group_add_outlined,
          onTap: joinBusy ? null : onToggleJoin,
          color: joined ? AppColors.statusSuccess : null,
        ),
      ],
    );
  }
}

class _ActionBtn extends StatelessWidget {
  final String label;
  final IconData icon;
  final VoidCallback? onTap;
  final bool filled;
  final Color? color;

  const _ActionBtn({
    required this.label,
    required this.icon,
    required this.onTap,
    this.filled = false,
    this.color,
  });

  @override
  Widget build(BuildContext context) {
    final c = color ?? AppColors.textSecondary;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: filled ? AppColors.textPrimary : AppColors.bgCard,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: filled ? Colors.transparent : AppColors.borderSubtle,
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon,
                size: 14,
                color: filled ? AppColors.bgPrimary : c),
            const SizedBox(width: 5),
            Text(
              label,
              style: AppTextStyles.labelSmall.copyWith(
                color: filled ? AppColors.bgPrimary : c,
                fontWeight: FontWeight.w600,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ─── Tab: About ──────────────────────────────────────────────────────────────

class _AboutTab extends StatelessWidget {
  final Group group;
  const _AboutTab({required this.group});

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (group.description.isNotEmpty) ...[
            _InfoCard(
              title: 'About this space',
              value: group.description,
            ),
            const SizedBox(height: 10),
          ],
          _InfoCard(
            title: 'Privacy',
            value: _privacyDescription(group.privacyLevel),
          ),
          const SizedBox(height: 10),
          _InfoCard(
            title: 'Created',
            value: _formatDate(group.createdAt),
          ),
          if (group.category != null) ...[
            const SizedBox(height: 10),
            _InfoCard(title: 'Category', value: group.category!),
          ],
          if (group.location != null) ...[
            const SizedBox(height: 10),
            _InfoCard(title: 'Location', value: group.location!),
          ],
          const SizedBox(height: 10),
          _InfoCard(
            title: 'Posts',
            value: group.postCount.toString(),
          ),
          const SizedBox(height: 10),
          _InfoCard(
            title: 'Members',
            value: group.memberCount.toString(),
          ),
        ],
      ),
    );
  }

  String _privacyDescription(String level) {
    return switch (level) {
      'public' => 'Anyone can find and join this space.',
      'restricted' => 'Visible to all; joining requires approval.',
      'private' => 'Invite only and hidden from discovery.',
      _ => level,
    };
  }

  String _formatDate(DateTime date) {
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return '${date.day} ${months[date.month - 1]} ${date.year}';
  }
}

// ─── Tab: Discussion ─────────────────────────────────────────────────────────

class _DiscussionTab extends ConsumerWidget {
  final String groupId;
  final String? channelId;
  final bool isMember;
  final ValueChanged<String?> onChannelChanged;

  const _DiscussionTab({
    required this.groupId,
    required this.channelId,
    required this.isMember,
    required this.onChannelChanged,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final postsAsync = ref.watch(
      groupPostsProvider(
        GroupPostsParams(groupId: groupId, channelId: channelId),
      ),
    );
    final channelsAsync = ref.watch(groupChannelsProvider(groupId));

    return CustomScrollView(
      slivers: [
        // Channel chips
        SliverToBoxAdapter(
          child: channelsAsync.valueOrNull?.isNotEmpty == true
              ? _ChannelChips(
                  channels: channelsAsync.value!,
                  selected: channelId,
                  onSelect: onChannelChanged,
                )
              : const SizedBox.shrink(),
        ),

        // Mini composer
        if (isMember)
          SliverToBoxAdapter(
            child: Padding(
              padding:
                  AppSpacing.pagePadding.copyWith(top: 8, bottom: 4),
              child: GestureDetector(
                onTap: () => context.push('/groups/$groupId/post'),
                child: Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 12),
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Row(
                    children: [
                      Icon(
                        Icons.edit_outlined,
                        size: 16,
                        color: AppColors.postbookPrimary
                            .withValues(alpha: 0.7),
                      ),
                      const SizedBox(width: 10),
                      Text(
                        'Write something…',
                        style: AppTextStyles.body.copyWith(
                          color: AppColors.textDim,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),

        // Posts
        postsAsync.when(
          loading: () => const SliverFillRemaining(
            child: Center(
              child: CircularProgressIndicator(
                  color: AppColors.postbookPrimary),
            ),
          ),
          error: (_, _) => SliverToBoxAdapter(
            child: _RetryCard(
              message: 'Could not load discussion.',
              onRetry: () => ref.invalidate(
                groupPostsProvider(
                    GroupPostsParams(groupId: groupId, channelId: channelId)),
              ),
            ),
          ),
          data: (posts) {
            if (posts.isEmpty) {
              return const SliverFillRemaining(
                child: Center(
                  child: Padding(
                    padding: EdgeInsets.all(32),
                    child: Text(
                      'No posts yet. Be the first!',
                      style: TextStyle(color: AppColors.textMuted),
                    ),
                  ),
                ),
              );
            }
            return SliverList(
              delegate: SliverChildBuilderDelegate(
                (context, i) => Padding(
                  padding: const EdgeInsets.fromLTRB(12, 0, 12, 10),
                  child: GroupPostCard(
                    post: posts[i],
                    onTap: () =>
                        context.push('/comments/${posts[i].id}'),
                    onSpark: () => ref
                        .read(groupPostsRepositoryProvider)
                        .sparkPost(groupId, posts[i].id),
                    onComment: () =>
                        context.push('/comments/${posts[i].id}'),
                    onStash: () => ref
                        .read(groupPostsRepositoryProvider)
                        .stashPost(groupId, posts[i].id),
                  ),
                ),
                childCount: posts.length,
              ),
            );
          },
        ),
      ],
    );
  }
}

class _ChannelChips extends StatelessWidget {
  final List<GroupChannel> channels;
  final String? selected;
  final ValueChanged<String?> onSelect;

  const _ChannelChips({
    required this.channels,
    required this.selected,
    required this.onSelect,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 40,
      child: ListView.builder(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        itemCount: channels.length + 1,
        itemBuilder: (context, i) {
          if (i == 0) {
            return _ChipItem(
              label: 'All',
              isSelected: selected == null,
              onTap: () => onSelect(null),
            );
          }
          final ch = channels[i - 1];
          return _ChipItem(
            label: '#${ch.name}',
            isSelected: selected == ch.id,
            onTap: () => onSelect(ch.id),
          );
        },
      ),
    );
  }
}

class _ChipItem extends StatelessWidget {
  final String label;
  final bool isSelected;
  final VoidCallback onTap;

  const _ChipItem({
    required this.label,
    required this.isSelected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(right: 8),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: isSelected
              ? AppColors.textPrimary
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: isSelected
                ? Colors.transparent
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: isSelected ? AppColors.bgPrimary : AppColors.textMuted,
            fontWeight: isSelected ? FontWeight.w700 : FontWeight.w500,
          ),
        ),
      ),
    );
  }
}

// ─── Tab: People ─────────────────────────────────────────────────────────────

class _PeopleTab extends ConsumerWidget {
  final String groupId;
  final Group group;

  const _PeopleTab({required this.groupId, required this.group});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final membersAsync = ref.watch(groupMembersProvider(groupId));

    return membersAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => _RetryCard(
        message: 'Could not load members.',
        onRetry: () => ref.invalidate(groupMembersProvider(groupId)),
      ),
      data: (members) {
        if (members.isEmpty) {
          return const Center(
            child: Text('No members yet.',
                style: TextStyle(color: AppColors.textMuted)),
          );
        }

        // Admins first, then regular members
        final sorted = [...members]
          ..sort((a, b) {
            int rank(GroupMember m) =>
                m.isOwner ? 0 : m.isAdmin ? 1 : m.isMod ? 2 : 3;
            return rank(a).compareTo(rank(b));
          });

        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: sorted.length + 1,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, i) {
            if (i == 0) {
              return Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Text(
                  'Members (${members.length})',
                  style: AppTextStyles.h3,
                ),
              );
            }
            final member = sorted[i - 1];
            return _MemberTile(member: member, viewerGroup: group);
          },
        );
      },
    );
  }
}

class _MemberTile extends StatelessWidget {
  final GroupMember member;
  final Group viewerGroup;

  const _MemberTile({required this.member, required this.viewerGroup});

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
          GestureDetector(
            onTap: () => context.push('/profile/${member.userId}'),
            child: CircleAvatar(
              radius: 20,
              backgroundColor:
                  AppColors.postbookPrimary.withValues(alpha: 0.15),
              child: member.avatarMediaId != null
                  ? ClipOval(
                      child: Image.network(
                        '/v1/media/${member.avatarMediaId}/serve',
                        fit: BoxFit.cover,
                        errorBuilder: (_, _, _) => Text(
                          member.avatarInitial,
                          style: AppTextStyles.label.copyWith(
                            color: AppColors.postbookPrimary,
                          ),
                        ),
                      ),
                    )
                  : Text(
                      member.avatarInitial,
                      style: AppTextStyles.label
                          .copyWith(color: AppColors.postbookPrimary),
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
                    Text(member.displayLabel, style: AppTextStyles.label),
                    if (member.isOwner) ...[
                      const SizedBox(width: 6),
                      _RoleBadge(label: 'Owner', color: const Color(0xFFF59E0B)),
                    ] else if (member.isAdmin) ...[
                      const SizedBox(width: 6),
                      _RoleBadge(label: 'Admin', color: AppColors.accentPurple),
                    ] else if (member.isMod) ...[
                      const SizedBox(width: 6),
                      _RoleBadge(label: 'Mod', color: AppColors.posttubePrimary),
                    ],
                  ],
                ),
                if (member.username != null)
                  Text(
                    '@${member.username}',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.textMuted),
                  ),
              ],
            ),
          ),
          const Icon(Icons.chevron_right_rounded,
              color: AppColors.textDim, size: 18),
        ],
      ),
    );
  }
}

class _RoleBadge extends StatelessWidget {
  final String label;
  final Color color;
  const _RoleBadge({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(
          fontSize: 9,
          color: color,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

// ─── Tab: Media ──────────────────────────────────────────────────────────────

class _MediaTab extends ConsumerStatefulWidget {
  final String groupId;
  const _MediaTab({required this.groupId});

  @override
  ConsumerState<_MediaTab> createState() => _MediaTabState();
}

class _MediaTabState extends ConsumerState<_MediaTab>
    with SingleTickerProviderStateMixin {
  late TabController _inner;

  @override
  void initState() {
    super.initState();
    _inner = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _inner.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        TabBar(
          controller: _inner,
          labelColor: AppColors.textPrimary,
          unselectedLabelColor: AppColors.textMuted,
          indicatorColor: AppColors.textPrimary,
          dividerColor: AppColors.borderSubtle,
          tabs: const [Tab(text: 'Photos'), Tab(text: 'Videos')],
        ),
        Expanded(
          child: TabBarView(
            controller: _inner,
            children: [
              _MediaGrid(groupId: widget.groupId, type: 'photo'),
              _MediaGrid(groupId: widget.groupId, type: 'video'),
            ],
          ),
        ),
      ],
    );
  }
}

class _MediaGrid extends ConsumerWidget {
  final String groupId;
  final String type;
  const _MediaGrid({required this.groupId, required this.type});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final mediaAsync = ref.watch(groupMediaProvider(groupId));

    return mediaAsync.when(
      loading: () => GridView.builder(
        padding: const EdgeInsets.all(8),
        gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: 3,
          crossAxisSpacing: 3,
          mainAxisSpacing: 3,
        ),
        itemCount: 9,
        itemBuilder: (_, _) => Container(
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(8),
          ),
        ),
      ),
      error: (_, _) => const Center(
        child: Text('Could not load media.',
            style: TextStyle(color: AppColors.textMuted)),
      ),
      data: (posts) {
        final filtered = posts.where((p) {
          if (type == 'photo') {
            return p.contentType == 'photo' ||
                p.contentType == 'image' ||
                p.contentType == 'post';
          }
          return p.contentType == 'video' || p.contentType == 'reel';
        }).toList();

        if (filtered.isEmpty) {
          return Center(
            child: Text(
              type == 'photo' ? 'No photos yet.' : 'No videos yet.',
              style: const TextStyle(color: AppColors.textMuted),
            ),
          );
        }

        return GridView.builder(
          padding: const EdgeInsets.all(8),
          gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
            crossAxisCount: 3,
            crossAxisSpacing: 3,
            mainAxisSpacing: 3,
          ),
          itemCount: filtered.length,
          itemBuilder: (context, i) {
            final post = filtered[i];
            return GestureDetector(
              onTap: () => context.push('/comments/${post.id}'),
              child: Container(
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    ClipRRect(
                      borderRadius: BorderRadius.circular(8),
                      child: Container(
                        color: AppColors.postbookPrimary.withValues(alpha: 0.08),
                        child: Center(
                          child: Icon(
                            type == 'video'
                                ? Icons.play_circle_outline
                                : Icons.image_outlined,
                            color: AppColors.textMuted,
                            size: 24,
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            );
          },
        );
      },
    );
  }
}

// ─── Tab: Rules ──────────────────────────────────────────────────────────────

class _RulesTab extends ConsumerWidget {
  final String groupId;
  final bool isAdmin;
  const _RulesTab({required this.groupId, required this.isAdmin});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rulesAsync = ref.watch(groupRulesProvider(groupId));

    return rulesAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => _RetryCard(
        message: 'Could not load rules.',
        onRetry: () => ref.invalidate(groupRulesProvider(groupId)),
      ),
      data: (rules) {
        if (rules.isEmpty) {
          return Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(Icons.gavel_outlined,
                    size: 40, color: AppColors.textMuted),
                const SizedBox(height: 12),
                const Text('No rules set.',
                    style: TextStyle(color: AppColors.textMuted)),
                if (isAdmin) ...[
                  const SizedBox(height: 16),
                  TextButton.icon(
                    onPressed: () =>
                        _addRuleDialog(context, ref, groupId),
                    icon: const Icon(Icons.add),
                    label: const Text('Add Rule'),
                  ),
                ],
              ],
            ),
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: rules.length + (isAdmin ? 1 : 0),
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, i) {
            if (i == rules.length) {
              return TextButton.icon(
                onPressed: () =>
                    _addRuleDialog(context, ref, groupId),
                icon: const Icon(Icons.add),
                label: const Text('Add Rule'),
              );
            }
            final rule = rules[i];
            return _RuleCard(rule: rule, index: i);
          },
        );
      },
    );
  }

  void _addRuleDialog(BuildContext context, WidgetRef ref, String groupId) {
    // Admins add rules from the admin screen for now.
    context.push('/groups/$groupId/admin');
  }
}

class _RuleCard extends StatelessWidget {
  final GroupRule rule;
  final int index;
  const _RuleCard({required this.rule, required this.index});

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
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 24,
            height: 24,
            decoration: BoxDecoration(
              color: AppColors.postbookPrimary.withValues(alpha: 0.15),
              shape: BoxShape.circle,
            ),
            child: Center(
              child: Text(
                '${index + 1}',
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.postbookPrimary,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(rule.title, style: AppTextStyles.label),
                if (rule.description.isNotEmpty) ...[
                  const SizedBox(height: 3),
                  Text(rule.description,
                      style: AppTextStyles.bodySmall
                          .copyWith(color: AppColors.textMuted)),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Tab: Manage ─────────────────────────────────────────────────────────────

class _ManageTab extends StatelessWidget {
  final String groupId;
  final Group group;
  const _ManageTab({required this.groupId, required this.group});

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Space Settings', style: AppTextStyles.h2),
          const SizedBox(height: 16),
          _SettingsTile(
            icon: Icons.settings_outlined,
            title: 'Space details',
            subtitle: 'Name, description, privacy, cover',
            onTap: () => context.push('/groups/$groupId/admin'),
          ),
          const SizedBox(height: 8),
          _SettingsTile(
            icon: Icons.person_add_outlined,
            title: 'Member requests',
            badge: group.pendingRequestCount,
            subtitle: '${group.pendingRequestCount} pending',
            onTap: () => context.push('/groups/$groupId/admin'),
          ),
          const SizedBox(height: 8),
          _SettingsTile(
            icon: Icons.pending_actions_outlined,
            title: 'Pending posts',
            subtitle: 'Review posts awaiting approval',
            onTap: () => context.push('/groups/$groupId/admin'),
          ),
          const SizedBox(height: 8),
          _SettingsTile(
            icon: Icons.block_outlined,
            title: 'Banned members',
            subtitle: 'View and manage bans',
            onTap: () => context.push('/groups/$groupId/admin'),
          ),
          const SizedBox(height: 8),
          _SettingsTile(
            icon: Icons.gavel_outlined,
            title: 'Rules',
            subtitle: 'Add or edit space rules',
            onTap: () => context.push('/groups/$groupId/admin'),
          ),
        ],
      ),
    );
  }
}

class _SettingsTile extends StatelessWidget {
  final IconData icon;
  final String title;
  final String? subtitle;
  final int badge;
  final VoidCallback onTap;

  const _SettingsTile({
    required this.icon,
    required this.title,
    this.subtitle,
    this.badge = 0,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            Icon(icon, color: AppColors.textSecondary, size: 20),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: AppTextStyles.label),
                  if (subtitle != null)
                    Text(
                      subtitle!,
                      style: AppTextStyles.labelSmall
                          .copyWith(color: AppColors.textMuted),
                    ),
                ],
              ),
            ),
            if (badge > 0) ...[
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
                decoration: BoxDecoration(
                  color: AppColors.postbookPrimary,
                  borderRadius: BorderRadius.circular(10),
                ),
                child: Text(
                  '$badge',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: Colors.white,
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              const SizedBox(width: 8),
            ],
            const Icon(Icons.chevron_right_rounded,
                color: AppColors.textDim, size: 18),
          ],
        ),
      ),
    );
  }
}

// ─── Shared helpers ──────────────────────────────────────────────────────────

class _InfoCard extends StatelessWidget {
  final String title;
  final String value;

  const _InfoCard({required this.title, required this.value});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
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
            title,
            style: AppTextStyles.labelSmall
                .copyWith(color: AppColors.textMuted),
          ),
          const SizedBox(height: 4),
          Text(value, style: AppTextStyles.bodySmall),
        ],
      ),
    );
  }
}

class _RetryCard extends StatelessWidget {
  final String message;
  final VoidCallback onRetry;

  const _RetryCard({required this.message, required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            const Icon(Icons.error_outline, color: AppColors.textMuted),
            const SizedBox(width: 10),
            Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
            TextButton(
              onPressed: onRetry,
              child: Text(
                'Retry',
                style: AppTextStyles.label
                    .copyWith(color: AppColors.postbookPrimary),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _TabBarDelegate extends SliverPersistentHeaderDelegate {
  const _TabBarDelegate(this.tabBar);
  final TabBar tabBar;

  @override
  double get minExtent => tabBar.preferredSize.height;
  @override
  double get maxExtent => tabBar.preferredSize.height;

  @override
  Widget build(
      BuildContext context, double shrinkOffset, bool overlapsContent) {
    return Container(
      color: AppColors.bgPrimary,
      child: tabBar,
    );
  }

  @override
  bool shouldRebuild(covariant _TabBarDelegate old) => false;
}

List<Color> _categoryColors(String? category) {
  return switch (category?.toLowerCase()) {
    'gaming' => [const Color(0xFF7C3AED), const Color(0xFF4338CA)],
    'technology' => [const Color(0xFF0EA5E9), const Color(0xFF2563EB)],
    'music' => [const Color(0xFFEC4899), const Color(0xFFBE185D)],
    'sports' => [const Color(0xFF16A34A), const Color(0xFF059669)],
    'art' => [const Color(0xFFF59E0B), const Color(0xFFEA580C)],
    _ => [AppColors.postbookPrimary, AppColors.accentPurple],
  };
}
