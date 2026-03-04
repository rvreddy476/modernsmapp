import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class GroupsListScreen extends ConsumerWidget {
  const GroupsListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final repo = ref.read(groupsRepositoryProvider);

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        backgroundColor: AppColors.bgPrimary,
        appBar: AppBar(
          backgroundColor: AppColors.bgPrimary,
          elevation: 0,
          leading: IconButton(
            icon: const Icon(Icons.arrow_back, color: AppColors.textPrimary),
            onPressed: () => context.pop(),
          ),
          title: Text('Groups', style: AppTextStyles.h2),
          actions: [
            IconButton(
              icon: const Icon(Icons.add, color: AppColors.postbookPrimary),
              onPressed: () => context.push('/groups/create'),
            ),
          ],
          bottom: TabBar(
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textDim,
            indicatorColor: AppColors.postbookPrimary,
            labelStyle: AppTextStyles.label,
            tabs: const [
              Tab(text: 'My Groups'),
              Tab(text: 'Discover'),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            _GroupsList(
              provider: myGroupsProvider,
              repo: repo,
              emptyMessage: 'You have not joined any groups yet.',
            ),
            _GroupsList(
              provider: discoverGroupsProvider,
              repo: repo,
              emptyMessage: 'No public groups found.',
            ),
          ],
        ),
      ),
    );
  }
}

class _GroupsList extends ConsumerWidget {
  final ProviderBase<AsyncValue<List<Group>>> provider;
  final GroupsRepository repo;
  final String emptyMessage;

  const _GroupsList({
    required this.provider,
    required this.repo,
    required this.emptyMessage,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(provider);
    return async.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, color: AppColors.textDim, size: 40),
            const SizedBox(height: 12),
            Text('Failed to load groups', style: AppTextStyles.body),
            const SizedBox(height: 8),
            TextButton(
              onPressed: () => ref.invalidate(provider),
              child: Text('Retry',
                  style: AppTextStyles.label
                      .copyWith(color: AppColors.postbookPrimary)),
            ),
          ],
        ),
      ),
      data: (groups) {
        if (groups.isEmpty) {
          return Center(
            child: Text(emptyMessage,
                style:
                    AppTextStyles.body.copyWith(color: AppColors.textSecondary)),
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: groups.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, index) =>
              _GroupTile(group: groups[index], repo: repo),
        );
      },
    );
  }
}

class _GroupTile extends StatefulWidget {
  final Group group;
  final GroupsRepository repo;

  const _GroupTile({required this.group, required this.repo});

  @override
  State<_GroupTile> createState() => _GroupTileState();
}

class _GroupTileState extends State<_GroupTile> {
  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        leading: Container(
          width: 48,
          height: 48,
          decoration: BoxDecoration(
            gradient: AppColors.postbookGradient,
            borderRadius: BorderRadius.circular(12),
          ),
          child: const Icon(Icons.group, color: Colors.white, size: 24),
        ),
        title: Text(widget.group.name, style: AppTextStyles.h3),
        subtitle: Text(
          '${widget.group.memberCount} members · ${widget.group.privacy}',
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.textSecondary),
        ),
        trailing: _JoinButton(group: widget.group, repo: widget.repo),
        onTap: () => context.push('/groups/${widget.group.id}'),
      ),
    );
  }
}

class _JoinButton extends StatefulWidget {
  final Group group;
  final GroupsRepository repo;

  const _JoinButton({required this.group, required this.repo});

  @override
  State<_JoinButton> createState() => _JoinButtonState();
}

class _JoinButtonState extends State<_JoinButton> {
  late bool _joined;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _joined = widget.group.isMember;
  }

  Future<void> _toggle() async {
    if (_loading) return;
    final wasJoined = _joined;
    setState(() {
      _joined = !_joined;
      _loading = true;
    });
    try {
      if (wasJoined) {
        await widget.repo.leaveGroup(widget.group.id);
      } else {
        await widget.repo.joinGroup(widget.group.id);
      }
    } catch (_) {
      if (mounted) {
        setState(() => _joined = wasJoined);
      }
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
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
        onPressed: _toggle,
        style: OutlinedButton.styleFrom(
          foregroundColor: AppColors.textSecondary,
          side: const BorderSide(color: AppColors.borderSubtle),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          minimumSize: Size.zero,
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
          ),
        ),
        child: Text('Joined', style: AppTextStyles.labelSmall),
      );
    }
    return Container(
      decoration: BoxDecoration(
        gradient: AppColors.postbookGradient,
        borderRadius: BorderRadius.circular(20),
      ),
      child: OutlinedButton(
        onPressed: _toggle,
        style: OutlinedButton.styleFrom(
          foregroundColor: Colors.white,
          side: BorderSide.none,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          minimumSize: Size.zero,
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
          ),
        ),
        child: Text('Join', style: AppTextStyles.labelSmall),
      ),
    );
  }
}
