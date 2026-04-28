import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// All of the current user's posts in a 3-column grid sorted newest-first.
/// Hits `/v1/posts/by-author/:userId?limit=N` (already used by ProfileNotifier
/// and the web app), then sorts client-side by `createdAt desc` as a guard
/// in case the backend returns mixed order.
class MyMediaScreen extends ConsumerStatefulWidget {
  const MyMediaScreen({super.key});

  @override
  ConsumerState<MyMediaScreen> createState() => _MyMediaScreenState();
}

class _MyMediaScreenState extends ConsumerState<MyMediaScreen> {
  final _scroll = ScrollController();

  static const _pageSize = 30;

  final List<Post> _posts = [];
  bool _loading = false;
  bool _hasMore = true;
  bool _initialised = false;
  String? _error;
  String? _userId;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scroll
      ..removeListener(_onScroll)
      ..dispose();
    super.dispose();
  }

  void _onScroll() {
    if (!_hasMore || _loading) return;
    if (_scroll.position.pixels >= _scroll.position.maxScrollExtent - 400) {
      _loadMore();
    }
  }

  Future<void> _ensureInit() async {
    if (_initialised) return;
    _initialised = true;
    try {
      final me = await ref.read(currentUserProvider.future);
      _userId = me.id;
      await _loadMore();
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
    }
  }

  Future<void> _loadMore() async {
    if (_loading || !_hasMore || _userId == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final api = ref.read(apiClientProvider);
      final response = await api.get(
        '/v1/posts/by-author/$_userId',
        queryParameters: {
          'limit': _pageSize,
          'offset': _posts.length,
        },
      );
      final raw = response.data['data'];
      final List items;
      if (raw is List) {
        items = raw;
      } else if (raw is Map && raw['items'] is List) {
        items = raw['items'] as List;
      } else {
        items = const [];
      }

      final newPosts = items
          .map((e) => Post.fromJson(e as Map<String, dynamic>))
          .toList();

      if (!mounted) return;
      setState(() {
        _posts.addAll(newPosts);
        _posts.sort((a, b) => b.createdAt.compareTo(a.createdAt));
        _hasMore = newPosts.length >= _pageSize;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e.toString();
      });
    }
  }

  Future<void> _refresh() async {
    setState(() {
      _posts.clear();
      _hasMore = true;
      _initialised = false;
    });
    await _ensureInit();
  }

  @override
  Widget build(BuildContext context) {
    // Kick off the first load on first build.
    if (!_initialised) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _ensureInit());
    }

    return Scaffold(
      backgroundColor: const Color(0xFF111111),
      appBar: AppBar(
        backgroundColor: const Color(0xFF111111),
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded, color: Colors.white),
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/'),
        ),
        title: Text(
          'My Media',
          style: AppTextStyles.h2.copyWith(color: Colors.white),
        ),
        centerTitle: false,
      ),
      body: RefreshIndicator(
        color: AppColors.postbookPrimary,
        backgroundColor: const Color(0xFF1C1C1C),
        onRefresh: _refresh,
        child: _buildBody(),
      ),
    );
  }

  Widget _buildBody() {
    if (_error != null && _posts.isEmpty) {
      return _ErrorView(message: _error!, onRetry: _refresh);
    }
    if (!_loading && _posts.isEmpty && _initialised) {
      return _EmptyView(onRefresh: _refresh);
    }
    if (_posts.isEmpty) {
      return const Center(
        child: CircularProgressIndicator(
          color: AppColors.postbookPrimary,
        ),
      );
    }

    final groups = _groupByMonth(_posts);

    return CustomScrollView(
      controller: _scroll,
      physics: const AlwaysScrollableScrollPhysics(),
      slivers: [
        for (final group in groups) ...[
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(16, 18, 16, 8),
              child: Text(
                group.label,
                style: const TextStyle(
                  color: Color(0xFFAAAAAA),
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  letterSpacing: 0.6,
                ),
              ),
            ),
          ),
          SliverPadding(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            sliver: SliverGrid(
              gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: 3,
                mainAxisSpacing: 4,
                crossAxisSpacing: 4,
                childAspectRatio: 1,
              ),
              delegate: SliverChildBuilderDelegate(
                (context, i) => _PostTile(post: group.posts[i]),
                childCount: group.posts.length,
              ),
            ),
          ),
        ],
        if (_loading && _posts.isNotEmpty)
          const SliverToBoxAdapter(
            child: Padding(
              padding: EdgeInsets.symmetric(vertical: 24),
              child: Center(
                child: SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
            ),
          )
        else if (!_hasMore)
          const SliverToBoxAdapter(
            child: Padding(
              padding: EdgeInsets.symmetric(vertical: 24),
              child: Center(
                child: Text(
                  "You're all caught up.",
                  style:
                      TextStyle(color: Color(0xFF555555), fontSize: 12),
                ),
              ),
            ),
          ),
        const SliverToBoxAdapter(child: SizedBox(height: 32)),
      ],
    );
  }

  List<_MonthGroup> _groupByMonth(List<Post> posts) {
    final out = <_MonthGroup>[];
    String? current;
    final now = DateTime.now();
    for (final p in posts) {
      final label = _monthLabel(p.createdAt, now);
      if (label != current) {
        current = label;
        out.add(_MonthGroup(label, []));
      }
      out.last.posts.add(p);
    }
    return out;
  }

  static const _months = [
    'January', 'February', 'March', 'April', 'May', 'June',
    'July', 'August', 'September', 'October', 'November', 'December',
  ];

  String _monthLabel(DateTime d, DateTime now) {
    if (d.year == now.year && d.month == now.month) return 'This month';
    if (d.year == now.year) return _months[d.month - 1];
    return '${_months[d.month - 1]} ${d.year}';
  }
}

class _MonthGroup {
  _MonthGroup(this.label, this.posts);
  final String label;
  final List<Post> posts;
}

class _PostTile extends StatelessWidget {
  const _PostTile({required this.post});

  final Post post;

  @override
  Widget build(BuildContext context) {
    final hasMedia = post.mediaIds.isNotEmpty;
    final mediaUrl =
        hasMedia ? '${Environment.apiBaseUrl}${post.firstMediaUrl}' : null;

    return GestureDetector(
      onTap: () => context.push('/comments/${post.id}'),
      child: Stack(
        fit: StackFit.expand,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(8),
            child: Container(
              color: const Color(0xFF1A1A1A),
              child: hasMedia
                  ? Image.network(
                      mediaUrl!,
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) => _textTilePreview(),
                      loadingBuilder: (_, child, prog) => prog == null
                          ? child
                          : Center(
                              child: SizedBox(
                                width: 18,
                                height: 18,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  color: AppColors.postbookPrimary
                                      .withValues(alpha: 0.7),
                                ),
                              ),
                            ),
                    )
                  : _textTilePreview(),
            ),
          ),
          if (post.isVideo || post.isReel)
            const Positioned(
              top: 6,
              right: 6,
              child: Icon(Icons.play_circle_fill,
                  color: Colors.white, size: 18),
            ),
          if (post.mediaIds.length > 1)
            const Positioned(
              top: 6,
              right: 6,
              child: Icon(Icons.collections_rounded,
                  color: Colors.white, size: 16),
            ),
        ],
      ),
    );
  }

  Widget _textTilePreview() {
    final preview = post.content.trim();
    return Container(
      padding: const EdgeInsets.all(8),
      alignment: Alignment.center,
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0xFF1F1F2E), Color(0xFF131320)],
        ),
      ),
      child: preview.isEmpty
          ? const Icon(Icons.image_outlined,
              color: Color(0xFF333333), size: 22)
          : Text(
              preview,
              maxLines: 4,
              overflow: TextOverflow.ellipsis,
              textAlign: TextAlign.center,
              style: const TextStyle(
                color: Color(0xFFCCCCCC),
                fontSize: 11,
                height: 1.3,
              ),
            ),
    );
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView({required this.onRefresh});
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [
        const SizedBox(height: 120),
        Center(
          child: Container(
            width: 72,
            height: 72,
            decoration: BoxDecoration(
              color: AppColors.postbookPrimary.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(20),
            ),
            child: const Icon(
              Icons.photo_library_outlined,
              color: AppColors.postbookPrimary,
              size: 32,
            ),
          ),
        ),
        const SizedBox(height: 16),
        const Center(
          child: Text(
            'No posts yet',
            style: TextStyle(
              color: Colors.white,
              fontSize: 16,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
        const SizedBox(height: 6),
        const Center(
          child: Padding(
            padding: EdgeInsets.symmetric(horizontal: 32),
            child: Text(
              'Posts and reels you create will show up here, sorted by newest first.',
              textAlign: TextAlign.center,
              style: TextStyle(
                color: Color(0xFF888888),
                fontSize: 13,
                height: 1.5,
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.message, required this.onRetry});
  final String message;
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) {
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      children: [
        const SizedBox(height: 100),
        const Center(
          child: Icon(Icons.error_outline,
              color: Colors.redAccent, size: 40),
        ),
        const SizedBox(height: 12),
        const Center(
          child: Text("Couldn't load your media",
              style: TextStyle(color: Colors.white)),
        ),
        const SizedBox(height: 16),
        Center(
          child: TextButton(
            onPressed: onRetry,
            child: const Text('Retry'),
          ),
        ),
      ],
    );
  }
}
