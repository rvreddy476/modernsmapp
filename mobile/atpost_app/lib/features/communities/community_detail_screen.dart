import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/repositories/communities_repository.dart';
import 'package:atpost_app/providers/communities_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CommunityDetailScreen extends ConsumerStatefulWidget {
  final String communityId;
  const CommunityDetailScreen({super.key, required this.communityId});

  @override
  ConsumerState<CommunityDetailScreen> createState() =>
      _CommunityDetailScreenState();
}

class _CommunityDetailScreenState
    extends ConsumerState<CommunityDetailScreen>
    with SingleTickerProviderStateMixin {
  late TabController _tabCtrl;
  bool _joined = false;
  bool _toggleLoading = false;

  @override
  void initState() {
    super.initState();
    _tabCtrl = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabCtrl.dispose();
    super.dispose();
  }

  Future<void> _toggleJoin() async {
    if (_toggleLoading) return;
    final wasJoined = _joined;
    setState(() {
      _joined = !_joined;
      _toggleLoading = true;
    });
    try {
      final repo = ref.read(communitiesRepositoryProvider);
      if (wasJoined) {
        await repo.leave(widget.communityId);
      } else {
        await repo.join(widget.communityId);
      }
      ref.invalidate(communityDetailProvider(widget.communityId));
    } catch (_) {
      if (mounted) setState(() => _joined = wasJoined);
    } finally {
      if (mounted) setState(() => _toggleLoading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final communityAsync =
        ref.watch(communityDetailProvider(widget.communityId));
    final spacesAsync =
        ref.watch(communitySpacesProvider(widget.communityId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: communityAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.error_outline,
                  color: AppColors.textDim, size: 40),
              const SizedBox(height: 12),
              Text('Failed to load community', style: AppTextStyles.body),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () => ref.invalidate(
                    communityDetailProvider(widget.communityId)),
                child: Text('Retry',
                    style: AppTextStyles.label
                        .copyWith(color: AppColors.postbookPrimary)),
              ),
            ],
          ),
        ),
        data: (community) {
          // Sync join state
          if (!_toggleLoading) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (mounted && !_toggleLoading) {
                setState(() => _joined = community.viewerRole != null &&
                    community.viewerRole != 'outsider');
              }
            });
          }

          return NestedScrollView(
            headerSliverBuilder: (context, _) => [
              SliverAppBar(
                expandedHeight: 200,
                pinned: true,
                backgroundColor: AppColors.bgPrimary,
                leading: IconButton(
                  icon: const Icon(Icons.arrow_back, color: Colors.white),
                  onPressed: () => context.pop(),
                ),
                flexibleSpace: FlexibleSpaceBar(
                  background: Container(
                    decoration: const BoxDecoration(
                      gradient: LinearGradient(
                        begin: Alignment.topLeft,
                        end: Alignment.bottomRight,
                        colors: [
                          AppColors.postbookPrimary,
                          AppColors.accentPurple,
                        ],
                      ),
                    ),
                    child: SafeArea(
                      child: Padding(
                        padding:
                            const EdgeInsets.fromLTRB(20, 60, 20, 20),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisAlignment: MainAxisAlignment.end,
                          children: [
                            Row(
                              children: [
                                Container(
                                  width: 56,
                                  height: 56,
                                  decoration: BoxDecoration(
                                    color:
                                        Colors.white.withValues(alpha: 0.2),
                                    borderRadius:
                                        BorderRadius.circular(16),
                                  ),
                                  child: Center(
                                    child: Text(
                                      community.name.isNotEmpty
                                          ? community.name[0].toUpperCase()
                                          : 'C',
                                      style: const TextStyle(
                                        color: Colors.white,
                                        fontWeight: FontWeight.w900,
                                        fontSize: 24,
                                      ),
                                    ),
                                  ),
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Row(
                                        children: [
                                          Flexible(
                                            child: Text(
                                              community.name,
                                              style: const TextStyle(
                                                color: Colors.white,
                                                fontWeight: FontWeight.w700,
                                                fontSize: 18,
                                              ),
                                            ),
                                          ),
                                          if (community.isVerified) ...[
                                            const SizedBox(width: 4),
                                            const Icon(Icons.verified,
                                                color: Colors.white,
                                                size: 18),
                                          ],
                                        ],
                                      ),
                                      Text(
                                        '@${community.handle}',
                                        style: TextStyle(
                                          color: Colors.white
                                              .withValues(alpha: 0.8),
                                          fontSize: 13,
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                              ],
                            ),
                            const SizedBox(height: 8),
                            Text(
                              '${community.memberCount} members · ${community.spaceCount} spaces · ${community.communityType}',
                              style: TextStyle(
                                color:
                                    Colors.white.withValues(alpha: 0.8),
                                fontSize: 12,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
              ),

              // Join / Leave button
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding
                      .copyWith(top: 16, bottom: 8),
                  child: SizedBox(
                    width: double.infinity,
                    child: _toggleLoading
                        ? const Center(
                            child: Padding(
                              padding: EdgeInsets.all(12),
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                          )
                        : _joined
                            ? OutlinedButton(
                                onPressed: _toggleJoin,
                                style: OutlinedButton.styleFrom(
                                  foregroundColor:
                                      AppColors.textSecondary,
                                  side: const BorderSide(
                                      color: AppColors.borderSubtle),
                                  padding: const EdgeInsets.symmetric(
                                      vertical: 12),
                                  shape: RoundedRectangleBorder(
                                    borderRadius: BorderRadius.circular(
                                        AppSpacing.radiusMedium),
                                  ),
                                ),
                                child: Text('Leave Community',
                                    style: AppTextStyles.label),
                              )
                            : Container(
                                decoration: BoxDecoration(
                                  gradient: AppColors.postbookGradient,
                                  borderRadius: BorderRadius.circular(
                                      AppSpacing.radiusMedium),
                                ),
                                child: OutlinedButton(
                                  onPressed: _toggleJoin,
                                  style: OutlinedButton.styleFrom(
                                    foregroundColor: Colors.white,
                                    side: BorderSide.none,
                                    padding: const EdgeInsets.symmetric(
                                        vertical: 12),
                                    shape: RoundedRectangleBorder(
                                      borderRadius: BorderRadius.circular(
                                          AppSpacing.radiusMedium),
                                    ),
                                  ),
                                  child: Text('Join Community',
                                      style: AppTextStyles.label),
                                ),
                              ),
                  ),
                ),
              ),

              // Description
              if (community.description.isNotEmpty)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: AppSpacing.pagePadding
                        .copyWith(top: 8, bottom: 12),
                    child: Text(
                      community.description,
                      style: AppTextStyles.body
                          .copyWith(color: AppColors.textSecondary),
                    ),
                  ),
                ),

              // Tabs
              SliverPersistentHeader(
                pinned: true,
                delegate: _TabBarDelegate(
                  TabBar(
                    controller: _tabCtrl,
                    labelColor: AppColors.postbookPrimary,
                    unselectedLabelColor: AppColors.textDim,
                    indicatorColor: AppColors.postbookPrimary,
                    labelStyle: AppTextStyles.label,
                    tabs: const [
                      Tab(text: 'Spaces'),
                      Tab(text: 'Members'),
                    ],
                  ),
                ),
              ),
            ],
            body: TabBarView(
              controller: _tabCtrl,
              children: [
                // Spaces tab
                spacesAsync.when(
                  loading: () => const Center(
                    child: CircularProgressIndicator(
                        color: AppColors.postbookPrimary),
                  ),
                  error: (_, _) => Center(
                    child: Text('Failed to load spaces',
                        style: AppTextStyles.body),
                  ),
                  data: (spaces) {
                    if (spaces.isEmpty) {
                      return Center(
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            const Icon(Icons.space_dashboard_outlined,
                                color: AppColors.textDim, size: 40),
                            const SizedBox(height: 8),
                            Text('No spaces yet',
                                style: AppTextStyles.body.copyWith(
                                    color: AppColors.textSecondary)),
                          ],
                        ),
                      );
                    }
                    return ListView.separated(
                      padding: AppSpacing.pagePadding
                          .copyWith(top: 12, bottom: 100),
                      itemCount: spaces.length,
                      separatorBuilder: (_, _) =>
                          const SizedBox(height: 8),
                      itemBuilder: (context, index) =>
                          _SpaceTile(space: spaces[index]),
                    );
                  },
                ),

                // Members tab — placeholder
                Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.people_outline,
                          color: AppColors.textDim, size: 40),
                      const SizedBox(height: 8),
                      Text(
                        '${community.memberCount} members',
                        style: AppTextStyles.body
                            .copyWith(color: AppColors.textSecondary),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          );
        },
      ),
    );
  }
}

class _SpaceTile extends StatelessWidget {
  final CommunitySpace space;
  const _SpaceTile({required this.space});

  IconData _iconForType(String type) {
    switch (type) {
      case 'group':
        return Icons.group;
      case 'channel':
        return Icons.campaign;
      case 'discussion':
        return Icons.forum;
      case 'events':
        return Icons.event;
      case 'resources':
        return Icons.folder_open;
      default:
        return Icons.space_dashboard;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        leading: Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Icon(
            _iconForType(space.spaceType),
            color: AppColors.postbookPrimary,
            size: 20,
          ),
        ),
        title: Row(
          children: [
            Flexible(
              child: Text(space.name, style: AppTextStyles.h3),
            ),
            if (space.isQuarantined) ...[
              const SizedBox(width: 4),
              const Icon(Icons.warning_amber, color: Colors.amber, size: 14),
            ],
          ],
        ),
        subtitle: Text(
          space.description.isNotEmpty
              ? space.description
              : space.spaceType,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.textSecondary),
        ),
        trailing: Container(
          padding:
              const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Text(
            space.spaceType,
            style: AppTextStyles.labelSmall
                .copyWith(color: AppColors.textDim, fontSize: 10),
          ),
        ),
        onTap: () {
          if (space.linkedGroupId != null) {
            context.push('/groups/${space.linkedGroupId}');
          } else if (space.linkedChannelId != null) {
            context.push('/channels/${space.linkedChannelId}');
          }
        },
      ),
    );
  }
}

class _TabBarDelegate extends SliverPersistentHeaderDelegate {
  final TabBar tabBar;
  _TabBarDelegate(this.tabBar);

  @override
  Widget build(
      BuildContext context, double shrinkOffset, bool overlapsContent) {
    return Container(color: AppColors.bgPrimary, child: tabBar);
  }

  @override
  double get maxExtent => tabBar.preferredSize.height;

  @override
  double get minExtent => tabBar.preferredSize.height;

  @override
  bool shouldRebuild(covariant _TabBarDelegate oldDelegate) => false;
}
