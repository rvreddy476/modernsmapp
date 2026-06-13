import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/providers/data_saver_provider.dart';
import 'package:atpost_app/providers/profile_provider.dart';
import 'package:atpost_app/services/image_url_helper.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// The signed-in user's own profile.
///
/// Instagram-style layout: a gradient cover with overlaid share/settings
/// actions, an avatar that overhangs the cover, a four-up stats card, and a
/// Posts / Reels / Saved tab strip over a 3-column media grid.
class ProfileScreen extends ConsumerStatefulWidget {
  const ProfileScreen({super.key});

  @override
  ConsumerState<ProfileScreen> createState() => _ProfileScreenState();
}

enum _ProfileTab { posts, reels, saved }

class _ProfileScreenState extends ConsumerState<ProfileScreen> {
  _ProfileTab _tab = _ProfileTab.posts;

  @override
  Widget build(BuildContext context) {
    final profileAsync = ref.watch(profileProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: profileAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => _buildError(),
        data: (state) => state.user == null
            ? _buildError()
            : RefreshIndicator(
                color: AppColors.postbookPrimary,
                backgroundColor: AppColors.bgSecondary,
                onRefresh: () => ref.read(profileProvider.notifier).refresh(),
                child: _buildBody(state),
              ),
      ),
    );
  }

  // ---------------- Body ----------------

  Widget _buildBody(ProfileState state) {
    final posts = state.posts.where((p) => !p.isReel).toList();
    final reels = state.posts.where((p) => p.isReel).toList();
    final dataSaver = ref.watch(effectiveDataSaverProvider);

    final tabItems = switch (_tab) {
      _ProfileTab.posts => posts,
      _ProfileTab.reels => reels,
      _ProfileTab.saved => const <Post>[],
    };

    return CustomScrollView(
      physics: const AlwaysScrollableScrollPhysics(),
      slivers: [
        SliverToBoxAdapter(child: _buildHeader(state)),
        SliverToBoxAdapter(child: _buildTabBar()),
        if (_tab == _ProfileTab.saved)
          SliverFillRemaining(
            hasScrollBody: false,
            child: _emptyState(
              icon: Icons.bookmark_border_rounded,
              title: 'Nothing saved yet',
              subtitle: 'Posts you bookmark will show up here.',
            ),
          )
        else if (tabItems.isEmpty)
          SliverFillRemaining(
            hasScrollBody: false,
            child: _emptyState(
              icon: _tab == _ProfileTab.reels
                  ? Icons.movie_outlined
                  : Icons.grid_view_rounded,
              title: _tab == _ProfileTab.reels
                  ? 'No reels yet'
                  : 'No posts yet',
              subtitle: _tab == _ProfileTab.reels
                  ? 'Reels you create will appear here.'
                  : 'Share your first post to fill this grid.',
            ),
          )
        else
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(14, 14, 14, 32),
            sliver: SliverGrid(
              gridDelegate:
                  const SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: 3,
                crossAxisSpacing: 10,
                mainAxisSpacing: 10,
              ),
              delegate: SliverChildBuilderDelegate(
                (context, i) {
                  final post = tabItems[i];
                  return _GridTile(
                    post: post,
                    dataSaver: dataSaver,
                    onTap: () => context.push('/comments/${post.id}'),
                  );
                },
                childCount: tabItems.length,
              ),
            ),
          ),
      ],
    );
  }

  // ---------------- Header (cover + avatar + identity + stats) ----

  Widget _buildHeader(ProfileState state) {
    final user = state.user!;
    final topInset = MediaQuery.viewPaddingOf(context).top;
    const coverHeight = 148.0;
    const avatarSize = 84.0;
    final coverTotal = coverHeight + topInset;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          height: coverTotal + 48,
          child: Stack(
            clipBehavior: Clip.none,
            children: [
              // Gradient cover (or the uploaded cover photo).
              Positioned(
                top: 0,
                left: 0,
                right: 0,
                child: _buildCover(user, coverTotal),
              ),
              // Top-left: share profile (rounded-square glass button).
              Positioned(
                top: topInset + 10,
                left: 16,
                child: _glassButton(
                  icon: Icons.ios_share_rounded,
                  circle: false,
                  onTap: () => _toast('Profile share — coming soon'),
                ),
              ),
              // Top-right: settings (circular glass button).
              Positioned(
                top: topInset + 10,
                right: 16,
                child: _glassButton(
                  icon: Icons.settings_outlined,
                  circle: true,
                  onTap: () => context.push('/settings'),
                ),
              ),
              // Avatar — overhangs the cover's bottom edge.
              Positioned(
                left: 16,
                top: coverTotal - avatarSize / 2,
                child: _buildAvatar(user, avatarSize),
              ),
              // Edit profile + QR — aligned with the avatar's lower half.
              Positioned(
                right: 16,
                top: coverTotal + 2,
                child: Row(
                  children: [
                    _editProfileButton(),
                    const SizedBox(width: 8),
                    _circleAction(
                      icon: Icons.qr_code_rounded,
                      onTap: () => _toast('Profile QR — coming soon'),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: 8),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 18),
          child: _buildIdentity(user),
        ),
        const SizedBox(height: 16),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: _buildStatsCard(state),
        ),
        const SizedBox(height: 4),
      ],
    );
  }

  Widget _buildCover(User user, double height) {
    return Container(
      height: height,
      width: double.infinity,
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            AppColors.accentPurple,
            Color(0xFF4A6CF7),
            AppColors.posttubePrimary,
          ],
        ),
        image: user.hasCover
            ? DecorationImage(
                image: NetworkImage(
                  resolveImageUrl(
                    user.coverUrl!,
                    dataSaver: ref.watch(effectiveDataSaverProvider),
                    size: ImageSize.large,
                  ),
                ),
                fit: BoxFit.cover,
                onError: (_, _) {},
              )
            : null,
      ),
    );
  }

  Widget _buildAvatar(User user, double size) {
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        gradient: const LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0xFF5C8DFF), Color(0xFF4A3FB0)],
        ),
        border: Border.all(color: AppColors.bgPrimary, width: 3.5),
        image: user.hasAvatar
            ? DecorationImage(
                image: NetworkImage(
                  resolveImageUrl(
                    user.avatarUrl,
                    dataSaver: ref.watch(effectiveDataSaverProvider),
                    size: ImageSize.medium,
                  ),
                ),
                fit: BoxFit.cover,
              )
            : null,
      ),
      alignment: Alignment.center,
      child: user.hasAvatar
          ? null
          : Text(
              _initialsFor(user.displayName),
              style: const TextStyle(
                fontSize: 26,
                fontWeight: FontWeight.w700,
                color: Colors.white,
              ),
            ),
    );
  }

  Widget _editProfileButton() {
    return GestureDetector(
      onTap: () => context.push('/settings/profile'),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 9),
        decoration: BoxDecoration(
          color: const Color(0x1FFFFFFF),
          borderRadius: BorderRadius.circular(22),
          border: Border.all(color: AppColors.borderMedium),
        ),
        child: const Text(
          'Edit profile',
          style: TextStyle(
            color: Colors.white,
            fontSize: 13,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }

  Widget _circleAction({
    required IconData icon,
    required VoidCallback onTap,
  }) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: const Color(0x1FFFFFFF),
          shape: BoxShape.circle,
          border: Border.all(color: AppColors.borderMedium),
        ),
        alignment: Alignment.center,
        child: Icon(icon, size: 17, color: Colors.white),
      ),
    );
  }

  Widget _glassButton({
    required IconData icon,
    required bool circle,
    required VoidCallback onTap,
  }) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 38,
        height: 38,
        decoration: BoxDecoration(
          color: const Color(0x40000000),
          shape: circle ? BoxShape.circle : BoxShape.rectangle,
          borderRadius: circle ? null : BorderRadius.circular(12),
          border: Border.all(color: const Color(0x33FFFFFF)),
        ),
        alignment: Alignment.center,
        child: Icon(icon, size: 18, color: Colors.white),
      ),
    );
  }

  Widget _buildIdentity(User user) {
    final handle =
        user.username.isNotEmpty ? '@${user.username}' : '@user';
    final pronouns = user.pronouns?.trim();
    final location = user.location?.trim();
    final handleLine = [
      handle,
      if (pronouns != null && pronouns.isNotEmpty) pronouns,
      if (location != null && location.isNotEmpty) location,
    ].join('  ·  ');
    final bio = user.bio?.trim() ?? '';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Flexible(
              child: Text(
                user.displayName,
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 24,
                  fontWeight: FontWeight.w800,
                  height: 1.1,
                ),
              ),
            ),
            if (user.isVerified) ...[
              const SizedBox(width: 6),
              const Icon(
                Icons.verified,
                size: 18,
                color: AppColors.postbookPrimary,
              ),
            ],
          ],
        ),
        const SizedBox(height: 4),
        Text(
          handleLine,
          style: const TextStyle(
            color: AppColors.textTertiary,
            fontSize: 13,
            fontWeight: FontWeight.w500,
          ),
        ),
        if (bio.isNotEmpty) ...[
          const SizedBox(height: 10),
          Text(
            bio,
            style: const TextStyle(
              color: AppColors.textSecondary,
              fontSize: 13.5,
              height: 1.5,
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildStatsCard(ProfileState state) {
    final user = state.user!;
    final postsCount =
        user.postCount > 0 ? user.postCount : state.posts.length;
    final totalLikes =
        state.posts.fold<int>(0, (sum, p) => sum + p.likeCount);

    return Container(
      padding: const EdgeInsets.symmetric(vertical: 14, horizontal: 4),
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          _stat(_compactCount(user.followerCount), 'Followers'),
          _statDivider(),
          _stat(_compactCount(user.followingCount), 'Following'),
          _statDivider(),
          _stat(_compactCount(postsCount), 'Posts'),
          _statDivider(),
          _stat(_compactCount(totalLikes), 'Likes'),
        ],
      ),
    );
  }

  Widget _stat(String value, String label) {
    return Expanded(
      child: Column(
        children: [
          Text(
            value,
            style: const TextStyle(
              color: Colors.white,
              fontSize: 16,
              fontWeight: FontWeight.w800,
            ),
          ),
          const SizedBox(height: 3),
          Text(
            label.toUpperCase(),
            style: const TextStyle(
              color: AppColors.textMuted,
              fontSize: 9.5,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.6,
            ),
          ),
        ],
      ),
    );
  }

  Widget _statDivider() => Container(
        width: 1,
        height: 26,
        color: AppColors.borderMedium,
      );

  // ---------------- Tab bar ----------------

  Widget _buildTabBar() {
    return Container(
      margin: const EdgeInsets.only(top: 4),
      decoration: const BoxDecoration(
        border: Border(
          bottom: BorderSide(color: AppColors.borderSubtle),
        ),
      ),
      child: Row(
        children: [
          _tabItem(_ProfileTab.posts, Icons.grid_view_rounded, 'Posts'),
          _tabItem(_ProfileTab.reels, Icons.movie_outlined, 'Reels'),
          _tabItem(_ProfileTab.saved, Icons.bookmark_border_rounded, 'Saved'),
        ],
      ),
    );
  }

  Widget _tabItem(_ProfileTab tab, IconData icon, String label) {
    final active = _tab == tab;
    final color =
        active ? AppColors.postbookPrimary : AppColors.textMuted;
    return Expanded(
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: () => setState(() => _tab = tab),
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 12),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Icon(icon, size: 16, color: color),
                  const SizedBox(width: 7),
                  Text(
                    label,
                    style: TextStyle(
                      color: color,
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
            ),
            Container(
              height: 2.5,
              color: active
                  ? AppColors.postbookPrimary
                  : Colors.transparent,
            ),
          ],
        ),
      ),
    );
  }

  // ---------------- Empty / error states ----------------

  Widget _emptyState({
    required IconData icon,
    required String title,
    required String subtitle,
  }) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(32, 40, 32, 40),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 36, color: AppColors.textMuted),
            const SizedBox(height: 12),
            Text(
              title,
              style: const TextStyle(
                color: AppColors.textSecondary,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              subtitle,
              textAlign: TextAlign.center,
              style: const TextStyle(
                color: AppColors.textMuted,
                fontSize: 12,
                height: 1.5,
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildError() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, color: Colors.redAccent, size: 40),
          const SizedBox(height: 12),
          const Text(
            'Could not load profile.',
            style: TextStyle(color: Colors.white70),
          ),
          const SizedBox(height: 8),
          TextButton(
            onPressed: () => ref.read(profileProvider.notifier).refresh(),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }

  // ---------------- Misc ----------------

  void _toast(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: AppColors.bgSecondary,
        behavior: SnackBarBehavior.floating,
        duration: const Duration(seconds: 2),
      ),
    );
  }

  String _initialsFor(String displayName) {
    final parts = displayName
        .trim()
        .split(RegExp(r'\s+'))
        .where((p) => p.isNotEmpty)
        .toList();
    if (parts.isEmpty) return 'U';
    if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }
}

/// Compact follower-style count: 2.1M, 12.4K, 482.
String _compactCount(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}K';
  return n.toString();
}

/// Decorative gradients for media-less post tiles, picked deterministically
/// from the post id so a given post always renders the same colour.
const List<List<Color>> _tileGradients = [
  [Color(0xFF7B5BFF), Color(0xFF4A3FB0)], // violet
  [Color(0xFF1FB6AD), Color(0xFF155E63)], // teal
  [Color(0xFFD6249F), Color(0xFF7B2FF7)], // magenta
  [Color(0xFF5B8DEF), Color(0xFF3A4FB8)], // blue
];

/// A single 3-column grid tile: the post's first media if it has any,
/// otherwise a deterministic gradient with a short text preview.
class _GridTile extends StatelessWidget {
  const _GridTile({
    required this.post,
    required this.dataSaver,
    required this.onTap,
  });

  final Post post;
  final bool dataSaver;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors =
        _tileGradients[post.id.hashCode.abs() % _tileGradients.length];

    return GestureDetector(
      onTap: onTap,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(14),
        child: Stack(
          fit: StackFit.expand,
          children: [
            if (post.mediaIds.isNotEmpty)
              Image.network(
                resolveImageUrl(
                  '${Environment.apiBaseUrl}${post.firstMediaUrl}',
                  dataSaver: dataSaver,
                  size: ImageSize.medium,
                ),
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => _gradientFill(colors),
              )
            else
              _gradientFill(colors),
            // Top-right marker: play for reels, stack for carousels.
            if (post.isReel)
              const Positioned(
                top: 7,
                right: 7,
                child: _TileChip(icon: Icons.play_arrow_rounded),
              )
            else if (post.mediaIds.length > 1)
              const Positioned(
                top: 7,
                right: 7,
                child: _TileChip(icon: Icons.collections_rounded),
              ),
            // Bottom-left: like count over a soft scrim.
            if (post.likeCount > 0)
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: Container(
                  padding: const EdgeInsets.fromLTRB(8, 16, 8, 7),
                  decoration: const BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.bottomCenter,
                      end: Alignment.topCenter,
                      colors: [Color(0x99000000), Color(0x00000000)],
                    ),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(
                        Icons.favorite,
                        size: 12,
                        color: Colors.white,
                      ),
                      const SizedBox(width: 4),
                      Text(
                        _compactCount(post.likeCount),
                        style: const TextStyle(
                          color: Colors.white,
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _gradientFill(List<Color> colors) {
    final preview = post.content.trim();
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: colors,
        ),
      ),
      padding: const EdgeInsets.all(10),
      alignment: Alignment.topLeft,
      child: preview.isEmpty
          ? null
          : Text(
              preview,
              maxLines: 5,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: Colors.white.withValues(alpha: 0.92),
                fontSize: 11,
                height: 1.35,
                fontWeight: FontWeight.w500,
              ),
            ),
    );
  }
}

/// Small translucent corner chip used for the carousel / reel markers.
class _TileChip extends StatelessWidget {
  const _TileChip({required this.icon});

  final IconData icon;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 22,
      height: 22,
      decoration: const BoxDecoration(
        color: Color(0x59000000),
        shape: BoxShape.circle,
      ),
      alignment: Alignment.center,
      child: Icon(icon, size: 14, color: Colors.white),
    );
  }
}
