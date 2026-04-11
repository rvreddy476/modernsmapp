import 'dart:async';

import 'package:atpost_app/core/errors/app_exception.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/live_stream.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/live_repository.dart';
import 'package:atpost_app/features/live/live_whip_publisher.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:atpost_app/shared/widgets/video_player_widget.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:go_router/go_router.dart';

enum _LiveMessageAction { pin, mute }

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
  final TextEditingController _wordFilterController = TextEditingController();
  final ScrollController _chatScrollController = ScrollController();
  final LiveWhipPublisher _publisher = LiveWhipPublisher();
  StreamSubscription<RealtimeEvent>? _realtimeSub;

  LiveStream? _stream;
  List<LiveChatMessage> _messages = const [];
  List<LiveMute> _mutedUsers = const [];
  List<LiveWordFilter> _wordFilters = const [];
  bool _loading = true;
  bool _sending = false;
  bool _ending = false;
  bool _moderating = false;
  bool _joinedAsViewer = false;
  bool _selfMuted = false;
  int _viewerCount = 0;
  String? _error;
  String? _pinnedMessageId;

  String? get _currentUserId => ref.read(authServiceProvider).userId;
  bool get _hostCanStart =>
      _stream != null &&
      _isHost(_stream!) &&
      !_stream!.isEnded &&
      !_publisher.isPublishing &&
      !_publisher.isBusy &&
      _stream!.canPublishFromMobile;

  @override
  void initState() {
    super.initState();
    _publisher.addListener(_handlePublisherChanged);
    _subscribeToRealtime();
    unawaited(_initialize());
  }

  @override
  void dispose() {
    _publisher.removeListener(_handlePublisherChanged);
    _realtimeSub?.cancel();
    ref
        .read(realtimeServiceProvider)
        .unsubscribeFromLiveStream(widget.streamId);
    _chatController.dispose();
    _wordFilterController.dispose();
    _chatScrollController.dispose();
    final stream = _stream;
    if (stream != null && !_isHost(stream) && _joinedAsViewer) {
      unawaited(ref.read(liveRepositoryProvider).leaveStream(stream.id));
    }
    unawaited(_publisher.disposeAsync());
    super.dispose();
  }

  Future<void> _initialize() async {
    await _refresh();
    final stream = _stream;
    if (stream == null) return;
    if (_isHost(stream)) {
      unawaited(_prepareHostPublisher(stream));
      return;
    }

    try {
      final viewerCount = await ref
          .read(liveRepositoryProvider)
          .joinStream(stream.id);
      if (!mounted) return;
      setState(() {
        _joinedAsViewer = true;
        _viewerCount = viewerCount;
      });
    } catch (error) {
      if (!mounted) return;
      setState(() {
        _error = _errorText(error, 'Could not join this live stream.');
      });
    }
  }

  void _subscribeToRealtime() {
    final realtime = ref.read(realtimeServiceProvider);
    realtime.subscribeToLiveStream(widget.streamId);
    _realtimeSub?.cancel();
    _realtimeSub = realtime.events.listen(_handleRealtimeEvent);
  }

  void _handlePublisherChanged() {
    if (!mounted) return;
    setState(() {});
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
      final mutedUsers = await repo.getMutedUsers(widget.streamId);
      final wordFilters = _isHostId(stream.hostId)
          ? await repo.getWordFilters(widget.streamId)
          : const <LiveWordFilter>[];
      messages.sort((a, b) => a.createdAt.compareTo(b.createdAt));

      if (!mounted) return;
      setState(() {
        _stream = stream;
        _viewerCount = viewerCount;
        _messages = messages;
        _mutedUsers = mutedUsers;
        _wordFilters = wordFilters;
        _selfMuted =
            _currentUserId != null &&
            mutedUsers.any((mute) => mute.userId == _currentUserId);
        _pinnedMessageId = _resolvePinnedMessageId(messages, _pinnedMessageId);
        _loading = false;
        _error = null;
      });
      if (_isHost(stream)) {
        unawaited(_prepareHostPublisher(stream));
      }
      _scrollChatToBottom(jump: true);
    } catch (error) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = _errorText(error, 'Could not load the live session.');
      });
    }
  }

  Future<void> _prepareHostPublisher(LiveStream stream) async {
    if (!_isHost(stream) ||
        stream.isEnded ||
        !stream.canPublishFromMobile ||
        _publisher.hasPreview ||
        _publisher.isPreparingPreview) {
      return;
    }
    try {
      await _publisher.ensurePreview(stream);
    } catch (error) {
      if (!mounted) return;
      _showSnack(
        _errorText(error, 'Camera preview could not be initialized.'),
      );
    }
  }

  void _handleRealtimeEvent(RealtimeEvent event) {
    if (!mounted) return;
    if (event is LiveChatMessageEvent && event.streamId == widget.streamId) {
      final message = LiveChatMessage(
        id: event.messageId,
        streamId: event.streamId,
        userId: event.userId,
        message: event.message,
        isPinned: event.isPinned,
        createdAt: event.createdAt,
      );
      setState(() {
        _messages = _upsertMessage(_messages, message);
        if (message.isPinned) _pinnedMessageId = message.id;
      });
      _scrollChatToBottom();
      return;
    }
    if (event is LiveStreamViewersEvent && event.streamId == widget.streamId) {
      setState(() {
        _viewerCount = event.viewerCount;
        if (_stream != null) {
          _stream = _stream!.copyWith(
            peakViewers: event.peakViewers ?? _stream!.peakViewers,
            totalViewers: event.totalViewers ?? _stream!.totalViewers,
          );
        }
      });
      return;
    }
    if (event is LiveStreamLikesEvent && event.streamId == widget.streamId) {
      if (_stream == null) return;
      setState(() {
        _stream = _stream!.copyWith(likeCount: event.likeCount);
      });
      return;
    }
    if (event is LiveMessagePinnedEvent && event.streamId == widget.streamId) {
      setState(() {
        _messages = _setPinnedMessage(_messages, event.messageId);
        _pinnedMessageId = event.messageId;
      });
      return;
    }
    if (event is LiveStreamEndedEvent && event.streamId == widget.streamId) {
      if (_stream == null) return;
      setState(() {
        _stream = _stream!.copyWith(
          status: 'ended',
          endedAt: event.endedAt,
          peakViewers: event.peakViewers ?? _stream!.peakViewers,
          totalViewers: event.totalViewers ?? _stream!.totalViewers,
        );
      });
      _showSnack('This stream has ended.');
      return;
    }
    if (event is LiveUserMutedEvent && event.streamId == widget.streamId) {
      final mute = LiveMute(
        streamId: event.streamId,
        userId: event.userId,
        mutedBy: event.mutedBy,
        mutedAt: event.mutedAt,
      );
      final isSelf = event.userId == _currentUserId;
      setState(() {
        _mutedUsers = _upsertMute(_mutedUsers, mute);
        if (isSelf) _selfMuted = true;
      });
      if (isSelf) _showSnack('You have been muted in this stream.');
      return;
    }
    if (event is LiveUserUnmutedEvent && event.streamId == widget.streamId) {
      final isSelf = event.userId == _currentUserId;
      setState(() {
        _mutedUsers = _mutedUsers
            .where((mute) => mute.userId != event.userId)
            .toList(growable: false);
        if (isSelf) _selfMuted = false;
      });
      if (isSelf) _showSnack('You can chat again.');
      return;
    }
    if (event is LiveWordFilterAddedEvent &&
        event.streamId == widget.streamId) {
      setState(() {
        _wordFilters = _upsertWordFilter(
          _wordFilters,
          LiveWordFilter(
            streamId: event.streamId,
            word: event.word,
            addedBy: event.addedBy,
          ),
        );
      });
      return;
    }
    if (event is LiveWordFilterRemovedEvent &&
        event.streamId == widget.streamId) {
      setState(() {
        _wordFilters = _wordFilters
            .where(
              (filter) => filter.word.toLowerCase() != event.word.toLowerCase(),
            )
            .toList(growable: false);
      });
    }
  }

  Future<void> _sendMessage() async {
    final text = _chatController.text.trim();
    if (text.isEmpty || _sending || _selfMuted || (_stream?.isEnded ?? false)) {
      return;
    }

    setState(() => _sending = true);
    try {
      final message = await ref
          .read(liveRepositoryProvider)
          .sendChatMessage(widget.streamId, text);
      if (!mounted) return;
      _chatController.clear();
      setState(() {
        _messages = _upsertMessage(_messages, message);
      });
      _scrollChatToBottom();
    } catch (error) {
      if (!mounted) return;
      if (_errorText(error, '').toLowerCase().contains('muted')) {
        setState(() => _selfMuted = true);
      }
      _showSnack(_errorText(error, 'Could not send live chat message.'));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _likeStream() async {
    final stream = _stream;
    if (stream == null || stream.isEnded) return;
    setState(() {
      _stream = stream.copyWith(likeCount: stream.likeCount + 1);
    });
    try {
      await ref.read(liveRepositoryProvider).likeStream(stream.id);
    } catch (error) {
      if (!mounted) return;
      setState(() {
        _stream = stream;
      });
      _showSnack(_errorText(error, 'Could not like this stream.'));
    }
  }

  Future<void> _pinMessage(LiveChatMessage message) async {
    if (_moderating) return;
    setState(() => _moderating = true);
    try {
      await ref
          .read(liveRepositoryProvider)
          .pinMessage(widget.streamId, message.id);
      if (!mounted) return;
      setState(() {
        _messages = _setPinnedMessage(_messages, message.id);
        _pinnedMessageId = message.id;
      });
      _showSnack('Message pinned.');
    } catch (error) {
      if (!mounted) return;
      _showSnack(_errorText(error, 'Could not pin this message.'));
    } finally {
      if (mounted) setState(() => _moderating = false);
    }
  }

  Future<void> _muteUser(String userId) async {
    if (_moderating || userId.isEmpty) return;
    final optimisticMute = LiveMute(
      streamId: widget.streamId,
      userId: userId,
      mutedBy: _currentUserId ?? '',
      mutedAt: DateTime.now(),
    );
    setState(() {
      _moderating = true;
      _mutedUsers = _upsertMute(_mutedUsers, optimisticMute);
      if (userId == _currentUserId) _selfMuted = true;
    });
    try {
      await ref.read(liveRepositoryProvider).muteUser(widget.streamId, userId);
      if (!mounted) return;
      _showSnack('User muted.');
    } catch (error) {
      if (!mounted) return;
      setState(() {
        _mutedUsers = _mutedUsers
            .where((mute) => mute.userId != userId)
            .toList(growable: false);
        if (userId == _currentUserId) _selfMuted = false;
      });
      _showSnack(_errorText(error, 'Could not mute this user.'));
    } finally {
      if (mounted) setState(() => _moderating = false);
    }
  }

  Future<void> _unmuteUser(String userId) async {
    if (_moderating || userId.isEmpty) return;
    final previous = _mutedUsers;
    final isSelf = userId == _currentUserId;
    setState(() {
      _moderating = true;
      _mutedUsers = _mutedUsers
          .where((mute) => mute.userId != userId)
          .toList(growable: false);
      if (isSelf) _selfMuted = false;
    });
    try {
      await ref
          .read(liveRepositoryProvider)
          .unmuteUser(widget.streamId, userId);
      if (!mounted) return;
      _showSnack('User unmuted.');
    } catch (error) {
      if (!mounted) return;
      setState(() {
        _mutedUsers = previous;
        if (isSelf) _selfMuted = true;
      });
      _showSnack(_errorText(error, 'Could not unmute this user.'));
    } finally {
      if (mounted) setState(() => _moderating = false);
    }
  }

  Future<void> _addWordFilter() async {
    final word = _wordFilterController.text.trim();
    if (_moderating || word.isEmpty) return;
    final optimistic = LiveWordFilter(
      streamId: widget.streamId,
      word: word,
      addedBy: _currentUserId ?? '',
    );
    setState(() {
      _moderating = true;
      _wordFilters = _upsertWordFilter(_wordFilters, optimistic);
    });
    try {
      await ref
          .read(liveRepositoryProvider)
          .addWordFilter(widget.streamId, word);
      if (!mounted) return;
      _wordFilterController.clear();
      _showSnack('Blocked word added.');
    } catch (error) {
      if (!mounted) return;
      setState(() {
        _wordFilters = _wordFilters
            .where((filter) => filter.word.toLowerCase() != word.toLowerCase())
            .toList(growable: false);
      });
      _showSnack(_errorText(error, 'Could not add this word filter.'));
    } finally {
      if (mounted) setState(() => _moderating = false);
    }
  }

  Future<void> _removeWordFilter(String word) async {
    if (_moderating || word.isEmpty) return;
    final previous = _wordFilters;
    setState(() {
      _moderating = true;
      _wordFilters = _wordFilters
          .where((filter) => filter.word.toLowerCase() != word.toLowerCase())
          .toList(growable: false);
    });
    try {
      await ref
          .read(liveRepositoryProvider)
          .removeWordFilter(widget.streamId, word);
      if (!mounted) return;
      _showSnack('Blocked word removed.');
    } catch (error) {
      if (!mounted) return;
      setState(() => _wordFilters = previous);
      _showSnack(_errorText(error, 'Could not remove this word filter.'));
    } finally {
      if (mounted) setState(() => _moderating = false);
    }
  }

  Future<void> _startHostBroadcast() async {
    final stream = _stream;
    if (stream == null ||
        !_isHost(stream) ||
        stream.isEnded ||
        _publisher.isBusy ||
        _publisher.isPublishing) {
      return;
    }

    final repo = ref.read(liveRepositoryProvider);
    try {
      await _prepareHostPublisher(stream);
      await _publisher.startPublishing(stream: stream, repository: repo);
      if (!stream.isLive) {
        await repo.goLive(stream.id);
      }
      if (!mounted) return;
      setState(() {
        _stream = stream.copyWith(
          status: 'live',
          startedAt: stream.startedAt ?? DateTime.now(),
        );
      });
      _showSnack('Mobile live publishing started.');
    } catch (error) {
      try {
        await _publisher.stopPublishing(
          stream: stream,
          repository: repo,
          preservePreview: true,
        );
      } catch (_) {}
      if (!mounted) return;
      _showSnack(_errorText(error, 'Could not start mobile live publishing.'));
    }
  }

  Future<void> _stopHostBroadcast({bool preservePreview = false}) async {
    final stream = _stream;
    if (stream == null || !_isHost(stream) || _publisher.isBusy) return;

    final repo = ref.read(liveRepositoryProvider);
    Object? publishError;

    try {
      await _publisher.stopPublishing(
        stream: stream,
        repository: repo,
        preservePreview: preservePreview,
      );
    } catch (error) {
      publishError = error;
    }

    try {
      if (stream.isLive) {
        await repo.endStream(stream.id);
      }
      if (!mounted) return;
      setState(() {
        _stream = stream.copyWith(
          status: 'ended',
          endedAt: DateTime.now(),
        );
      });
      if (publishError == null) {
        _showSnack('Live broadcast stopped.');
      }
    } catch (error) {
      if (!mounted) return;
      _showSnack(_errorText(error, 'Could not end this live stream.'));
      return;
    }

    if (publishError != null && mounted) {
      _showSnack(
        _errorText(
          publishError,
          'The stream ended, but the publisher session did not close cleanly.',
        ),
      );
    }
  }

  void _toggleHostMic() {
    _publisher.toggleAudioEnabled();
  }

  void _toggleHostCamera() {
    _publisher.toggleVideoEnabled();
  }

  Future<void> _endOrLeave() async {
    final stream = _stream;
    if (stream == null || _ending) return;

    setState(() => _ending = true);
    try {
      final repo = ref.read(liveRepositoryProvider);
      if (_isHost(stream)) {
        if (_publisher.isPublishing || _publisher.hasSession) {
          try {
            await _publisher.stopPublishing(
              stream: stream,
              repository: repo,
              preservePreview: false,
            );
          } catch (_) {}
        } else {
          await _publisher.disposeAsync();
        }
        await repo.endStream(stream.id);
      } else if (_joinedAsViewer) {
        await repo.leaveStream(stream.id);
      }
      if (!mounted) return;
      context.pop();
    } catch (error) {
      if (!mounted) return;
      setState(() => _ending = false);
      _showSnack(
        _errorText(
          error,
          _isHost(stream) ? 'Could not end stream.' : 'Could not leave stream.',
        ),
      );
    }
  }

  bool _isHost(LiveStream stream) => _isHostId(stream.hostId);

  bool _isHostId(String hostId) =>
      _currentUserId != null && hostId == _currentUserId;

  bool _isUserMuted(String userId) =>
      _mutedUsers.any((mute) => mute.userId == userId);

  LiveChatMessage? get _pinnedMessage {
    if (_pinnedMessageId != null) {
      for (final message in _messages) {
        if (message.id == _pinnedMessageId) return message;
      }
    }
    for (final message in _messages.reversed) {
      if (message.isPinned) return message;
    }
    return null;
  }

  List<LiveChatMessage> _upsertMessage(
    List<LiveChatMessage> items,
    LiveChatMessage incoming,
  ) {
    final next = [...items];
    final index = next.indexWhere((message) => message.id == incoming.id);
    if (index >= 0) {
      next[index] = incoming;
    } else {
      next.add(incoming);
    }
    next.sort((a, b) => a.createdAt.compareTo(b.createdAt));
    return next;
  }

  List<LiveChatMessage> _setPinnedMessage(
    List<LiveChatMessage> items,
    String messageId,
  ) {
    return items
        .map(
          (message) => message.id == messageId
              ? message.copyWith(isPinned: true)
              : message,
        )
        .toList(growable: false);
  }

  List<LiveMute> _upsertMute(List<LiveMute> items, LiveMute incoming) {
    final next = [
      ...items.where((mute) => mute.userId != incoming.userId),
      incoming,
    ];
    next.sort((a, b) => a.mutedAt.compareTo(b.mutedAt));
    return next;
  }

  List<LiveWordFilter> _upsertWordFilter(
    List<LiveWordFilter> items,
    LiveWordFilter incoming,
  ) {
    return [
      ...items.where(
        (filter) => filter.word.toLowerCase() != incoming.word.toLowerCase(),
      ),
      incoming,
    ]..sort((a, b) => a.word.compareTo(b.word));
  }

  String? _resolvePinnedMessageId(
    List<LiveChatMessage> items,
    String? existing,
  ) {
    if (existing != null && items.any((message) => message.id == existing)) {
      return existing;
    }
    for (final message in items.reversed) {
      if (message.isPinned) return message.id;
    }
    return null;
  }

  void _scrollChatToBottom({bool jump = false}) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_chatScrollController.hasClients) return;
      final offset = _chatScrollController.position.maxScrollExtent;
      if (jump) {
        _chatScrollController.jumpTo(offset);
      } else {
        _chatScrollController.animateTo(
          offset,
          duration: const Duration(milliseconds: 220),
          curve: Curves.easeOut,
        );
      }
    });
  }

  void _showSnack(String message) {
    if (!mounted || message.isEmpty) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  String _errorText(Object error, String fallback) {
    if (error is AppException) {
      if (error.message.isNotEmpty) return error.message;
      return error.userMessage;
    }
    return fallback;
  }

  String _shortUserId(String userId) =>
      userId.length <= 8 ? userId : '${userId.substring(0, 8)}...';

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
                        style: AppTextStyles.body.copyWith(
                          color: Colors.white70,
                        ),
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
                          if (_isHost(stream)) ...[
                            const SizedBox(height: 16),
                            _buildModerationCard(),
                          ],
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
              color: stream.isEnded ? Colors.white24 : AppColors.liveRed,
              borderRadius: BorderRadius.circular(999),
            ),
            child: Text(
              stream.isEnded ? 'ENDED' : 'LIVE',
              style: TextStyle(
                color: Colors.white,
                fontWeight: FontWeight.bold,
              ),
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
    final videoUrl = stream.preferredVideoUrl;
    return ClipRRect(
      borderRadius: BorderRadius.circular(18),
      child: AspectRatio(
        aspectRatio: 16 / 9,
        child: _isHost(stream) && _publisher.hasPreview
            ? Stack(
                fit: StackFit.expand,
                children: [
                  RTCVideoView(
                    _publisher.previewRenderer,
                    mirror: true,
                    objectFit: RTCVideoViewObjectFit.RTCVideoViewObjectFitCover,
                  ),
                  Positioned(
                    left: 12,
                    top: 12,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 10,
                        vertical: 6,
                      ),
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: 0.45),
                        borderRadius: BorderRadius.circular(999),
                        border: Border.all(color: Colors.white24),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(
                            _publisher.isPublishing
                                ? Icons.radio_button_checked
                                : Icons.videocam,
                            size: 14,
                            color: _publisher.isPublishing
                                ? AppColors.liveRed
                                : Colors.white70,
                          ),
                          const SizedBox(width: 6),
                          Text(
                            _publisher.isPublishing
                                ? 'Camera live from mobile'
                                : 'Camera preview ready',
                            style: AppTextStyles.labelSmall.copyWith(
                              color: Colors.white,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              )
            : videoUrl != null && videoUrl.isNotEmpty
            ? VideoPlayerWidget(
                videoUrl: videoUrl,
                autoPlay: true,
                looping: false,
                showControls: true,
              )
            : Container(
                decoration: const BoxDecoration(
                  gradient: LinearGradient(
                    colors: [
                      Color(0xFF1B1B1B),
                      Color(0xFF3B1222),
                      Color(0xFF12333B),
                    ],
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
                        _isHost(stream)
                            ? 'Live control plane connected'
                            : stream.isEnded
                            ? 'Live session ended'
                            : 'Live session joined',
                        style: AppTextStyles.h2.copyWith(color: Colors.white),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        _isHost(stream)
                            ? _publisher.hasPreview
                                  ? _publisher.isPublishing
                                        ? 'Your phone camera and microphone are publishing directly into this live room. Viewer playback appears as soon as the playback manifest is ready.'
                                        : 'Your phone camera preview is ready. Start mobile publishing below to send this camera feed into the live room.'
                                  : stream.hasLivePlayback
                                  ? 'Ingest is configured and viewer playback is now exposed for this live room. Chat, viewer counts, likes, and moderation update in real time.'
                                  : 'Use your broadcaster with the ingest details below. Chat, viewer counts, likes, and moderation update in real time even if this environment has no active live playback origin yet.'
                            : stream.isEnded
                            ? stream.hasReplay
                                  ? 'This session has ended. Replay playback is available below.'
                                  : 'This session has ended and no replay URL is available yet.'
                            : stream.hasLivePlayback
                            ? 'Realtime chat, viewer presence, likes, moderation, and live video playback are active.'
                            : 'Realtime chat, viewer presence, likes, and moderation are active. Live video playback is not configured for this environment yet.',
                        style: AppTextStyles.bodySmall.copyWith(
                          color: Colors.white70,
                        ),
                      ),
                      if (_isHost(stream) &&
                          (stream.ingestUrl ?? '').isNotEmpty) ...[
                        const SizedBox(height: 14),
                        Container(
                          padding: const EdgeInsets.all(12),
                          decoration: BoxDecoration(
                            color: Colors.black.withValues(alpha: 0.28),
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: Colors.white12),
                          ),
                          child: SelectableText(
                            '${(stream.ingestProtocol ?? 'rtmp').toUpperCase()} ingest: ${stream.ingestUrl}',
                            style: AppTextStyles.monoSmall.copyWith(
                              color: Colors.white,
                            ),
                          ),
                        ),
                      ],
                      if (_isHost(stream) &&
                          (stream.streamKey ?? '').isNotEmpty) ...[
                        const SizedBox(height: 14),
                        Container(
                          padding: const EdgeInsets.all(12),
                          decoration: BoxDecoration(
                            color: Colors.black.withValues(alpha: 0.28),
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: Colors.white12),
                          ),
                          child: SelectableText(
                            'Stream key: ${stream.streamKey}',
                            style: AppTextStyles.monoSmall.copyWith(
                              color: Colors.white,
                            ),
                          ),
                        ),
                      ],
                      if (_isHost(stream) &&
                          (stream.playbackUrl ?? '').isNotEmpty) ...[
                        const SizedBox(height: 14),
                        Container(
                          padding: const EdgeInsets.all(12),
                          decoration: BoxDecoration(
                            color: Colors.black.withValues(alpha: 0.28),
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: Colors.white12),
                          ),
                          child: SelectableText(
                            '${(stream.playbackProtocol ?? 'hls').toUpperCase()} playback: ${stream.playbackUrl}',
                            style: AppTextStyles.monoSmall.copyWith(
                              color: Colors.white,
                            ),
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
              if (_selfMuted) _metaPill('Muted'),
            ],
          ),
          const SizedBox(height: 14),
          Row(
            children: [
              ElevatedButton.icon(
                onPressed: stream.isEnded ? null : _likeStream,
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
                icon: Icon(
                  _isHost(stream) ? Icons.stop_circle_outlined : Icons.logout,
                ),
                label: Text(_isHost(stream) ? 'End stream' : 'Leave'),
              ),
            ],
          ),
          if (_isHost(stream) && stream.canPublishFromMobile) ...[
            const SizedBox(height: 14),
            Wrap(
              spacing: 10,
              runSpacing: 10,
              children: [
                ElevatedButton.icon(
                  onPressed: _hostCanStart ? _startHostBroadcast : null,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                    foregroundColor: Colors.white,
                  ),
                  icon: _publisher.isBusy && !_publisher.isPublishing
                      ? const SizedBox(
                          width: 14,
                          height: 14,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : const Icon(Icons.broadcast_on_personal_rounded),
                  label: Text(
                    _publisher.isPublishing
                        ? 'Publishing from phone'
                        : 'Start mobile camera',
                  ),
                ),
                OutlinedButton.icon(
                  onPressed: _publisher.isPublishing && !_publisher.isBusy
                      ? _stopHostBroadcast
                      : null,
                  style: OutlinedButton.styleFrom(
                    foregroundColor: Colors.white,
                    side: const BorderSide(color: Colors.white24),
                  ),
                  icon: const Icon(Icons.stop_circle_outlined),
                  label: const Text('Stop mobile live'),
                ),
                OutlinedButton.icon(
                  onPressed: _publisher.hasPreview ? _toggleHostMic : null,
                  style: OutlinedButton.styleFrom(
                    foregroundColor: Colors.white,
                    side: const BorderSide(color: Colors.white24),
                  ),
                  icon: Icon(
                    _publisher.isAudioEnabled ? Icons.mic : Icons.mic_off,
                  ),
                  label: Text(
                    _publisher.isAudioEnabled ? 'Mute mic' : 'Unmute mic',
                  ),
                ),
                OutlinedButton.icon(
                  onPressed: _publisher.hasPreview ? _toggleHostCamera : null,
                  style: OutlinedButton.styleFrom(
                    foregroundColor: Colors.white,
                    side: const BorderSide(color: Colors.white24),
                  ),
                  icon: Icon(
                    _publisher.isVideoEnabled
                        ? Icons.videocam
                        : Icons.videocam_off,
                  ),
                  label: Text(
                    _publisher.isVideoEnabled
                        ? 'Camera on'
                        : 'Camera off',
                  ),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildModerationCard() {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: const Color(0xFF0F1217),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: Colors.white10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                'Moderation',
                style: AppTextStyles.h3.copyWith(color: Colors.white),
              ),
              const Spacer(),
              Text(
                'Realtime',
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.liveRed,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            'Pin chat messages from the menu below. Manage muted viewers and blocked words here.',
            style: AppTextStyles.bodySmall.copyWith(color: Colors.white60),
          ),
          const SizedBox(height: 16),
          Text(
            'Muted Users',
            style: AppTextStyles.label.copyWith(color: Colors.white70),
          ),
          const SizedBox(height: 10),
          if (_mutedUsers.isEmpty)
            Text(
              'No one is muted.',
              style: AppTextStyles.bodySmall.copyWith(color: Colors.white54),
            )
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: _mutedUsers
                  .map(
                    (mute) => InputChip(
                      label: Text(_shortUserId(mute.userId)),
                      onDeleted: _moderating
                          ? null
                          : () => _unmuteUser(mute.userId),
                      deleteIconColor: Colors.white70,
                      backgroundColor: Colors.white.withValues(alpha: 0.06),
                      side: const BorderSide(color: Colors.white10),
                      labelStyle: AppTextStyles.labelSmall.copyWith(
                        color: Colors.white,
                      ),
                    ),
                  )
                  .toList(),
            ),
          const SizedBox(height: 16),
          Text(
            'Blocked Words',
            style: AppTextStyles.label.copyWith(color: Colors.white70),
          ),
          const SizedBox(height: 10),
          if (_wordFilters.isEmpty)
            Text(
              'No blocked words configured.',
              style: AppTextStyles.bodySmall.copyWith(color: Colors.white54),
            )
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: _wordFilters
                  .map(
                    (filter) => InputChip(
                      label: Text(filter.word),
                      onDeleted: _moderating
                          ? null
                          : () => _removeWordFilter(filter.word),
                      deleteIconColor: Colors.white70,
                      backgroundColor: Colors.white.withValues(alpha: 0.06),
                      side: const BorderSide(color: Colors.white10),
                      labelStyle: AppTextStyles.labelSmall.copyWith(
                        color: Colors.white,
                      ),
                    ),
                  )
                  .toList(),
            ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _wordFilterController,
                  enabled: !_moderating,
                  style: const TextStyle(color: Colors.white),
                  onSubmitted: (_) => _addWordFilter(),
                  decoration: InputDecoration(
                    hintText: 'Add blocked word or phrase',
                    hintStyle: const TextStyle(color: Colors.white54),
                    filled: true,
                    fillColor: Colors.white.withValues(alpha: 0.06),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(14),
                      borderSide: BorderSide.none,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 10),
              ElevatedButton(
                onPressed: _moderating ? null : _addWordFilter,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                ),
                child: _moderating
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Text('Add'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildLiveChatCard(LiveStream stream) {
    final pinnedMessage = _pinnedMessage;
    final composerEnabled = !_selfMuted && !stream.isEnded;
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
          if (pinnedMessage != null) ...[
            const SizedBox(height: 12),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: AppColors.liveRed.withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(12),
                border: Border.all(
                  color: AppColors.liveRed.withValues(alpha: 0.3),
                ),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      const Icon(
                        Icons.push_pin_rounded,
                        color: AppColors.liveRed,
                        size: 16,
                      ),
                      const SizedBox(width: 6),
                      Text(
                        'Pinned Message',
                        style: AppTextStyles.label.copyWith(
                          color: Colors.white,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 6),
                  Text(
                    _shortUserId(pinnedMessage.userId),
                    style: AppTextStyles.labelSmall.copyWith(
                      color: Colors.white70,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    pinnedMessage.message,
                    style: AppTextStyles.body.copyWith(color: Colors.white),
                  ),
                ],
              ),
            ),
          ],
          const SizedBox(height: 12),
          if (_selfMuted)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Text(
                'You have been muted by the host.',
                style: AppTextStyles.bodySmall.copyWith(color: Colors.white60),
              ),
            )
          else if (stream.isEnded)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Text(
                'Chat is closed because the stream has ended.',
                style: AppTextStyles.bodySmall.copyWith(color: Colors.white60),
              ),
            ),
          SizedBox(
            height: 260,
            child: _messages.isEmpty
                ? Center(
                    child: Text(
                      'No chat messages yet.',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: Colors.white54,
                      ),
                    ),
                  )
                : ListView.separated(
                    controller: _chatScrollController,
                    itemCount: _messages.length,
                    separatorBuilder: (context, index) =>
                        const SizedBox(height: 8),
                    itemBuilder: (context, index) {
                      final message = _messages[index];
                      return Container(
                        padding: const EdgeInsets.all(10),
                        decoration: BoxDecoration(
                          color: Colors.white.withValues(alpha: 0.04),
                          borderRadius: BorderRadius.circular(12),
                          border: message.isPinned
                              ? Border.all(
                                  color: AppColors.liveRed.withValues(
                                    alpha: 0.35,
                                  ),
                                )
                              : null,
                        ),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Row(
                              children: [
                                Expanded(
                                  child: Text(
                                    message.userId,
                                    style: AppTextStyles.labelSmall.copyWith(
                                      color: Colors.white70,
                                    ),
                                  ),
                                ),
                                if (message.isPinned)
                                  Padding(
                                    padding: const EdgeInsets.only(right: 4),
                                    child: Text(
                                      'Pinned',
                                      style: AppTextStyles.labelSmall.copyWith(
                                        color: AppColors.liveRed,
                                      ),
                                    ),
                                  ),
                                if (_isHost(stream) &&
                                    message.userId != _currentUserId &&
                                    message.userId.isNotEmpty)
                                  PopupMenuButton<_LiveMessageAction>(
                                    color: const Color(0xFF171A1F),
                                    icon: const Icon(
                                      Icons.more_horiz,
                                      color: Colors.white70,
                                      size: 18,
                                    ),
                                    onSelected: (action) {
                                      switch (action) {
                                        case _LiveMessageAction.pin:
                                          _pinMessage(message);
                                        case _LiveMessageAction.mute:
                                          _muteUser(message.userId);
                                      }
                                    },
                                    itemBuilder: (context) {
                                      final items =
                                          <PopupMenuEntry<_LiveMessageAction>>[
                                            const PopupMenuItem(
                                              value: _LiveMessageAction.pin,
                                              child: Text('Pin message'),
                                            ),
                                          ];
                                      if (!_isUserMuted(message.userId)) {
                                        items.add(
                                          const PopupMenuItem(
                                            value: _LiveMessageAction.mute,
                                            child: Text('Mute user'),
                                          ),
                                        );
                                      }
                                      return items;
                                    },
                                  ),
                              ],
                            ),
                            const SizedBox(height: 4),
                            Text(
                              message.message,
                              style: AppTextStyles.body.copyWith(
                                color: Colors.white,
                              ),
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
                  enabled: composerEnabled,
                  style: const TextStyle(color: Colors.white),
                  onSubmitted: (_) => _sendMessage(),
                  decoration: InputDecoration(
                    hintText: stream.isEnded
                        ? 'Stream has ended'
                        : _selfMuted
                        ? 'You are muted in this stream'
                        : 'Send a live message',
                    hintStyle: const TextStyle(color: Colors.white54),
                    filled: true,
                    fillColor: Colors.white.withValues(alpha: 0.06),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(14),
                      borderSide: BorderSide.none,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 10),
              ElevatedButton(
                onPressed: _sending || !composerEnabled ? null : _sendMessage,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.liveRed,
                  foregroundColor: Colors.white,
                ),
                child: _sending
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
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
        color: Colors.white.withValues(alpha: 0.05),
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
