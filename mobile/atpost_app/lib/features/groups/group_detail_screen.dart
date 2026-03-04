import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final _groupMembersProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, groupId) async {
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
          elevation: 0,
          leading: IconButton(
            icon: const Icon(Icons.arrow_back, color: AppColors.textPrimary),
            onPressed: () => context.pop(),
          ),
        ),
        body: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.error_outline,
                  color: AppColors.textDim, size: 48),
              const SizedBox(height: 12),
              Text('Failed to load group', style: AppTextStyles.body),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () =>
                    ref.invalidate(groupDetailProvider(widget.groupId)),
                child: Text('Retry',
                    style: AppTextStyles.label
                        .copyWith(color: AppColors.postbookPrimary)),
              ),
            ],
          ),
        ),
      ),
      data: (group) => _GroupDetailBody(
        group: group,
        groupId: widget.groupId,
      ),
    );
  }
}

class _GroupDetailBody extends ConsumerStatefulWidget {
  final Group group;
  final String groupId;

  const _GroupDetailBody({required this.group, required this.groupId});

  @override
  ConsumerState<_GroupDetailBody> createState() => _GroupDetailBodyState();
}

class _GroupDetailBodyState extends ConsumerState<_GroupDetailBody> {
  late bool _joined;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _joined = widget.group.isMember;
  }

  Future<void> _toggleJoin() async {
    if (_loading) return;
    final repo = ref.read(groupsRepositoryProvider);
    final wasJoined = _joined;
    setState(() {
      _joined = !_joined;
      _loading = true;
    });
    try {
      if (wasJoined) {
        await repo.leaveGroup(widget.groupId);
      } else {
        await repo.joinGroup(widget.groupId);
      }
    } catch (_) {
      if (mounted) {
        setState(() => _joined = wasJoined);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(wasJoined ? 'Failed to leave group' : 'Failed to join group'),
            backgroundColor: AppColors.bgCard,
            behavior: SnackBarBehavior.floating,
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
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
          headerSliverBuilder: (context, innerBoxIsScrolled) => [
            SliverAppBar(
              expandedHeight: 200,
              pinned: true,
              backgroundColor: AppColors.bgPrimary,
              leading: IconButton(
                icon: const Icon(Icons.arrow_back, color: Colors.white),
                onPressed: () => context.pop(),
              ),
              flexibleSpace: FlexibleSpaceBar(
                collapseMode: CollapseMode.pin,
                background: Stack(
                  fit: StackFit.expand,
                  children: [
                    Container(
                      decoration: BoxDecoration(
                        gradient: AppColors.postbookGradient,
                      ),
                      child: const Icon(
                        Icons.group,
                        color: Colors.white54,
                        size: 80,
                      ),
                    ),
                    Positioned(
                      left: 16,
                      right: 16,
                      bottom: 16,
                      child: Text(
                        group.name,
                        style: AppTextStyles.h1.copyWith(color: Colors.white),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                ),
              ),
            ),
            SliverToBoxAdapter(
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                child: Row(
                  children: [
                    Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '${group.memberCount} members',
                          style: AppTextStyles.label,
                        ),
                        const SizedBox(height: 4),
                        Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 8, vertical: 2),
                          decoration: BoxDecoration(
                            color: AppColors.postbookPrimary.withValues(alpha: 0.15),
                            borderRadius: BorderRadius.circular(8),
                          ),
                          child: Text(
                            group.privacy,
                            style: AppTextStyles.labelSmall.copyWith(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        ),
                      ],
                    ),
                    const Spacer(),
                    _buildJoinButton(),
                  ],
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
                  labelStyle: AppTextStyles.label,
                  tabs: const [
                    Tab(text: 'Feed'),
                    Tab(text: 'Members'),
                    Tab(text: 'About'),
                  ],
                ),
              ),
            ),
          ],
          body: TabBarView(
            children: [
              _FeedTab(),
              _MembersTab(groupId: widget.groupId),
              _AboutTab(group: group),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildJoinButton() {
    if (_loading) {
      return const SizedBox(
        width: 20,
        height: 20,
        child: CircularProgressIndicator(
          strokeWidth: 2,
          color: AppColors.postbookPrimary,
        ),
      );
    }
    if (_joined) {
      return OutlinedButton(
        onPressed: _toggleJoin,
        style: OutlinedButton.styleFrom(
          foregroundColor: AppColors.textSecondary,
          side: const BorderSide(color: AppColors.borderSubtle),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
          ),
        ),
        child: Text('Joined', style: AppTextStyles.label),
      );
    }
    return Container(
      decoration: BoxDecoration(
        gradient: AppColors.postbookGradient,
        borderRadius: BorderRadius.circular(20),
      ),
      child: OutlinedButton(
        onPressed: _toggleJoin,
        style: OutlinedButton.styleFrom(
          foregroundColor: Colors.white,
          side: BorderSide.none,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
          ),
        ),
        child: Text('Join Group', style: AppTextStyles.label),
      ),
    );
  }
}

class _FeedTab extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.article_outlined,
              color: AppColors.textDim, size: 48),
          const SizedBox(height: 12),
          Text('Group posts coming soon',
              style: AppTextStyles.body.copyWith(color: AppColors.textSecondary)),
        ],
      ),
    );
  }
}

class _MembersTab extends ConsumerWidget {
  final String groupId;

  const _MembersTab({required this.groupId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final membersAsync = ref.watch(_groupMembersProvider(groupId));
    return membersAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Text('Failed to load members',
            style: AppTextStyles.body.copyWith(color: AppColors.textSecondary)),
      ),
      data: (members) {
        if (members.isEmpty) {
          return Center(
            child: Text('No members yet',
                style:
                    AppTextStyles.body.copyWith(color: AppColors.textSecondary)),
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: members.length,
          separatorBuilder: (_, _) => const SizedBox(height: 4),
          itemBuilder: (context, index) {
            final member = members[index];
            return ListTile(
              leading: CircleAvatar(
                radius: 20,
                backgroundColor:
                    AppColors.postbookPrimary.withValues(alpha: 0.2),
                child: Text(
                  member.displayName.isNotEmpty
                      ? member.displayName[0].toUpperCase()
                      : '?',
                  style: AppTextStyles.label
                      .copyWith(color: AppColors.postbookPrimary),
                ),
              ),
              title: Text(member.displayName, style: AppTextStyles.h3),
              subtitle: Text('@${member.username}',
                  style: AppTextStyles.bodySmall
                      .copyWith(color: AppColors.textDim)),
            );
          },
        );
      },
    );
  }
}

class _AboutTab extends StatelessWidget {
  final Group group;

  const _AboutTab({required this.group});

  @override
  Widget build(BuildContext context) {
    final privacyDescription = switch (group.privacy) {
      'public' => 'Anyone can see the group, its members and their posts.',
      'private' => 'Anyone can find the group. Only members can see posts.',
      'secret' => 'Only members can find and see the group.',
      _ => '',
    };

    return SingleChildScrollView(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Description', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          Text(
            group.description.isNotEmpty
                ? group.description
                : 'No description provided.',
            style:
                AppTextStyles.body.copyWith(color: AppColors.textSecondary),
          ),
          const SizedBox(height: 20),
          Text('Privacy', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          Row(
            children: [
              Icon(
                group.privacy == 'public'
                    ? Icons.public
                    : group.privacy == 'private'
                        ? Icons.lock_outline
                        : Icons.visibility_off_outlined,
                color: AppColors.textDim,
                size: 18,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  privacyDescription,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textSecondary),
                ),
              ),
            ],
          ),
          const SizedBox(height: 20),
          Text('Created', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          Text(
            _formatDate(group.createdAt),
            style:
                AppTextStyles.body.copyWith(color: AppColors.textSecondary),
          ),
        ],
      ),
    );
  }

  String _formatDate(DateTime date) {
    return '${date.day} ${_month(date.month)} ${date.year}';
  }

  String _month(int m) {
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return months[m - 1];
  }
}

class _TabBarDelegate extends SliverPersistentHeaderDelegate {
  final TabBar tabBar;

  _TabBarDelegate(this.tabBar);

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
  bool shouldRebuild(_TabBarDelegate oldDelegate) => false;
}
