import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class DiscoverScreen extends ConsumerStatefulWidget {
  const DiscoverScreen({super.key});

  @override
  ConsumerState<DiscoverScreen> createState() => _DiscoverScreenState();
}

class _DiscoverScreenState extends ConsumerState<DiscoverScreen> {
  final TextEditingController _searchController = TextEditingController();

  bool _loading = true;
  List<String> _hashtags = const [];
  List<Map<String, dynamic>> _suggestions = const [];
  final Set<String> _following = <String>{};

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _loadData() async {
    setState(() => _loading = true);
    try {
      final api = ref.read(apiClientProvider);
      final responses = await Future.wait([
        api.get('/v1/search/trending'),
        api.get('/v1/graph/suggestions'),
      ]);

      final tags = _parseTags(responses[0].data['data']);
      final suggestions = _parseSuggestions(responses[1].data['data']);

      if (!mounted) return;
      setState(() {
        _hashtags = tags;
        _suggestions = suggestions;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _hashtags = const [];
        _suggestions = const [];
        _loading = false;
      });
    }
  }

  List<String> _parseTags(dynamic raw) {
    if (raw is! List) return const [];

    return raw
        .map((item) {
          if (item is Map<String, dynamic>) {
            final tag = (item['tag'] ?? item['name'] ?? '').toString();
            return tag.replaceFirst('#', '').trim();
          }
          return item.toString().replaceFirst('#', '').trim();
        })
        .where((tag) => tag.isNotEmpty)
        .toSet()
        .take(16)
        .toList();
  }

  List<Map<String, dynamic>> _parseSuggestions(dynamic raw) {
    if (raw is! List) return const [];

    return raw
        .whereType<Map>()
        .map((entry) => Map<String, dynamic>.from(entry))
        .toList();
  }

  String _userId(Map<String, dynamic> item) {
    return (item['user_id'] ?? item['id'] ?? '').toString();
  }

  String _name(Map<String, dynamic> item) {
    final display = (item['display_name'] ?? '').toString().trim();
    if (display.isNotEmpty) return display;

    final username = (item['username'] ?? '').toString().trim();
    if (username.isNotEmpty) return username;

    return 'User';
  }

  String _username(Map<String, dynamic> item) {
    final username = (item['username'] ?? '').toString().trim();
    return username.isNotEmpty ? username : 'user';
  }

  String _initials(String value) {
    final parts = value
        .split(' ')
        .where((element) => element.trim().isNotEmpty)
        .toList();

    if (parts.isEmpty) return 'U';
    if (parts.length == 1) {
      return parts.first.substring(0, 1).toUpperCase();
    }
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }

  void _search(String query) {
    final value = query.trim();
    if (value.isEmpty) return;
    context.push('/search/results?q=${Uri.encodeComponent(value)}');
  }

  Future<void> _toggleFollow(Map<String, dynamic> item) async {
    final userId = _userId(item);
    if (userId.isEmpty) return;

    final alreadyFollowing = _following.contains(userId);

    setState(() {
      if (alreadyFollowing) {
        _following.remove(userId);
      } else {
        _following.add(userId);
      }
    });

    try {
      final repo = ref.read(userRepositoryProvider);
      if (alreadyFollowing) {
        await repo.unfollowUser(userId);
      } else {
        await repo.followUser(userId);
      }
    } catch (_) {
      if (!mounted) return;
      setState(() {
        if (alreadyFollowing) {
          _following.add(userId);
        } else {
          _following.remove(userId);
        }
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update follow status.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final features = <_RouteFeature>[
      const _RouteFeature(
        title: 'Groups',
        subtitle: 'Communities',
        icon: Icons.groups_rounded,
        route: '/groups',
        colors: [AppColors.posttubePrimary, AppColors.accentPurple],
      ),
      const _RouteFeature(
        title: 'Shop',
        subtitle: 'Marketplace',
        icon: Icons.storefront_rounded,
        route: '/shop',
        colors: [AppColors.postbookPrimary, AppColors.postbookSecondary],
      ),
      const _RouteFeature(
        title: 'Live',
        subtitle: 'Streams',
        icon: Icons.live_tv_rounded,
        route: '/live',
        colors: [AppColors.liveRed, AppColors.postgramSecondary],
      ),
      const _RouteFeature(
        title: 'PostTube',
        subtitle: 'Videos',
        icon: Icons.ondemand_video_rounded,
        route: '/posttube',
        colors: [AppColors.posttubeSecondary, AppColors.posttubePrimary],
      ),
      const _RouteFeature(
        title: 'Memories',
        subtitle: 'On this day',
        icon: Icons.photo_album_rounded,
        route: '/memories',
        colors: [AppColors.accentPurple, AppColors.postgramSecondary],
      ),
      const _RouteFeature(
        title: 'Monetize',
        subtitle: 'Creator tools',
        icon: Icons.monetization_on_outlined,
        route: '/monetization',
        colors: [AppColors.postgramPrimary, AppColors.postbookPrimary],
      ),
    ];

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: _loadData,
          child: CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(
                    top: 12,
                    bottom: 110,
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      _DiscoverHero(onBack: () => context.pop()),
                      const SizedBox(height: 14),
                      Container(
                        decoration: BoxDecoration(
                          color: AppColors.bgCard,
                          borderRadius: BorderRadius.circular(
                            AppSpacing.radiusXL,
                          ),
                          border: Border.all(color: AppColors.borderSubtle),
                        ),
                        child: TextField(
                          controller: _searchController,
                          textInputAction: TextInputAction.search,
                          onSubmitted: _search,
                          style: AppTextStyles.body,
                          decoration: InputDecoration(
                            border: InputBorder.none,
                            hintText: 'Search topics, users, and tags',
                            hintStyle: AppTextStyles.bodySmall,
                            prefixIcon: const Icon(
                              Icons.search,
                              color: AppColors.textMuted,
                            ),
                            suffixIcon: IconButton(
                              onPressed: () => _search(_searchController.text),
                              icon: const Icon(
                                Icons.arrow_forward_rounded,
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(height: 20),
                      Text('Discover Channels', style: AppTextStyles.h2),
                      const SizedBox(height: 10),
                      LayoutBuilder(
                        builder: (context, constraints) {
                          final columns = constraints.maxWidth >= 700
                              ? 4
                              : constraints.maxWidth >= 480
                              ? 3
                              : 2;
                          const gap = 10.0;
                          final width =
                              (constraints.maxWidth - (columns - 1) * gap) /
                              columns;

                          return Wrap(
                            spacing: gap,
                            runSpacing: gap,
                            children: features
                                .map(
                                  (feature) => SizedBox(
                                    width: width,
                                    child: _FeatureCard(
                                      feature: feature,
                                      onTap: () => context.push(feature.route),
                                    ),
                                  ),
                                )
                                .toList(),
                          );
                        },
                      ),
                      const SizedBox(height: 22),
                      Row(
                        children: [
                          Text('Trending Hashtags', style: AppTextStyles.h2),
                          const Spacer(),
                          if (_hashtags.isNotEmpty)
                            TextButton(
                              onPressed: () => _search('#${_hashtags.first}'),
                              child: Text(
                                'Open',
                                style: AppTextStyles.label.copyWith(
                                  color: AppColors.postbookPrimary,
                                ),
                              ),
                            ),
                        ],
                      ),
                      const SizedBox(height: 8),
                      if (_loading)
                        const Padding(
                          padding: EdgeInsets.symmetric(vertical: 16),
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        )
                      else if (_hashtags.isEmpty)
                        _EmptyCard(
                          message:
                              'No trending hashtags are available right now.',
                          onRetry: _loadData,
                        )
                      else
                        Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          children: _hashtags
                              .map(
                                (tag) => ActionChip(
                                  onPressed: () => _search('#$tag'),
                                  label: Text(
                                    '#$tag',
                                    style: AppTextStyles.label.copyWith(
                                      color: AppColors.postbookPrimary,
                                    ),
                                  ),
                                  side: const BorderSide(
                                    color: AppColors.borderSubtle,
                                  ),
                                  backgroundColor: AppColors.postbookPrimary
                                      .withValues(alpha: 0.18),
                                ),
                              )
                              .toList(),
                        ),
                      const SizedBox(height: 22),
                      Text('Suggested Accounts', style: AppTextStyles.h2),
                      const SizedBox(height: 10),
                      if (_loading)
                        const SizedBox(
                          height: 140,
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        )
                      else if (_suggestions.isEmpty)
                        _EmptyCard(
                          message: 'No suggestions right now. Pull to refresh.',
                          onRetry: _loadData,
                        )
                      else
                        SizedBox(
                          height: 160,
                          child: ListView.separated(
                            scrollDirection: Axis.horizontal,
                            itemCount: _suggestions.length,
                            separatorBuilder: (_, _) =>
                                const SizedBox(width: 10),
                            itemBuilder: (context, index) {
                              final item = _suggestions[index];
                              final userId = _userId(item);
                              final name = _name(item);
                              final username = _username(item);
                              final following = _following.contains(userId);

                              return _SuggestionCard(
                                name: name,
                                username: username,
                                initials: _initials(name),
                                following: following,
                                onTap: () {
                                  if (userId.isNotEmpty) {
                                    context.push('/profile/$userId');
                                  }
                                },
                                onFollowTap: () => _toggleFollow(item),
                              );
                            },
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _DiscoverHero extends StatelessWidget {
  const _DiscoverHero({required this.onBack});

  final VoidCallback onBack;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
        gradient: const LinearGradient(
          colors: [Color(0x3325B2FF), Color(0x33FF6B35), Color(0x334ECDC4)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
      ),
      child: Row(
        children: [
          IconButton(
            onPressed: onBack,
            icon: const Icon(
              Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary,
              size: 18,
            ),
          ),
          const SizedBox(width: 4),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Discover',
                  style: AppTextStyles.h1.copyWith(fontSize: 30),
                ),
                Text(
                  'Find people, trends, and spaces worth following.',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(999),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Text('Hot', style: AppTextStyles.labelSmall),
          ),
        ],
      ),
    );
  }
}

class _RouteFeature {
  const _RouteFeature({
    required this.title,
    required this.subtitle,
    required this.icon,
    required this.route,
    required this.colors,
  });

  final String title;
  final String subtitle;
  final IconData icon;
  final String route;
  final List<Color> colors;
}

class _FeatureCard extends StatelessWidget {
  const _FeatureCard({required this.feature, required this.onTap});

  final _RouteFeature feature;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: Ink(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 38,
                height: 38,
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(12),
                  gradient: LinearGradient(colors: feature.colors),
                ),
                child: Icon(feature.icon, color: Colors.white, size: 20),
              ),
              const SizedBox(height: 10),
              Text(feature.title, style: AppTextStyles.h3),
              const SizedBox(height: 2),
              Text(feature.subtitle, style: AppTextStyles.labelSmall),
            ],
          ),
        ),
      ),
    );
  }
}

class _SuggestionCard extends StatelessWidget {
  const _SuggestionCard({
    required this.name,
    required this.username,
    required this.initials,
    required this.following,
    required this.onTap,
    required this.onFollowTap,
  });

  final String name;
  final String username;
  final String initials;
  final bool following;
  final VoidCallback onTap;
  final VoidCallback onFollowTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Ink(
        width: 170,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            CircleAvatar(
              radius: 18,
              backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
              child: Text(
                initials,
                style: AppTextStyles.label.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
            const Spacer(),
            Text(
              name,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.h3,
            ),
            Text(
              '@$username',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.labelSmall,
            ),
            const SizedBox(height: 8),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton(
                onPressed: onFollowTap,
                style: ElevatedButton.styleFrom(
                  elevation: 0,
                  backgroundColor: following
                      ? AppColors.bgTertiary
                      : AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10),
                    side: BorderSide(
                      color: following
                          ? AppColors.borderSubtle
                          : AppColors.postbookPrimary,
                    ),
                  ),
                ),
                child: Text(following ? 'Following' : 'Follow'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyCard extends StatelessWidget {
  const _EmptyCard({required this.message, this.onRetry});

  final String message;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          const Icon(Icons.insights_outlined, color: AppColors.textMuted),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
          if (onRetry != null)
            TextButton(
              onPressed: onRetry,
              child: Text(
                'Retry',
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
