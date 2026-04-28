import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/group_posts_repository.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:atpost_app/providers/group_posts_provider.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:atpost_app/shared/widgets/group_post_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final _groupMembersProvider = FutureProvider.autoDispose
    .family<List<User>, String>((ref, groupId) async {
      return ref.watch(groupsRepositoryProvider).getGroupMembers(groupId);
    });

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
          child: _InlineStateCard(
            icon: Icons.error_outline,
            message: 'Failed to load this group.',
            action: 'Retry',
            onTap: () => ref.invalidate(groupDetailProvider(widget.groupId)),
          ),
        ),
      ),
      data: (group) => _GroupDetailBody(group: group, groupId: widget.groupId),
    );
  }
}

class _GroupDetailBody extends ConsumerStatefulWidget {
  const _GroupDetailBody({required this.group, required this.groupId});

  final Group group;
  final String groupId;

  @override
  ConsumerState<_GroupDetailBody> createState() => _GroupDetailBodyState();
}

class _GroupDetailBodyState extends ConsumerState<_GroupDetailBody> {
  late bool _joined;
  late int _memberCount;
  bool _joinBusy = false;
  String? _selectedChannelId;

  @override
  void initState() {
    super.initState();
    _joined = widget.group.isMember;
    _memberCount = widget.group.memberCount;
  }

  Future<void> _toggleJoin() async {
    if (_joinBusy) return;

    final previouslyJoined = _joined;
    final nextJoined = !previouslyJoined;

    setState(() {
      _joinBusy = true;
      _joined = nextJoined;
      _memberCount += nextJoined ? 1 : -1;
      if (_memberCount < 0) _memberCount = 0;
    });

    try {
      final repo = ref.read(groupsRepositoryProvider);
      if (previouslyJoined) {
        await repo.leaveGroup(widget.groupId);
      } else {
        await repo.joinGroup(widget.groupId);
      }

      ref.invalidate(groupDetailProvider(widget.groupId));
      ref.invalidate(myGroupsProvider);
      ref.invalidate(discoverGroupsProvider);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _joined = previouslyJoined;
        _memberCount += previouslyJoined ? 1 : -1;
        if (_memberCount < 0) _memberCount = 0;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            previouslyJoined
                ? 'Could not leave group.'
                : 'Could not join group.',
          ),
        ),
      );
    } finally {
      if (mounted) {
        setState(() => _joinBusy = false);
      }
    }
  }

  Future<void> _inviteUser() async {
    final controller = TextEditingController();
    final userId = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Invite user', style: AppTextStyles.h3),
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
      await ref
          .read(groupsRepositoryProvider)
          .inviteUser(widget.groupId, userId);
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Invite sent.')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not send invite.')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final group = widget.group;

    return DefaultTabController(
      length: 3,
      child: Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: NestedScrollView(
          headerSliverBuilder: (context, innerBoxIsScrolled) {
            return [
              SliverAppBar(
                backgroundColor: AppColors.bgPrimary,
                expandedHeight: 250,
                pinned: true,
                elevation: 0,
                leading: IconButton(
                  onPressed: () => context.pop(),
                  icon: const Icon(Icons.arrow_back_rounded),
                ),
                actions: [
                  IconButton(
                    onPressed: () {
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(
                          content: Text('Share link copied soon.'),
                        ),
                      );
                    },
                    icon: const Icon(Icons.share_outlined),
                  ),
                ],
                flexibleSpace: FlexibleSpaceBar(
                  collapseMode: CollapseMode.pin,
                  background: Container(
                    decoration: BoxDecoration(
                      gradient: const LinearGradient(
                        begin: Alignment.topLeft,
                        end: Alignment.bottomRight,
                        colors: [
                          Color(0x33FF6B35),
                          Color(0x334ECDC4),
                          Color(0x337B68EE),
                        ],
                      ),
                      border: Border(
                        bottom: BorderSide(
                          color: AppColors.borderSubtle,
                          width: 1,
                        ),
                      ),
                    ),
                    child: Padding(
                      padding: const EdgeInsets.fromLTRB(16, 92, 16, 16),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            children: [
                              Container(
                                width: 52,
                                height: 52,
                                decoration: BoxDecoration(
                                  borderRadius: BorderRadius.circular(16),
                                  gradient: AppColors.postbookGradient,
                                ),
                                child: const Icon(
                                  Icons.groups_rounded,
                                  color: Colors.white,
                                ),
                              ),
                              const SizedBox(width: 10),
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      group.name,
                                      maxLines: 2,
                                      overflow: TextOverflow.ellipsis,
                                      style: AppTextStyles.h1.copyWith(
                                        fontSize: 28,
                                      ),
                                    ),
                                    const SizedBox(height: 2),
                                    Text(
                                      '${group.postCount} posts  |  ${group.privacy}',
                                      style: AppTextStyles.bodySmall,
                                    ),
                                  ],
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 10),
                          Text(
                            group.description.isEmpty
                                ? 'No description available.'
                                : group.description,
                            maxLines: 3,
                            overflow: TextOverflow.ellipsis,
                            style: AppTextStyles.bodySmall.copyWith(
                              color: AppColors.textSecondary,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 12),
                  child: Column(
                    children: [
                      Row(
                        children: [
                          Expanded(
                            child: _MetricCard(
                              label: 'Members',
                              value: _memberCount.toString(),
                            ),
                          ),
                          const SizedBox(width: 8),
                          Expanded(
                            child: _MetricCard(
                              label: 'Posts',
                              value: group.postCount.toString(),
                            ),
                          ),
                          const SizedBox(width: 8),
                          Expanded(
                            child: _MetricCard(
                              label: 'Privacy',
                              value: group.privacy,
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 10),
                      Row(
                        children: [
                          Expanded(
                            child: ElevatedButton.icon(
                              onPressed: _joinBusy ? null : _toggleJoin,
                              style: ElevatedButton.styleFrom(
                                backgroundColor: _joined
                                    ? AppColors.bgTertiary
                                    : AppColors.postbookPrimary,
                                foregroundColor: Colors.white,
                                shape: RoundedRectangleBorder(
                                  borderRadius: BorderRadius.circular(12),
                                  side: BorderSide(
                                    color: _joined
                                        ? AppColors.borderSubtle
                                        : AppColors.postbookPrimary,
                                  ),
                                ),
                              ),
                              icon: _joinBusy
                                  ? const SizedBox(
                                      width: 14,
                                      height: 14,
                                      child: CircularProgressIndicator(
                                        strokeWidth: 2,
                                        color: Colors.white,
                                      ),
                                    )
                                  : Icon(
                                      _joined
                                          ? Icons.check_circle_outline
                                          : Icons.group_add_outlined,
                                    ),
                              label: Text(_joined ? 'Joined' : 'Join Group'),
                            ),
                          ),
                          const SizedBox(width: 8),
                          OutlinedButton.icon(
                            onPressed: _inviteUser,
                            style: OutlinedButton.styleFrom(
                              foregroundColor: AppColors.textSecondary,
                              side: const BorderSide(
                                color: AppColors.borderSubtle,
                              ),
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(12),
                              ),
                            ),
                            icon: const Icon(Icons.person_add_alt_1_outlined),
                            label: const Text('Invite'),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
              // Channel tabs (horizontal scroll)
              SliverToBoxAdapter(
                child: _ChannelTabs(
                  groupId: widget.groupId,
                  selectedChannelId: _selectedChannelId,
                  onChannelSelected: (channelId) {
                    setState(() => _selectedChannelId = channelId);
                  },
                ),
              ),
              // Mini composer bar
              if (_joined)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 4),
                    child: GestureDetector(
                      onTap: () =>
                          context.push('/groups/${widget.groupId}/post'),
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
                          border: Border.all(color: AppColors.borderSubtle),
                        ),
                        child: Row(
                          children: [
                            CircleAvatar(
                              radius: 14,
                              backgroundColor: AppColors.postbookPrimary
                                  .withValues(alpha: 0.2),
                              child: const Icon(
                                Icons.edit,
                                size: 14,
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                            const SizedBox(width: 10),
                            Text(
                              'Write something...',
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
              SliverPersistentHeader(
                pinned: true,
                delegate: _TabBarDelegate(
                  TabBar(
                    labelColor: AppColors.postbookPrimary,
                    unselectedLabelColor: AppColors.textDim,
                    indicatorColor: AppColors.postbookPrimary,
                    tabs: const [
                      Tab(text: 'Feed'),
                      Tab(text: 'Members'),
                      Tab(text: 'About'),
                    ],
                  ),
                ),
              ),
            ];
          },
          body: TabBarView(
            children: [
              _GroupFeedTab(
                groupId: widget.groupId,
                channelId: _selectedChannelId,
              ),
              _GroupMembersTab(groupId: widget.groupId),
              _GroupAboutTab(group: group),
            ],
          ),
        ),
        // Admin FAB
        floatingActionButton: group.isAdmin
            ? FloatingActionButton(
                onPressed: () =>
                    context.push('/groups/${widget.groupId}/admin'),
                backgroundColor: AppColors.postbookPrimary,
                child: const Icon(
                  Icons.admin_panel_settings,
                  color: Colors.white,
                ),
              )
            : null,
      ),
    );
  }
}

class _ChannelTabs extends ConsumerWidget {
  final String groupId;
  final String? selectedChannelId;
  final ValueChanged<String?> onChannelSelected;

  const _ChannelTabs({
    required this.groupId,
    required this.selectedChannelId,
    required this.onChannelSelected,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final channelsAsync = ref.watch(groupChannelsProvider(groupId));

    return channelsAsync.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (channels) {
        if (channels.isEmpty) return const SizedBox.shrink();
        return SizedBox(
          height: 40,
          child: ListView.builder(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 18),
            itemCount: channels.length + 1,
            itemBuilder: (context, index) {
              if (index == 0) {
                final isSelected = selectedChannelId == null;
                return Padding(
                  padding: const EdgeInsets.only(right: 8),
                  child: _ChannelChip(
                    label: 'All',
                    isSelected: isSelected,
                    onTap: () => onChannelSelected(null),
                  ),
                );
              }
              final ch = channels[index - 1];
              final isSelected = selectedChannelId == ch.id;
              return Padding(
                padding: const EdgeInsets.only(right: 8),
                child: _ChannelChip(
                  label: '#${ch.name}',
                  isSelected: isSelected,
                  onTap: () => onChannelSelected(ch.id),
                ),
              );
            },
          ),
        );
      },
    );
  }
}

class _ChannelChip extends StatelessWidget {
  final String label;
  final bool isSelected;
  final VoidCallback onTap;

  const _ChannelChip({
    required this.label,
    required this.isSelected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: isSelected
              ? AppColors.postbookPrimary.withValues(alpha: 0.2)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: isSelected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: isSelected
                ? AppColors.postbookPrimary
                : AppColors.textSecondary,
          ),
        ),
      ),
    );
  }
}

class _GroupFeedTab extends ConsumerWidget {
  const _GroupFeedTab({required this.groupId, this.channelId});

  final String groupId;
  final String? channelId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final postsAsync = ref.watch(
      groupPostsProvider(
        GroupPostsParams(groupId: groupId, channelId: channelId),
      ),
    );

    return postsAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: _InlineStateCard(
          icon: Icons.article_outlined,
          message: 'Could not load group posts.',
          action: 'Retry',
          onTap: () => ref.invalidate(
            groupPostsProvider(
              GroupPostsParams(groupId: groupId, channelId: channelId),
            ),
          ),
        ),
      ),
      data: (posts) {
        if (posts.isEmpty) {
          return Center(
            child: _InlineStateCard(
              icon: Icons.notes_outlined,
              message: 'No posts have been shared yet.',
              action: 'Refresh',
              onTap: () => ref.invalidate(
                groupPostsProvider(
                  GroupPostsParams(groupId: groupId, channelId: channelId),
                ),
              ),
            ),
          );
        }

        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: posts.length,
          separatorBuilder: (_, _) => const SizedBox(height: 10),
          itemBuilder: (context, index) {
            final post = posts[index];
            return GroupPostCard(
              post: post,
              onTap: () => context.push('/comments/${post.id}'),
              onSpark: () {
                ref
                    .read(groupPostsRepositoryProvider)
                    .sparkPost(groupId, post.id);
              },
              onComment: () => context.push('/comments/${post.id}'),
              onStash: () {
                ref
                    .read(groupPostsRepositoryProvider)
                    .stashPost(groupId, post.id);
              },
            );
          },
        );
      },
    );
  }
}

class _GroupMembersTab extends ConsumerWidget {
  const _GroupMembersTab({required this.groupId});

  final String groupId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final membersAsync = ref.watch(_groupMembersProvider(groupId));

    return membersAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: _InlineStateCard(
          icon: Icons.groups_outlined,
          message: 'Could not load members.',
          action: 'Retry',
          onTap: () => ref.invalidate(_groupMembersProvider(groupId)),
        ),
      ),
      data: (members) {
        if (members.isEmpty) {
          return Center(
            child: _InlineStateCard(
              icon: Icons.group_off_outlined,
              message: 'No members yet.',
              action: 'Refresh',
              onTap: () => ref.invalidate(_groupMembersProvider(groupId)),
            ),
          );
        }

        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: members.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, index) {
            final member = members[index];
            final initial = member.displayName.isEmpty
                ? 'U'
                : member.displayName.substring(0, 1).toUpperCase();

            return ListTile(
              tileColor: AppColors.bgCard,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                side: const BorderSide(color: AppColors.borderSubtle),
              ),
              leading: CircleAvatar(
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
              title: Text(member.displayName, style: AppTextStyles.label),
              subtitle: Text(
                '@${member.username}',
                style: AppTextStyles.labelSmall,
              ),
              trailing: const Icon(
                Icons.chevron_right_rounded,
                color: AppColors.textMuted,
              ),
              onTap: () => context.push('/profile/${member.id}'),
            );
          },
        );
      },
    );
  }
}

class _GroupAboutTab extends StatelessWidget {
  const _GroupAboutTab({required this.group});

  final Group group;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _InfoCard(
            title: 'Description',
            value: group.description.isEmpty
                ? 'No description provided.'
                : group.description,
          ),
          const SizedBox(height: 10),
          _InfoCard(
            title: 'Privacy',
            value: _privacyDescription(group.privacy),
          ),
          const SizedBox(height: 10),
          _InfoCard(title: 'Created', value: _formatDate(group.createdAt)),
          if ((group.creatorId ?? '').isNotEmpty) ...[
            const SizedBox(height: 10),
            _InfoCard(title: 'Creator ID', value: group.creatorId!),
          ],
        ],
      ),
    );
  }

  String _privacyDescription(String privacy) {
    return switch (privacy) {
      'public' => 'Anyone can discover and join this group.',
      'private' => 'Visible to all; membership is approval based.',
      'secret' => 'Invite only and hidden from discovery.',
      _ => privacy,
    };
  }

  String _formatDate(DateTime date) {
    final monthNames = [
      'Jan',
      'Feb',
      'Mar',
      'Apr',
      'May',
      'Jun',
      'Jul',
      'Aug',
      'Sep',
      'Oct',
      'Nov',
      'Dec',
    ];
    return '${date.day} ${monthNames[date.month - 1]} ${date.year}';
  }
}

class _MetricCard extends StatelessWidget {
  const _MetricCard({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Text(
            value,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: AppTextStyles.h3,
          ),
          const SizedBox(height: 2),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _InfoCard extends StatelessWidget {
  const _InfoCard({required this.title, required this.value});

  final String title;
  final String value;

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
            style: AppTextStyles.labelSmall.copyWith(
              color: AppColors.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          Text(value, style: AppTextStyles.bodySmall),
        ],
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

class _TabBarDelegate extends SliverPersistentHeaderDelegate {
  const _TabBarDelegate(this.tabBar);

  final TabBar tabBar;

  @override
  double get minExtent => tabBar.preferredSize.height;

  @override
  double get maxExtent => tabBar.preferredSize.height;

  @override
  Widget build(
    BuildContext context,
    double shrinkOffset,
    bool overlapsContent,
  ) {
    return Container(color: AppColors.bgPrimary, child: tabBar);
  }

  @override
  bool shouldRebuild(covariant _TabBarDelegate oldDelegate) => false;
}
