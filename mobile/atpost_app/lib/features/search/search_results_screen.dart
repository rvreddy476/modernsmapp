import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// ---------------------------------------------------------------------------
// Existing providers
// ---------------------------------------------------------------------------

final _searchPostsProvider =
    FutureProvider.autoDispose.family<List<Post>, String>((ref, query) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/search',
    queryParameters: {'q': query, 'type': 'posts', 'limit': 20},
  );
  final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
  return items.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();
});

final _searchUsersProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, query) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/search',
    queryParameters: {'q': query, 'type': 'users', 'limit': 20},
  );
  final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
  return items.map((e) => User.fromJson(e as Map<String, dynamic>)).toList();
});

final _searchTagsProvider =
    FutureProvider.autoDispose.family<List<String>, String>((ref, query) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get(
    '/v1/search',
    queryParameters: {'q': query, 'type': 'hashtags', 'limit': 20},
  );
  final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
  return items.map((e) {
    final m = e as Map<String, dynamic>;
    return m['tag']?.toString() ?? m['name']?.toString() ?? e.toString();
  }).toList();
});

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

  static const _tabs = [
    Tab(icon: Icon(Icons.article_outlined), text: 'Posts'),
    Tab(icon: Icon(Icons.people_outline), text: 'People'),
    Tab(icon: Icon(Icons.tag), text: 'Tags'),
    Tab(icon: Icon(Icons.shopping_bag_outlined), text: 'Products'),
    Tab(icon: Icon(Icons.event_outlined), text: 'Events'),
    Tab(icon: Icon(Icons.message_outlined), text: 'Messages'),
  ];

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


  @override
  Widget build(BuildContext context) {
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
          tabs: _tabs,
        ),
      ),
      body: Column(
        children: [
          // History panel shown when query is very short
          if (_query.trim().length <= 2)
            _SearchHistoryPanel(
              onQuerySelected: (q) {
                // Navigate to new search results
                context.push('/search/results?q=${Uri.encodeComponent(q)}');
              },
            ),
          Expanded(
            child: TabBarView(
              controller: _tabController,
              children: [
                // Posts tab
                _queryGuard(
                  ref.watch(_searchPostsProvider(_query)).when(
                        loading: () =>
                            const Center(child: CircularProgressIndicator()),
                        error: (_, _) => Center(
                          child: Text(
                            'Could not load posts',
                            style: AppTextStyles.bodySmall,
                          ),
                        ),
                        data: (posts) => posts.isEmpty
                            ? Center(
                                child: Text(
                                  'No posts found',
                                  style: AppTextStyles.bodySmall,
                                ),
                              )
                            : ListView.builder(
                                itemCount: posts.length,
                                itemBuilder: (_, i) =>
                                    _PostResult(post: posts[i]),
                              ),
                      ),
                ),
                // People tab
                _queryGuard(
                  ref.watch(_searchUsersProvider(_query)).when(
                        loading: () =>
                            const Center(child: CircularProgressIndicator()),
                        error: (_, _) => Center(
                          child: Text(
                            'Could not load people',
                            style: AppTextStyles.bodySmall,
                          ),
                        ),
                        data: (users) => users.isEmpty
                            ? Center(
                                child: Text(
                                  'No people found',
                                  style: AppTextStyles.bodySmall,
                                ),
                              )
                            : ListView.builder(
                                itemCount: users.length,
                                itemBuilder: (_, i) =>
                                    _UserResult(user: users[i]),
                              ),
                      ),
                ),
                // Tags tab
                _queryGuard(
                  ref.watch(_searchTagsProvider(_query)).when(
                        loading: () =>
                            const Center(child: CircularProgressIndicator()),
                        error: (_, _) => Center(
                          child: Text(
                            'Could not load tags',
                            style: AppTextStyles.bodySmall,
                          ),
                        ),
                        data: (tags) => tags.isEmpty
                            ? Center(
                                child: Text(
                                  'No tags found',
                                  style: AppTextStyles.bodySmall,
                                ),
                              )
                            : ListView.builder(
                                itemCount: tags.length,
                                itemBuilder: (_, i) =>
                                    _TagResult(tag: tags[i]),
                              ),
                      ),
                ),
                // Products tab
                _ProductsTab(query: _query),
                // Events tab
                _EventsTab(query: _query),
                // Messages tab
                _MessagesTab(query: _query),
              ],
            ),
          ),
        ],
      ),
    );
  }

  /// Wraps a widget: if query is too short, show a hint instead.
  Widget _queryGuard(Widget child) {
    if (_query.trim().length <= 2) {
      return Center(
        child: Text(
          'Enter at least 3 characters to search',
          style: AppTextStyles.bodySmall,
        ),
      );
    }
    return child;
  }
}

// ---------------------------------------------------------------------------
// Search History Panel
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
        _history = histItems.map((e) {
          final m = e as Map<String, dynamic>;
          return m['query']?.toString() ?? '';
        }).where((s) => s.isNotEmpty).toList();

        final savedItems =
            (savedRes.data['data']?['items'] as List<dynamic>?) ?? [];
        _saved = savedItems
            .map((e) => e as Map<String, dynamic>)
            .toList();

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
        setState(() => _saved.removeWhere((s) => s['id']?.toString() == savedId));
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
                  labelStyle: const TextStyle(color: AppColors.textSecondary),
                  deleteIcon:
                      const Icon(Icons.close, size: 14, color: AppColors.textMuted),
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
                  labelStyle: const TextStyle(color: AppColors.textSecondary),
                  deleteIcon: const Icon(Icons.delete_outline,
                      size: 14, color: AppColors.textMuted),
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

// ---------------------------------------------------------------------------
// Products tab
// ---------------------------------------------------------------------------

class _ProductsTab extends ConsumerWidget {
  const _ProductsTab({required this.query});

  final String query;

  String _formatPrice(dynamic price) {
    if (price == null) return '';
    final d = double.tryParse(price.toString()) ?? 0.0;
    return '\$${d.toStringAsFixed(2)}';
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

    return FutureBuilder<List<Map<String, dynamic>>>(
      future: ref
          .read(apiClientProvider)
          .get('/v1/search/products', queryParameters: {'q': query})
          .then((r) {
        final items = (r.data['data']?['items'] as List<dynamic>?) ?? [];
        return items.map((e) => e as Map<String, dynamic>).toList();
      }),
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return const Center(child: Text('Error loading results'));
        }
        final products = snapshot.data ?? [];
        if (products.isEmpty) {
          return Center(
            child: Text(
              'No products found for "$query"',
              style: AppTextStyles.bodySmall,
            ),
          );
        }
        return ListView.builder(
          itemCount: products.length,
          itemBuilder: (context, i) {
            final p = products[i];
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
                  p['name']?.toString() ?? 'Product',
                  style: AppTextStyles.h3,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                subtitle: Text(
                  p['description']?.toString() ?? '',
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.bodySmall,
                ),
                trailing: Text(
                  _formatPrice(p['price']),
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
            );
          },
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Events tab
// ---------------------------------------------------------------------------

class _EventsTab extends ConsumerWidget {
  const _EventsTab({required this.query});

  final String query;

  String _formatTime(dynamic raw) {
    if (raw == null) return '';
    try {
      final dt = DateTime.parse(raw.toString()).toLocal();
      return '${dt.year}-${dt.month.toString().padLeft(2, '0')}-${dt.day.toString().padLeft(2, '0')} '
          '${dt.hour.toString().padLeft(2, '0')}:${dt.minute.toString().padLeft(2, '0')}';
    } catch (_) {
      return raw.toString();
    }
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

    return FutureBuilder<List<Map<String, dynamic>>>(
      future: ref
          .read(apiClientProvider)
          .get('/v1/search/events', queryParameters: {'q': query})
          .then((r) {
        final items = (r.data['data']?['items'] as List<dynamic>?) ?? [];
        return items.map((e) => e as Map<String, dynamic>).toList();
      }),
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return const Center(child: Text('Error loading results'));
        }
        final events = snapshot.data ?? [];
        if (events.isEmpty) {
          return Center(
            child: Text(
              'No events found for "$query"',
              style: AppTextStyles.bodySmall,
            ),
          );
        }
        return ListView.builder(
          itemCount: events.length,
          itemBuilder: (context, i) {
            final e = events[i];
            final location = e['location']?.toString();
            final subtitle = [
              if (e['description']?.toString().isNotEmpty == true)
                e['description']!.toString(),
              _formatTime(e['start_time']),
              if (location != null && location.isNotEmpty) location,
            ].join(' · ');
            return Card(
              color: AppColors.bgCard,
              margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
                side: const BorderSide(color: AppColors.borderSubtle),
              ),
              child: ListTile(
                leading: const Icon(
                  Icons.event_outlined,
                  color: AppColors.posttubePrimary,
                ),
                title: Text(
                  e['title']?.toString() ?? 'Event',
                  style: AppTextStyles.h3,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                subtitle: Text(
                  subtitle,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.bodySmall,
                ),
              ),
            );
          },
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Messages tab
// ---------------------------------------------------------------------------

class _MessagesTab extends ConsumerWidget {
  const _MessagesTab({required this.query});

  final String query;

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

    return FutureBuilder<List<Map<String, dynamic>>>(
      future: ref
          .read(apiClientProvider)
          .get('/v1/search/messages', queryParameters: {'q': query})
          .then((r) {
        final items = (r.data['data']?['items'] as List<dynamic>?) ?? [];
        return items.map((e) => e as Map<String, dynamic>).toList();
      }),
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return const Center(child: Text('Error loading results'));
        }
        final messages = snapshot.data ?? [];
        if (messages.isEmpty) {
          return Center(
            child: Text(
              'No messages found for "$query"',
              style: AppTextStyles.bodySmall,
            ),
          );
        }
        return ListView.builder(
          itemCount: messages.length,
          itemBuilder: (context, i) {
            final m = messages[i];
            final conversationId = m['conversation_id']?.toString() ?? '';
            return Card(
              color: AppColors.bgCard,
              margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
                side: const BorderSide(color: AppColors.borderSubtle),
              ),
              child: ListTile(
                leading: const Icon(
                  Icons.message_outlined,
                  color: AppColors.accentPurple,
                ),
                title: Text(
                  m['content']?.toString() ?? '',
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.bodySmall,
                ),
                trailing: conversationId.isNotEmpty
                    ? TextButton(
                        onPressed: () =>
                            context.push('/chat/$conversationId'),
                        child: Text(
                          'View',
                          style: AppTextStyles.labelSmall.copyWith(
                            color: AppColors.postbookPrimary,
                          ),
                        ),
                      )
                    : null,
              ),
            );
          },
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Existing result widgets (unchanged)
// ---------------------------------------------------------------------------

class _PostResult extends StatelessWidget {
  const _PostResult({required this.post});

  final Post post;

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
        title: Text(post.authorName ?? 'User', style: AppTextStyles.h3),
        subtitle: Text(
          post.content,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.bodySmall,
        ),
        trailing: Text(
          '${post.likeCount} likes',
          style: AppTextStyles.labelSmall,
        ),
      ),
    );
  }
}

class _UserResult extends StatefulWidget {
  const _UserResult({required this.user});

  final User user;

  @override
  State<_UserResult> createState() => _UserResultState();
}

class _UserResultState extends State<_UserResult> {
  bool _following = false;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.25),
        child: Text(
          widget.user.displayName.isNotEmpty
              ? widget.user.displayName[0].toUpperCase()
              : 'U',
          style: AppTextStyles.label.copyWith(color: AppColors.postbookPrimary),
        ),
      ),
      title: Text(widget.user.displayName, style: AppTextStyles.h3),
      subtitle: Text('@${widget.user.username}', style: AppTextStyles.bodySmall),
      trailing: GestureDetector(
        onTap: () => setState(() => _following = !_following),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            gradient: _following ? null : AppColors.postbookGradient,
            color: _following ? AppColors.bgCard : null,
            borderRadius: BorderRadius.circular(20),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Text(
            _following ? 'Following' : 'Follow',
            style: AppTextStyles.labelSmall.copyWith(
              color: _following ? AppColors.textSecondary : Colors.white,
            ),
          ),
        ),
      ),
      onTap: () => context.push('/profile/${widget.user.id}'),
    );
  }
}

class _TagResult extends StatelessWidget {
  const _TagResult({required this.tag});

  final String tag;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: const Icon(Icons.tag, color: AppColors.postbookPrimary),
      title: Text('#$tag', style: AppTextStyles.h3),
      onTap: () => context.push(
        '/search/results?q=${Uri.encodeComponent('#$tag')}',
      ),
    );
  }
}
