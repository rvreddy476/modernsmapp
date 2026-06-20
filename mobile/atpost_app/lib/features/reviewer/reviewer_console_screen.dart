// Reviewer console — a reviewer watches the next assigned video and either
// APPROVEs it (publishes) or ESCALATEs with comments to the super-admin.
import 'dart:async';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/data/repositories/reviewer_repository.dart';
import 'package:atpost_app/shared/widgets/video_player_widget.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ReviewerConsoleScreen extends ConsumerStatefulWidget {
  const ReviewerConsoleScreen({super.key});

  @override
  ConsumerState<ReviewerConsoleScreen> createState() => _ReviewerConsoleScreenState();
}

class _ReviewerConsoleScreenState extends ConsumerState<ReviewerConsoleScreen> {
  ReviewAssignment? _current;
  String _videoUrl = '';
  bool _loading = true;
  bool _acting = false;
  String? _error;
  Timer? _heartbeat;

  @override
  void initState() {
    super.initState();
    _start();
  }

  @override
  void dispose() {
    _heartbeat?.cancel();
    super.dispose();
  }

  Future<void> _start() async {
    try {
      await ref.read(reviewerRepositoryProvider).optIn();
    } catch (_) {
      // already opted in / non-fatal
    }
    await _loadNext();
  }

  Future<void> _loadNext() async {
    _heartbeat?.cancel();
    setState(() {
      _loading = true;
      _error = null;
      _current = null;
      _videoUrl = '';
    });
    try {
      final a = await ref.read(reviewerRepositoryProvider).next();
      if (a == null) {
        setState(() => _loading = false);
        return;
      }
      // Resolve the playable URL from the post's first media.
      String url = '';
      try {
        final post = await ref.read(postRepositoryProvider).getPostDetail(a.contentId);
        if (post.firstMediaUrl.isNotEmpty) {
          url = '${Environment.apiBaseUrl}${post.firstMediaUrl}';
        }
      } catch (_) {}
      if (!mounted) return;
      setState(() {
        _current = a;
        _videoUrl = url;
        _loading = false;
      });
      _startHeartbeat();
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = 'Could not load the next review. Pull to retry.';
      });
    }
  }

  void _startHeartbeat() {
    _heartbeat?.cancel();
    final a = _current;
    if (a == null) return;
    _heartbeat = Timer.periodic(const Duration(seconds: 10), (_) {
      ref.read(reviewerRepositoryProvider).heartbeat(a.id, 10).catchError((_) {});
    });
  }

  Future<void> _approve() async {
    final a = _current;
    if (a == null) return;
    setState(() => _acting = true);
    try {
      await ref.read(reviewerRepositoryProvider).decide(a.id, 'approve');
      _toast('Approved — published.');
      await _loadNext();
    } catch (_) {
      _toast('Could not submit. Try again.');
    } finally {
      if (mounted) setState(() => _acting = false);
    }
  }

  Future<void> _escalate() async {
    final a = _current;
    if (a == null) return;
    final comments = await _promptComments();
    if (comments == null || comments.trim().isEmpty) return;
    setState(() => _acting = true);
    try {
      await ref.read(reviewerRepositoryProvider).decide(a.id, 'escalate', comments: comments.trim());
      _toast('Escalated to admin.');
      await _loadNext();
    } catch (_) {
      _toast('Could not submit. Try again.');
    } finally {
      if (mounted) setState(() => _acting = false);
    }
  }

  Future<String?> _promptComments() {
    final ctrl = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Escalate to admin', style: AppTextStyles.h3),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          maxLines: 3,
          style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
          decoration: const InputDecoration(hintText: "What's the concern? (required)"),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(ctx, ctrl.text), child: const Text('Escalate')),
        ],
      ),
    );
  }

  void _toast(String m) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(m)));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(title: const Text('Reviewer'), backgroundColor: AppColors.bgPrimary),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return _centered(_error!, action: ('Retry', _loadNext));
    }
    if (_current == null) {
      return _centered("You're all caught up — no videos to review right now.",
          action: ('Check again', _loadNext));
    }
    return Column(
      children: [
        Expanded(
          child: Container(
            color: Colors.black,
            alignment: Alignment.center,
            child: _videoUrl.isEmpty
                ? Text('Preview unavailable',
                    style: AppTextStyles.body.copyWith(color: Colors.white54))
                : VideoPlayerWidget(
                    videoUrl: _videoUrl, autoPlay: true, looping: true, showControls: true),
          ),
        ),
        SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                Expanded(
                  child: OutlinedButton.icon(
                    onPressed: _acting ? null : _escalate,
                    icon: const Icon(Icons.flag_outlined),
                    label: const Text('Escalate'),
                    style: OutlinedButton.styleFrom(
                      foregroundColor: AppColors.statusWarning,
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: FilledButton.icon(
                    onPressed: _acting ? null : _approve,
                    icon: const Icon(Icons.check_rounded),
                    label: const Text('Approve'),
                    style: FilledButton.styleFrom(
                      backgroundColor: const Color(0xFF16A34A),
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }

  Widget _centered(String text, {(String, VoidCallback)? action}) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(text, textAlign: TextAlign.center, style: AppTextStyles.body),
            if (action != null) ...[
              const SizedBox(height: 16),
              FilledButton(onPressed: action.$2, child: Text(action.$1)),
            ],
          ],
        ),
      ),
    );
  }
}
