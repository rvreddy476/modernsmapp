import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/live_stream.dart';
import 'package:atpost_app/data/repositories/live_repository.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:atpost_app/shared/widgets/video_player_widget.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BroadcastScreen extends ConsumerStatefulWidget {
  final String streamId;
  final String title;

  const BroadcastScreen({
    super.key,
    required this.streamId,
    required this.title,
  });

  @override
  ConsumerState<BroadcastScreen> createState() => _BroadcastScreenState();
}

class _BroadcastScreenState extends ConsumerState<BroadcastScreen> {
  final TextEditingController _chatController = TextEditingController();

  LiveStream? _stream;
  List<LiveChatMessage> _messages = const [];
  Timer? _pollTimer;
  bool _loading = true;
  bool _sending = false;
  bool _ending = false;
  bool _joinedAsViewer = false;
  int _viewerCount = 0;
  String? _error;

  @override
  void initState() {
    super.initState();
    _initialize();
    _pollTimer = Timer.periodic(
      const Duration(seconds: 5),
      (_) => _refresh(showLoader: false),
    );
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    _chatController.dispose();
    final stream = _stream;
    if (stream != null && !_isHost(stream) && _joinedAsViewer) {
      unawaited(ref.read(liveRepositoryProvider).leaveStream(stream.id));
    }
    super.dispose();
  }

  Future<void> _initialize() async {
    await _refresh();
    final stream = _stream;
    if (stream == null) return;
    if (_isHost(stream)) return;

    try {
      final viewerCount = await ref.read(liveRepositoryProvider).joinStream(stream.id);
      if (!mounted) return;
      setState(() {
        _joinedAsViewer = true;
        _viewerCount = viewerCount;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not join this live stream.';
      });
    }
  }

  Future<void> _refresh({bool showLoader = true}) async {
    if (showLoader && mounted) {
      setState(() {
        _loading = true;
        _error = null;
      });
    }

    try {
      final repo = ref.read(liveRepositoryProvider);
      final stream = await repo.getStream(widget.streamId);
      final viewerCount = await repo.getViewerCount(widget.streamId);
      final messages = await repo.getChatMessages(widget.streamId, limit: 50);
      messages.sort((a, b) => a.createdAt.compareTo(b.createdAt));

      if (!mounted) return;
      setState(() {
        _stream = stream;
        _viewerCount = viewerCount;
        _messages = messages;
        _loading = false;
        _error = null;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = 'Could not load the live session.';
      });
    }
  }

  Future<void> _sendMessage() async {
    final text = _chatController.text.trim();
    if (text.isEmpty || _sending) return;

    setState(() => _sending = true);
    try {
      final message = await ref
          .read(liveRepositoryProvider)
          .sendChatMessage(widget.streamId, text);
      if (!mounted) return;
      _chatController.clear();
      setState(() {
        _messages = [..._messages, message]..sort((a, b) => a.createdAt.compareTo(b.createdAt));
        _sending = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _sending = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not send live chat message.')),
      );
    }
  }

  Future<void> _likeStream() async {
    final stream = _stream;
    if (stream == null) return;
    try {
      await ref.read(liveRepositoryProvider).likeStream(stream.id);
      await _refresh(showLoader: false);
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not like this stream.')),
      );
    }
  }

  Future<void> _endOrLeave() async {
    final stream = _stream;
    if (stream == null || _ending) return;

    setState(() => _ending = true);
    try {
      final repo = ref.read(liveRepositoryProvider);
      if (_isHost(stream)) {
        await repo.endStream(stream.id);
      } else if (_joinedAsViewer) {
        await repo.leaveStream(stream.id);
      }
      if (!mounted) return;
      context.pop();
    } catch (_) {
      if (!mounted) return;
      setState(() => _ending = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(_isHost(stream) ? 'Could not end stream.' : 'Could not leave stream.'),
        ),
      );
    }
  }

  bool _isHost(LiveStream stream) {
    return stream.hostId == ref.read(authServiceProvider).userId;
  }

  @override
  Widget build(BuildContext context) {
    final stream = _stream;

    return Scaffold(
      backgroundColor: Colors.black,
      body: SafeArea(
        child: _loading && stream == null
            ? const Center(
                child: CircularProgressIndicator(color: AppColors.liveRed),
              )
            : _error != null && stream == null
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(
                            _error!,
                            textAlign: TextAlign.center,
                            style: AppTextStyles.body.copyWith(color: Colors.white70),
                          ),
                          const SizedBox(height: 12),
                          TextButton(
                            onPressed: _refresh,
                            child: const Text('Retry'),
                          ),
                        ],
                      ),
                    ),
                  )
                : Column(
                    children: [
                      _buildTopBar(stream!),
                      Expanded(
                        child: RefreshIndicator(
                          color: AppColors.liveRed,
                          onRefresh: () => _refresh(showLoader: false),
                          child: ListView(
                            physics: const AlwaysScrollableScrollPhysics(),
                            padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
                            children: [
                              _buildPreview(stream),
                              const SizedBox(height: 16),
                              _buildDetails(stream),
                              const SizedBox(height: 16),
                              _buildLiveChatCard(stream),
                            ],
                          ),
                        ),
                      ),
                    ],
                  ),
      ),
    );
  }

  Widget _buildTopBar(LiveStream stream) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back,
            tooltip: 'Back',
            onPressed: _endOrLeave,
          ),
          const SizedBox(width: 10),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
            decoration: BoxDecoration(
              color: AppColors.liveRed,
              borderRadius: BorderRadius.circular(999),
            ),
            child: const Text(
              'LIVE',
              style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              stream.title.isEmpty ? widget.title : stream.title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.h3.copyWith(color: Colors.white),
            ),
          ),
          const SizedBox(width: 10),
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.visibility, color: Colors.white70, size: 16),
              const SizedBox(width: 4),
              Text(
                '$_viewerCount',
                style: AppTextStyles.label.copyWith(color: Colors.white),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildPreview(LiveStream stream) {
    final replayUrl = stream.replayUrl;
    return ClipRRect(
      borderRadius: BorderRadius.circular(18),
      child: AspectRatio(
        aspectRatio: 16 / 9,
        child: replayUrl != null && replayUrl.isNotEmpty
            ? VideoPlayerWidget(
                videoUrl: replayUrl,
                autoPlay: true,
                looping: false,
                showControls: true,
              )
            : Container(
                decoration: const BoxDecoration(
                  gradient: LinearGradient(
                    colors: [Color(0xFF1B1B1B), Color(0xFF3B1222), Color(0xFF12333B)],
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                  ),
                ),
                child: Padding(
                  padding: const EdgeInsets.all(18),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      Text(
                        _isHost(stream) ? 'Live control plane connected' : 'Live session joined',
                        style: AppTextStyles.h2.copyWith(color: Colors.white),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        _isHost(stream)
                            ? 'Use your broadcaster with the stream key below. Playback is not exposed by the current mobile/backend contract.'
                            : 'Realtime chat and viewer presence are active. The backend does not currently expose a mobile playback URL for active live video.',
                        style: AppTextStyles.bodySmall.copyWith(color: Colors.white70),
                      ),
                      if (_isHost(stream) && (stream.streamKey ?? '').isNotEmpty) ...[
                        const SizedBox(height: 14),
                        Container(
                          padding: const EdgeInsets.all(12),
                          decoration: BoxDecoration(
                            color: Colors.black.withOpacity(0.28),
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: Colors.white12),
                          ),
                          child: SelectableText(
                            'Stream key: ${stream.streamKey}',
                            style: AppTextStyles.monoSmall.copyWith(color: Colors.white),
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
              ),
      ),
    );
  }

  Widget _buildDetails(LiveStream stream) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: const Color(0xFF111111),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: Colors.white10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            stream.title.isEmpty ? widget.title : stream.title,
            style: AppTextStyles.h2.copyWith(color: Colors.white),
          ),
          if (stream.description.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              stream.description,
              style: AppTextStyles.body.copyWith(color: Colors.white70),
            ),
          ],
          const SizedBox(height: 12),
          Wrap(
            spacing: 12,
            runSpacing: 8,
            children: [
              _metaPill('Host ${stream.hostId}'),
              _metaPill(stream.visibility.toUpperCase()),
              _metaPill('Likes ${stream.likeCount}'),
              _metaPill('Peak ${stream.peakViewers}'),
            ],
          ),
          const SizedBox(height: 14),
          Row(
            children: [
              ElevatedButton.icon(
                onPressed: _likeStream,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.liveRed,
                  foregroundColor: Colors.white,
                ),
                icon: const Icon(Icons.favorite_border),
                label: Text('${stream.likeCount} likes'),
              ),
              const SizedBox(width: 10),
              OutlinedButton.icon(
                onPressed: _ending ? null : _endOrLeave,
                style: OutlinedButton.styleFrom(
                  foregroundColor: Colors.white,
                  side: const BorderSide(color: Colors.white24),
                ),
                icon: Icon(_isHost(stream) ? Icons.stop_circle_outlined : Icons.logout),
                label: Text(_isHost(stream) ? 'End stream' : 'Leave'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildLiveChatCard(LiveStream stream) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: const Color(0xFF101214),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: Colors.white10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                'Live Chat',
                style: AppTextStyles.h3.copyWith(color: Colors.white),
              ),
              const Spacer(),
              Text(
                '${_messages.length} messages',
                style: AppTextStyles.labelSmall.copyWith(color: Colors.white60),
              ),
            ],
          ),
          const SizedBox(height: 12),
          SizedBox(
            height: 260,
            child: _messages.isEmpty
                ? Center(
                    child: Text(
                      'No chat messages yet.',
                      style: AppTextStyles.bodySmall.copyWith(color: Colors.white54),
                    ),
                  )
                : ListView.separated(
                    itemCount: _messages.length,
                    separatorBuilder: (_, __) => const SizedBox(height: 8),
                    itemBuilder: (context, index) {
                      final message = _messages[index];
                      return Container(
                        padding: const EdgeInsets.all(10),
                        decoration: BoxDecoration(
                          color: Colors.white.withOpacity(0.04),
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              message.userId,
                              style: AppTextStyles.labelSmall.copyWith(color: Colors.white70),
                            ),
                            const SizedBox(height: 4),
                            Text(
                              message.message,
                              style: AppTextStyles.body.copyWith(color: Colors.white),
                            ),
                          ],
                        ),
                      );
                    },
                  ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _chatController,
                  style: const TextStyle(color: Colors.white),
                  decoration: InputDecoration(
                    hintText: 'Send a live message',
                    hintStyle: const TextStyle(color: Colors.white54),
                    filled: true,
                    fillColor: Colors.white.withOpacity(0.06),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(14),
                      borderSide: BorderSide.none,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 10),
              ElevatedButton(
                onPressed: _sending ? null : _sendMessage,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.liveRed,
                  foregroundColor: Colors.white,
                ),
                child: _sending
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                      )
                    : const Text('Send'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _metaPill(String label) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: Colors.white.withOpacity(0.05),
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: Colors.white10),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(color: Colors.white70),
      ),
    );
  }
}

