import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PostCard extends ConsumerStatefulWidget {
  const PostCard({super.key, required this.post});

  final Post post;

  @override
  ConsumerState<PostCard> createState() => _PostCardState();
}

class _PostCardState extends ConsumerState<PostCard> {
  late bool _liked;
  late int _likeCount;
  late int _shareCount;
  bool _actionPending = false;

  Post get post => widget.post;

  @override
  void initState() {
    super.initState();
    _liked = post.isLiked;
    _likeCount = post.likeCount;
    _shareCount = post.shareCount;
  }

  @override
  void didUpdateWidget(PostCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.post.id != widget.post.id) {
      _liked = widget.post.isLiked;
      _likeCount = widget.post.likeCount;
      _shareCount = widget.post.shareCount;
      _actionPending = false;
    }
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  String get _postLink {
    final base = Environment.externalDomain == null
        ? Environment.apiBaseUrl
        : 'https://${Environment.externalDomain}';
    return '$base/posts/${post.id}';
  }

  Future<void> _toggleReaction() async {
    if (_actionPending) return;

    final previousLiked = _liked;
    final previousCount = _likeCount;
    setState(() {
      _actionPending = true;
      _liked = !_liked;
      _likeCount += _liked ? 1 : -1;
      if (_likeCount < 0) _likeCount = 0;
    });

    try {
      await ref.read(postRepositoryProvider).toggleReaction(post.id);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _liked = previousLiked;
        _likeCount = previousCount;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update reaction.')),
      );
    } finally {
      if (mounted) setState(() => _actionPending = false);
    }
  }

  Future<void> _sharePost() async {
    await Clipboard.setData(ClipboardData(text: _postLink));
    if (!mounted) return;

    setState(() => _shareCount += 1);
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('Post link copied.')));

    try {
      await ref.read(postRepositoryProvider).sharePost(post.id);
    } catch (_) {
      if (mounted && _shareCount > 0) setState(() => _shareCount -= 1);
    }
  }

  Future<void> _reportPost() async {
    final reason = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: const [
            _ReportReasonTile(label: 'Spam', value: 'spam'),
            _ReportReasonTile(label: 'Harassment', value: 'harassment'),
            _ReportReasonTile(label: 'Hate speech', value: 'hate_speech'),
            _ReportReasonTile(label: 'Violence', value: 'violence'),
            _ReportReasonTile(label: 'Nudity', value: 'nudity'),
            _ReportReasonTile(label: 'Misinformation', value: 'misinformation'),
            _ReportReasonTile(label: 'Other', value: 'other'),
          ],
        ),
      ),
    );
    if (reason == null) return;

    try {
      await ref
          .read(postRepositoryProvider)
          .submitReport(
            targetType: post.isReel
                ? 'reel'
                : (post.isVideo ? 'video' : 'post'),
            targetId: post.id,
            reason: reason,
          );
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Report submitted.')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not submit report.')));
    }
  }

  Future<void> _deletePost() async {
    final confirmed =
        await showDialog<bool>(
          context: context,
          builder: (dialogContext) {
            return AlertDialog(
              backgroundColor: AppColors.bgSecondary,
              title: const Text('Delete post'),
              content: const Text(
                'This removes the post from your feed and profile. This action cannot be undone.',
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(dialogContext).pop(false),
                  child: const Text('Cancel'),
                ),
                FilledButton(
                  style: FilledButton.styleFrom(backgroundColor: Colors.red),
                  onPressed: () => Navigator.of(dialogContext).pop(true),
                  child: const Text('Delete'),
                ),
              ],
            );
          },
        ) ??
        false;
    if (!confirmed) return;

    try {
      await ref.read(postRepositoryProvider).deletePost(post.id);
      ref.read(homeFeedProvider.notifier).removePost(post.id);
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Post deleted.')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not delete post.')));
    }
  }

  // Pick a deterministic gradient for text-only posts based on the author id
  // so each user's text posts get a stable, recognisable colour.
  LinearGradient _textPostGradient() {
    const palette = <List<Color>>[
      [AppColors.postbookPrimary, AppColors.postgramPrimary],
      [AppColors.posttubePrimary, AppColors.accentPurple],
      [AppColors.accentPurple, AppColors.postgramPrimary],
      [AppColors.postbookPrimary, AppColors.postbookSecondary],
      [AppColors.postgramSecondary, AppColors.postgramPrimary],
    ];
    final seed = (post.authorId.isNotEmpty ? post.authorId : post.id).hashCode;
    final colours = palette[seed.abs() % palette.length];
    return LinearGradient(
      begin: Alignment.topLeft,
      end: Alignment.bottomRight,
      colors: colours,
    );
  }

  @override
  Widget build(BuildContext context) {
    // PRODUCTION OPTIMIZATION: RepaintBoundary isolates this complex card from other screen repaints.
    final hasMedia = post.mediaIds.isNotEmpty;
    final hasContent = post.content.trim().isNotEmpty;
    final isTextOnly = hasContent && !hasMedia;

    return RepaintBoundary(
      child: Container(
        margin: const EdgeInsets.only(bottom: 14),
        width: double.infinity,
        clipBehavior: Clip.antiAlias,
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Header always sits inside padding.
            Padding(
              padding: const EdgeInsets.fromLTRB(14, 14, 8, 12),
              child: _buildHeader(context),
            ),

            if (isTextOnly)
              _buildTextPostBody()
            else if (hasMedia) ...[
              if (hasContent)
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                  child: Text(post.content, style: AppTextStyles.body),
                ),
              _buildMediaBlock(),
            ] else
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                child: Text(
                  'Shared a post',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ),

            if (post.tags.isNotEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                child: _buildTags(),
              ),
            Padding(
              padding: const EdgeInsets.fromLTRB(8, 8, 8, 8),
              child: _buildActionRow(),
            ),
          ],
        ),
      ),
    ).animate().fadeIn(duration: 400.ms).slideY(begin: 0.05, end: 0);
  }

  Widget _buildTextPostBody() {
    final body = post.content.trim();
    final isShort = body.length <= 140;
    return Container(
      margin: const EdgeInsets.fromLTRB(14, 0, 14, 8),
      padding: EdgeInsets.symmetric(
        horizontal: 18,
        vertical: isShort ? 28 : 22,
      ),
      decoration: BoxDecoration(
        gradient: _textPostGradient(),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Text(
        body,
        style: AppTextStyles.h2.copyWith(
          color: Colors.white,
          fontSize: isShort ? 22 : 17,
          height: 1.35,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }

  Widget _buildMediaBlock() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 0, 14, 8),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: AspectRatio(
          aspectRatio: 4 / 5,
          child: Stack(
            fit: StackFit.expand,
            children: [
              Image.network(
                '${Environment.apiBaseUrl}${post.firstMediaUrl}',
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => Container(
                  color: AppColors.bgTertiary,
                  alignment: Alignment.center,
                  child: Text(
                    'Media unavailable',
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textSecondary,
                    ),
                  ),
                ),
              ),
              if (post.isVideo || post.isReel)
                const Center(
                  child: DecoratedBox(
                    decoration: BoxDecoration(
                      color: Color(0x66000000),
                      shape: BoxShape.circle,
                    ),
                    child: Padding(
                      padding: EdgeInsets.all(14),
                      child: Icon(
                        Icons.play_arrow_rounded,
                        color: Colors.white,
                        size: 36,
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(BuildContext context) {
    final avatarUrl = (post.authorAvatar ?? '').trim();
    final authorName = (post.authorName ?? '').trim();
    final authorInitial = authorName.isNotEmpty
        ? authorName[0].toUpperCase()
        : '?';
    return Row(
      children: [
        CircleAvatar(
          radius: 20,
          backgroundColor: AppColors.bgTertiary,
          backgroundImage: avatarUrl.isNotEmpty
              ? NetworkImage(avatarUrl)
              : null,
          child: avatarUrl.isEmpty
              ? Text(authorInitial, style: AppTextStyles.h3)
              : null,
        ),
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                authorName.isNotEmpty ? authorName : 'Anonymous',
                style: AppTextStyles.h3,
              ),
              Text(
                '@${(authorName.isNotEmpty ? authorName : 'user').toLowerCase().replaceAll(' ', '_')} \u2022 ${_timeAgo(post.createdAt)}',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textDim,
                ),
              ),
            ],
          ),
        ),
        _buildMoreMenu(context),
      ],
    );
  }

  Widget _buildMoreMenu(BuildContext context) {
    final currentUserId =
        ref.watch(authStateProvider).valueOrNull?.userId ??
        ref.read(authServiceProvider).userId;
    final canDelete = currentUserId == post.authorId;
    return PopupMenuButton<String>(
      color: AppColors.bgSecondary,
      icon: const Icon(Icons.more_horiz, color: AppColors.textMuted),
      onSelected: (value) {
        switch (value) {
          case 'copy':
            Clipboard.setData(ClipboardData(text: _postLink));
            ScaffoldMessenger.of(
              context,
            ).showSnackBar(const SnackBar(content: Text('Post link copied.')));
            return;
          case 'report':
            _reportPost();
            return;
          case 'delete':
            _deletePost();
            return;
        }
      },
      itemBuilder: (context) {
        final items = <PopupMenuEntry<String>>[
          const PopupMenuItem(value: 'copy', child: Text('Copy link')),
          const PopupMenuItem(value: 'report', child: Text('Report')),
        ];
        if (canDelete) {
          items.add(
            const PopupMenuItem(value: 'delete', child: Text('Delete')),
          );
        }
        return items;
      },
    );
  }

  Widget _buildTags() {
    return Padding(
      padding: const EdgeInsets.only(top: 10),
      child: Wrap(
        spacing: 8,
        runSpacing: 6,
        children: post.tags
            .map(
              (tag) => Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
                decoration: BoxDecoration(
                  color: Colors.white.withOpacity(0.05),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  '#$tag',
                  style: AppTextStyles.tag.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
            )
            .toList(),
      ),
    );
  }

  Widget _buildActionRow() {
    return Row(
      children: [
        _ActionPill(
          icon: _liked ? Icons.favorite : Icons.favorite_border,
          label: _formatCount(_likeCount),
          active: _liked,
          onTap: _toggleReaction,
        ),
        const SizedBox(width: 8),
        _ActionPill(
          icon: Icons.chat_bubble_outline,
          label: _formatCount(post.commentCount),
          onTap: () => context.push('/comments/${post.id}'),
        ),
        const Spacer(),
        InkWell(
          onTap: _sharePost,
          borderRadius: BorderRadius.circular(999),
          child: Padding(
            padding: const EdgeInsets.all(8),
            child: Row(
              children: [
                const Icon(
                  Icons.share_outlined,
                  size: 20,
                  color: AppColors.textMuted,
                ),
                if (_shareCount > 0) ...[
                  const SizedBox(width: 4),
                  Text(
                    _formatCount(_shareCount),
                    style: AppTextStyles.labelSmall,
                  ),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d';
    if (diff.inHours > 0) return '${diff.inHours}h';
    return '${diff.inMinutes}m';
  }
}

class _ReportReasonTile extends StatelessWidget {
  const _ReportReasonTile({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      title: Text(label, style: AppTextStyles.body),
      onTap: () => Navigator.of(context).pop(value),
    );
  }
}

class ReelCard extends StatelessWidget {
  final String title;
  final String creator;
  final String duration;
  final VoidCallback onTap;

  const ReelCard({
    super.key,
    required this.title,
    required this.creator,
    required this.duration,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        height: 200,
        margin: const EdgeInsets.only(bottom: 14),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          image: const DecorationImage(
            image: NetworkImage(
              'https://images.unsplash.com/photo-1611162617474-5b21e879e113?w=800&q=80',
            ),
            fit: BoxFit.cover,
          ),
        ),
        child: Stack(
          children: [
            Container(
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [Colors.transparent, Colors.black.withOpacity(0.8)],
                ),
              ),
            ),
            Positioned(
              left: 16,
              bottom: 16,
              right: 16,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: AppTextStyles.h3.copyWith(color: Colors.white),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 4),
                  Row(
                    children: [
                      Text(
                        creator,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: Colors.white70,
                        ),
                      ),
                      const Spacer(),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 6,
                          vertical: 2,
                        ),
                        decoration: BoxDecoration(
                          color: Colors.black54,
                          borderRadius: BorderRadius.circular(4),
                        ),
                        child: Text(
                          duration,
                          style: AppTextStyles.labelTiny.copyWith(
                            color: Colors.white,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
            const Center(
              child: Icon(
                Icons.play_circle_outline,
                color: Colors.white,
                size: 50,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class VideoCard extends StatelessWidget {
  final String title;
  final String stats;
  final VoidCallback onTap;

  const VideoCard({
    super.key,
    required this.title,
    required this.stats,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(bottom: 14),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            AspectRatio(
              aspectRatio: 16 / 9,
              child: Container(
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  image: const DecorationImage(
                    image: NetworkImage(
                      'https://images.unsplash.com/photo-1492691527719-9d1e07e534b4?w=800&q=80',
                    ),
                    fit: BoxFit.cover,
                  ),
                ),
                child: const Center(
                  child: Icon(
                    Icons.play_arrow_rounded,
                    color: Colors.white,
                    size: 40,
                  ),
                ),
              ),
            ),
            const SizedBox(height: 10),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: AppTextStyles.h3,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    stats,
                    style: AppTextStyles.bodySmall.copyWith(
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
}

class _ActionPill extends StatelessWidget {
  final IconData icon;
  final String label;
  final bool active;
  final VoidCallback onTap;

  const _ActionPill({
    required this.icon,
    required this.label,
    required this.onTap,
    this.active = false,
  });

  @override
  Widget build(BuildContext context) {
    final color = active ? AppColors.postbookPrimary : AppColors.textSecondary;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: active
              ? color.withOpacity(0.1)
              : Colors.white.withOpacity(0.05),
          borderRadius: BorderRadius.circular(20),
        ),
        child: Row(
          children: [
            Icon(icon, size: 16, color: color),
            const SizedBox(width: 6),
            Text(
              label,
              style: AppTextStyles.label.copyWith(
                color: color,
                fontWeight: FontWeight.bold,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
