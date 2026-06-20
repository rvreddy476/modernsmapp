// Video-scoped search — opened from the Reels / PostTube search button.
//
// Unlike the global SearchTab, this searches ONLY video posts and lays them
// out the way the product wants: short videos in a horizontal rail, long-form
// videos in a vertical list. Tapping a result resolves the full post and plays
// it in a lightweight full-screen player.

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/search_results.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/data/repositories/search_repository.dart';
import 'package:atpost_app/shared/widgets/video_player_widget.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class VideoSearchScreen extends ConsumerStatefulWidget {
  const VideoSearchScreen({super.key});

  @override
  ConsumerState<VideoSearchScreen> createState() => _VideoSearchScreenState();
}

class _VideoSearchScreenState extends ConsumerState<VideoSearchScreen> {
  final TextEditingController _controller = TextEditingController();
  final FocusNode _focus = FocusNode();

  bool _loading = false;
  bool _searched = false;
  String? _error;
  List<PostHit> _shorts = const [];
  List<PostHit> _longs = const [];

  @override
  void dispose() {
    _controller.dispose();
    _focus.dispose();
    super.dispose();
  }

  static bool _isShort(String? t) {
    final v = t?.toLowerCase();
    return v == 'reel' || v == 'flick' || v == 'short';
  }

  static bool _isLong(String? t) {
    final v = t?.toLowerCase();
    return v == 'video' || v == 'long_video' || v == 'posttube' || v == 'long';
  }

  Future<void> _runSearch() async {
    final q = _controller.text.trim();
    if (q.isEmpty) return;
    _focus.unfocus();
    setState(() {
      _loading = true;
      _error = null;
      _searched = true;
    });
    try {
      final results = await ref.read(searchRepositoryProvider).multiEntitySearch(
            query: q,
            types: [SearchEntity.posts],
            limit: 40,
          );
      final hits = results.posts.items;
      if (!mounted) return;
      setState(() {
        _shorts = hits.where((h) => _isShort(h.postType)).toList();
        _longs = hits.where((h) => _isLong(h.postType)).toList();
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not search right now. Please try again.';
        _loading = false;
      });
    }
  }

  Future<void> _openVideo(PostHit hit) async {
    final messenger = ScaffoldMessenger.of(context);
    final navigator = Navigator.of(context);
    try {
      final post = await ref.read(postRepositoryProvider).getPostDetail(hit.postId);
      final mediaUrl = post.firstMediaUrl;
      if (mediaUrl.isEmpty) {
        messenger.showSnackBar(
          const SnackBar(content: Text('This video is not available.')),
        );
        return;
      }
      navigator.push(
        MaterialPageRoute<void>(
          builder: (_) => _VideoPlayerScreen(
            url: '${Environment.apiBaseUrl}$mediaUrl',
            title: post.content,
            isShort: _isShort(post.contentType),
          ),
        ),
      );
    } catch (_) {
      messenger.showSnackBar(
        const SnackBar(content: Text('Could not open this video.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        titleSpacing: 0,
        title: _SearchField(
          controller: _controller,
          focus: _focus,
          onSubmitted: (_) => _runSearch(),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.search_rounded),
            color: AppColors.textPrimary,
            onPressed: _runSearch,
          ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Text(_error!, style: AppTextStyles.body),
      );
    }
    if (!_searched) {
      return _Hint(
        icon: Icons.video_library_outlined,
        text: 'Search for short and long videos',
      );
    }
    if (_shorts.isEmpty && _longs.isEmpty) {
      return _Hint(
        icon: Icons.search_off_rounded,
        text: 'No videos found for "${_controller.text.trim()}"',
      );
    }
    return ListView(
      padding: const EdgeInsets.only(bottom: 24),
      children: [
        if (_shorts.isNotEmpty) ...[
          const _SectionHeader('Shorts'),
          SizedBox(
            height: 200,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 16),
              itemCount: _shorts.length,
              separatorBuilder: (_, _) => const SizedBox(width: 12),
              itemBuilder: (_, i) =>
                  _ShortCard(hit: _shorts[i], onTap: () => _openVideo(_shorts[i])),
            ),
          ),
          const SizedBox(height: 8),
        ],
        if (_longs.isNotEmpty) ...[
          const _SectionHeader('Videos'),
          ..._longs.map(
            (h) => _LongCard(hit: h, onTap: () => _openVideo(h)),
          ),
        ],
      ],
    );
  }
}

class _SearchField extends StatelessWidget {
  const _SearchField({
    required this.controller,
    required this.focus,
    required this.onSubmitted,
  });

  final TextEditingController controller;
  final FocusNode focus;
  final ValueChanged<String> onSubmitted;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 42,
      margin: const EdgeInsets.only(right: 8),
      padding: const EdgeInsets.symmetric(horizontal: 14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      alignment: Alignment.center,
      child: TextField(
        controller: controller,
        focusNode: focus,
        autofocus: true,
        textInputAction: TextInputAction.search,
        onSubmitted: onSubmitted,
        style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
        decoration: InputDecoration(
          isCollapsed: true,
          filled: false,
          border: InputBorder.none,
          enabledBorder: InputBorder.none,
          focusedBorder: InputBorder.none,
          hintText: 'Search videos',
          hintStyle: AppTextStyles.body.copyWith(color: AppColors.textGhost),
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.label);
  final String label;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 18, 16, 10),
      child: Text(label, style: AppTextStyles.h3),
    );
  }
}

class _ShortCard extends StatelessWidget {
  const _ShortCard({required this.hit, required this.onTap});
  final PostHit hit;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: SizedBox(
        width: 124,
        child: ClipRRect(
          borderRadius: BorderRadius.circular(14),
          child: Stack(
            fit: StackFit.expand,
            children: [
              const DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                    colors: [Color(0xFF2A1B3D), Color(0xFF11121C)],
                  ),
                ),
              ),
              const Center(
                child: Icon(Icons.play_circle_fill_rounded,
                    color: Colors.white70, size: 40),
              ),
              Positioned(
                left: 8,
                right: 8,
                bottom: 8,
                child: Text(
                  hit.text.isEmpty ? 'Short' : hit.text,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.labelSmall.copyWith(
                    color: Colors.white,
                    fontWeight: FontWeight.w600,
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

class _LongCard extends StatelessWidget {
  const _LongCard({required this.hit, required this.onTap});
  final PostHit hit;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: Container(
                width: 150,
                height: 84,
                decoration: const BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                    colors: [Color(0xFF12303A), Color(0xFF0E1018)],
                  ),
                ),
                alignment: Alignment.center,
                child: const Icon(Icons.play_circle_fill_rounded,
                    color: Colors.white70, size: 36),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    hit.text.isEmpty ? 'Video' : hit.text,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.body.copyWith(
                      color: AppColors.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 6),
                  Text(
                    '@${hit.authorUsername ?? 'user'}',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.textTertiary),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    '${hit.likeCount} likes • ${hit.commentCount} comments',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.textDim),
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

class _Hint extends StatelessWidget {
  const _Hint({required this.icon, required this.text});
  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, color: AppColors.textDim, size: 48),
          const SizedBox(height: 12),
          Text(
            text,
            textAlign: TextAlign.center,
            style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
          ),
        ],
      ),
    );
  }
}

class _VideoPlayerScreen extends StatelessWidget {
  const _VideoPlayerScreen({
    required this.url,
    required this.title,
    required this.isShort,
  });

  final String url;
  final String title;
  final bool isShort;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: Text(
          title.isEmpty ? 'Video' : title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.h3.copyWith(color: Colors.white),
        ),
      ),
      body: Center(
        child: VideoPlayerWidget(
          videoUrl: url,
          autoPlay: true,
          looping: isShort,
          showControls: true,
        ),
      ),
    );
  }
}
