import 'dart:async';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/services/data/service_registry.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/widgets/service_icon.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ExploreScreen extends ConsumerStatefulWidget {
  const ExploreScreen({super.key});

  @override
  ConsumerState<ExploreScreen> createState() => _ExploreScreenState();
}

class _ExploreScreenState extends ConsumerState<ExploreScreen> {
  final TextEditingController _searchController = TextEditingController();
  Timer? _debounce;

  bool _loading = true;
  bool _loadingAutocomplete = false;
  List<String> _trendingTags = const [];
  List<Map<String, dynamic>> _suggestedAccounts = const [];
  List<Map<String, dynamic>> _autocompleteResults = const [];
  final Set<String> _followingUserIds = <String>{};

  @override
  void initState() {
    super.initState();
    _loadDiscovery();
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _loadDiscovery() async {
    setState(() => _loading = true);
    try {
      final api = ref.read(apiClientProvider);
      final responses = await Future.wait([
        api.get('/v1/discover/trending'),
        api.get('/v1/suggestions'),
      ]);

      final tags = _parseTags(responses[0].data['data']);
      final suggestions = _parseUsers(responses[1].data['data']);

      if (!mounted) return;
      setState(() {
        _trendingTags = tags;
        _suggestedAccounts = suggestions;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _trendingTags = const [];
        _suggestedAccounts = const [];
        _loading = false;
      });
    }
  }

  // PRODUCTION OPTIMIZATION: Debounced search to prevent API spamming
  void _onSearchChanged(String raw) {
    if (_debounce?.isActive ?? false) _debounce!.cancel();
    _debounce = Timer(
      const Duration(milliseconds: 400),
      () => _performSearch(raw),
    );
  }

  Future<void> _performSearch(String raw) async {
    final query = raw.trim();
    if (query.isEmpty) {
      if (!mounted) return;
      setState(() {
        _autocompleteResults = const [];
        _loadingAutocomplete = false;
      });
      return;
    }

    setState(() => _loadingAutocomplete = true);
    try {
      final results = await ref
          .read(userRepositoryProvider)
          .searchAutocomplete(query, limit: 8);

      if (!mounted || _searchController.text.trim() != query) return;

      setState(() {
        _autocompleteResults = results;
        _loadingAutocomplete = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _autocompleteResults = const [];
        _loadingAutocomplete = false;
      });
    }
  }

  Future<void> _toggleFollow(Map<String, dynamic> user) async {
    final userId = _userId(user);
    if (userId.isEmpty) return;

    final wasFollowing = _followingUserIds.contains(userId);
    setState(() {
      if (wasFollowing) {
        _followingUserIds.remove(userId);
      } else {
        _followingUserIds.add(userId);
      }
    });

    try {
      final repo = ref.read(userRepositoryProvider);
      if (wasFollowing) {
        await repo.unfollowUser(userId);
      } else {
        await repo.followUser(userId);
      }
    } catch (_) {
      if (!mounted) return;
      setState(() {
        if (wasFollowing) {
          _followingUserIds.add(userId);
        } else {
          _followingUserIds.remove(userId);
        }
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update follow status.')),
      );
    }
  }

  void _submitSearch([String? candidate]) {
    final query = (candidate ?? _searchController.text).trim();
    if (query.isEmpty) return;
    FocusScope.of(context).unfocus();
    context.push('/search/results?q=${Uri.encodeComponent(query)}');
  }

  List<String> _parseTags(dynamic raw) {
    if (raw is! List) return const [];
    return raw
        .map((e) {
          if (e is Map<String, dynamic>) {
            return (e['tag'] ?? e['name'] ?? '').toString();
          }
          return e.toString();
        })
        .map((v) => v.replaceFirst('#', '').trim())
        .where((v) => v.isNotEmpty)
        .toSet()
        .take(12)
        .toList();
  }

  List<Map<String, dynamic>> _parseUsers(dynamic raw) {
    if (raw is! List) return const [];
    return raw
        .whereType<Map>()
        .map((e) => Map<String, dynamic>.from(e))
        .toList();
  }

  String _userId(Map<String, dynamic> user) {
    return (user['id'] ?? user['user_id'] ?? '').toString();
  }

  String _displayName(Map<String, dynamic> user) {
    final display = (user['display_name'] ?? '').toString().trim();
    if (display.isNotEmpty) return display;
    final username = (user['username'] ?? '').toString().trim();
    if (username.isNotEmpty) return username;
    return 'User';
  }

  String _username(Map<String, dynamic> user) {
    final value = (user['username'] ?? '').toString().trim();
    return value.isNotEmpty ? value : 'user';
  }

  String _initials(String value) {
    final parts = value
        .split(' ')
        .where((segment) => segment.trim().isNotEmpty)
        .toList();
    if (parts.isEmpty) return 'U';
    if (parts.length == 1) {
      return parts.first.substring(0, 1).toUpperCase();
    }
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }

  @override
  Widget build(BuildContext context) {
    final actions = <_ExploreAction>[
      const _ExploreAction(
        icon: Icons.apps_rounded,
        title: 'Services',
        subtitle: 'Mini App Center',
        route: '/services',
        gradient: LinearGradient(
          colors: [AppColors.accentPurple, AppColors.posttubePrimary],
        ),
      ),
      const _ExploreAction(
        icon: Icons.question_answer_rounded,
        title: 'Q&A',
        subtitle: 'Ask and answer',
        route: '/qa',
        gradient: LinearGradient(
          colors: [AppColors.postbookPrimary, AppColors.posttubePrimary],
        ),
      ),
      const _ExploreAction(
        icon: Icons.extension_rounded,
        title: 'Mini Apps',
        subtitle: 'Tools and games',
        route: '/apps',
        gradient: LinearGradient(
          colors: [AppColors.accentPurple, AppColors.postbookSecondary],
        ),
      ),
      const _ExploreAction(
        icon: Icons.favorite_rounded,
        title: 'PostMatch',
        subtitle: 'Meet people',
        route: '/postmatch',
        gradient: LinearGradient(
          colors: [AppColors.postgramPrimary, AppColors.liveRed],
        ),
      ),
      const _ExploreAction(
        icon: Icons.groups_rounded,
        title: 'Groups',
        subtitle: 'Communities',
        route: '/groups',
        gradient: LinearGradient(
          colors: [AppColors.posttubePrimary, AppColors.accentPurple],
        ),
      ),
      const _ExploreAction(
        icon: Icons.storefront_rounded,
        title: 'Shop',
        subtitle: 'Products',
        route: '/shop',
        gradient: LinearGradient(
          colors: [AppColors.postbookPrimary, AppColors.postbookSecondary],
        ),
      ),
      const _ExploreAction(
        icon: Icons.live_tv_rounded,
        title: 'Live',
        subtitle: 'Go live now',
        route: '/live',
        gradient: LinearGradient(
          colors: [AppColors.liveRed, AppColors.postgramSecondary],
        ),
      ),
      const _ExploreAction(
        icon: Icons.video_collection_rounded,
        title: 'PostTube',
        subtitle: 'Long videos',
        route: '/posttube',
        gradient: LinearGradient(
          colors: [AppColors.posttubePrimary, AppColors.posttubeSecondary],
        ),
      ),
      const _ExploreAction(
        icon: Icons.photo_album_rounded,
        title: 'Memories',
        subtitle: 'On this day',
        route: '/memories',
        gradient: LinearGradient(
          colors: [AppColors.accentPurple, AppColors.postgramSecondary],
        ),
      ),
      const _ExploreAction(
        icon: Icons.savings_rounded,
        title: 'Monetize',
        subtitle: 'Creator tools',
        route: '/monetization',
        gradient: LinearGradient(
          colors: [AppColors.postgramPrimary, AppColors.postbookPrimary],
        ),
      ),
    ];

    final hasSearchText = _searchController.text.trim().isNotEmpty;

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: RefreshIndicator(
          onRefresh: _loadDiscovery,
          color: AppColors.postbookPrimary,
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
                      _ExploreHero(
                        onActionTap: () => context.push('/discover'),
                      ),
                      const SizedBox(height: 16),
                      _SearchBar(
                        controller: _searchController,
                        onChanged: _onSearchChanged,
                        onSubmitted: _submitSearch,
                        onClear: () {
                          _searchController.clear();
                          setState(() => _autocompleteResults = const []);
                        },
                      ),
                      if (hasSearchText) ...[
                        const SizedBox(height: 10),
                        _AutocompletePanel(
                          loading: _loadingAutocomplete,
                          results: _autocompleteResults,
                          onTapResult: (item) {
                            final userId = _userId(item);
                            if (userId.isNotEmpty) {
                              context.push('/profile/$userId');
                            } else {
                              _submitSearch('@${_username(item)}');
                            }
                          },
                        ),
                      ],
                      const SizedBox(height: 22),
                      Row(
                        children: [
                          Text('Services', style: AppTextStyles.h2),
                          const Spacer(),
                          TextButton(
                            onPressed: () => context.push('/services'),
                            child: Text(
                              'View all',
                              style: AppTextStyles.label.copyWith(
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 4),
                      _ServicesRail(
                        onTap: (app) {
                          if (app.status.isOpenable && app.route != null) {
                            context.push(app.route!);
                          } else {
                            context.push('/services/${app.slug}');
                          }
                        },
                      ),
                      const SizedBox(height: 22),
                      Text('Quick Actions', style: AppTextStyles.h2),
                      const SizedBox(height: 10),
                      LayoutBuilder(
                        builder: (context, constraints) {
                          final columns = constraints.maxWidth >= 700
                              ? 4
                              : constraints.maxWidth >= 480
                              ? 3
                              : 2;
                          const spacing = 10.0;
                          final width =
                              (constraints.maxWidth - (columns - 1) * spacing) /
                              columns;
                          return Wrap(
                            spacing: spacing,
                            runSpacing: spacing,
                            children: actions
                                .map(
                                  (action) => SizedBox(
                                    width: width,
                                    child: _QuickActionCard(
                                      action: action,
                                      onTap: () => context.push(action.route),
                                    ),
                                  ),
                                )
                                .toList(),
                          );
                        },
                      ),
                      const SizedBox(height: 24),
                      Row(
                        children: [
                          Text('Trending Now', style: AppTextStyles.h2),
                          const Spacer(),
                          if (_trendingTags.isNotEmpty)
                            TextButton(
                              onPressed: () =>
                                  _submitSearch('#${_trendingTags.first}'),
                              child: Text(
                                'See all',
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
                          padding: EdgeInsets.symmetric(vertical: 22),
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        )
                      else if (_trendingTags.isEmpty)
                        _MutedCard(
                          message:
                              'Trending tags are loading from your network.',
                          onRetry: _loadDiscovery,
                        )
                      else
                        Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          children: _trendingTags
                              .map(
                                (tag) => ActionChip(
                                  onPressed: () => _submitSearch('#$tag'),
                                  backgroundColor: AppColors.postbookPrimary
                                      .withValues(alpha: 0.15),
                                  side: const BorderSide(
                                    color: AppColors.borderSubtle,
                                  ),
                                  label: Text(
                                    '#$tag',
                                    style: AppTextStyles.label.copyWith(
                                      color: AppColors.postbookPrimary,
                                    ),
                                  ),
                                ),
                              )
                              .toList(),
                        ),
                      const SizedBox(height: 24),
                      Text('Suggested Creators', style: AppTextStyles.h2),
                      const SizedBox(height: 10),
                      if (_loading)
                        const SizedBox(
                          height: 142,
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        )
                      else if (_suggestedAccounts.isEmpty)
                        _MutedCard(
                          message: 'No suggestions right now. Pull to refresh.',
                          onRetry: _loadDiscovery,
                        )
                      else
                        SizedBox(
                          height: 152,
                          child: ListView.separated(
                            scrollDirection: Axis.horizontal,
                            itemCount: _suggestedAccounts.length,
                            separatorBuilder: (_, _) =>
                                const SizedBox(width: 10),
                            itemBuilder: (context, index) {
                              final user = _suggestedAccounts[index];
                              final userId = _userId(user);
                              final name = _displayName(user);
                              final username = _username(user);
                              final following = _followingUserIds.contains(
                                userId,
                              );
                              return _CreatorCard(
                                name: name,
                                username: username,
                                initials: _initials(name),
                                following: following,
                                onTap: () {
                                  if (userId.isNotEmpty) {
                                    context.push('/profile/$userId');
                                  }
                                },
                                onFollowTap: () => _toggleFollow(user),
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

class _ExploreHero extends StatelessWidget {
  const _ExploreHero({required this.onActionTap});

  final VoidCallback onActionTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0x3325B2FF), Color(0x33FF6B35), Color(0x333CDECC)],
        ),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Explore', style: AppTextStyles.h1.copyWith(fontSize: 30)),
          const SizedBox(height: 6),
          Text(
            'Discover creators, trends, and community spaces built for Postbook.',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: 14),
          Row(
            children: [
              _HeroMetric(label: 'Trending', value: '24+'),
              const SizedBox(width: 10),
              _HeroMetric(label: 'Creators', value: '120k'),
              const Spacer(),
              TextButton.icon(
                onPressed: onActionTap,
                style: TextButton.styleFrom(
                  foregroundColor: AppColors.textPrimary,
                  backgroundColor: AppColors.bgCard,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(999),
                  ),
                ),
                icon: const Icon(Icons.open_in_new, size: 16),
                label: const Text('Discover'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _HeroMetric extends StatelessWidget {
  const _HeroMetric({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            value,
            style: AppTextStyles.h3.copyWith(color: AppColors.textPrimary),
          ),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _SearchBar extends StatelessWidget {
  const _SearchBar({
    required this.controller,
    required this.onChanged,
    required this.onSubmitted,
    required this.onClear,
  });

  final TextEditingController controller;
  final ValueChanged<String> onChanged;
  final ValueChanged<String> onSubmitted;
  final VoidCallback onClear;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: TextField(
        controller: controller,
        onChanged: onChanged,
        onSubmitted: onSubmitted,
        style: AppTextStyles.body,
        textInputAction: TextInputAction.search,
        decoration: InputDecoration(
          border: InputBorder.none,
          hintText: 'Search people, tags, and posts',
          hintStyle: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
          prefixIcon: const Icon(Icons.search, color: AppColors.textMuted),
          suffixIcon: controller.text.isEmpty
              ? null
              : IconButton(
                  onPressed: onClear,
                  icon: const Icon(Icons.close, color: AppColors.textMuted),
                ),
          contentPadding: const EdgeInsets.symmetric(vertical: 12),
        ),
      ),
    );
  }
}

class _AutocompletePanel extends StatelessWidget {
  const _AutocompletePanel({
    required this.loading,
    required this.results,
    required this.onTapResult,
  });

  final bool loading;
  final List<Map<String, dynamic>> results;
  final ValueChanged<Map<String, dynamic>> onTapResult;

  @override
  Widget build(BuildContext context) {
    if (loading) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 8),
        child: LinearProgressIndicator(
          minHeight: 2,
          color: AppColors.postbookPrimary,
          backgroundColor: AppColors.bgCard,
        ),
      );
    }

    if (results.isEmpty) {
      return const SizedBox.shrink();
    }

    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: List.generate(results.length, (index) {
          final item = results[index];
          final displayName =
              (item['display_name'] ?? item['username'] ?? 'User').toString();
          final username = (item['username'] ?? 'user').toString();
          final initials = displayName.isEmpty
              ? 'U'
              : displayName.substring(0, 1).toUpperCase();

          return ListTile(
            dense: true,
            onTap: () => onTapResult(item),
            leading: CircleAvatar(
              radius: 16,
              backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
              child: Text(
                initials,
                style: AppTextStyles.label.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
            title: Text(displayName, style: AppTextStyles.label),
            subtitle: Text('@$username', style: AppTextStyles.labelSmall),
          );
        }),
      ),
    );
  }
}

class _MutedCard extends StatelessWidget {
  const _MutedCard({required this.message, this.onRetry});

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
          const Icon(
            Icons.insights_outlined,
            color: AppColors.textMuted,
            size: 20,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              message,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textSecondary,
              ),
            ),
          ),
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

class _ExploreAction {
  const _ExploreAction({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.route,
    required this.gradient,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final String route;
  final Gradient gradient;
}

class _QuickActionCard extends StatelessWidget {
  const _QuickActionCard({required this.action, required this.onTap});

  final _ExploreAction action;
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
                  gradient: action.gradient,
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Icon(action.icon, color: Colors.white, size: 20),
              ),
              const SizedBox(height: 12),
              Text(action.title, style: AppTextStyles.h3),
              const SizedBox(height: 2),
              Text(action.subtitle, style: AppTextStyles.labelSmall),
            ],
          ),
        ),
      ),
    );
  }
}

class _CreatorCard extends StatelessWidget {
  const _CreatorCard({
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
        width: 160,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                CircleAvatar(
                  radius: 18,
                  backgroundColor: AppColors.postbookPrimary.withValues(
                    alpha: 0.2,
                  ),
                  child: Text(
                    initials,
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                ),
                const Spacer(),
                Icon(
                  Icons.verified,
                  size: 16,
                  color: AppColors.posttubePrimary.withValues(alpha: 0.8),
                ),
              ],
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
            const SizedBox(height: 10),
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

class _ServicesRail extends StatelessWidget {
  const _ServicesRail({required this.onTap});

  final ValueChanged<ServiceApp> onTap;

  @override
  Widget build(BuildContext context) {
    final apps = ServiceRegistry.featured().take(8).toList();
    if (apps.isEmpty) return const SizedBox.shrink();
    return SizedBox(
      height: 102,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: apps.length,
        separatorBuilder: (_, _) => const SizedBox(width: 12),
        itemBuilder: (_, i) {
          final app = apps[i];
          return InkWell(
            onTap: () => onTap(app),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            child: SizedBox(
              width: 76,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Container(
                    width: 56,
                    height: 56,
                    decoration: BoxDecoration(
                      color: app.accentColor.withValues(alpha: 0.18),
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusLarge),
                      border: Border.all(
                        color: app.accentColor.withValues(alpha: 0.4),
                      ),
                    ),
                    alignment: Alignment.center,
                    child: Icon(
                      iconForServiceName(app.iconName),
                      color: app.accentColor,
                      size: 26,
                    ),
                  ),
                  const SizedBox(height: 6),
                  Text(
                    app.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    textAlign: TextAlign.center,
                    style: AppTextStyles.labelSmall.copyWith(
                      color: AppColors.textPrimary,
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}
