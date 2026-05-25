// Multi-entity search results screen — consumes the search-service
// ranked API via `multiEntitySearchControllerProvider`. Six tabs:
// Posts, People, Hashtags, Products, Communities, Channels. Each tab
// has its own "Show more" button driven by the per-bucket
// `next_cursor`. Every result tap fires a fire-and-forget
// /v1/search/click for analytics.
//
// The legacy `_searchPostsProvider` / `_searchUsersProvider` /
// `_searchTagsProvider` hooks (and the per-tab Events / Messages
// FutureBuilders) were retired in favor of the unified ranked
// response; Events + Messages aren't part of the multi-entity API
// (they're separate endpoints) so they're dropped from this screen
// for now — the dedicated repository methods remain available on
// `SearchExtrasRepository` for any caller that still needs them.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/search_results.dart';
import 'package:atpost_app/providers/search_providers.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

class SearchResultsScreen extends ConsumerStatefulWidget {
  const SearchResultsScreen({super.key, required this.query});

  final String query;

  @override
  ConsumerState<SearchResultsScreen> createState() =>
      _SearchResultsScreenState();
}

class _SearchResultsScreenState extends ConsumerState<SearchResultsScreen>
    with SingleTickerProviderStateMixin {
  late final String _query = widget.query;
  late final TabController _tabController;

  // Tab order matches SearchEntity.values; default tab is picked once
  // the first response lands (whichever bucket has the most hits).
  static const _tabs = <SearchEntity>[
    SearchEntity.posts,
    SearchEntity.users,
    SearchEntity.hashtags,
    SearchEntity.products,
    SearchEntity.communities,
    SearchEntity.channels,
  ];

  bool _defaultTabPicked = false;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: _tabs.length, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  void _maybePickDefaultTab(MultiEntitySearchResults r) {
    if (_defaultTabPicked) return;
    _defaultTabPicked = true;
    final counts = <SearchEntity, int>{
      SearchEntity.posts: r.posts.items.length,
      SearchEntity.users: r.users.items.length,
      SearchEntity.hashtags: r.hashtags.items.length,
      SearchEntity.products: r.products.items.length,
      SearchEntity.communities: r.communities.items.length,
      SearchEntity.channels: r.channels.items.length,
    };
    // Pick the bucket with the most hits; ties keep the default order.
    SearchEntity best = SearchEntity.posts;
    int bestN = -1;
    for (final e in _tabs) {
      final n = counts[e] ?? 0;
      if (n > bestN) {
        bestN = n;
        best = e;
      }
    }
    if (bestN > 0) {
      final idx = _tabs.indexOf(best);
      if (idx >= 0 && idx != _tabController.index) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _tabController.animateTo(idx);
        });
      }
    }
  }

  EntityBucket bucketFor(MultiEntitySearchResults r, SearchEntity e) {
    switch (e) {
      case SearchEntity.posts:
        return r.posts;
      case SearchEntity.users:
        return r.users;
      case SearchEntity.hashtags:
        return r.hashtags;
      case SearchEntity.products:
        return r.products;
      case SearchEntity.communities:
        return r.communities;
      case SearchEntity.channels:
        return r.channels;
    }
  }

  @override
  Widget build(BuildContext context) {
    final asyncResults =
        ref.watch(multiEntitySearchControllerProvider(_query));

    if (asyncResults is AsyncData<MultiEntitySearchResults>) {
      _maybePickDefaultTab(asyncResults.value);
    }

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: Text('"$_query"', style: AppTextStyles.h3),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          color: AppColors.textSecondary,
          onPressed: () => context.pop(),
        ),
        bottom: TabBar(
          controller: _tabController,
          labelColor: AppColors.postbookPrimary,
          unselectedLabelColor: AppColors.textDim,
          indicatorColor: AppColors.postbookPrimary,
          labelStyle: AppTextStyles.label,
          isScrollable: true,
          tabAlignment: TabAlignment.start,
          tabs: _tabs
              .map((e) => Tab(icon: Icon(_iconFor(e)), text: e.label))
              .toList(growable: false),
        ),
      ),
      body: Column(
        children: [
          if (_query.trim().length <= 2)
            _SearchHistoryPanel(
              onQuerySelected: (q) {
                context.push('/search/results?q=${Uri.encodeComponent(q)}');
              },
            ),
          Expanded(
            child: asyncResults.when(
              loading: () =>
                  const Center(child: CircularProgressIndicator()),
              error: (_, _) => Center(
                child: Text(
                  'Could not load results',
                  style: AppTextStyles.bodySmall,
                ),
              ),
              data: (results) => TabBarView(
                controller: _tabController,
                children: _tabs.map((entity) {
                  return _EntityTabContent(
                    query: _query,
                    entity: entity,
                    bucket: bucketFor(results, entity),
                    queryId: results.queryId,
                  );
                }).toList(growable: false),
              ),
            ),
          ),
        ],
      ),
    );
  }

  static IconData _iconFor(SearchEntity e) {
    switch (e) {
      case SearchEntity.posts:
        return Icons.article_outlined;
      case SearchEntity.users:
        return Icons.people_outline;
      case SearchEntity.hashtags:
        return Icons.tag;
      case SearchEntity.products:
        return Icons.shopping_bag_outlined;
      case SearchEntity.communities:
        return Icons.groups_outlined;
      case SearchEntity.channels:
        return Icons.podcasts_outlined;
    }
  }
}

// ---------------------------------------------------------------------------
// Per-entity tab content
// ---------------------------------------------------------------------------

class _EntityTabContent extends ConsumerWidget {
  const _EntityTabContent({
    required this.query,
    required this.entity,
    required this.bucket,
    required this.queryId,
  });

  final String query;
  final SearchEntity entity;
  final EntityBucket bucket;
  final String? queryId;

  void _onTap(WidgetRef ref, String entityId, int position) {
    if (queryId == null || queryId!.isEmpty) return;
    // Fire-and-forget click analytics; do not await.
    ref.read(searchClickLoggerProvider).call(
          queryId: queryId!,
          entityType: entity,
          entityId: entityId,
          position: position,
        );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (query.trim().length <= 2) {
      return Center(
        child: Text(
          'Enter at least 3 characters to search',
          style: AppTextStyles.bodySmall,
        ),
      );
    }

    final items = bucket.items;
    if (items.isEmpty) {
      return Center(
        child: Text(
          'No ${entity.label.toLowerCase()} found for "$query"',
          style: AppTextStyles.bodySmall,
        ),
      );
    }

    final controller =
        ref.read(multiEntitySearchControllerProvider(query).notifier);
    final isLoadingMore = controller.isLoadingMore(entity);
    final hasMore = (bucket.nextCursor ?? '').isNotEmpty;

    return ListView.builder(
      itemCount: items.length + (hasMore ? 1 : 0),
      itemBuilder: (context, i) {
        if (i == items.length) {
          return Padding(
            padding: const EdgeInsets.all(12),
            child: Center(
              child: isLoadingMore
                  ? const CircularProgressIndicator()
                  : OutlinedButton(
                      style: OutlinedButton.styleFrom(
                        side: const BorderSide(color: AppColors.borderSubtle),
                        foregroundColor: AppColors.textPrimary,
                      ),
                      onPressed: () => controller.loadMore(entity),
                      child: Text('Show more', style: AppTextStyles.label),
                    ),
            ),
          );
        }
        final item = items[i];
        return _buildRow(context, ref, item, i);
      },
    );
  }

  Widget _buildRow(
    BuildContext context,
    WidgetRef ref,
    dynamic item,
    int position,
  ) {
    switch (entity) {
      case SearchEntity.posts:
        return _PostRow(post: item as PostHit, onTap: () {
          _onTap(ref, (item).postId, position);
        });
      case SearchEntity.users:
        return _UserRow(
          user: item as UserHit,
          onTap: () {
            _onTap(ref, (item).userId, position);
            context.push('/profile/${(item).userId}');
          },
        );
      case SearchEntity.hashtags:
        return _HashtagRow(
          hashtag: item as HashtagHit,
          onTap: () {
            _onTap(ref, (item).hashtag, position);
            context.push(
              '/hashtag/${Uri.encodeComponent((item).hashtag)}',
            );
          },
        );
      case SearchEntity.products:
        return _ProductRow(
          product: item as ProductHit,
          onTap: () {
            _onTap(ref, (item).productId, position);
            context.push('/commerce/product/${(item).productId}');
          },
        );
      case SearchEntity.communities:
        return _CommunityRow(
          community: item as CommunityHit,
          onTap: () {
            _onTap(ref, (item).communityId, position);
          },
        );
      case SearchEntity.channels:
        return _ChannelRow(
          channel: item as ChannelHit,
          onTap: () {
            _onTap(ref, (item).channelId, position);
          },
        );
    }
  }
}

// ---------------------------------------------------------------------------
// Row widgets
// ---------------------------------------------------------------------------

class _PostRow extends StatelessWidget {
  const _PostRow({required this.post, required this.onTap});

  final PostHit post;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        title: Text(
          post.authorUsername != null ? '@${post.authorUsername}' : 'Post',
          style: AppTextStyles.h3,
        ),
        subtitle: Text(
          post.text,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.bodySmall,
        ),
        trailing: Text(
          '${post.likeCount} likes',
          style: AppTextStyles.labelSmall,
        ),
        onTap: onTap,
      ),
    );
  }
}

class _UserRow extends StatelessWidget {
  const _UserRow({required this.user, required this.onTap});

  final UserHit user;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: CircleAvatar(
        backgroundColor:
            AppColors.postbookPrimary.withValues(alpha: 0.25),
        child: Text(
          user.displayName.isNotEmpty
              ? user.displayName[0].toUpperCase()
              : 'U',
          style:
              AppTextStyles.label.copyWith(color: AppColors.postbookPrimary),
        ),
      ),
      title: Row(
        children: [
          Flexible(
            child: Text(
              user.displayName,
              style: AppTextStyles.h3,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (user.isVerified) ...[
            const SizedBox(width: 4),
            const Icon(Icons.verified, size: 14, color: Colors.blue),
          ],
        ],
      ),
      subtitle: Text('@${user.username}', style: AppTextStyles.bodySmall),
      onTap: onTap,
    );
  }
}

class _HashtagRow extends StatelessWidget {
  const _HashtagRow({required this.hashtag, required this.onTap});

  final HashtagHit hashtag;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: const Icon(Icons.tag, color: AppColors.postbookPrimary),
      title: Text('#${hashtag.hashtag}', style: AppTextStyles.h3),
      subtitle: Text(
        '${hashtag.useCount} posts',
        style: AppTextStyles.labelSmall,
      ),
      onTap: onTap,
    );
  }
}

class _ProductRow extends StatelessWidget {
  const _ProductRow({required this.product, required this.onTap});

  final ProductHit product;
  final VoidCallback onTap;

  String _formatPrice(double? p) {
    if (p == null || p <= 0) return '';
    return '\$${p.toStringAsFixed(2)}';
  }

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        leading: const Icon(
          Icons.shopping_bag_outlined,
          color: AppColors.postbookPrimary,
        ),
        title: Text(
          product.title,
          style: AppTextStyles.h3,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        subtitle: Text(
          product.description ?? '',
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.bodySmall,
        ),
        trailing: Text(
          _formatPrice(product.price),
          style: AppTextStyles.label
              .copyWith(color: AppColors.postbookPrimary),
        ),
        onTap: onTap,
      ),
    );
  }
}

class _CommunityRow extends StatelessWidget {
  const _CommunityRow({required this.community, required this.onTap});

  final CommunityHit community;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        leading: const Icon(
          Icons.groups_outlined,
          color: AppColors.postbookPrimary,
        ),
        title: Row(
          children: [
            Flexible(
              child: Text(
                community.name,
                style: AppTextStyles.h3,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (community.isVerified) ...[
              const SizedBox(width: 4),
              const Icon(Icons.verified, size: 14, color: Colors.blue),
            ],
          ],
        ),
        subtitle: Text(
          '@${community.handle} · ${community.memberCount} members',
          style: AppTextStyles.bodySmall,
        ),
        onTap: onTap,
      ),
    );
  }
}

class _ChannelRow extends StatelessWidget {
  const _ChannelRow({required this.channel, required this.onTap});

  final ChannelHit channel;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        leading: const Icon(
          Icons.podcasts_outlined,
          color: AppColors.postbookPrimary,
        ),
        title: Row(
          children: [
            Flexible(
              child: Text(
                channel.name,
                style: AppTextStyles.h3,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (channel.isVerified) ...[
              const SizedBox(width: 4),
              const Icon(Icons.verified, size: 14, color: Colors.blue),
            ],
          ],
        ),
        subtitle: Text(
          '@${channel.handle} · ${channel.subscriberCount} subscribers',
          style: AppTextStyles.bodySmall,
        ),
        onTap: onTap,
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Search History Panel (kept from the previous implementation, unchanged
// shape — wired to the same /v1/search/history + /v1/search/saved
// endpoints via the raw api client).
// ---------------------------------------------------------------------------

class _SearchHistoryPanel extends ConsumerStatefulWidget {
  const _SearchHistoryPanel({required this.onQuerySelected});

  final ValueChanged<String> onQuerySelected;

  @override
  ConsumerState<_SearchHistoryPanel> createState() =>
      _SearchHistoryPanelState();
}

class _SearchHistoryPanelState extends ConsumerState<_SearchHistoryPanel> {
  List<String> _history = [];
  List<Map<String, dynamic>> _saved = [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final api = ref.read(apiClientProvider);
      final histRes = await api.get('/v1/search/history');
      final savedRes = await api.get('/v1/search/saved');

      if (!mounted) return;
      setState(() {
        final histItems =
            (histRes.data['data']?['items'] as List<dynamic>?) ?? [];
        _history = histItems
            .map((e) {
              final m = e as Map<String, dynamic>;
              return m['query']?.toString() ?? '';
            })
            .where((s) => s.isNotEmpty)
            .toList();

        final savedItems =
            (savedRes.data['data']?['items'] as List<dynamic>?) ?? [];
        _saved =
            savedItems.map((e) => e as Map<String, dynamic>).toList();

        _loading = false;
      });
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _clearHistory() async {
    try {
      await ref.read(apiClientProvider).delete('/v1/search/history');
      if (mounted) setState(() => _history = []);
    } catch (_) {}
  }

  Future<void> _deleteSaved(String savedId) async {
    try {
      await ref.read(apiClientProvider).delete('/v1/search/saved/$savedId');
      if (mounted) {
        setState(() =>
            _saved.removeWhere((s) => s['id']?.toString() == savedId));
      }
    } catch (_) {}
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Padding(
        padding: EdgeInsets.all(16),
        child: Center(child: CircularProgressIndicator()),
      );
    }

    if (_history.isEmpty && _saved.isEmpty) return const SizedBox.shrink();

    return Container(
      color: AppColors.bgSecondary,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (_history.isNotEmpty) ...[
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Recent Searches', style: AppTextStyles.label),
                TextButton(
                  onPressed: _clearHistory,
                  child: Text(
                    'Clear all',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: AppColors.postgramPrimary,
                    ),
                  ),
                ),
              ],
            ),
            Wrap(
              spacing: 8,
              runSpacing: 4,
              children: _history.map((q) {
                return InputChip(
                  label: Text(q, style: AppTextStyles.labelSmall),
                  backgroundColor: AppColors.bgTertiary,
                  side: const BorderSide(color: AppColors.borderSubtle),
                  labelStyle:
                      const TextStyle(color: AppColors.textSecondary),
                  deleteIcon: const Icon(
                    Icons.close,
                    size: 14,
                    color: AppColors.textMuted,
                  ),
                  onDeleted: () => setState(() => _history.remove(q)),
                  onPressed: () => widget.onQuerySelected(q),
                );
              }).toList(),
            ),
            const SizedBox(height: 12),
          ],
          if (_saved.isNotEmpty) ...[
            Text('Saved Searches', style: AppTextStyles.label),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 4,
              children: _saved.map((s) {
                final query = s['query']?.toString() ?? '';
                final id = s['id']?.toString() ?? '';
                return InputChip(
                  label: Text(query, style: AppTextStyles.labelSmall),
                  backgroundColor: AppColors.bgTertiary,
                  side: const BorderSide(color: AppColors.borderSubtle),
                  labelStyle:
                      const TextStyle(color: AppColors.textSecondary),
                  deleteIcon: const Icon(
                    Icons.delete_outline,
                    size: 14,
                    color: AppColors.textMuted,
                  ),
                  onDeleted: id.isNotEmpty ? () => _deleteSaved(id) : null,
                  onPressed: () => widget.onQuerySelected(query),
                );
              }).toList(),
            ),
          ],
        ],
      ),
    );
  }
}
