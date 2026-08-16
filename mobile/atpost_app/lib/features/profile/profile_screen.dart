import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/providers/data_saver_provider.dart';
import 'package:atpost_app/providers/profile_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/image_url_helper.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ProfileScreen extends ConsumerStatefulWidget {
  const ProfileScreen({super.key});

  @override
  ConsumerState<ProfileScreen> createState() => _ProfileScreenState();
}

enum _ProfileTab { posts, reels, media }

class _ProfileScreenState extends ConsumerState<ProfileScreen> {
  _ProfileTab _tab = _ProfileTab.posts;

  Future<void> _handleLogout() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
        title: Text('Log Out', style: AppTextStyles.h3),
        content: Text(
          'Are you sure you want to log out? You will need to sign in again to access your account.',
          style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text('Cancel', style: TextStyle(color: AppColors.textMuted)),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Log Out', style: TextStyle(color: AppColors.statusError, fontWeight: FontWeight.bold)),
          ),
        ],
      ),
    );

    if (confirmed == true && mounted) {
      ref.read(authServiceProvider).logout();
      context.go('/login');
    }
  }

  @override
  Widget build(BuildContext context) {
    final profileAsync = ref.watch(profileProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: profileAsync.when(
        loading: () => const Center(child: CircularProgressIndicator(color: AppColors.postbookPrimary)),
        error: (err, _) => _buildError(err.toString()),
        data: (state) => RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () => ref.read(profileProvider.notifier).refresh(),
          child: _buildProfileContent(state),
        ),
      ),
    );
  }

  Widget _buildProfileContent(ProfileState state) {
    final user = state.user!;
    final posts = state.posts.where((p) => !p.isReel).toList();
    final reels = state.posts.where((p) => p.isReel).toList();
    final media = state.posts.where((p) => p.mediaIds.isNotEmpty).toList();

    final tabItems = switch (_tab) {
      _ProfileTab.posts => posts,
      _ProfileTab.reels => reels,
      _ProfileTab.media => media,
    };

    return CustomScrollView(
      slivers: [
        // Premium Header
        SliverToBoxAdapter(child: _buildHeader(user, state)),

        // Tab Bar
        SliverPersistentHeader(
          pinned: true,
          delegate: _SliverAppBarDelegate(
            child: _buildTabBar(),
          ),
        ),

        // Grid Content
        if (tabItems.isEmpty)
          SliverFillRemaining(
            child: _buildEmptyState(),
          )
        else
          SliverPadding(
            padding: const EdgeInsets.all(2),
            sliver: SliverGrid(
              gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: 3,
                crossAxisSpacing: 2,
                mainAxisSpacing: 2,
              ),
              delegate: SliverChildBuilderDelegate(
                (context, i) => _buildGridTile(tabItems[i]),
                childCount: tabItems.length,
              ),
            ),
          ),

        const SliverToBoxAdapter(child: SizedBox(height: 100)),
      ],
    );
  }

  Widget _buildHeader(User user, ProfileState state) {
    final topPadding = MediaQuery.of(context).padding.top;

    return Column(
      children: [
        Stack(
          clipBehavior: Clip.none,
          children: [
            // Cover Photo
            Container(
              height: 180 + topPadding,
              width: double.infinity,
              decoration: BoxDecoration(
                gradient: AppColors.ctaGradient,
                image: user.hasCover ? DecorationImage(
                  image: NetworkImage(user.coverUrl!),
                  fit: BoxFit.cover,
                ) : null,
              ),
              child: Container(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.topCenter,
                    end: Alignment.bottomCenter,
                    colors: [Colors.black.withOpacity(0.4), Colors.transparent],
                  ),
                ),
              ),
            ),

            // Top Actions
            Positioned(
              top: topPadding + 10,
              left: 16,
              right: 16,
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  _HeaderAction(icon: Icons.arrow_back_ios_new_rounded, onTap: () => context.pop()),
                  _HeaderAction(icon: Icons.logout_rounded, color: Colors.white, onTap: _handleLogout),
                ],
              ),
            ),

            // Avatar
            Positioned(
              bottom: -40,
              left: 20,
              child: Container(
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  border: Border.all(color: AppColors.bgPrimary, width: 4),
                  boxShadow: [BoxShadow(color: Colors.black26, blurRadius: 10)],
                ),
                child: CircleAvatar(
                  radius: 46,
                  backgroundColor: AppColors.bgTertiary,
                  backgroundImage: user.hasAvatar ? NetworkImage(user.avatarUrl) : null,
                  child: !user.hasAvatar ? Text(user.displayName[0].toUpperCase(), style: AppTextStyles.h1) : null,
                ),
              ),
            ),
          ],
        ),

        const SizedBox(height: 48),

        // User Info
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(user.displayName, style: AppTextStyles.h2.copyWith(fontSize: 24)),
                        Text('@${user.username}', style: AppTextStyles.body.copyWith(color: AppColors.textMuted)),
                      ],
                    ),
                  ),
                  _buildEditButton(),
                ],
              ),
              if (user.bio != null) ...[
                const SizedBox(height: 12),
                Text(user.bio!, style: AppTextStyles.body),
              ],
              const SizedBox(height: 16),
              _buildStatsRow(user),
            ],
          ),
        ),
        const SizedBox(height: 20),
      ],
    );
  }

  Widget _buildEditButton() {
    return ElevatedButton(
      onPressed: () => context.push('/settings/profile'),
      style: ElevatedButton.styleFrom(
        backgroundColor: AppColors.bgCard,
        foregroundColor: Colors.white,
        elevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: const BorderSide(color: AppColors.borderSubtle),
        ),
        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
      ),
      child: Text('Edit Profile', style: AppTextStyles.label),
    );
  }

  Widget _buildStatsRow(User user) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceAround,
      children: [
        _buildStatItem('Posts', user.postCount.toString()),
        _buildStatItem('Followers', _compactCount(user.followerCount)),
        _buildStatItem('Following', _compactCount(user.followingCount)),
      ],
    );
  }

  Widget _buildStatItem(String label, String value) {
    return Column(
      children: [
        Text(value, style: AppTextStyles.h3.copyWith(fontWeight: FontWeight.bold)),
        Text(label, style: AppTextStyles.bodySmall.copyWith(color: AppColors.textMuted)),
      ],
    );
  }

  Widget _buildTabBar() {
    return Container(
      color: AppColors.bgPrimary,
      child: Row(
        children: [
          _buildTabItem(_ProfileTab.posts, Icons.grid_view_rounded),
          _buildTabItem(_ProfileTab.reels, Icons.movie_filter_rounded),
          _buildTabItem(_ProfileTab.media, Icons.photo_library_rounded),
        ],
      ),
    );
  }

  Widget _buildTabItem(_ProfileTab tab, IconData icon) {
    final active = _tab == tab;
    return Expanded(
      child: GestureDetector(
        onTap: () => setState(() => _tab = tab),
        child: Container(
          padding: const EdgeInsets.symmetric(vertical: 12),
          decoration: BoxDecoration(
            border: Border(
              bottom: BorderSide(
                color: active ? AppColors.postbookPrimary : Colors.transparent,
                width: 2,
              ),
            ),
          ),
          child: Icon(
            icon,
            color: active ? AppColors.postbookPrimary : AppColors.textMuted,
          ),
        ),
      ),
    );
  }

  Widget _buildGridTile(Post post) {
    return GestureDetector(
      onTap: () => context.push('/comments/${post.id}'),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          image: post.mediaIds.isNotEmpty ? DecorationImage(
            image: NetworkImage('${Environment.apiBaseUrl}${post.firstMediaUrl}'),
            fit: BoxFit.cover,
          ) : null,
        ),
        child: post.mediaIds.isEmpty ? Center(
          child: Padding(
            padding: const EdgeInsets.all(8.0),
            child: Text(
              post.content,
              maxLines: 4,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.bodySmall.copyWith(fontSize: 10),
            ),
          ),
        ) : null,
      ),
    );
  }

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.layers_clear_rounded, size: 64, color: AppColors.textDim),
          const SizedBox(height: 16),
          Text('No content found', style: AppTextStyles.h3),
          Text('Share your first post today!', style: AppTextStyles.bodySmall),
        ],
      ),
    );
  }

  Widget _buildError(String msg) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Text('Oops!', style: AppTextStyles.h1),
          const SizedBox(height: 8),
          Text(msg, textAlign: TextAlign.center),
          TextButton(onPressed: () => setState(() {}), child: const Text('Try Again')),
        ],
      ),
    );
  }

  String _compactCount(int n) {
    if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}K';
    return n.toString();
  }
}

class _HeaderAction extends StatelessWidget {
  final IconData icon;
  final VoidCallback onTap;
  final Color? color;

  const _HeaderAction({required this.icon, required this.onTap, this.color});

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: Colors.black26,
          shape: BoxShape.circle,
          border: Border.all(color: Colors.white10),
        ),
        child: Icon(icon, color: color ?? Colors.white, size: 20),
      ),
    );
  }
}

class _SliverAppBarDelegate extends SliverPersistentHeaderDelegate {
  _SliverAppBarDelegate({required this.child});
  final Widget child;

  @override
  double get minExtent => 48;
  @override
  double get maxExtent => 48;

  @override
  Widget build(BuildContext context, double shrinkOffset, bool overlapsContent) {
    return child;
  }

  @override
  bool shouldRebuild(_SliverAppBarDelegate oldDelegate) => false;
}
