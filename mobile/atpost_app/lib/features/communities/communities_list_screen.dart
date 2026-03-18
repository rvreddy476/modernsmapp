import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/repositories/communities_repository.dart';
import 'package:atpost_app/providers/communities_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CommunitiesListScreen extends ConsumerWidget {
  const CommunitiesListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final repo = ref.read(communitiesRepositoryProvider);

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
          title: Text('Communities', style: AppTextStyles.h2),
          actions: [
            IconButton(
              icon: const Icon(Icons.add, color: AppColors.postbookPrimary),
              onPressed: () => context.push('/communities/create'),
            ),
          ],
          bottom: TabBar(
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textDim,
            indicatorColor: AppColors.postbookPrimary,
            labelStyle: AppTextStyles.label,
            tabs: const [
              Tab(text: 'My Communities'),
              Tab(text: 'Discover'),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            _CommunitiesList(
              provider: myCommunitiesProvider,
              repo: repo,
              emptyMessage: 'You have not joined any communities yet.',
            ),
            _CommunitiesList(
              provider: discoverCommunitiesProvider,
              repo: repo,
              emptyMessage: 'No communities to discover.',
            ),
          ],
        ),
      ),
    );
  }
}

class _CommunitiesList extends ConsumerWidget {
  final ProviderBase<AsyncValue<List<Community>>> provider;
  final CommunitiesRepository repo;
  final String emptyMessage;

  const _CommunitiesList({
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
            Text('Failed to load communities', style: AppTextStyles.body),
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
      data: (communities) {
        if (communities.isEmpty) {
          return Center(
            child: Text(emptyMessage,
                style:
                    AppTextStyles.body.copyWith(color: AppColors.textSecondary)),
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: communities.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, index) =>
              _CommunityTile(community: communities[index], repo: repo),
        );
      },
    );
  }
}

class _CommunityTile extends StatefulWidget {
  final Community community;
  final CommunitiesRepository repo;

  const _CommunityTile({required this.community, required this.repo});

  @override
  State<_CommunityTile> createState() => _CommunityTileState();
}

class _CommunityTileState extends State<_CommunityTile> {
  late bool _joined;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _joined = widget.community.viewerRole != null &&
        widget.community.viewerRole != 'outsider';
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
        await widget.repo.leave(widget.community.id);
      } else {
        await widget.repo.join(widget.community.id);
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
          child: Center(
            child: Text(
              widget.community.name.isNotEmpty
                  ? widget.community.name[0].toUpperCase()
                  : 'C',
              style: const TextStyle(
                color: Colors.white,
                fontWeight: FontWeight.w900,
                fontSize: 18,
              ),
            ),
          ),
        ),
        title: Row(
          children: [
            Flexible(
              child: Text(widget.community.name, style: AppTextStyles.h3),
            ),
            if (widget.community.isVerified) ...[
              const SizedBox(width: 4),
              const Icon(Icons.verified, color: Colors.blue, size: 16),
            ],
          ],
        ),
        subtitle: Text(
          '@${widget.community.handle} · ${widget.community.memberCount} members · ${widget.community.communityType}',
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.textSecondary),
        ),
        trailing: _loading
            ? const SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(
                  strokeWidth: 2,
                  color: AppColors.postbookPrimary,
                ),
              )
            : _joined
                ? OutlinedButton(
                    onPressed: _toggle,
                    style: OutlinedButton.styleFrom(
                      foregroundColor: AppColors.textSecondary,
                      side: const BorderSide(color: AppColors.borderSubtle),
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 4),
                      minimumSize: Size.zero,
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(20),
                      ),
                    ),
                    child: Text('Joined', style: AppTextStyles.labelSmall),
                  )
                : Container(
                    decoration: BoxDecoration(
                      gradient: AppColors.postbookGradient,
                      borderRadius: BorderRadius.circular(20),
                    ),
                    child: OutlinedButton(
                      onPressed: _toggle,
                      style: OutlinedButton.styleFrom(
                        foregroundColor: Colors.white,
                        side: BorderSide.none,
                        padding: const EdgeInsets.symmetric(
                            horizontal: 12, vertical: 4),
                        minimumSize: Size.zero,
                        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(20),
                        ),
                      ),
                      child: Text('Join', style: AppTextStyles.labelSmall),
                    ),
                  ),
        onTap: () => context.push('/communities/${widget.community.id}'),
      ),
    );
  }
}
