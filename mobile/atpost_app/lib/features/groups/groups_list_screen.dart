import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_animate/flutter_animate.dart';

class GroupsListScreen extends ConsumerWidget {
  const GroupsListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final groupsState = ref.watch(groupsProvider);

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        backgroundColor: Colors.black,
        body: Container(
          decoration: const BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
              colors: [Color(0xFF0F111A), Color(0xFF090A11)],
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
                      _GroupsList(
                        groups: groupsState.valueOrNull?.myGroups ?? [],
                        isLoading: groupsState.isLoading,
                        emptyMessage: 'You haven\'t joined any groups yet.',
                      ),
                      _GroupsList(
                        groups: groupsState.valueOrNull?.discoveredGroups ?? [],
                        isLoading: groupsState.isLoading,
                        emptyMessage: 'No public groups found.',
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
          Text('Groups', style: AppTextStyles.h1),
          const Spacer(),
          GestureDetector(
            onTap: () => context.push('/groups/create'),
            child: Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                gradient: AppColors.ctaGradient,
                shape: BoxShape.circle,
                boxShadow: [
                  BoxShadow(
                    color: AppColors.postbookPrimary.withOpacity(0.3),
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
          color: Colors.white.withOpacity(0.05),
          borderRadius: BorderRadius.circular(30),
        ),
        child: TabBar(
          indicator: BoxDecoration(
            color: AppColors.postbookPrimary,
            borderRadius: BorderRadius.circular(26),
          ),
          indicatorSize: TabBarIndicatorSize.tab,
          labelColor: Colors.white,
          unselectedLabelColor: Colors.white38,
          labelStyle: AppTextStyles.label.copyWith(fontWeight: FontWeight.bold),
          dividerColor: Colors.transparent,
          tabs: const [
            Tab(text: 'My Groups'),
            Tab(text: 'Discover'),
          ],
        ),
      ),
    );
  }
}

class _GroupsList extends ConsumerWidget {
  final List<Group> groups;
  final bool isLoading;
  final String emptyMessage;

  const _GroupsList({
    required this.groups,
    required this.isLoading,
    required this.emptyMessage,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (isLoading && groups.isEmpty) {
      return const Center(child: CircularProgressIndicator(color: AppColors.postbookPrimary));
    }

    if (groups.isEmpty) {
      return Center(
        child: Text(emptyMessage, style: AppTextStyles.bodySmall.copyWith(color: Colors.white24)),
      );
    }

    return ListView.builder(
      padding: const EdgeInsets.all(16),
      itemCount: groups.length,
      itemBuilder: (context, index) {
        final group = groups[index];
        return Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: _GroupGlassTile(group: group),
        );
      },
    );
  }
}

class _GroupGlassTile extends ConsumerWidget {
  final Group group;
  const _GroupGlassTile({required this.group});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return RepaintBoundary(
      child: GestureDetector(
        onTap: () => context.push('/groups/${group.id}'),
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: Colors.white.withOpacity(0.03),
            borderRadius: BorderRadius.circular(24),
            border: Border.all(color: Colors.white.withOpacity(0.05)),
          ),
          child: Row(
            children: [
              _buildAvatar(),
              const SizedBox(width: 16),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(group.name, style: AppTextStyles.h3),
                    const SizedBox(height: 4),
                    Text(
                      '${group.memberCount} members · ${group.privacy}',
                      style: AppTextStyles.labelSmall.copyWith(color: Colors.white38),
                    ),
                  ],
                ),
              ),
              _JoinButton(groupId: group.id, isMember: group.isMember),
            ],
          ),
        ),
      ),
    ).animate().fadeIn(duration: 300.ms).slideX(begin: 0.1, end: 0);
  }

  Widget _buildAvatar() {
    return Container(
      width: 52,
      height: 52,
      decoration: BoxDecoration(
        gradient: AppColors.postbookGradient,
        borderRadius: BorderRadius.circular(16),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withOpacity(0.2),
            blurRadius: 8,
            offset: const Offset(0, 4),
          ),
        ],
      ),
      child: const Icon(Icons.group, color: Colors.white, size: 26),
    );
  }
}

class _JoinButton extends ConsumerWidget {
  final String groupId;
  final bool isMember;

  const _JoinButton({required this.groupId, required this.isMember});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return ElevatedButton(
      onPressed: () => ref.read(groupsProvider.notifier).toggleJoin(groupId),
      style: ElevatedButton.styleFrom(
        backgroundColor: isMember ? Colors.white.withOpacity(0.05) : AppColors.postbookPrimary,
        foregroundColor: isMember ? Colors.white70 : Colors.white,
        elevation: 0,
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        minimumSize: Size.zero,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(20),
          side: isMember ? BorderSide(color: Colors.white.withOpacity(0.1)) : BorderSide.none,
        ),
      ),
      child: Text(
        isMember ? 'Joined' : 'Join',
        style: AppTextStyles.labelSmall.copyWith(fontWeight: FontWeight.bold),
      ),
    );
  }
}
