import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

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

class SearchResultsScreen extends ConsumerStatefulWidget {
  const SearchResultsScreen({super.key, required this.query});

  final String query;

  @override
  ConsumerState<SearchResultsScreen> createState() =>
      _SearchResultsScreenState();
}

class _SearchResultsScreenState extends ConsumerState<SearchResultsScreen> {
  late final String _query = widget.query;

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 3,
      child: Scaffold(
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
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textDim,
            indicatorColor: AppColors.postbookPrimary,
            labelStyle: AppTextStyles.label,
            tabs: const [
              Tab(text: 'Posts'),
              Tab(text: 'People'),
              Tab(text: 'Tags'),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            // Posts tab
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
                          itemBuilder: (_, i) => _PostResult(post: posts[i]),
                        ),
                ),
            // People tab
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
                          itemBuilder: (_, i) => _UserResult(user: users[i]),
                        ),
                ),
            // Tags tab
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
                          itemBuilder: (_, i) => _TagResult(tag: tags[i]),
                        ),
                ),
          ],
        ),
      ),
    );
  }
}

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
