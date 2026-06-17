import 'dart:async';
import 'dart:math' as math;

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/analytics_repository.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/comments_provider.dart';
import 'package:atpost_app/providers/data_saver_provider.dart';
import 'package:atpost_app/providers/feed_provider.dart';
import 'package:atpost_app/shared/widgets/caption_toggle.dart';
import 'package:atpost_app/shared/widgets/video_player_widget.dart';
import 'package:flutter/services.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PosttubeScreen extends ConsumerStatefulWidget {
  const PosttubeScreen({super.key});

  @override
  ConsumerState<PosttubeScreen> createState() => _PosttubeScreenState();
}

class _PosttubeScreenState extends ConsumerState<PosttubeScreen> {
  double _progress = 0.38;
  bool _playing = true;
  bool _descriptionExpanded = false;
  int _contentTab = 0;
  List<Post> _videos = const [];
  int _currentVideoIndex = 0;

  // When the viewer landed on the current video — used to emit a
  // play_end view event on dispose.
  DateTime? _videoViewStartedAt;

  // Per-video engagement state, keyed by post.id. Lifted out of individual
  // widgets so optimistic updates survive scroll-driven rebuilds. Mirrors the
  // pattern used by reels_screen.dart so future maintenance only has to learn
  // one shape.
  final Map<String, _PosttubeEngagement> _engagementByPostId = {};

  // Per-author follow state. Hits /v1/graph/follow which is a global state
  // (you either follow them or you don't), so one bool per authorId is fine.
  final Map<String, bool> _followedAuthors = {};

  // Captions toggle state, keyed by post.id so the user's preference
  // persists when they scroll between videos in the watch surface.
  final Map<String, bool> _captionsEnabledByPostId = {};

  @override
  void dispose() {
    _flushVideoView();
    super.dispose();
  }

  // Emit a play_end view event for the video the viewer was watching.
  void _flushVideoView() {
    final startedAt = _videoViewStartedAt;
    if (startedAt == null || _videos.isEmpty) return;
    final post = _videos[_currentVideoIndex.clamp(0, _videos.length - 1)];
    if (post.id.isEmpty) return;
    final watchedMs = DateTime.now().difference(startedAt).inMilliseconds;
    if (watchedMs <= 1000) return;
    unawaited(
      ref.read(analyticsRepositoryProvider).recordVideoView(
        contentId: post.id,
        creatorId: post.authorId,
        // Short content earns only as 'flick' (settlement ignores 'reel').
        contentType: (post.contentType == 'reel' || post.contentType == 'flick') ? 'flick' : 'long_video',
        watchedMs: watchedMs,
        durationMs: (post.durationSeconds ?? 0) * 1000,
        surface: 'posttube_watch',
      ),
    );
  }

  bool _captionsEnabled(String postId) =>
      _captionsEnabledByPostId[postId] ?? false;

  void _toggleCaptions(String postId) {
    setState(() {
      _captionsEnabledByPostId[postId] = !_captionsEnabled(postId);
    });
  }

  _PosttubeEngagement _ensureEngagement(Post post) {
    return _engagementByPostId.putIfAbsent(
      post.id,
      () => _PosttubeEngagement(
        likeCount: post.likeCount,
        dislikeCount: 0,
        commentCount: post.commentCount,
        shareCount: post.shareCount,
        liked: post.isLiked,
        disliked: false,
        saved: post.isBookmarked,
      ),
    );
  }

  Future<void> _toggleLike(Post post) async {
    final eng = _ensureEngagement(post);
    final prev = eng.copy();
    setState(() {
      if (eng.liked) {
        eng.liked = false;
        eng.likeCount = math.max(0, eng.likeCount - 1);
      } else {
        eng.liked = true;
        eng.likeCount += 1;
        if (eng.disliked) {
          eng.disliked = false;
          eng.dislikeCount = math.max(0, eng.dislikeCount - 1);
        }
      }
    });
    try {
      await ref.read(postRepositoryProvider).toggleReaction(post.id);
    } catch (_) {
      if (!mounted) return;
      setState(() => eng.restoreFrom(prev));
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update like.')),
      );
    }
  }

  Future<void> _toggleDislike(Post post) async {
    final eng = _ensureEngagement(post);
    final prev = eng.copy();
    final shouldEnable = !eng.disliked;
    setState(() {
      if (eng.disliked) {
        eng.disliked = false;
        eng.dislikeCount = math.max(0, eng.dislikeCount - 1);
      } else {
        eng.disliked = true;
        eng.dislikeCount += 1;
        if (eng.liked) {
          eng.liked = false;
          eng.likeCount = math.max(0, eng.likeCount - 1);
        }
      }
    });
    if (!shouldEnable) return;
    try {
      await ref.read(postRepositoryProvider).toggleReaction(post.id, emoji: '👎');
    } catch (_) {
      if (!mounted) return;
      setState(() => eng.restoreFrom(prev));
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update dislike.')),
      );
    }
  }

  Future<void> _toggleSave(Post post) async {
    final eng = _ensureEngagement(post);
    final prev = eng.saved;
    setState(() => eng.saved = !eng.saved);
    try {
      await ref.read(postRepositoryProvider).toggleBookmark(post.id);
    } catch (_) {
      if (!mounted) return;
      setState(() => eng.saved = prev);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update save.')),
      );
    }
  }

  Future<void> _share(Post post) async {
    final eng = _ensureEngagement(post);
    final link = '${Environment.apiBaseUrl}/posts/${post.id}';
    await Clipboard.setData(ClipboardData(text: link));
    if (!mounted) return;
    setState(() => eng.shareCount += 1);
    // Fire-and-forget the analytics call. Failure shouldn't block the UX.
    unawaited(ref.read(postRepositoryProvider).sharePost(post.id));
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Video link copied to clipboard.')),
    );
  }

  void _openComments(Post post) {
    context.push('/comments/${post.id}');
  }

  Future<void> _toggleSubscribe(Post post) async {
    final authorId = post.authorId;
    if (authorId.isEmpty) return;
    final wasFollowing = _followedAuthors[authorId] ?? false;
    setState(() => _followedAuthors[authorId] = !wasFollowing);
    try {
      final repo = ref.read(userRepositoryProvider);
      if (wasFollowing) {
        await repo.unfollowUser(authorId);
      } else {
        await repo.followUser(authorId);
      }
    } catch (_) {
      if (!mounted) return;
      setState(() => _followedAuthors[authorId] = wasFollowing);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update subscription.')),
      );
    }
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d ago';
    if (diff.inHours > 0) return '${diff.inHours}h ago';
    return '${diff.inMinutes}m ago';
  }

  // Build the playback URL with an optional `?quality=240p` hint when
  // data-saver is on. media-service's `/serve` endpoint accepts a
  // quality hint; for HLS streams the player picks the matching
  // rendition out of the manifest.
  String _resolveVideoUrl(Post? post, {required bool dataSaver}) {
    if (post == null || post.firstMediaUrl.isEmpty) return '';
    final base = '${Environment.apiBaseUrl}${post.firstMediaUrl}';
    if (!dataSaver) return base;
    final separator = base.contains('?') ? '&' : '?';
    return '${base}${separator}quality=240p';
  }

  // Chapters are now derived from video metadata when available.
  // Placeholder chapters shown only when a video is loaded.
  List<_Chapter> get _chapters {
    if (_videos.isEmpty) return [];
    // TODO: Parse chapter markers from video metadata API response.
    return const [_Chapter(time: '00:00', label: 'Intro')];
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<AsyncValue<List<Post>>>(videoFeedProvider, (_, next) {
      next.whenData((posts) {
        if (mounted && posts.isNotEmpty) {
          setState(() {
            _videos = posts;
            _currentVideoIndex = 0;
          });
          _videoViewStartedAt ??= DateTime.now();
        }
      });
    });

    final currentVideo = _videos.isNotEmpty
        ? _videos[_currentVideoIndex]
        : null;

    // Data-saver bias: suppresses the autoplay default and biases the
    // playback URL toward the lowest available rendition.
    final dataSaver = ref.watch(effectiveDataSaverProvider);

    return Scaffold(
      body: SafeArea(
        child: CustomScrollView(
          slivers: [
            SliverToBoxAdapter(
              child: Padding(
                padding: AppSpacing.pagePadding.copyWith(top: 10, bottom: 14),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Discovery strip — surfaces /subscriptions and /history
                    // since this screen is the top-level Posttube tab and
                    // those pages are otherwise unreachable from the shell.
                    SizedBox(
                      height: 36,
                      child: ListView(
                        scrollDirection: Axis.horizontal,
                        children: [
                          _DiscoveryPill(
                            icon: Icons.local_fire_department_outlined,
                            label: 'Trending',
                            onTap: () => context.push('/posttube/trending'),
                          ),
                          const SizedBox(width: 8),
                          _DiscoveryPill(
                            icon: Icons.subscriptions_outlined,
                            label: 'Subscriptions',
                            onTap: () => context.push('/posttube/subscriptions'),
                          ),
                          const SizedBox(width: 8),
                          _DiscoveryPill(
                            icon: Icons.history,
                            label: 'History',
                            onTap: () => context.push('/posttube/history'),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 12),
                    _VideoPanel(
                      videoUrl: _resolveVideoUrl(
                        currentVideo,
                        dataSaver: dataSaver,
                      ),
                      progress: _progress,
                      isPlaying: _playing,
                      dataSaver: dataSaver,
                      onProgressChanged: (value) =>
                          setState(() => _progress = value),
                      onTogglePlay: () => setState(() => _playing = !_playing),
                    ),
                    const SizedBox(height: 14),
                    Text(
                      currentVideo?.content ?? '',
                      style: AppTextStyles.h2.copyWith(fontSize: 19),
                    ),
                    const SizedBox(height: 6),
                    Text(
                      currentVideo != null
                          ? '${_formatCount(currentVideo.viewCount)} views  •  ${_timeAgo(currentVideo.createdAt)}'
                          : '',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.textDim,
                      ),
                    ),
                    const SizedBox(height: 14),
                    if (currentVideo != null)
                      Builder(
                        builder: (_) {
                          final eng = _ensureEngagement(currentVideo);
                          return SingleChildScrollView(
                            scrollDirection: Axis.horizontal,
                            child: Row(
                              children: [
                                ActionPillButton(
                                  icon: eng.liked
                                      ? Icons.thumb_up_alt
                                      : Icons.thumb_up_alt_outlined,
                                  label: _formatCount(eng.likeCount),
                                  active: eng.liked,
                                  onTap: () => _toggleLike(currentVideo),
                                ),
                                const SizedBox(width: 8),
                                ActionPillButton(
                                  icon: eng.disliked
                                      ? Icons.thumb_down_alt
                                      : Icons.thumb_down_alt_outlined,
                                  label: _formatCount(eng.dislikeCount),
                                  active: eng.disliked,
                                  onTap: () => _toggleDislike(currentVideo),
                                ),
                                const SizedBox(width: 8),
                                ActionPillButton(
                                  icon: Icons.chat_bubble_outline,
                                  label: _formatCount(eng.commentCount),
                                  onTap: () => _openComments(currentVideo),
                                ),
                                const SizedBox(width: 8),
                                ActionPillButton(
                                  icon: Icons.share_outlined,
                                  label: 'Share',
                                  onTap: () => _share(currentVideo),
                                ),
                                const SizedBox(width: 8),
                                ActionPillButton(
                                  icon: eng.saved
                                      ? Icons.bookmark
                                      : Icons.bookmark_outline,
                                  label: 'Save',
                                  active: eng.saved,
                                  onTap: () => _toggleSave(currentVideo),
                                ),
                                const SizedBox(width: 8),
                                // Captions (CC) toggle. Hidden when
                                // media-service has no caption tracks
                                // for the current video's primary
                                // media id; mediaIds may be empty for
                                // text/image posts that the user can
                                // still react to but we never render
                                // captions for.
                                if (currentVideo.mediaIds.isNotEmpty)
                                  CaptionToggle(
                                    mediaId: currentVideo.mediaIds.first,
                                    enabled:
                                        _captionsEnabled(currentVideo.id),
                                    onToggle: () =>
                                        _toggleCaptions(currentVideo.id),
                                  ),
                              ],
                            ),
                          );
                        },
                      ),
                    const SizedBox(height: 16),
                    if (currentVideo != null)
                      _ChannelCard(
                        subscribed:
                            _followedAuthors[currentVideo.authorId] ?? false,
                        channelName: currentVideo.authorName ?? 'Creator',
                        onSubscribeTap: () => _toggleSubscribe(currentVideo),
                        onTap: currentVideo.authorId.isEmpty
                            ? null
                            : () => context.push(
                                '/posttube/channel/${currentVideo.authorId}',
                              ),
                      ),
                    const SizedBox(height: 16),
                    Text('Chapters', style: AppTextStyles.h3),
                    const SizedBox(height: 10),
                    SizedBox(
                      height: 38,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        itemCount: _chapters.length,
                        separatorBuilder: (_, _) => const SizedBox(width: 8),
                        itemBuilder: (context, index) {
                          final chapter = _chapters[index];
                          return Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 11,
                              vertical: 8,
                            ),
                            decoration: BoxDecoration(
                              color: AppColors.bgCard,
                              borderRadius: BorderRadius.circular(
                                AppSpacing.radiusLarge,
                              ),
                              border: Border.all(color: AppColors.borderSubtle),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Text(
                                  chapter.time,
                                  style: AppTextStyles.monoSmall.copyWith(
                                    color: AppColors.posttubePrimary,
                                  ),
                                ),
                                const SizedBox(width: 6),
                                Text(
                                  chapter.label,
                                  style: AppTextStyles.labelSmall,
                                ),
                              ],
                            ),
                          );
                        },
                      ),
                    ),
                    const SizedBox(height: 16),
                    _DescriptionCard(
                      expanded: _descriptionExpanded,
                      onTap: () => setState(
                        () => _descriptionExpanded = !_descriptionExpanded,
                      ),
                    ),
                    const SizedBox(height: 16),
                    _ContentTabs(
                      activeIndex: _contentTab,
                      onChanged: (value) => setState(() => _contentTab = value),
                    ),
                    const SizedBox(height: 12),
                    if (_contentTab == 0)
                      _CommentsSection(post: currentVideo)
                    else
                      _UpNextSection(
                        videos: _videos.length > 1
                            ? _videos.sublist(1)
                            : const [],
                      ),
                    const SizedBox(height: 100),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _VideoPanel extends StatefulWidget {
  const _VideoPanel({
    required this.videoUrl,
    required this.progress,
    required this.isPlaying,
    required this.dataSaver,
    required this.onProgressChanged,
    required this.onTogglePlay,
  });

  final String videoUrl;
  final double progress;
  final bool isPlaying;
  final bool dataSaver;
  final ValueChanged<double> onProgressChanged;
  final VoidCallback onTogglePlay;

  @override
  State<_VideoPanel> createState() => _VideoPanelState();
}

class _VideoPanelState extends State<_VideoPanel> {
  // Data-saver: gate the player widget behind a manual tap so we
  // never auto-fetch video bytes. Resets when the URL changes (the
  // user has scrolled to a new video).
  bool _userTappedPlay = false;

  @override
  void didUpdateWidget(_VideoPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.videoUrl != widget.videoUrl) {
      _userTappedPlay = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final hasVideo = widget.videoUrl.isNotEmpty;
    final shouldAutoplay = !widget.dataSaver || _userTappedPlay;

    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          ClipRRect(
            borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
            child: SizedBox(
              height: 230,
              width: double.infinity,
              child: hasVideo && shouldAutoplay
                  ? VideoPlayerWidget(
                      videoUrl: widget.videoUrl,
                      autoPlay: true,
                      looping: false,
                      showControls: true,
                      aspectRatio: 16 / 9,
                      placeholder: _gradientPlaceholder(
                        widget.onTogglePlay,
                        widget.isPlaying,
                      ),
                    )
                  : GestureDetector(
                      onTap: hasVideo
                          ? () => setState(() => _userTappedPlay = true)
                          : null,
                      child: _gradientPlaceholder(
                        widget.onTogglePlay,
                        widget.isPlaying,
                      ),
                    ),
            ),
          ),
        ],
      ),
    );
  }

  static Widget _gradientPlaceholder(
    VoidCallback onTogglePlay,
    bool isPlaying,
  ) {
    return Container(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0xFF163B42), Color(0xFF151524)],
        ),
      ),
      child: Stack(
        children: [
          Positioned(
            top: 10,
            left: 10,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
              decoration: BoxDecoration(
                color: AppColors.posttubePrimary.withValues(alpha: 0.18),
                borderRadius: BorderRadius.circular(999),
              ),
              child: Text(
                'POSTTUBE',
                style: AppTextStyles.labelTiny.copyWith(
                  color: AppColors.posttubePrimary,
                ),
              ),
            ),
          ),
          Center(
            child: GestureDetector(
              onTap: onTogglePlay,
              child: Container(
                width: 62,
                height: 62,
                decoration: BoxDecoration(
                  color: Colors.white.withValues(alpha: 0.14),
                  shape: BoxShape.circle,
                  border: Border.all(
                    color: Colors.white.withValues(alpha: 0.2),
                  ),
                ),
                child: Icon(
                  isPlaying ? Icons.pause : Icons.play_arrow,
                  color: Colors.white,
                  size: 28,
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _ChannelCard extends StatelessWidget {
  const _ChannelCard({
    required this.subscribed,
    required this.onSubscribeTap,
    this.channelName,
    this.onTap,
  });

  final bool subscribed;
  final VoidCallback onSubscribeTap;
  // channelName + onTap added so the watch screen can route to the
  // creator's channel page on avatar/name tap. Defaults preserve the
  // original demo layout for any caller that hasn't migrated.
  final String? channelName;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final name = channelName ?? 'VChat engineering';
    final initials = _channelInitials(name);
    final card = Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 46,
            height: 46,
            decoration: BoxDecoration(
              gradient: AppColors.posttubeGradient,
              shape: BoxShape.circle,
              border: Border.all(
                color: AppColors.posttubePrimary.withValues(alpha: 0.5),
              ),
            ),
            child: Center(
              child: Text(
                initials,
                style: AppTextStyles.label.copyWith(color: Colors.white),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Flexible(
                      child: Text(
                        name,
                        style: AppTextStyles.h3,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const SizedBox(width: 4),
                    const Icon(
                      Icons.verified,
                      size: 16,
                      color: AppColors.posttubePrimary,
                    ),
                  ],
                ),
                const SizedBox(height: 2),
                Text('Tap to view channel', style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          GestureDetector(
            onTap: onSubscribeTap,
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
              child: Text(
                subscribed ? 'Subscribed' : 'Subscribe',
                style: AppTextStyles.label.copyWith(
                  color: subscribed ? AppColors.textSecondary : Colors.white,
                ),
              ),
            ),
          ),
        ],
      ),
    );
    if (onTap == null) return card;
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: card,
    );
  }
}

// Compact pill used on the Posttube discovery strip to link out to
// /posttube/subscriptions and /posttube/history. Visual matches the
// existing ActionPillButton family but skips the count label so the
// pill stays tight when many appear in a row.
class _DiscoveryPill extends StatelessWidget {
  const _DiscoveryPill({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 16, color: AppColors.posttubePrimary),
            const SizedBox(width: 8),
            Text(label, style: AppTextStyles.labelSmall),
          ],
        ),
      ),
    );
  }
}

// Initials lifted from a display name; "VChat engineering" -> "VE".
String _channelInitials(String name) {
  final parts = name.trim().split(RegExp(r'\s+'));
  if (parts.isEmpty || parts[0].isEmpty) return '??';
  if (parts.length == 1) {
    return parts[0].substring(0, parts[0].length > 1 ? 2 : 1).toUpperCase();
  }
  return (parts[0].substring(0, 1) + parts[1].substring(0, 1)).toUpperCase();
}

class _DescriptionCard extends StatelessWidget {
  const _DescriptionCard({required this.expanded, required this.onTap});

  final bool expanded;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Description', style: AppTextStyles.h3),
            const SizedBox(height: 6),
            Text(
              expanded
                  ? 'In this session we break down the gateway, fanout feed write pipeline, '
                        'ranking refresh strategy, and cache invalidation signals used to keep '
                        'the home timeline snappy under burst load.'
                  : 'In this session we break down the gateway, fanout feed write pipeline...',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textSecondary,
              ),
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                _TagChip(label: '#architecture'),
                _TagChip(label: '#golang'),
                _TagChip(label: '#distributed-systems'),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _TagChip extends StatelessWidget {
  const _TagChip({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
      decoration: BoxDecoration(
        color: AppColors.bgPrimary,
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Text(label, style: AppTextStyles.tag),
    );
  }
}

class _ContentTabs extends StatelessWidget {
  const _ContentTabs({required this.activeIndex, required this.onChanged});

  final int activeIndex;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        _ContentTabItem(
          label: 'Comments (5)',
          active: activeIndex == 0,
          onTap: () => onChanged(0),
        ),
        const SizedBox(width: 8),
        _ContentTabItem(
          label: 'Up Next',
          active: activeIndex == 1,
          onTap: () => onChanged(1),
        ),
      ],
    );
  }
}

class _ContentTabItem extends StatelessWidget {
  const _ContentTabItem({
    required this.label,
    required this.active,
    required this.onTap,
  });

  final String label;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 220),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
        decoration: BoxDecoration(
          gradient: active ? AppColors.posttubeGradient : null,
          color: active ? null : AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(
            color: active
                ? AppColors.posttubePrimary.withValues(alpha: 0.4)
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: active ? Colors.white : AppColors.textMuted,
          ),
        ),
      ),
    );
  }
}

class _CommentsSection extends ConsumerWidget {
  const _CommentsSection({required this.post});

  final Post? post;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final p = post;
    if (p == null) {
      return const SizedBox.shrink();
    }
    final commentsAsync = ref.watch(commentsProvider(p.id));
    return commentsAsync.when(
      data: (comments) {
        // Show a preview of the first three comments inline; tapping the
        // header (handled by the action pill above) navigates to the full
        // /comments/:postId screen for replies + composer.
        if (comments.isEmpty) {
          return GestureDetector(
            onTap: () => context.push('/comments/${p.id}'),
            child: Container(
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Row(
                children: [
                  const Icon(
                    Icons.chat_bubble_outline,
                    size: 18,
                    color: AppColors.textDim,
                  ),
                  const SizedBox(width: 10),
                  Text(
                    'Be the first to comment',
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textDim,
                    ),
                  ),
                ],
              ),
            ),
          );
        }
        final preview = comments.take(3).toList();
        return Column(
          children: [
            for (var i = 0; i < preview.length; i++) ...[
              if (i > 0) const SizedBox(height: 10),
              _CommentTile(
                initials: _initialsFor(preview[i].authorName ?? 'User'),
                name: preview[i].authorName ?? 'User',
                time: _timeAgoStatic(preview[i].createdAt),
                text: preview[i].text,
              ),
            ],
            if (comments.length > 3) ...[
              const SizedBox(height: 10),
              GestureDetector(
                onTap: () => context.push('/comments/${p.id}'),
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 14,
                    vertical: 10,
                  ),
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(999),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Text(
                    'View all ${comments.length} comments',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: AppColors.posttubePrimary,
                    ),
                  ),
                ),
              ),
            ],
          ],
        );
      },
      loading: () => const Padding(
        padding: EdgeInsets.symmetric(vertical: 18),
        child: Center(child: CircularProgressIndicator()),
      ),
      error: (e, _) => Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Text(
          'Could not load comments.',
          style: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
        ),
      ),
    );
  }
}

// initials taken from a display name; "Aarav Dev" -> "AD".
String _initialsFor(String name) {
  final parts = name.trim().split(RegExp(r'\s+'));
  if (parts.isEmpty) return '??';
  if (parts.length == 1) return parts[0].substring(0, math.min(2, parts[0].length)).toUpperCase();
  return (parts[0].substring(0, 1) + parts[1].substring(0, 1)).toUpperCase();
}

String _timeAgoStatic(DateTime dt) {
  final diff = DateTime.now().difference(dt);
  if (diff.inDays > 0) return '${diff.inDays}d';
  if (diff.inHours > 0) return '${diff.inHours}h';
  if (diff.inMinutes > 0) return '${diff.inMinutes}m';
  return 'now';
}

class _CommentTile extends StatelessWidget {
  const _CommentTile({
    required this.initials,
    required this.name,
    required this.time,
    required this.text,
  });

  final String initials;
  final String name;
  final String time;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(11),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 34,
            height: 34,
            decoration: BoxDecoration(
              color: AppColors.bgTertiary,
              borderRadius: BorderRadius.circular(12),
            ),
            child: Center(
              child: Text(initials, style: AppTextStyles.labelSmall),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(child: Text(name, style: AppTextStyles.h3)),
                    Text(time, style: AppTextStyles.monoSmall),
                  ],
                ),
                const SizedBox(height: 4),
                Text(
                  text,
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _UpNextSection extends StatelessWidget {
  const _UpNextSection({this.videos = const []});

  final List<Post> videos;

  @override
  Widget build(BuildContext context) {
    if (videos.isEmpty) {
      return Column(
        children: const [
          _RelatedVideoTile(
            title: 'Realtime notifications at scale',
            stats: '11K views • 5h ago',
          ),
          SizedBox(height: 10),
          _RelatedVideoTile(
            title: 'From monolith to service mesh',
            stats: '29K views • 1d ago',
          ),
          SizedBox(height: 10),
          _RelatedVideoTile(
            title: 'Optimizing write fanout and storage',
            stats: '8.2K views • 2d ago',
          ),
        ],
      );
    }
    return Column(
      children: videos
          .map(
            (v) => Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: _RelatedVideoTile(
                title: v.content.length > 60
                    ? '${v.content.substring(0, 60)}...'
                    : v.content,
                stats:
                    '${_formatCount(v.viewCount)} views • ${_timeAgo(v.createdAt)}',
              ),
            ),
          )
          .toList(),
    );
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d ago';
    if (diff.inHours > 0) return '${diff.inHours}h ago';
    return '${diff.inMinutes}m ago';
  }
}

class _RelatedVideoTile extends StatelessWidget {
  const _RelatedVideoTile({required this.title, required this.stats});

  final String title;
  final String stats;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Container(
            width: 120,
            height: 78,
            decoration: const BoxDecoration(
              borderRadius: BorderRadius.horizontal(
                left: Radius.circular(AppSpacing.radiusLarge),
              ),
              gradient: LinearGradient(
                begin: Alignment.topLeft,
                end: Alignment.bottomRight,
                colors: [Color(0xFF1D4047), Color(0xFF1B1B28)],
              ),
            ),
            child: const Center(
              child: Icon(
                Icons.play_circle_fill,
                color: Colors.white70,
                size: 28,
              ),
            ),
          ),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: AppTextStyles.h3,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 5),
                  Text(stats, style: AppTextStyles.bodySmall),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class ActionPillButton extends StatelessWidget {
  const ActionPillButton({
    super.key,
    required this.icon,
    required this.label,
    this.onTap,
    this.active = false,
  });
  final IconData icon;
  final String label;
  final VoidCallback? onTap;
  // active = true tints the pill in the brand colour to signal "you've already
  // liked / saved / etc. this." Default false keeps the legacy neutral look.
  final bool active;

  @override
  Widget build(BuildContext context) {
    final iconColor = active ? AppColors.posttubePrimary : AppColors.textPrimary;
    final borderColor = active
        ? AppColors.posttubePrimary.withValues(alpha: 0.5)
        : AppColors.borderSubtle;
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: borderColor),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 18, color: iconColor),
            const SizedBox(width: 8),
            Text(
              label,
              style: AppTextStyles.labelSmall.copyWith(
                color: active ? AppColors.posttubePrimary : null,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Chapter {
  const _Chapter({required this.time, required this.label});

  final String time;
  final String label;
}

/// Per-video engagement state. Lives inside _PosttubeScreenState so optimistic
/// updates persist while the user scrolls through Up Next without refetching.
/// Mirrors _ReelEngagement in reels_screen.dart by intent (so future engineers
/// only learn one shape).
class _PosttubeEngagement {
  _PosttubeEngagement({
    required this.likeCount,
    required this.dislikeCount,
    required this.commentCount,
    required this.shareCount,
    required this.liked,
    required this.disliked,
    required this.saved,
  });

  int likeCount;
  int dislikeCount;
  int commentCount;
  int shareCount;
  bool liked;
  bool disliked;
  bool saved;

  // copy / restoreFrom let callers snapshot before optimistic mutation and
  // roll back on API failure without writing the field list out twice.
  _PosttubeEngagement copy() => _PosttubeEngagement(
        likeCount: likeCount,
        dislikeCount: dislikeCount,
        commentCount: commentCount,
        shareCount: shareCount,
        liked: liked,
        disliked: disliked,
        saved: saved,
      );

  void restoreFrom(_PosttubeEngagement other) {
    likeCount = other.likeCount;
    dislikeCount = other.dislikeCount;
    commentCount = other.commentCount;
    shareCount = other.shareCount;
    liked = other.liked;
    disliked = other.disliked;
    saved = other.saved;
  }
}
