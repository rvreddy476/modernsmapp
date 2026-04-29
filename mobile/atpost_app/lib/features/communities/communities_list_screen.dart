import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/providers/communities_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_animate/flutter_animate.dart';

class CommunitiesListScreen extends ConsumerWidget {
  const CommunitiesListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(communitiesProvider);

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        backgroundColor: Colors.black,
        body: Container(
          decoration: const BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
              colors: [Color(0xFF0F111A), Color(0xFF141726)],
            ),
          ),
          child: SafeArea(
            child: Column(
              children: [
                _buildHeader(context),
                _buildTabBar(),
                Expanded(
                  child: TabBarView(
                    children: [
                      _CommunitiesList(
                        communities: state.valueOrNull?.myCommunities ?? [],
                        isLoading: state.isLoading,
                        emptyMessage: 'You haven\'t joined any communities yet.',
                      ),
                      _CommunitiesList(
                        communities: state.valueOrNull?.discoveredCommunities ?? [],
                        isLoading: state.isLoading,
                        emptyMessage: 'No communities found to discover.',
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back_ios_new,
            tooltip: 'Back',
            onPressed: () => context.pop(),
          ),
          const SizedBox(width: 12),
          Text('Communities', style: AppTextStyles.h1),
          const Spacer(),
          GestureDetector(
            onTap: () => context.push('/communities/create'),
            child: Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                gradient: AppColors.posttubeGradient,
                shape: BoxShape.circle,
                boxShadow: [
                  BoxShadow(
                    color: AppColors.posttubePrimary.withValues(alpha: 0.3),
                    blurRadius: 12,
                    offset: const Offset(0, 4),
                  ),
                ],
              ),
              child: const Icon(Icons.add, color: Colors.white, size: 20),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildTabBar() {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Container(
        padding: const EdgeInsets.all(4),
        decoration: BoxDecoration(
          color: Colors.white.withValues(alpha: 0.05),
          borderRadius: BorderRadius.circular(30),
        ),
        child: TabBar(
          indicator: BoxDecoration(
            color: AppColors.posttubePrimary,
            borderRadius: BorderRadius.circular(26),
          ),
          indicatorSize: TabBarIndicatorSize.tab,
          labelColor: Colors.white,
          unselectedLabelColor: Colors.white38,
          labelStyle: AppTextStyles.label.copyWith(fontWeight: FontWeight.bold),
          dividerColor: Colors.transparent,
          tabs: const [
            Tab(text: 'My Feed'),
            Tab(text: 'Discover'),
          ],
        ),
      ),
    );
  }
}

class _CommunitiesList extends ConsumerWidget {
  final List<Community> communities;
  final bool isLoading;
  final String emptyMessage;

  const _CommunitiesList({
    required this.communities,
    required this.isLoading,
    required this.emptyMessage,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (isLoading && communities.isEmpty) {
      return const Center(child: CircularProgressIndicator(color: AppColors.posttubePrimary));
    }

    if (communities.isEmpty) {
      return Center(
        child: Text(emptyMessage, style: AppTextStyles.bodySmall.copyWith(color: Colors.white24)),
      );
    }

    return ListView.builder(
      padding: const EdgeInsets.all(16),
      itemCount: communities.length,
      itemBuilder: (context, index) {
        final community = communities[index];
        return Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: _CommunityGlassTile(community: community),
        );
      },
    );
  }
}

class _CommunityGlassTile extends ConsumerWidget {
  final Community community;
  const _CommunityGlassTile({required this.community});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isJoined = community.viewerRole != null && community.viewerRole != 'outsider';

    return RepaintBoundary(
      child: GestureDetector(
        onTap: () => context.push('/communities/${community.id}'),
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: 0.03),
            borderRadius: BorderRadius.circular(24),
            border: Border.all(color: Colors.white.withValues(alpha: 0.05)),
          ),
          child: Row(
            children: [
              _buildAvatar(),
              const SizedBox(width: 16),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Flexible(child: Text(community.name, style: AppTextStyles.h3)),
                        if (community.isVerified) ...[
                          const SizedBox(width: 4),
                          const Icon(Icons.verified, color: Colors.blue, size: 14),
                        ],
                      ],
                    ),
                    const SizedBox(height: 4),
                    Text(
                      '@${community.handle} · ${community.memberCount} members',
                      style: AppTextStyles.labelSmall.copyWith(color: Colors.white38),
                    ),
                  ],
                ),
              ),
              _JoinButton(communityId: community.id, isJoined: isJoined),
            ],
          ),
        ),
      ),
    ).animate().fadeIn(duration: 300.ms).scale(begin: const Offset(0.98, 0.98), end: const Offset(1, 1));
  }

  Widget _buildAvatar() {
    return Container(
      width: 52,
      height: 52,
      decoration: BoxDecoration(
        gradient: AppColors.posttubeGradient,
        borderRadius: BorderRadius.circular(16),
      ),
      child: Center(
        child: Text(
          community.name.isNotEmpty ? community.name[0].toUpperCase() : 'C',
          style: const TextStyle(color: Colors.white, fontWeight: FontWeight.w900, fontSize: 20),
        ),
      ),
    );
  }
}

class _JoinButton extends ConsumerWidget {
  final String communityId;
  final bool isJoined;

  const _JoinButton({required this.communityId, required this.isJoined});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return ElevatedButton(
      onPressed: () => ref.read(communitiesProvider.notifier).toggleJoin(communityId),
      style: ElevatedButton.styleFrom(
        backgroundColor: isJoined ? Colors.white.withValues(alpha: 0.05) : AppColors.posttubePrimary,
        foregroundColor: isJoined ? Colors.white70 : Colors.white,
        elevation: 0,
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        minimumSize: Size.zero,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(20),
          side: isJoined ? BorderSide(color: Colors.white.withValues(alpha: 0.1)) : BorderSide.none,
        ),
      ),
      child: Text(
        isJoined ? 'Joined' : 'Join',
        style: AppTextStyles.labelSmall.copyWith(fontWeight: FontWeight.bold),
      ),
    );
  }
}
