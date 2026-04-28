import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Per-user channel page: avatar, subscribe toggle, then a grid of every
/// long_video the creator has published. Mirrors the web `/posttube/channel/[handle]`
/// route — same backend endpoints, just rendered for the mobile shell.
///
/// The video list comes from `/v1/posts/by-author/:userId?content_type=long_video`,
/// which post-service exposes for every creator.
class PosttubeChannelScreen extends ConsumerStatefulWidget {
  const PosttubeChannelScreen({super.key, required this.userId});

  final String userId;

  @override
  ConsumerState<PosttubeChannelScreen> createState() =>
      _PosttubeChannelScreenState();
}

class _PosttubeChannelScreenState extends ConsumerState<PosttubeChannelScreen> {
  bool _subscribed = false;
  bool _toggling = false;
  late Future<List<Post>> _videosFuture;

  @override
  void initState() {
    super.initState();
    _videosFuture = _loadVideos();
  }

  Future<List<Post>> _loadVideos() async {
    final api = ref.read(apiClientProvider);
    final res = await api.get(
      '/v1/posts/by-author/${widget.userId}',
      queryParameters: {'content_type': 'long_video', 'limit': 50},
    );
    final data = res.data;
    final list = (data is Map && data['data'] is List)
        ? data['data'] as List
        : (data is List ? data : const []);
    return list
        .whereType<Map>()
        .map((e) => Post.fromJson(Map<String, dynamic>.from(e)))
        .toList();
  }

  Future<void> _toggleSubscribe() async {
    if (_toggling) return;
    setState(() => _toggling = true);
    final wasSubscribed = _subscribed;
    setState(() => _subscribed = !_subscribed);
    try {
      final repo = ref.read(userRepositoryProvider);
      if (wasSubscribed) {
        await repo.unfollowUser(widget.userId);
      } else {
        await repo.followUser(widget.userId);
      }
    } catch (_) {
      if (!mounted) return;
      setState(() => _subscribed = wasSubscribed);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update subscription.')),
      );
    } finally {
      if (mounted) setState(() => _toggling = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: const Text('Channel'),
      ),
      body: FutureBuilder<List<Post>>(
        future: _videosFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  'Could not load this channel.',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textDim,
                  ),
                ),
              ),
            );
          }
          final videos = snapshot.data ?? const [];
          final headerName = videos.isNotEmpty
              ? (videos.first.authorName ?? 'Creator')
              : 'Creator';

          return CustomScrollView(
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding,
                  child: Row(
                    children: [
                      Container(
                        width: 64,
                        height: 64,
                        decoration: BoxDecoration(
                          gradient: AppColors.posttubeGradient,
                          shape: BoxShape.circle,
                        ),
                        child: Center(
                          child: Text(
                            _initialsFor(headerName),
                            style: AppTextStyles.h2.copyWith(
                              color: Colors.white,
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(width: 14),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(headerName, style: AppTextStyles.h2),
                            const SizedBox(height: 4),
                            Text(
                              '${videos.length} video${videos.length == 1 ? '' : 's'}',
                              style: AppTextStyles.bodySmall.copyWith(
                                color: AppColors.textDim,
                              ),
                            ),
                          ],
                        ),
                      ),
                      _SubscribeButton(
                        subscribed: _subscribed,
                        loading: _toggling,
                        onTap: _toggleSubscribe,
                      ),
                    ],
                  ),
                ),
              ),
              if (videos.isEmpty)
                SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Text(
                        "This creator hasn't uploaded any videos yet.",
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textDim,
                        ),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                )
              else
                SliverPadding(
                  padding: AppSpacing.pagePadding.copyWith(top: 0),
                  sliver: SliverGrid.builder(
                    gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                      crossAxisCount: 2,
                      mainAxisSpacing: 12,
                      crossAxisSpacing: 12,
                      childAspectRatio: 16 / 13,
                    ),
                    itemCount: videos.length,
                    itemBuilder: (_, i) => _VideoCard(post: videos[i]),
                  ),
                ),
            ],
          );
        },
      ),
    );
  }
}

class _SubscribeButton extends StatelessWidget {
  const _SubscribeButton({
    required this.subscribed,
    required this.loading,
    required this.onTap,
  });

  final bool subscribed;
  final bool loading;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: loading ? null : onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
        decoration: BoxDecoration(
          gradient: subscribed ? null : AppColors.posttubeGradient,
          color: subscribed ? Colors.white.withValues(alpha: 0.06) : null,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: subscribed
                ? AppColors.borderSubtle
                : AppColors.posttubePrimary.withValues(alpha: 0.4),
          ),
        ),
        child: loading
            ? const SizedBox(
                width: 14,
                height: 14,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : Text(
                subscribed ? 'Subscribed' : 'Subscribe',
                style: AppTextStyles.label.copyWith(
                  color: subscribed ? AppColors.textSecondary : Colors.white,
                ),
              ),
      ),
    );
  }
}

class _VideoCard extends StatelessWidget {
  const _VideoCard({required this.post});

  final Post post;

  @override
  Widget build(BuildContext context) {
    final thumb = post.firstMediaUrl.isNotEmpty
        ? '${Environment.apiBaseUrl}${post.firstMediaUrl}'
        : null;
    return GestureDetector(
      onTap: () => context.push('/posttube'),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ClipRRect(
              borderRadius: const BorderRadius.vertical(
                top: Radius.circular(20),
              ),
              child: AspectRatio(
                aspectRatio: 16 / 9,
                child: thumb != null
                    ? Image.network(
                        thumb,
                        fit: BoxFit.cover,
                        errorBuilder: (_, _, _) => _placeholder(),
                      )
                    : _placeholder(),
              ),
            ),
            Padding(
              padding: const EdgeInsets.all(10),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    post.content.isEmpty ? 'Untitled' : post.content,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.label,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    '${post.likeCount} likes',
                    style: AppTextStyles.monoSmall.copyWith(
                      color: AppColors.textDim,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _placeholder() => Container(color: AppColors.bgTertiary);
}

String _initialsFor(String name) {
  final parts = name.trim().split(RegExp(r'\s+'));
  if (parts.isEmpty || parts[0].isEmpty) return '??';
  if (parts.length == 1) {
    return parts[0]
        .substring(0, parts[0].length > 1 ? 2 : 1)
        .toUpperCase();
  }
  return (parts[0].substring(0, 1) + parts[1].substring(0, 1)).toUpperCase();
}
