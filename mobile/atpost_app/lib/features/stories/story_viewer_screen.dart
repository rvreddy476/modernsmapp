import 'dart:async';

import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/providers/stories_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class StoryViewerScreen extends ConsumerStatefulWidget {
  const StoryViewerScreen({super.key, required this.userId});

  final String userId;

  @override
  ConsumerState<StoryViewerScreen> createState() => _StoryViewerScreenState();
}

class _StoryViewerScreenState extends ConsumerState<StoryViewerScreen> {
  int _currentIndex = 0;
  List<StoryItem> _items = [];
  Timer? _timer;
  double _progress = 0;
  bool _storyLoaded = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      // Timer starts after story data is loaded — triggered by _onStoryLoaded
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  void _onStoryLoaded(Story story) {
    if (!_storyLoaded) {
      _storyLoaded = true;
      _items = story.items;
      if (_items.isNotEmpty) {
        _startTimer();
      }
    }
  }

  void _startTimer() {
    _timer?.cancel();
    _progress = 0;
    _timer = Timer.periodic(const Duration(milliseconds: 100), (_) {
      if (mounted) {
        setState(() {
          _progress += 0.02; // 100ms * 0.02 = ~5 second total
          if (_progress >= 1.0) _nextStory();
        });
      }
    });
  }

  void _nextStory() {
    if (_currentIndex < _items.length - 1) {
      setState(() {
        _currentIndex++;
        _progress = 0;
      });
      _startTimer();
    } else {
      context.pop();
    }
  }

  void _prevStory() {
    if (_currentIndex > 0) {
      setState(() {
        _currentIndex--;
        _progress = 0;
      });
      _startTimer();
    }
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d ago';
    if (diff.inHours > 0) return '${diff.inHours}h ago';
    return '${diff.inMinutes}m ago';
  }

  @override
  Widget build(BuildContext context) {
    final storyAsync = ref.watch(userStoryProvider(widget.userId));

    return storyAsync.when(
      loading: () => const Scaffold(
        backgroundColor: Colors.black,
        body: Center(child: CircularProgressIndicator(color: Colors.white)),
      ),
      error: (_, _) => Scaffold(
        backgroundColor: Colors.black,
        body: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                'Could not load story',
                style: AppTextStyles.body.copyWith(color: Colors.white),
              ),
              const SizedBox(height: 16),
              TextButton(
                onPressed: () => context.pop(),
                child: const Text('Go back', style: TextStyle(color: Colors.white)),
              ),
            ],
          ),
        ),
      ),
      data: (story) {
        // Trigger timer setup after first data load
        WidgetsBinding.instance.addPostFrameCallback((_) {
          _onStoryLoaded(story);
        });

        if (story.items.isEmpty) {
          return Scaffold(
            backgroundColor: Colors.black,
            body: Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'No story items',
                    style: AppTextStyles.body.copyWith(color: Colors.white),
                  ),
                  const SizedBox(height: 16),
                  TextButton(
                    onPressed: () => context.pop(),
                    child: const Text('Go back', style: TextStyle(color: Colors.white)),
                  ),
                ],
              ),
            ),
          );
        }

        final item = _items.isNotEmpty ? _items[_currentIndex] : story.items[0];
        final size = MediaQuery.of(context).size;

        return Scaffold(
          backgroundColor: Colors.black,
          body: GestureDetector(
            onTapDown: (details) {
              final x = details.localPosition.dx;
              if (x < size.width / 3) {
                _prevStory();
              } else if (x > size.width * 2 / 3) {
                _nextStory();
              }
            },
            onVerticalDragEnd: (details) {
              if ((details.primaryVelocity ?? 0) > 500) {
                context.pop();
              }
            },
            child: Stack(
              fit: StackFit.expand,
              children: [
                // Background / media
                if (item.mediaType == 'image')
                  Image.network(
                    item.mediaId,
                    fit: BoxFit.cover,
                    errorBuilder: (_, _, _) => Container(
                      color: const Color(0xFF14141F),
                      child: const Center(
                        child: Icon(Icons.image_not_supported, color: Colors.white38, size: 64),
                      ),
                    ),
                  )
                else
                  Container(
                    color: const Color(0xFF14141F),
                    child: const Center(
                      child: Icon(Icons.play_circle_outline, color: Colors.white54, size: 80),
                    ),
                  ),

                // Dark gradient overlay at top and bottom
                DecoratedBox(
                  decoration: BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.topCenter,
                      end: Alignment.center,
                      colors: [Colors.black.withAlpha(160), Colors.transparent],
                    ),
                  ),
                ),
                DecoratedBox(
                  decoration: BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.bottomCenter,
                      end: Alignment.center,
                      colors: [Colors.black.withAlpha(180), Colors.transparent],
                    ),
                  ),
                ),

                // Content
                SafeArea(
                  child: Column(
                    children: [
                      // Progress bars
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 8),
                        child: Row(
                          children: List.generate(
                            _items.isNotEmpty ? _items.length : story.items.length,
                            (i) => Expanded(
                              child: Padding(
                                padding: const EdgeInsets.symmetric(horizontal: 2),
                                child: LinearProgressIndicator(
                                  value: i < _currentIndex
                                      ? 1.0
                                      : i == _currentIndex
                                          ? _progress
                                          : 0.0,
                                  backgroundColor: Colors.white30,
                                  valueColor: const AlwaysStoppedAnimation<Color>(Colors.white),
                                  minHeight: 2,
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),

                      // Author row
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
                        child: Row(
                          children: [
                            CircleAvatar(
                              radius: 18,
                              backgroundColor: Colors.white24,
                              child: Text(
                                story.authorName.isNotEmpty
                                    ? story.authorName[0].toUpperCase()
                                    : '?',
                                style: AppTextStyles.label.copyWith(color: Colors.white),
                              ),
                            ),
                            const SizedBox(width: 10),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(
                                    story.authorName,
                                    style: AppTextStyles.label.copyWith(color: Colors.white),
                                  ),
                                  Text(
                                    _timeAgo(story.createdAt),
                                    style: AppTextStyles.labelSmall
                                        .copyWith(color: Colors.white60),
                                  ),
                                ],
                              ),
                            ),
                            IconButton(
                              icon: const Icon(Icons.close, color: Colors.white),
                              onPressed: () => context.pop(),
                            ),
                          ],
                        ),
                      ),

                      const Spacer(),

                      // Text overlay
                      if (item.text != null && item.text!.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 24),
                          child: Center(
                            child: Container(
                              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                              decoration: BoxDecoration(
                                color: Colors.black54,
                                borderRadius: BorderRadius.circular(12),
                              ),
                              child: Text(
                                item.text!,
                                textAlign: TextAlign.center,
                                style: AppTextStyles.body.copyWith(color: Colors.white),
                              ),
                            ),
                          ),
                        ),

                      const SizedBox(height: 24),
                    ],
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}
