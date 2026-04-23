import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/providers/profile_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ProfileScreen extends ConsumerStatefulWidget {
  const ProfileScreen({super.key});

  @override
  ConsumerState<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends ConsumerState<ProfileScreen>
    with SingleTickerProviderStateMixin {
  late TabController _tabController;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final profileAsync = ref.watch(profileProvider);

    return Scaffold(
      backgroundColor: Colors.black,
      body: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [Color(0xFF0F111A), Color(0xFF090A11), Color(0xFF141726)],
          ),
        ),
        child: SafeArea(
          child: profileAsync.when(
            loading: () => const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            ),
            error: (e, _) => _buildErrorState(),
            data: (state) => _buildProfileBody(state),
          ),
        ),
      ),
    );
  }

  Widget _buildProfileBody(ProfileState state) {
    final user = state.user!;
    return RefreshIndicator(
      onRefresh: () => ref.read(profileProvider.notifier).refresh(),
      color: AppColors.postbookPrimary,
      child: NestedScrollView(
        headerSliverBuilder: (context, _) => [
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.all(20),
              child: Column(
                children: [
                  _buildProfileHeader(user),
                  const SizedBox(height: 24),
                  _buildStatsRow(user),
                  const SizedBox(height: 24),
                  _buildActionButtons(),
                  const SizedBox(height: 24),
                  if (state.pins.isNotEmpty) _buildPinnedSection(state.pins),
                ],
              ),
            ),
          ),
          SliverPersistentHeader(
            pinned: true,
            delegate: _SliverTabBarDelegate(
              TabBar(
                controller: _tabController,
                indicatorColor: AppColors.postbookPrimary,
                labelColor: Colors.white,
                unselectedLabelColor: Colors.white24,
                dividerColor: Colors.transparent,
                tabs: const [
                  Tab(text: 'Posts'),
                  Tab(text: 'Portfolio'),
                ],
              ),
            ),
          ),
        ],
        body: TabBarView(
          controller: _tabController,
          children: [
            _buildPostsGrid(state.posts),
            _buildPortfolioList(state.portfolio),
          ],
        ),
      ),
    );
  }

  Widget _buildProfileHeader(User user) {
    return Row(
      children: [
        Container(
          padding: const EdgeInsets.all(3),
          decoration: BoxDecoration(
            gradient: AppColors.postbookGradient,
            shape: BoxShape.circle,
          ),
          child: CircleAvatar(
            radius: 42,
            backgroundColor: Colors.black,
            backgroundImage: user.hasAvatar
                ? NetworkImage(user.avatarUrl)
                : null,
            child: !user.hasAvatar
                ? Text(user.displayName[0], style: AppTextStyles.h1)
                : null,
          ),
        ).animate().scale(duration: 400.ms, curve: Curves.easeOutBack),
        const SizedBox(width: 20),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                user.displayName,
                style: AppTextStyles.h1.copyWith(fontSize: 24),
              ),
              Text(
                '@${user.username}',
                style: AppTextStyles.bodySmall.copyWith(color: Colors.white38),
              ),
              if (user.bio != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(
                    user.bio!,
                    style: AppTextStyles.labelSmall.copyWith(
                      color: Colors.white70,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildStatsRow(User user) {
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 16),
      decoration: BoxDecoration(
        color: Colors.white.withOpacity(0.03),
        borderRadius: BorderRadius.circular(24),
        border: Border.all(color: Colors.white.withOpacity(0.05)),
      ),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceAround,
        children: [
          _buildStatItem('Followers', user.followerCount),
          _buildStatItem('Following', user.followingCount),
          _buildStatItem('Friends', user.friendCount),
        ],
      ),
    );
  }

  Widget _buildStatItem(String label, int value) {
    return Column(
      children: [
        Text(_formatValue(value), style: AppTextStyles.h3),
        Text(
          label,
          style: AppTextStyles.labelTiny.copyWith(color: Colors.white38),
        ),
      ],
    );
  }

  Widget _buildActionButtons() {
    return Row(
      children: [
        Expanded(
          child: ElevatedButton(
            onPressed: () => context.push('/settings/profile'),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(16),
              ),
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
            child: const Text(
              'Edit Profile',
              style: TextStyle(fontWeight: FontWeight.bold),
            ),
          ),
        ),
        const SizedBox(width: 12),
        _buildSquareAction(Icons.share_outlined, () {}),
        const SizedBox(width: 12),
        _buildSquareAction(
          Icons.settings_outlined,
          () => context.push('/settings'),
        ),
      ],
    );
  }

  Widget _buildSquareAction(IconData icon, VoidCallback onTap) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: Colors.white.withOpacity(0.05),
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: Colors.white.withOpacity(0.08)),
        ),
        child: Icon(icon, color: Colors.white70, size: 20),
      ),
    );
  }

  Widget _buildPinnedSection(List<Map<String, dynamic>> pins) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            const Icon(
              Icons.push_pin,
              size: 14,
              color: AppColors.postbookPrimary,
            ),
            const SizedBox(width: 8),
            Text(
              'Pinned Highlights',
              style: AppTextStyles.label.copyWith(color: Colors.white70),
            ),
          ],
        ),
        const SizedBox(height: 12),
        SizedBox(
          height: 100,
          child: ListView.builder(
            scrollDirection: Axis.horizontal,
            itemCount: pins.length,
            itemBuilder: (context, index) => _buildPinCard(pins[index]),
          ),
        ),
      ],
    );
  }

  Widget _buildPinCard(Map<String, dynamic> pin) {
    return Container(
      width: 140,
      margin: const EdgeInsets.only(right: 12),
      decoration: BoxDecoration(
        color: Colors.white.withOpacity(0.03),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: Colors.white.withOpacity(0.05)),
      ),
      child: Center(
        child: Icon(Icons.grid_on, color: Colors.white10, size: 30),
      ),
    );
  }

  Widget _buildPostsGrid(List<Post> posts) {
    if (posts.isEmpty) return _buildEmptyState('No posts yet');
    return GridView.builder(
      padding: const EdgeInsets.all(16),
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 3,
        crossAxisSpacing: 8,
        mainAxisSpacing: 8,
      ),
      itemCount: posts.length,
      itemBuilder: (context, index) => ClipRRect(
        borderRadius: BorderRadius.circular(12),
        child: Container(
          color: Colors.white.withOpacity(0.05),
          child: const Icon(Icons.image_outlined, color: Colors.white10),
        ),
      ),
    );
  }

  Widget _buildPortfolioList(List<Map<String, dynamic>> portfolio) {
    if (portfolio.isEmpty) return _buildEmptyState('Portfolio is empty');
    return ListView.builder(
      padding: const EdgeInsets.all(16),
      itemCount: portfolio.length,
      itemBuilder: (context, index) => _buildPortfolioCard(portfolio[index]),
    );
  }

  Widget _buildPortfolioCard(Map<String, dynamic> item) {
    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: Colors.white.withOpacity(0.03),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: Colors.white.withOpacity(0.05)),
      ),
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: AppColors.postbookPrimary.withOpacity(0.1),
              borderRadius: BorderRadius.circular(12),
            ),
            child: const Icon(
              Icons.work_outline,
              color: AppColors.postbookPrimary,
              size: 20,
            ),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  item['title'] ?? 'Project',
                  style: AppTextStyles.label.copyWith(
                    fontWeight: FontWeight.bold,
                  ),
                ),
                Text(
                  item['item_type'] ?? 'Work',
                  style: AppTextStyles.labelTiny.copyWith(
                    color: Colors.white38,
                  ),
                ),
              ],
            ),
          ),
          const Icon(Icons.arrow_forward_ios, size: 12, color: Colors.white24),
        ],
      ),
    );
  }

  Widget _buildEmptyState(String msg) {
    return Center(
      child: Text(
        msg,
        style: AppTextStyles.bodySmall.copyWith(color: Colors.white24),
      ),
    );
  }

  Widget _buildErrorState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, color: Colors.redAccent, size: 40),
          const SizedBox(height: 16),
          const Text('Failed to load profile'),
          TextButton(
            onPressed: () => ref.read(profileProvider.notifier).refresh(),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }

  String _formatValue(int val) {
    if (val >= 1000) return '${(val / 1000).toStringAsFixed(1)}k';
    return val.toString();
  }
}

class _SliverTabBarDelegate extends SliverPersistentHeaderDelegate {
  _SliverTabBarDelegate(this.tabBar);
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
    return Container(color: Colors.black, child: tabBar);
  }

  @override
  bool shouldRebuild(_SliverTabBarDelegate oldDelegate) => false;
}
