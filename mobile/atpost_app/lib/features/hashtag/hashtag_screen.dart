import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/shared/widgets/content_cards.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Read-only feed of posts tagged with a given hashtag.
/// Hits GET /v1/hashtags/:tag/posts (post-service) and renders results
/// using the same [PostCard] widget as the home feed for visual consistency.
class HashtagScreen extends ConsumerStatefulWidget {
  const HashtagScreen({super.key, required this.tag});
  final String tag;

  @override
  ConsumerState<HashtagScreen> createState() => _HashtagScreenState();
}

class _HashtagScreenState extends ConsumerState<HashtagScreen> {
  late Future<List<Post>> _future;

  @override
  void initState() {
    super.initState();
    _future = _load();
  }

  String get _normalizedTag => widget.tag.replaceAll('#', '').trim();

  Future<List<Post>> _load() async {
    final api = ref.read(apiClientProvider);
    final response = await api.get('/v1/hashtags/$_normalizedTag/posts');
    final data = response.data['data'];
    final List<dynamic> raw = data is List
        ? data
        : (data is Map && data['items'] is List)
            ? data['items'] as List
            : <dynamic>[];
    final posts =
        raw.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();

    // Hydrate authors using batch profiles - mirrors FeedRepository pattern.
    final ids = <String>{
      for (final p in posts)
        if ((p.authorName ?? '').trim().isEmpty && p.authorId.isNotEmpty)
          p.authorId,
    };
    if (ids.isEmpty) return posts;
    try {
      final users = await ref.read(userRepositoryProvider).getUsersBatch(ids.toList());
      final byId = <String, User>{for (final u in users) u.id: u};
      return posts
          .map(
            (p) {
              final u = byId[p.authorId];
              if (u == null) return p;
              return p.copyWith(
                authorName: (p.authorName?.trim().isNotEmpty ?? false)
                    ? p.authorName
                    : u.displayName,
                authorAvatar: (p.authorAvatar?.trim().isNotEmpty ?? false)
                    ? p.authorAvatar
                    : u.avatarUrl,
              );
            },
          )
          .toList();
    } catch (_) {
      return posts;
    }
  }

  Future<void> _refresh() async {
    final next = _load();
    setState(() => _future = next);
    await next;
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded),
          onPressed: () => context.pop(),
        ),
        title: Row(
          children: [
            const Icon(
              Icons.tag_rounded,
              color: AppColors.accentPurple,
              size: 22,
            ),
            const SizedBox(width: 6),
            Flexible(
              child: Text(
                _normalizedTag,
                style: AppTextStyles.h2,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
      body: RefreshIndicator(
        color: AppColors.accentPurple,
        backgroundColor: AppColors.bgSecondary,
        onRefresh: _refresh,
        child: FutureBuilder<List<Post>>(
          future: _future,
          builder: (context, snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError) {
              return ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.symmetric(vertical: 60),
                children: [
                  Center(
                    child: Text(
                      'Could not load #$_normalizedTag',
                      style: AppTextStyles.body.copyWith(
                        color: AppColors.statusError,
                      ),
                    ),
                  ),
                ],
              );
            }
            final posts = snapshot.data ?? const [];
            if (posts.isEmpty) {
              return ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.symmetric(vertical: 60),
                children: [
                  Center(
                    child: Text(
                      'No posts tagged #$_normalizedTag yet.',
                      style: AppTextStyles.body.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                  ),
                ],
              );
            }
            return ListView.builder(
              padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 130),
              physics: const AlwaysScrollableScrollPhysics(),
              itemCount: posts.length,
              itemBuilder: (context, index) => Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: PostCard(post: posts[index]),
              ),
            );
          },
        ),
      ),
    );
  }
}
