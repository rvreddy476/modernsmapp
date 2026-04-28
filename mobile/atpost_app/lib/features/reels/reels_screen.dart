import 'dart:math' as math;

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/shared/widgets/video_player_widget.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ReelsScreen extends ConsumerStatefulWidget {
  const ReelsScreen({super.key, this.fullscreenRoute = false});

  final bool fullscreenRoute;

  @override
  ConsumerState<ReelsScreen> createState() => _ReelsScreenState();
}

class _ReelsScreenState extends ConsumerState<ReelsScreen> {
  static const List<List<Color>> _palette = [
    [Color(0xFF1D102D), Color(0xFF090913)],
    [Color(0xFF20140D), Color(0xFF0D111B)],
    [Color(0xFF0E2330), Color(0xFF0A0F1E)],
    [Color(0xFF22111B), Color(0xFF0D0D18)],
    [Color(0xFF10251F), Color(0xFF0A101A)],
  ];

  late final PageController _pageController;

  final List<Post> _reels = <Post>[];
  final Map<String, _ReelEngagement> _engagementByPostId =
      <String, _ReelEngagement>{};

  bool _loadingInitial = true;
  bool _loadingMore = false;
  bool _muted = false;

  String? _nextCursor;
  bool _hasMoreFromApi = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _pageController = PageController();
    _pageController.addListener(_maybeLoadMoreOnScroll);
    _loadInitial();
  }

  @override
  void dispose() {
    _pageController
      ..removeListener(_maybeLoadMoreOnScroll)
      ..dispose();
    super.dispose();
  }

  Future<void> _loadInitial() async {
    setState(() {
      _loadingInitial = true;
      _error = null;
    });

    try {
      final page = await ref
          .read(feedRepositoryProvider)
          .getReelFeedPage(limit: 10);
      final items = page.items.where((post) => post.id.isNotEmpty).toList();

      for (final post in items) {
        _ensureEngagement(post);
      }

      setState(() {
        _reels
          ..clear()
          ..addAll(items);
        _nextCursor = page.nextCursor;
        _hasMoreFromApi =
            page.nextCursor != null && page.nextCursor!.isNotEmpty;
      });

      if (_reels.isNotEmpty && !_hasMoreFromApi) {
        _appendLoopBatch();
      }
    } catch (e) {
      setState(() {
        _error = 'Could not load reels: $e';
      });
    } finally {
      if (mounted) {
        setState(() => _loadingInitial = false);
      }
    }
  }

  Future<void> _loadMore() async {
    if (_loadingMore || _loadingInitial) return;

    if (_hasMoreFromApi) {
      setState(() => _loadingMore = true);
      try {
        final page = await ref
            .read(feedRepositoryProvider)
            .getReelFeedPage(limit: 10, cursor: _nextCursor);

        final seen = _reels.map((post) => post.id).toSet();
        final newItems = page.items
            .where((post) => !seen.contains(post.id))
            .toList();

        for (final post in page.items) {
          _ensureEngagement(post);
        }

        if (newItems.isNotEmpty) {
          setState(() {
            _reels.addAll(newItems);
          });
        }

        final hasCursor =
            page.nextCursor != null && page.nextCursor!.isNotEmpty;
        final cursorChanged = page.nextCursor != _nextCursor;

        setState(() {
          _nextCursor = page.nextCursor;
          _hasMoreFromApi = hasCursor && cursorChanged;
        });

        if (!_hasMoreFromApi) {
          _appendLoopBatch();
        }
      } catch (_) {
        _appendLoopBatch();
      } finally {
        if (mounted) {
          setState(() => _loadingMore = false);
        }
      }
      return;
    }

    _appendLoopBatch();
  }

  void _appendLoopBatch() {
    if (_reels.isEmpty) return;

    final takeCount = math.min(6, _reels.length);
    final batch = _reels.take(takeCount).toList();

    setState(() {
      _reels.addAll(batch);
    });
  }

  void _maybeLoadMoreOnScroll() {
    final page = _pageController.page;
    if (page == null) return;

    if (page >= _reels.length - 3) {
      _loadMore();
    }
  }

  _ReelEngagement _ensureEngagement(Post post) {
    return _engagementByPostId.putIfAbsent(
      post.id,
      () => _ReelEngagement(
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

  String _countLabel(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }

  Future<void> _toggleLike(Post post) async {
    final engagement = _ensureEngagement(post);
    final prevLiked = engagement.liked;
    final prevDisliked = engagement.disliked;
    final prevLikeCount = engagement.likeCount;
    final prevDislikeCount = engagement.dislikeCount;

    setState(() {
      if (engagement.liked) {
        engagement.liked = false;
        engagement.likeCount = math.max(0, engagement.likeCount - 1);
      } else {
        engagement.liked = true;
        engagement.likeCount += 1;
        if (engagement.disliked) {
          engagement.disliked = false;
          engagement.dislikeCount = math.max(0, engagement.dislikeCount - 1);
        }
      }
    });

    try {
      await ref.read(postRepositoryProvider).toggleReaction(post.id);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        engagement.liked = prevLiked;
        engagement.disliked = prevDisliked;
        engagement.likeCount = prevLikeCount;
        engagement.dislikeCount = prevDislikeCount;
      });
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not update like.')));
    }
  }

  Future<void> _toggleDislike(Post post) async {
    final engagement = _ensureEngagement(post);
    final prevLiked = engagement.liked;
    final prevDisliked = engagement.disliked;
    final prevLikeCount = engagement.likeCount;
    final prevDislikeCount = engagement.dislikeCount;

    final shouldEnableDislike = !engagement.disliked;

    setState(() {
      if (engagement.disliked) {
        engagement.disliked = false;
        engagement.dislikeCount = math.max(0, engagement.dislikeCount - 1);
      } else {
        engagement.disliked = true;
        engagement.dislikeCount += 1;
        if (engagement.liked) {
          engagement.liked = false;
          engagement.likeCount = math.max(0, engagement.likeCount - 1);
        }
      }
    });

    if (!shouldEnableDislike) {
      return;
    }

    try {
      await ref.read(postRepositoryProvider).toggleReaction(post.id, emoji: '👎');
    } catch (_) {
      if (!mounted) return;
      setState(() {
        engagement.liked = prevLiked;
        engagement.disliked = prevDisliked;
        engagement.likeCount = prevLikeCount;
        engagement.dislikeCount = prevDislikeCount;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update dislike.')),
      );
    }
  }

  // Per-author follow state, keyed by post.authorId. Lifted out of the
  // individual reel pages so that the optimistic toggle survives scroll-
  // driven rebuilds and applies across every reel by the same creator in
  // the current page.
  final Map<String, bool> _followedAuthors = {};
  final Set<String> _followInFlight = {};

  Future<void> _toggleFollow(Post post) async {
    final authorId = post.authorId;
    if (authorId.isEmpty) return;
    if (_followInFlight.contains(authorId)) return;

    final wasFollowing = _followedAuthors[authorId] ?? false;
    setState(() {
      _followInFlight.add(authorId);
      _followedAuthors[authorId] = !wasFollowing;
    });
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
        const SnackBar(content: Text('Could not update follow.')),
      );
    } finally {
      if (mounted) {
        setState(() => _followInFlight.remove(authorId));
      }
    }
  }

  Future<void> _toggleSave(Post post) async {
    final engagement = _ensureEngagement(post);
    final prevSaved = engagement.saved;

    setState(() => engagement.saved = !engagement.saved);

    try {
      await ref.read(postRepositoryProvider).toggleBookmark(post.id);
    } catch (_) {
      if (!mounted) return;
      setState(() => engagement.saved = prevSaved);
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not update save.')));
    }
  }

  Future<void> _shareReel(Post post) async {
    final engagement = _ensureEngagement(post);

    final link = '${Environment.apiBaseUrl}/posts/${post.id}';
    await Clipboard.setData(ClipboardData(text: link));

    if (!mounted) return;
    setState(() => engagement.shareCount += 1);

    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Reel link copied to clipboard.')),
    );
  }

  Future<void> _openComments(Post post) async {
    await context.push('/comments/${post.id}');
  }

  @override
  Widget build(BuildContext context) {
    if (_loadingInitial) {
      return const Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(
          child: CircularProgressIndicator(color: AppColors.postgramPrimary),
        ),
      );
    }

    if (_error != null && _reels.isEmpty) {
      return Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  _error!,
                  textAlign: TextAlign.center,
                  style: AppTextStyles.body,
                ),
                const SizedBox(height: 12),
                ElevatedButton(
                  onPressed: _loadInitial,
                  child: const Text('Retry'),
                ),
              ],
            ),
          ),
        ),
      );
    }

    if (_reels.isEmpty) {
      return Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(
          child: Text(
            'No reels available right now.',
            style: AppTextStyles.body,
          ),
        ),
      );
    }

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: Stack(
        children: [
          PageView.builder(
            controller: _pageController,
            scrollDirection: Axis.vertical,
            itemCount: _reels.length,
            onPageChanged: (_) => _loadMore(),
            itemBuilder: (context, index) {
              final post = _reels[index];
              final engagement = _ensureEngagement(post);
              final colors = _palette[index % _palette.length];

              final canFollow = post.authorId.isNotEmpty;
              final isFollowing = _followedAuthors[post.authorId] ?? false;
              return GestureDetector(
                onDoubleTap: () => _toggleLike(post),
                child: _ReelPage(
                  post: post,
                  engagement: engagement,
                  colors: colors,
                  fullscreenRoute: widget.fullscreenRoute,
                  muted: _muted,
                  onBack: () => context.pop(),
                  onToggleMute: () => setState(() => _muted = !_muted),
                  onLike: () => _toggleLike(post),
                  onDislike: () => _toggleDislike(post),
                  onComment: () => _openComments(post),
                  onShare: () => _shareReel(post),
                  onSave: () => _toggleSave(post),
                  countLabel: _countLabel,
                  isFollowing: isFollowing,
                  onFollow: canFollow ? () => _toggleFollow(post) : null,
                ),
              );
            },
          ),
          if (_loadingMore)
            Positioned(
              left: 0,
              right: 0,
              bottom: widget.fullscreenRoute ? 18 : 100,
              child: const Center(
                child: SizedBox(
                  width: 24,
                  height: 24,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: AppColors.postgramPrimary,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _ReelPage extends StatefulWidget {
  const _ReelPage({
    required this.post,
    required this.engagement,
    required this.colors,
    required this.fullscreenRoute,
    required this.muted,
    required this.onBack,
    required this.onToggleMute,
    required this.onLike,
    required this.onDislike,
    required this.onComment,
    required this.onShare,
    required this.onSave,
    required this.countLabel,
    this.isFollowing = false,
    this.onFollow,
  });

  final Post post;
  final _ReelEngagement engagement;
  final List<Color> colors;
  final bool fullscreenRoute;
  final bool muted;
  final VoidCallback onBack;
  final VoidCallback onToggleMute;
  final VoidCallback onLike;
  final VoidCallback onDislike;
  final VoidCallback onComment;
  final VoidCallback onShare;
  final VoidCallback onSave;
  final String Function(int value) countLabel;
  // Follow state passed in from the parent so the button reflects the latest
  // optimistic value across rebuilds. onFollow is null for the user's own
  // reels — the parent decides who to suppress the button for.
  final bool isFollowing;
  final VoidCallback? onFollow;

  @override
  State<_ReelPage> createState() => _ReelPageState();
}

class _ReelPageState extends State<_ReelPage> {
  final GlobalKey<VideoPlayerWidgetState> _playerKey = GlobalKey();

  String get _title {
    final text = widget.post.content.trim();
    if (text.isNotEmpty) return text;
    return 'New reel from ${widget.post.authorName ?? 'creator'}';
  }

  String get _authorHandle {
    final raw = (widget.post.authorName ?? 'creator').toLowerCase().replaceAll(
      ' ',
      '.',
    );
    return '@$raw';
  }

  String get _tags {
    if (widget.post.tags.isEmpty) return '#reels #atpost';
    return widget.post.tags.take(4).map((tag) => '#$tag').join(' ');
  }

  String get _videoUrl {
    final mediaUrl = widget.post.firstMediaUrl;
    if (mediaUrl.isEmpty) return '';
    return '${Environment.apiBaseUrl}$mediaUrl';
  }

  @override
  Widget build(BuildContext context) {
    final hasVideo = _videoUrl.isNotEmpty;

    // Gradient background placeholder (used behind video or as fallback).
    final gradientBg = DecoratedBox(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: widget.colors,
        ),
      ),
    );

    return Stack(
      children: [
        // Background: video player or gradient fallback.
        Positioned.fill(
          child: hasVideo
              ? VideoPlayerWidget(
                  key: _playerKey,
                  videoUrl: _videoUrl,
                  autoPlay: true,
                  looping: true,
                  showControls: false,
                  placeholder: gradientBg,
                )
              : gradientBg,
        ),
        // Scrim overlay for readability of text/icons.
        Positioned.fill(
          child: IgnorePointer(
            child: DecoratedBox(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [
                    Colors.black.withValues(alpha: 0.35),
                    Colors.transparent,
                    Colors.black.withValues(alpha: 0.55),
                  ],
                ),
              ),
            ),
          ),
        ),
        // If no video, show the old placeholder icon.
        if (!hasVideo)
          Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  width: 110,
                  height: 110,
                  decoration: BoxDecoration(
                    color: Colors.white.withValues(alpha: 0.14),
                    shape: BoxShape.circle,
                    border: Border.all(
                      color: Colors.white.withValues(alpha: 0.2),
                    ),
                  ),
                  child: const Icon(
                    Icons.play_arrow_rounded,
                    size: 52,
                    color: Colors.white,
                  ),
                ),
                const SizedBox(height: 12),
                Text(
                  widget.post.contentType.toUpperCase(),
                  style: AppTextStyles.label.copyWith(color: Colors.white70),
                ),
              ],
            ),
          ),
        // Top bar.
        SafeArea(
          bottom: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 12, 0),
            child: Row(
              children: [
                if (widget.fullscreenRoute)
                  Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: _OverlayIconButton(
                      icon: Icons.arrow_back,
                      onTap: widget.onBack,
                    ),
                  ),
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 6,
                  ),
                  decoration: BoxDecoration(
                    color: AppColors.postgramPrimary.withValues(alpha: 0.2),
                    borderRadius: BorderRadius.circular(999),
                    border: Border.all(
                      color: AppColors.postgramPrimary.withValues(alpha: 0.35),
                    ),
                  ),
                  child: Text(
                    'POSTGRAM REELS',
                    style: AppTextStyles.labelTiny.copyWith(
                      color: AppColors.postgramPrimary,
                    ),
                  ),
                ),
                const Spacer(),
                _OverlayIconButton(
                  icon: widget.muted
                      ? Icons.volume_off_outlined
                      : Icons.volume_up_outlined,
                  onTap: widget.onToggleMute,
                ),
              ],
            ),
          ),
        ),
        // Engagement rail.
        Positioned(
          right: 12,
          bottom: widget.fullscreenRoute ? 132 : 214,
          child: _ActionRail(
            liked: widget.engagement.liked,
            disliked: widget.engagement.disliked,
            saved: widget.engagement.saved,
            likes: widget.countLabel(widget.engagement.likeCount),
            dislikes: widget.countLabel(widget.engagement.dislikeCount),
            comments: widget.countLabel(widget.engagement.commentCount),
            shares: widget.countLabel(widget.engagement.shareCount),
            onLike: widget.onLike,
            onDislike: widget.onDislike,
            onComment: widget.onComment,
            onShare: widget.onShare,
            onSave: widget.onSave,
          ),
        ),
        // Bottom info.
        Positioned(
          left: 12,
          right: 78,
          bottom: widget.fullscreenRoute ? 24 : 98,
          child: _BottomInfo(
            authorHandle: _authorHandle,
            title: _title,
            tags: _tags,
            mediaCount: widget.post.mediaIds.length,
            isFollowing: widget.isFollowing,
            onFollow: widget.onFollow,
          ),
        ),
      ],
    );
  }
}

class _OverlayIconButton extends StatelessWidget {
  const _OverlayIconButton({required this.icon, this.onTap});

  final IconData icon;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 38,
        height: 38,
        decoration: BoxDecoration(
          color: Colors.white.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: Colors.white.withValues(alpha: 0.12)),
        ),
        child: Icon(icon, color: Colors.white, size: 20),
      ),
    );
  }
}

class _ActionRail extends StatelessWidget {
  const _ActionRail({
    required this.liked,
    required this.disliked,
    required this.saved,
    required this.likes,
    required this.dislikes,
    required this.comments,
    required this.shares,
    required this.onLike,
    required this.onDislike,
    required this.onComment,
    required this.onShare,
    required this.onSave,
  });

  final bool liked;
  final bool disliked;
  final bool saved;
  final String likes;
  final String dislikes;
  final String comments;
  final String shares;
  final VoidCallback onLike;
  final VoidCallback onDislike;
  final VoidCallback onComment;
  final VoidCallback onShare;
  final VoidCallback onSave;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _RailButton(
          icon: liked ? Icons.favorite_rounded : Icons.favorite_border_rounded,
          label: likes,
          iconColor: liked ? AppColors.postgramPrimary : Colors.white,
          glow: liked,
          onTap: onLike,
        ),
        const SizedBox(height: 12),
        _RailButton(
          icon: disliked ? Icons.thumb_down_rounded : Icons.thumb_down_outlined,
          label: dislikes,
          iconColor: disliked ? AppColors.postbookPrimary : Colors.white,
          onTap: onDislike,
        ),
        const SizedBox(height: 12),
        _RailButton(
          icon: Icons.chat_bubble_outline_rounded,
          label: comments,
          onTap: onComment,
        ),
        const SizedBox(height: 12),
        _RailButton(icon: Icons.share_outlined, label: shares, onTap: onShare),
        const SizedBox(height: 12),
        _RailButton(
          icon: saved ? Icons.bookmark_rounded : Icons.bookmark_border_rounded,
          label: 'Save',
          iconColor: saved ? AppColors.posttubePrimary : Colors.white,
          onTap: onSave,
        ),
      ],
    );
  }
}

class _RailButton extends StatelessWidget {
  const _RailButton({
    required this.icon,
    required this.label,
    this.iconColor = Colors.white,
    this.glow = false,
    this.onTap,
  });

  final IconData icon;
  final String label;
  final Color iconColor;
  final bool glow;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Column(
        children: [
          Container(
            width: 44,
            height: 44,
            decoration: BoxDecoration(
              color: Colors.white.withValues(alpha: 0.12),
              shape: BoxShape.circle,
              border: Border.all(color: Colors.white.withValues(alpha: 0.16)),
              boxShadow: glow
                  ? const [
                      BoxShadow(
                        color: Color(0x66FF3366),
                        blurRadius: 16,
                        spreadRadius: 1,
                      ),
                    ]
                  : const [],
            ),
            child: Icon(icon, color: iconColor, size: 22),
          ),
          const SizedBox(height: 4),
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _BottomInfo extends StatelessWidget {
  const _BottomInfo({
    required this.authorHandle,
    required this.title,
    required this.tags,
    required this.mediaCount,
    this.isFollowing = false,
    this.onFollow,
  });

  final String authorHandle;
  final String title;
  final String tags;
  final int mediaCount;
  // Follow button is suppressed when onFollow is null (e.g. on the user's
  // own reels). isFollowing toggles between filled "Follow" and outlined
  // "Following" states with optimistic updates handled by the parent.
  final bool isFollowing;
  final VoidCallback? onFollow;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            Container(
              width: 38,
              height: 38,
              decoration: BoxDecoration(
                gradient: AppColors.postbookGradient,
                borderRadius: BorderRadius.circular(12),
              ),
              child: Center(
                child: Text(
                  authorHandle
                      .replaceFirst('@', '')
                      .substring(0, 1)
                      .toUpperCase(),
                  style: AppTextStyles.h3.copyWith(color: Colors.white),
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                authorHandle,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: AppTextStyles.h3.copyWith(color: Colors.white),
              ),
            ),
            if (onFollow != null) ...[
              const SizedBox(width: 8),
              GestureDetector(
                onTap: onFollow,
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 6,
                  ),
                  decoration: BoxDecoration(
                    color: isFollowing
                        ? Colors.white.withValues(alpha: 0.12)
                        : Colors.white,
                    borderRadius: BorderRadius.circular(999),
                    border: Border.all(
                      color: isFollowing
                          ? Colors.white.withValues(alpha: 0.4)
                          : Colors.white,
                    ),
                  ),
                  child: Text(
                    isFollowing ? 'Following' : 'Follow',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: isFollowing ? Colors.white : Colors.black,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
              ),
            ],
          ],
        ),
        const SizedBox(height: 8),
        Text(
          title,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.body.copyWith(color: Colors.white),
        ),
        const SizedBox(height: 5),
        Text(
          tags,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.labelSmall.copyWith(
            color: Colors.white.withValues(alpha: 0.78),
          ),
        ),
        const SizedBox(height: 6),
        Text(
          '$mediaCount media item(s) in this reel',
          style: AppTextStyles.monoSmall.copyWith(
            color: Colors.white.withValues(alpha: 0.7),
          ),
        ),
      ],
    );
  }
}

class _ReelEngagement {
  _ReelEngagement({
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
}
