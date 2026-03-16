import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final _myPostsProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/posts',
    queryParameters: {'author_id': 'me', 'limit': 30},
  );
  final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
  return items.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();
});

final _myPinsProvider = FutureProvider.autoDispose<List<Map<String, dynamic>>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/users/me/pins');
  final items = (response.data['data'] as List<dynamic>?) ??
      (response.data['pins'] as List<dynamic>?) ??
      [];
  return items.map((e) => e as Map<String, dynamic>).toList();
});

final _myPortfolioProvider = FutureProvider.autoDispose<List<Map<String, dynamic>>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/users/me/portfolio');
  final items = (response.data['data'] as List<dynamic>?) ??
      (response.data['items'] as List<dynamic>?) ??
      [];
  return items.map((e) => e as Map<String, dynamic>).toList();
});

class ProfileScreen extends ConsumerStatefulWidget {
  const ProfileScreen({super.key});

  @override
  ConsumerState<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends ConsumerState<ProfileScreen>
    with SingleTickerProviderStateMixin {
  _PostFilter _activeFilter = _PostFilter.all;
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

  Future<void> _refresh() async {
    ref.invalidate(currentUserProvider);
    ref.invalidate(_myPostsProvider);
    ref.invalidate(_myPinsProvider);
    ref.invalidate(_myPortfolioProvider);
  }

  List<Post> _filtered(List<Post> posts) {
    return posts.where((post) {
      return switch (_activeFilter) {
        _PostFilter.all => true,
        _PostFilter.posts => !post.isReel && !post.isVideo,
        _PostFilter.reels => post.isReel,
        _PostFilter.videos => post.isVideo,
      };
    }).toList();
  }

  void _showQrCodeModal(BuildContext context) {
    showModalBottomSheet(
      context: context,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => const _QrCodeSheet(),
    );
  }

  @override
  Widget build(BuildContext context) {
    final userAsync = ref.watch(currentUserProvider);
    final postsAsync = ref.watch(_myPostsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: userAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(
                  Icons.person_off_outlined,
                  color: AppColors.textMuted,
                ),
                const SizedBox(height: 8),
                Text('Could not load profile', style: AppTextStyles.bodySmall),
                const SizedBox(height: 6),
                TextButton(
                  onPressed: _refresh,
                  child: Text(
                    'Retry',
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                ),
              ],
            ),
          ),
          data: (user) {
            final filteredPosts = postsAsync.value != null
                ? _filtered(postsAsync.value!)
                : const <Post>[];

            return RefreshIndicator(
              color: AppColors.postbookPrimary,
              onRefresh: _refresh,
              child: NestedScrollView(
                physics: const AlwaysScrollableScrollPhysics(),
                headerSliverBuilder: (context, _) => [
                  SliverToBoxAdapter(
                    child: Padding(
                      padding: AppSpacing.pagePadding.copyWith(top: 12),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          // Profile header card
                          Container(
                            width: double.infinity,
                            padding: const EdgeInsets.all(16),
                            decoration: BoxDecoration(
                              borderRadius: BorderRadius.circular(
                                AppSpacing.radiusXL,
                              ),
                              border: Border.all(color: AppColors.borderMedium),
                              gradient: const LinearGradient(
                                colors: [
                                  Color(0x3325B2FF),
                                  Color(0x334ECDC4),
                                  Color(0x33FF6B35),
                                ],
                                begin: Alignment.topLeft,
                                end: Alignment.bottomRight,
                              ),
                            ),
                            child: Column(
                              children: [
                                Row(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    ClipRRect(
                                      borderRadius: BorderRadius.circular(20),
                                      child: user.hasAvatar
                                          ? Image.network(
                                              user.avatarUrl,
                                              width: 72,
                                              height: 72,
                                              fit: BoxFit.cover,
                                              errorBuilder: (_, _, _) =>
                                                  _AvatarFallback(
                                                    size: 72,
                                                    initial: user.displayName,
                                                    style: AppTextStyles.h2,
                                                  ),
                                            )
                                          : _AvatarFallback(
                                              size: 72,
                                              initial: user.displayName,
                                              style: AppTextStyles.h2,
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
                                              Expanded(
                                                child: Text(
                                                  user.displayName,
                                                  maxLines: 1,
                                                  overflow:
                                                      TextOverflow.ellipsis,
                                                  style: AppTextStyles.h1
                                                      .copyWith(fontSize: 28),
                                                ),
                                              ),
                                              if (user.isVerified)
                                                const Icon(
                                                  Icons.verified,
                                                  size: 18,
                                                  color:
                                                      AppColors.posttubePrimary,
                                                ),
                                              // QR Code button
                                              IconButton(
                                                icon: const Icon(
                                                  Icons.qr_code,
                                                  size: 20,
                                                  color: AppColors.textMuted,
                                                ),
                                                onPressed: () =>
                                                    _showQrCodeModal(context),
                                                tooltip: 'Your QR Code',
                                                padding: EdgeInsets.zero,
                                                constraints:
                                                    const BoxConstraints(),
                                              ),
                                            ],
                                          ),
                                          const SizedBox(height: 2),
                                          Text(
                                            '@${user.username}',
                                            style: AppTextStyles.bodySmall,
                                          ),
                                          if ((user.profession ?? '')
                                              .trim()
                                              .isNotEmpty)
                                            Padding(
                                              padding: const EdgeInsets.only(
                                                top: 2,
                                              ),
                                              child: Text(
                                                user.profession!,
                                                style:
                                                    AppTextStyles.labelSmall,
                                              ),
                                            ),
                                          if ((user.location ?? '')
                                              .trim()
                                              .isNotEmpty)
                                            Padding(
                                              padding: const EdgeInsets.only(
                                                top: 2,
                                              ),
                                              child: Text(
                                                user.location!,
                                                style:
                                                    AppTextStyles.labelSmall,
                                              ),
                                            ),
                                        ],
                                      ),
                                    ),
                                  ],
                                ),
                                if ((user.bio ?? '').trim().isNotEmpty) ...[
                                  const SizedBox(height: 12),
                                  Container(
                                    width: double.infinity,
                                    padding: const EdgeInsets.all(10),
                                    decoration: BoxDecoration(
                                      color: AppColors.bgCard,
                                      borderRadius: BorderRadius.circular(12),
                                      border: Border.all(
                                        color: AppColors.borderSubtle,
                                      ),
                                    ),
                                    child: Text(
                                      user.bio!,
                                      style: AppTextStyles.bodySmall.copyWith(
                                        color: AppColors.textSecondary,
                                      ),
                                    ),
                                  ),
                                ],
                                const SizedBox(height: 12),
                                Row(
                                  children: [
                                    Expanded(
                                      child: _StatChip(
                                        label: 'Followers',
                                        value: user.followerCount,
                                        onTap: () => context.push(
                                          '/followers/${user.id}',
                                        ),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _StatChip(
                                        label: 'Following',
                                        value: user.followingCount,
                                        onTap: () => context.push(
                                          '/following/${user.id}',
                                        ),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _StatChip(
                                        label: 'Friends',
                                        value: user.friendCount,
                                        onTap: () => context.push('/friends'),
                                      ),
                                    ),
                                  ],
                                ),
                                const SizedBox(height: 12),
                                Row(
                                  children: [
                                    Expanded(
                                      child: _ActionButton(
                                        icon: Icons.edit_outlined,
                                        label: 'Edit Profile',
                                        onTap: () =>
                                            context.push('/settings/profile'),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _ActionButton(
                                        icon: Icons.bookmark_border,
                                        label: 'Bookmarks',
                                        onTap: () =>
                                            context.push('/bookmarks'),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: _ActionButton(
                                        icon: Icons.settings_outlined,
                                        label: 'Settings',
                                        onTap: () => context.push('/settings'),
                                      ),
                                    ),
                                  ],
                                ),
                              ],
                            ),
                          ),
                          const SizedBox(height: 16),
                          // Pinned posts section
                          const _PinnedPostsSection(),
                          const SizedBox(height: 8),
                        ],
                      ),
                    ),
                  ),
                  // Tab bar
                  SliverPersistentHeader(
                    pinned: true,
                    delegate: _SliverTabBarDelegate(
                      TabBar(
                        controller: _tabController,
                        labelColor: AppColors.postbookPrimary,
                        unselectedLabelColor: AppColors.textMuted,
                        indicatorColor: AppColors.postbookPrimary,
                        tabs: const [
                          Tab(icon: Icon(Icons.grid_on_outlined), text: 'Posts'),
                          Tab(icon: Icon(Icons.work_outline), text: 'Portfolio'),
                        ],
                      ),
                    ),
                  ),
                ],
                body: TabBarView(
                  controller: _tabController,
                  children: [
                    // Posts tab
                    _PostsTabContent(
                      postsAsync: postsAsync,
                      filteredPosts: filteredPosts,
                      activeFilter: _activeFilter,
                      onFilterChanged: (f) =>
                          setState(() => _activeFilter = f),
                    ),
                    // Portfolio tab
                    const _PortfolioTab(),
                  ],
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Sliver tab bar delegate
// ---------------------------------------------------------------------------

class _SliverTabBarDelegate extends SliverPersistentHeaderDelegate {
  const _SliverTabBarDelegate(this.tabBar);

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
    return Container(
      color: AppColors.bgPrimary,
      child: tabBar,
    );
  }

  @override
  bool shouldRebuild(_SliverTabBarDelegate oldDelegate) => false;
}

// ---------------------------------------------------------------------------
// Posts tab content (extracted so the NestedScrollView body can host it)
// ---------------------------------------------------------------------------

class _PostsTabContent extends StatelessWidget {
  const _PostsTabContent({
    required this.postsAsync,
    required this.filteredPosts,
    required this.activeFilter,
    required this.onFilterChanged,
  });

  final AsyncValue<List<Post>> postsAsync;
  final List<Post> filteredPosts;
  final _PostFilter activeFilter;
  final void Function(_PostFilter) onFilterChanged;

  @override
  Widget build(BuildContext context) {
    return CustomScrollView(
      slivers: [
        SliverToBoxAdapter(
          child: Padding(
            padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 0),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text('Your Content', style: AppTextStyles.h2),
                    const Spacer(),
                    TextButton(
                      onPressed: () => context.push('/create'),
                      child: Text(
                        'Create',
                        style: AppTextStyles.label.copyWith(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    _FilterChip(
                      label: 'All',
                      selected: activeFilter == _PostFilter.all,
                      onTap: () => onFilterChanged(_PostFilter.all),
                    ),
                    _FilterChip(
                      label: 'Posts',
                      selected: activeFilter == _PostFilter.posts,
                      onTap: () => onFilterChanged(_PostFilter.posts),
                    ),
                    _FilterChip(
                      label: 'Reels',
                      selected: activeFilter == _PostFilter.reels,
                      onTap: () => onFilterChanged(_PostFilter.reels),
                    ),
                    _FilterChip(
                      label: 'Videos',
                      selected: activeFilter == _PostFilter.videos,
                      onTap: () => onFilterChanged(_PostFilter.videos),
                    ),
                  ],
                ),
                const SizedBox(height: 10),
              ],
            ),
          ),
        ),
        if (postsAsync.isLoading)
          const SliverToBoxAdapter(
            child: Padding(
              padding: EdgeInsets.symmetric(vertical: 24),
              child: Center(
                child: CircularProgressIndicator(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
          )
        else if (postsAsync.hasError)
          SliverToBoxAdapter(
            child: Padding(
              padding: AppSpacing.pagePadding,
              child: _InlineStateCard(
                icon: Icons.grid_off_outlined,
                message: 'Could not load posts.',
                action: 'Retry',
                onTap: () {},
              ),
            ),
          )
        else if (filteredPosts.isEmpty)
          SliverToBoxAdapter(
            child: Padding(
              padding: AppSpacing.pagePadding,
              child: _InlineStateCard(
                icon: Icons.photo_library_outlined,
                message: 'No posts in this filter yet.',
                action: 'Create one',
                onTap: () => context.push('/create'),
              ),
            ),
          )
        else
          SliverPadding(
            padding: AppSpacing.pagePadding.copyWith(bottom: 110),
            sliver: SliverGrid(
              gridDelegate:
                  const SliverGridDelegateWithMaxCrossAxisExtent(
                    maxCrossAxisExtent: 180,
                    mainAxisSpacing: 8,
                    crossAxisSpacing: 8,
                    childAspectRatio: 0.86,
                  ),
              delegate: SliverChildBuilderDelegate((context, index) {
                final post = filteredPosts[index];
                return _PostTile(
                  post: post,
                  onTap: () => context.push('/comments/${post.id}'),
                );
              }, childCount: filteredPosts.length),
            ),
          ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Pinned Posts Section
// ---------------------------------------------------------------------------

class _PinnedPostsSection extends ConsumerStatefulWidget {
  const _PinnedPostsSection();

  @override
  ConsumerState<_PinnedPostsSection> createState() =>
      _PinnedPostsSectionState();
}

class _PinnedPostsSectionState extends ConsumerState<_PinnedPostsSection> {
  Future<void> _unpin(String pinId) async {
    try {
      await ref.read(apiClientProvider).delete('/v1/users/me/pins/$pinId');
      ref.invalidate(_myPinsProvider);
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not unpin item.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final pinsAsync = ref.watch(_myPinsProvider);

    return pinsAsync.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (pins) {
        if (pins.isEmpty) return const SizedBox.shrink();
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.push_pin, size: 16, color: AppColors.textMuted),
                const SizedBox(width: 4),
                Text('Pinned', style: AppTextStyles.label),
              ],
            ),
            const SizedBox(height: 8),
            SizedBox(
              height: 80,
              child: ListView.separated(
                scrollDirection: Axis.horizontal,
                itemCount: pins.length,
                separatorBuilder: (_, _) => const SizedBox(width: 8),
                itemBuilder: (context, index) {
                  final pin = pins[index];
                  final pinId = pin['id']?.toString() ?? '';
                  final contentType =
                      pin['content_type']?.toString() ?? 'post';
                  final contentId =
                      pin['content_id']?.toString() ?? '';

                  return GestureDetector(
                    onLongPress: () {
                      showModalBottomSheet(
                        context: context,
                        backgroundColor: AppColors.bgCard,
                        shape: const RoundedRectangleBorder(
                          borderRadius: BorderRadius.vertical(
                            top: Radius.circular(16),
                          ),
                        ),
                        builder: (ctx) => SafeArea(
                          child: ListTile(
                            leading: const Icon(
                              Icons.remove_circle_outline,
                              color: Colors.red,
                            ),
                            title: const Text('Unpin'),
                            onTap: () {
                              Navigator.of(ctx).pop();
                              _unpin(pinId);
                            },
                          ),
                        ),
                      );
                    },
                    child: Container(
                      width: 130,
                      padding: const EdgeInsets.all(10),
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        borderRadius: BorderRadius.circular(12),
                        border: Border.all(color: AppColors.borderSubtle),
                      ),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          Row(
                            children: [
                              const Icon(
                                Icons.push_pin,
                                size: 12,
                                color: AppColors.postbookPrimary,
                              ),
                              const SizedBox(width: 4),
                              Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 6,
                                  vertical: 2,
                                ),
                                decoration: BoxDecoration(
                                  color: AppColors.postbookPrimary
                                      .withValues(alpha: 0.15),
                                  borderRadius: BorderRadius.circular(999),
                                ),
                                child: Text(
                                  contentType.toUpperCase(),
                                  style: AppTextStyles.labelSmall.copyWith(
                                    color: AppColors.postbookPrimary,
                                    fontSize: 9,
                                  ),
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 4),
                          Text(
                            contentId.length > 12
                                ? '${contentId.substring(0, 12)}…'
                                : contentId,
                            style: AppTextStyles.labelSmall,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ],
                      ),
                    ),
                  );
                },
              ),
            ),
            const SizedBox(height: 8),
          ],
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Portfolio Tab
// ---------------------------------------------------------------------------

class _PortfolioTab extends ConsumerStatefulWidget {
  const _PortfolioTab();

  @override
  ConsumerState<_PortfolioTab> createState() => _PortfolioTabState();
}

class _PortfolioTabState extends ConsumerState<_PortfolioTab> {
  void _showAddSheet() {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgCard,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => _AddPortfolioItemSheet(
        onAdded: () => ref.invalidate(_myPortfolioProvider),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final portfolioAsync = ref.watch(_myPortfolioProvider);

    return portfolioAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: _InlineStateCard(
          icon: Icons.work_off_outlined,
          message: 'Could not load portfolio.',
          action: 'Retry',
          onTap: () => ref.invalidate(_myPortfolioProvider),
        ),
      ),
      data: (items) => Stack(
        children: [
          items.isEmpty
              ? Center(
                  child: Padding(
                    padding: AppSpacing.pagePadding,
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(
                          Icons.work_outline,
                          size: 48,
                          color: AppColors.textMuted,
                        ),
                        const SizedBox(height: 12),
                        Text(
                          'Add your portfolio items to showcase your work',
                          style: AppTextStyles.bodySmall,
                          textAlign: TextAlign.center,
                        ),
                      ],
                    ),
                  ),
                )
              : ListView.separated(
                  padding: AppSpacing.pagePadding.copyWith(bottom: 100),
                  itemCount: items.length,
                  separatorBuilder: (_, _) => const SizedBox(height: 10),
                  itemBuilder: (context, index) {
                    final item = items[index];
                    return _PortfolioCard(item: item);
                  },
                ),
          Positioned(
            bottom: 24,
            right: 16,
            child: FloatingActionButton.extended(
              onPressed: _showAddSheet,
              backgroundColor: AppColors.postbookPrimary,
              icon: const Icon(Icons.add, color: Colors.white),
              label: const Text(
                'Add Item',
                style: TextStyle(color: Colors.white),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _PortfolioCard extends StatelessWidget {
  const _PortfolioCard({required this.item});

  final Map<String, dynamic> item;

  @override
  Widget build(BuildContext context) {
    final title = item['title']?.toString() ?? 'Untitled';
    final description = item['description']?.toString() ?? '';
    final type = item['item_type']?.toString() ?? item['type']?.toString() ?? 'other';
    final url = item['url']?.toString() ?? '';

    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  title,
                  style: AppTextStyles.label.copyWith(
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                decoration: BoxDecoration(
                  color: AppColors.postbookPrimary.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  type.toUpperCase(),
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.postbookPrimary,
                    fontSize: 10,
                  ),
                ),
              ),
            ],
          ),
          if (description.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              description,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textSecondary,
              ),
            ),
          ],
          if (url.isNotEmpty) ...[
            const SizedBox(height: 8),
            TextButton.icon(
              onPressed: () {},
              icon: const Icon(Icons.link, size: 14),
              label: Text(
                'View Link',
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
              style: TextButton.styleFrom(
                padding: EdgeInsets.zero,
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _AddPortfolioItemSheet extends StatefulWidget {
  const _AddPortfolioItemSheet({required this.onAdded});

  final VoidCallback onAdded;

  @override
  State<_AddPortfolioItemSheet> createState() => _AddPortfolioItemSheetState();
}

class _AddPortfolioItemSheetState extends State<_AddPortfolioItemSheet> {
  final _titleController = TextEditingController();
  final _descController = TextEditingController();
  final _urlController = TextEditingController();
  String _itemType = 'project';
  bool _submitting = false;
  String? _error;

  static const _itemTypes = [
    'project',
    'article',
    'video',
    'design',
    'other',
  ];

  @override
  void dispose() {
    _titleController.dispose();
    _descController.dispose();
    _urlController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_titleController.text.trim().isEmpty) {
      setState(() => _error = 'Title is required.');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final api = context
          .findAncestorStateOfType<ConsumerState>()
          ?.ref
          .read(apiClientProvider);
      if (api == null) {
        setState(() {
          _error = 'Could not reach API.';
          _submitting = false;
        });
        return;
      }
      await api.post('/v1/users/me/portfolio', data: {
        'title': _titleController.text.trim(),
        'description': _descController.text.trim(),
        'url': _urlController.text.trim(),
        'item_type': _itemType,
      });
      widget.onAdded();
      if (mounted) Navigator.of(context).pop();
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not add portfolio item.';
        _submitting = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 20,
        right: 20,
        top: 20,
        bottom: MediaQuery.of(context).viewInsets.bottom + 20,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.textMuted,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 16),
          Text('Add Portfolio Item', style: AppTextStyles.h2),
          const SizedBox(height: 16),
          TextField(
            controller: _titleController,
            style: const TextStyle(color: Colors.white),
            decoration: const InputDecoration(
              labelText: 'Title *',
              labelStyle: TextStyle(color: AppColors.textMuted),
              enabledBorder: UnderlineInputBorder(
                borderSide: BorderSide(color: AppColors.borderSubtle),
              ),
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _descController,
            style: const TextStyle(color: Colors.white),
            maxLines: 2,
            decoration: const InputDecoration(
              labelText: 'Description',
              labelStyle: TextStyle(color: AppColors.textMuted),
              enabledBorder: UnderlineInputBorder(
                borderSide: BorderSide(color: AppColors.borderSubtle),
              ),
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _urlController,
            style: const TextStyle(color: Colors.white),
            decoration: const InputDecoration(
              labelText: 'URL',
              labelStyle: TextStyle(color: AppColors.textMuted),
              enabledBorder: UnderlineInputBorder(
                borderSide: BorderSide(color: AppColors.borderSubtle),
              ),
            ),
          ),
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            initialValue: _itemType,
            dropdownColor: AppColors.bgCard,
            style: const TextStyle(color: Colors.white),
            decoration: const InputDecoration(
              labelText: 'Type',
              labelStyle: TextStyle(color: AppColors.textMuted),
              enabledBorder: UnderlineInputBorder(
                borderSide: BorderSide(color: AppColors.borderSubtle),
              ),
            ),
            items: _itemTypes
                .map(
                  (t) => DropdownMenuItem(
                    value: t,
                    child: Text(t[0].toUpperCase() + t.substring(1)),
                  ),
                )
                .toList(),
            onChanged: (val) {
              if (val != null) setState(() => _itemType = val);
            },
          ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(
              _error!,
              style: const TextStyle(color: Colors.redAccent, fontSize: 12),
            ),
          ],
          const SizedBox(height: 20),
          SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              onPressed: _submitting ? null : _submit,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
              child: _submitting
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: Colors.white,
                      ),
                    )
                  : const Text(
                      'Add to Portfolio',
                      style: TextStyle(color: Colors.white),
                    ),
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// QR Code Sheet
// ---------------------------------------------------------------------------

class _QrCodeSheet extends ConsumerStatefulWidget {
  const _QrCodeSheet();

  @override
  ConsumerState<_QrCodeSheet> createState() => _QrCodeSheetState();
}

class _QrCodeSheetState extends ConsumerState<_QrCodeSheet> {
  Map<String, dynamic>? _qrData;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _fetchQr();
  }

  Future<void> _fetchQr() async {
    try {
      final res = await ref.read(apiClientProvider).get('/v1/users/me/qr');
      final data = res.data['data'] as Map<String, dynamic>? ??
          res.data as Map<String, dynamic>? ??
          {};
      if (mounted) setState(() { _qrData = data; _loading = false; });
    } catch (_) {
      if (mounted) setState(() { _error = 'Could not load QR code.'; _loading = false; });
    }
  }

  void _copyLink(String url) {
    Clipboard.setData(ClipboardData(text: url));
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Copied!')),
    );
  }

  @override
  Widget build(BuildContext context) {
    final profileUrl = _qrData?['profile_url']?.toString() ?? '';
    final scanCount = _qrData?['scan_count'] ?? 0;

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 36,
            height: 4,
            margin: const EdgeInsets.only(bottom: 16),
            decoration: BoxDecoration(
              color: AppColors.textMuted,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          Text('Your Profile QR', style: AppTextStyles.h2),
          const SizedBox(height: 20),
          if (_loading)
            const CircularProgressIndicator(color: AppColors.postbookPrimary)
          else if (_error != null)
            Text(_error!, style: const TextStyle(color: Colors.redAccent))
          else ...[
            Container(
              width: 200,
              height: 200,
              decoration: BoxDecoration(
                border: Border.all(color: AppColors.borderMedium, width: 2),
                borderRadius: BorderRadius.circular(12),
                color: AppColors.bgCard,
              ),
              child: const Center(
                child: Icon(Icons.qr_code, size: 120, color: AppColors.textSecondary),
              ),
            ),
            const SizedBox(height: 16),
            if (profileUrl.isNotEmpty) ...[
              SelectableText(
                profileUrl,
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.textSecondary,
                ),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 8),
            ],
            Text(
              'Scan count: $scanCount times',
              style: AppTextStyles.labelSmall,
            ),
            const SizedBox(height: 16),
            ElevatedButton.icon(
              onPressed: profileUrl.isNotEmpty ? () => _copyLink(profileUrl) : null,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
              icon: const Icon(Icons.copy, color: Colors.white, size: 16),
              label: const Text(
                'Copy Link',
                style: TextStyle(color: Colors.white),
              ),
            ),
          ],
          const SizedBox(height: 8),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Shared small widgets (unchanged)
// ---------------------------------------------------------------------------

enum _PostFilter { all, posts, reels, videos }

class _AvatarFallback extends StatelessWidget {
  const _AvatarFallback({
    required this.size,
    required this.initial,
    required this.style,
  });

  final double size;
  final String initial;
  final TextStyle style;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: const BoxDecoration(gradient: AppColors.postbookGradient),
      child: Center(
        child: Text(
          initial.isNotEmpty ? initial[0].toUpperCase() : 'U',
          style: style.copyWith(color: Colors.white),
        ),
      ),
    );
  }
}

class _StatChip extends StatelessWidget {
  const _StatChip({
    required this.label,
    required this.value,
    required this.onTap,
  });

  final String label;
  final int value;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(12),
      child: Ink(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          children: [
            Text(_format(value), style: AppTextStyles.h3),
            const SizedBox(height: 2),
            Text(label, style: AppTextStyles.labelSmall),
          ],
        ),
      ),
    );
  }

  String _format(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }
}

class _ActionButton extends StatelessWidget {
  const _ActionButton({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      onPressed: onTap,
      style: OutlinedButton.styleFrom(
        foregroundColor: AppColors.textSecondary,
        side: const BorderSide(color: AppColors.borderSubtle),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      ),
      icon: Icon(icon, size: 16),
      label: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
    );
  }
}

class _FilterChip extends StatelessWidget {
  const _FilterChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: (_) => onTap(),
      selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
      backgroundColor: AppColors.bgCard,
      side: BorderSide(
        color: selected ? AppColors.postbookPrimary : AppColors.borderSubtle,
      ),
      labelStyle: AppTextStyles.label.copyWith(
        color: selected ? AppColors.postbookPrimary : AppColors.textSecondary,
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
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(14),
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

class _PostTile extends StatelessWidget {
  const _PostTile({required this.post, required this.onTap});

  final Post post;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final icon = switch (post.contentType) {
      'reel' => Icons.play_circle_fill_rounded,
      'video' => Icons.videocam_rounded,
      'poll' => Icons.poll_outlined,
      _ => Icons.image_outlined,
    };

    final badge = switch (post.contentType) {
      'reel' => 'REEL',
      'video' => 'VIDEO',
      'poll' => 'POLL',
      _ => 'POST',
    };

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(14),
        child: Ink(
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(color: AppColors.borderSubtle),
            gradient: const LinearGradient(
              colors: [Color(0x1A4ECDC4), Color(0x1AFF6B35), Color(0x1A7B68EE)],
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
            ),
          ),
          child: Padding(
            padding: const EdgeInsets.all(10),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 7,
                        vertical: 3,
                      ),
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: 0.35),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Text(
                        badge,
                        style: AppTextStyles.labelSmall.copyWith(
                          color: Colors.white,
                        ),
                      ),
                    ),
                    const Spacer(),
                    Icon(icon, color: AppColors.textPrimary, size: 20),
                  ],
                ),
                const Spacer(),
                Text(
                  post.content.isEmpty ? '(No caption)' : post.content,
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.label,
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    const Icon(
                      Icons.favorite_border,
                      size: 14,
                      color: AppColors.textMuted,
                    ),
                    const SizedBox(width: 4),
                    Text('${post.likeCount}', style: AppTextStyles.labelSmall),
                    const SizedBox(width: 10),
                    const Icon(
                      Icons.chat_bubble_outline,
                      size: 14,
                      color: AppColors.textMuted,
                    ),
                    const SizedBox(width: 4),
                    Text(
                      '${post.commentCount}',
                      style: AppTextStyles.labelSmall,
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
