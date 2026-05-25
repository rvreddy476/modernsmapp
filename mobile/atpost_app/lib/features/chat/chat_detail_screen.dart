import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/models/presence.dart';
import 'package:atpost_app/providers/chat_provider.dart';
import 'package:atpost_app/providers/presence_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ChatDetailScreen extends ConsumerStatefulWidget {
  const ChatDetailScreen({super.key, required this.conversationId});

  final String conversationId;

  @override
  ConsumerState<ChatDetailScreen> createState() => _ChatDetailScreenState();
}

class _ChatDetailScreenState extends ConsumerState<ChatDetailScreen> {
  final TextEditingController _composerController = TextEditingController();
  final ScrollController _scrollController = ScrollController();
  bool _attachmentOpen = false;
  int _lastMessageCount = 0;

  @override
  void initState() {
    super.initState();
    _composerController.addListener(() {
      ref
          .read(chatMessagesProvider(widget.conversationId).notifier)
          .onComposerChanged(_composerController.text);
      // M1: also push `typing.start` over the persistent WS so other
      // members see the indicator instantly. The controller throttles
      // outbound pings to once per 3s.
      if (_composerController.text.trim().isNotEmpty) {
        ref
            .read(
              conversationPresenceControllerProvider(widget.conversationId),
            )
            .onTyping();
      }
      setState(() {});
    });
    // Eagerly create the presence controller so `conversation.enter` +
    // the 15s heartbeat fire on screen open rather than waiting for the
    // first `build()` to read the provider.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        ref.read(
          conversationPresenceControllerProvider(widget.conversationId),
        );
      }
    });
  }

  @override
  void dispose() {
    _composerController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _scrollToBottom({bool animated = true}) {
    if (!_scrollController.hasClients) return;
    final position = _scrollController.position.maxScrollExtent;
    if (animated) {
      _scrollController.animateTo(
        position,
        duration: const Duration(milliseconds: 250),
        curve: Curves.easeOut,
      );
      return;
    }
    _scrollController.jumpTo(position);
  }

  @override
  Widget build(BuildContext context) {
    final chatState = ref.watch(chatMessagesProvider(widget.conversationId));
    final chatNotifier = ref.read(
      chatMessagesProvider(widget.conversationId).notifier,
    );
    final currentUserId = ref.watch(authServiceProvider).userId;
    final conversationAsync = ref.watch(
      chatConversationProvider(widget.conversationId),
    );

    if (chatState.messages.length != _lastMessageCount) {
      _lastMessageCount = chatState.messages.length;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) {
          _scrollToBottom(animated: chatState.messages.length > 1);
        }
      });
    }

    final conversation = conversationAsync.valueOrNull;
    final title =
        conversation?.displayNameFor(currentUserId) ??
        _titleFromConversationId(widget.conversationId);
    final peerId = conversation?.directPeerId(currentUserId);
    final peerPresence = peerId == null
        ? null
        : ref.watch(peerPresenceProvider(peerId));
    // M1 conversation-presence rollup (polled every 10s).
    final convPresenceAsync = ref.watch(
      conversationPresencePollProvider(widget.conversationId),
    );
    final convPresence = convPresenceAsync.valueOrNull;
    final subtitle = _subtitleFor(
      conversation: conversation,
      currentUserId: currentUserId,
      peerPresence: peerPresence,
      typingUserIds: chatState.typingUserIds,
      convPresence: convPresence,
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: Column(
          children: [
            _buildAppBar(
              context: context,
              title: title,
              subtitle: subtitle,
              conversation: conversation,
              peerId: peerId,
            ),
            _buildRealtimeNotice(),
            if (chatState.error != null)
              Padding(
                padding: AppSpacing.pagePadding.copyWith(bottom: 8),
                child: Text(
                  chatState.error!,
                  style: AppTextStyles.bodySmall.copyWith(
                    color: Colors.redAccent,
                  ),
                ),
              ),
            Expanded(
              child: _buildMessageList(
                state: chatState,
                currentUserId: currentUserId,
                conversation: conversation,
              ),
            ),
            _buildTypingIndicator(
              chatState: chatState,
              currentUserId: currentUserId,
              conversation: conversation,
            ),
            if (_attachmentOpen) const _AttachmentMenu(),
            _buildComposer(chatState, chatNotifier),
          ],
        ),
      ),
    );
  }

  Widget _buildAppBar({
    required BuildContext context,
    required String title,
    required String subtitle,
    required Conversation? conversation,
    required String? peerId,
  }) {
    final isGroup = conversation?.type == 'group';

    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 10, bottom: 10),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back,
            tooltip: 'Back',
            onPressed: () => context.pop(),
          ),
          const SizedBox(width: 10),
          _buildAvatar(title, isGroup),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppTextStyles.h2),
                Text(
                  subtitle,
                  style: AppTextStyles.bodySmall.copyWith(
                    color: subtitle == 'Online'
                        ? AppColors.onlineGreen
                        : AppColors.textSecondary,
                  ),
                ),
              ],
            ),
          ),
          GlassIconButton(
            icon: Icons.call_outlined,
            tooltip: 'Audio',
            onPressed: () => _startCall(
              context,
              peerId: peerId,
              title: title,
              type: CallType.audio,
            ),
          ),
          const SizedBox(width: 8),
          GlassIconButton(
            icon: Icons.videocam_outlined,
            tooltip: 'Video',
            onPressed: () => _startCall(
              context,
              peerId: peerId,
              title: title,
              type: CallType.video,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildAvatar(String title, bool isGroup) {
    return Container(
      width: 42,
      height: 42,
      decoration: BoxDecoration(
        gradient: isGroup
            ? AppColors.posttubeGradient
            : AppColors.postbookGradient,
        borderRadius: BorderRadius.circular(14),
      ),
      child: Center(
        child: Text(
          _initials(title),
          style: AppTextStyles.label.copyWith(color: Colors.white),
        ),
      ),
    );
  }

  Widget _buildRealtimeNotice() {
    return Container(
      margin: AppSpacing.pagePadding.copyWith(bottom: 10),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Text(
        'Realtime messaging active via ws-gateway',
        style: AppTextStyles.labelSmall,
      ),
    );
  }

  Widget _buildMessageList({
    required ChatMessagesState state,
    required String? currentUserId,
    required Conversation? conversation,
  }) {
    if (state.isLoading && state.messages.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }

    if (state.messages.isEmpty) {
      return Center(
        child: Text('No messages yet', style: AppTextStyles.bodySmall),
      );
    }

    final isGroup = conversation?.type == 'group';

    return ListView.builder(
      controller: _scrollController,
      padding: AppSpacing.pagePadding.copyWith(bottom: 14),
      itemCount: state.messages.length + (state.hasReachedEnd ? 0 : 1),
      itemBuilder: (context, index) {
        if (!state.hasReachedEnd && index == 0) {
          return Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Center(
              child: TextButton.icon(
                onPressed: state.isLoadingOlder
                    ? null
                    : () => ref
                          .read(
                            chatMessagesProvider(
                              widget.conversationId,
                            ).notifier,
                          )
                          .loadOlderMessages(),
                icon: state.isLoadingOlder
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.keyboard_arrow_up_rounded),
                label: Text(
                  state.isLoadingOlder ? 'Loading' : 'Older messages',
                ),
              ),
            ),
          );
        }

        final messageIndex = state.hasReachedEnd ? index : index - 1;
        final message = state.messages[messageIndex];
        final isMine = message.senderId == currentUserId;
        return RepaintBoundary(
          child: Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: _MessageBubble(
              message: message,
              isMine: isMine,
              showSender: isGroup && !isMine,
            ),
          ),
        );
      },
    );
  }

  /// Renders an animated "X is typing…" indicator above the composer
  /// whenever a remote participant is typing. The set of typing users
  /// is driven by realtime `TypingEvent`s and auto-clears via the
  /// notifier's per-user expiry timers, so a stale signal disappears
  /// on its own if no further ping arrives.
  Widget _buildTypingIndicator({
    required ChatMessagesState chatState,
    required String? currentUserId,
    required Conversation? conversation,
  }) {
    final remoteTypers = chatState.typingUserIds
        .where((id) => id != currentUserId)
        .toList();
    if (remoteTypers.isEmpty) {
      return const SizedBox.shrink();
    }

    final isGroup = conversation?.type == 'group';
    final label = _typingLabel(
      remoteTypers: remoteTypers,
      conversation: conversation,
      isGroup: isGroup,
    );

    return Padding(
      padding: AppSpacing.pagePadding.copyWith(top: 0, bottom: 6),
      child: Row(
        children: [
          const _TypingDots(),
          const SizedBox(width: 8),
          Flexible(
            child: Text(
              label,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textSecondary,
                fontStyle: FontStyle.italic,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }

  String _typingLabel({
    required List<String> remoteTypers,
    required Conversation? conversation,
    required bool isGroup,
  }) {
    if (!isGroup) {
      return 'typing…';
    }
    if (remoteTypers.length == 1) {
      final name = conversation?.memberNameFor(remoteTypers.first);
      return name != null && name.isNotEmpty
          ? '$name is typing…'
          : 'Someone is typing…';
    }
    return '${remoteTypers.length} people are typing…';
  }

  Widget _buildComposer(
    ChatMessagesState state,
    ChatMessagesNotifier notifier,
  ) {
    final hasText = _composerController.text.trim().isNotEmpty;

    return Padding(
      padding: AppSpacing.pagePadding.copyWith(bottom: 12),
      child: Row(
        children: [
          _ComposerActionButton(
            icon: Icons.add,
            onTap: () => setState(() => _attachmentOpen = !_attachmentOpen),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Container(
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: TextField(
                controller: _composerController,
                minLines: 1,
                maxLines: 4,
                style: AppTextStyles.body,
                decoration: InputDecoration(
                  border: InputBorder.none,
                  hintText: 'Message',
                  hintStyle: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textGhost,
                  ),
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 14,
                    vertical: 10,
                  ),
                  suffixIcon: const Icon(
                    Icons.emoji_emotions_outlined,
                    color: AppColors.textMuted,
                    size: 20,
                  ),
                ),
              ),
            ),
          ),
          const SizedBox(width: 10),
          _ComposerActionButton(
            icon: hasText ? Icons.send : Icons.mic_none,
            active: hasText && !state.isSending,
            onTap: hasText && !state.isSending
                ? () async {
                    final text = _composerController.text.trim();
                    _composerController.clear();
                    await notifier.sendMessage(text);
                    _scrollToBottom();
                  }
                : null,
          ),
        ],
      ),
    );
  }

  Future<void> _startCall(
    BuildContext context, {
    required String? peerId,
    required String title,
    required CallType type,
  }) async {
    if (peerId == null || peerId.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Audio and video calls are only available in direct chats.',
          ),
        ),
      );
      return;
    }

    await ref
        .read(callProvider.notifier)
        .initiateCall(
          contactId: peerId,
          contactName: title,
          contactAvatar: '',
          sourceType: 'chat',
          sourceId: widget.conversationId,
          type: type,
        );
  }

  String _subtitleFor({
    required Conversation? conversation,
    required String? currentUserId,
    required AsyncValue<bool>? peerPresence,
    required Set<String> typingUserIds,
    required ConversationPresence? convPresence,
  }) {
    // Prefer the M1 polled typing list (authoritative across devices),
    // falling back to the realtime ChatProvider set for snappier first
    // keystroke feedback.
    final pollTyping = (convPresence?.typingUsers ?? const <String>[])
        .where((id) => id != currentUserId)
        .toList();
    final realtimeTyping = typingUserIds.where((id) => id != currentUserId);
    final remoteTyping =
        pollTyping.isNotEmpty || realtimeTyping.isNotEmpty;
    if (remoteTyping) {
      return 'Typing...';
    }
    if (conversation == null) {
      return 'Loading conversation...';
    }
    // M1 in-conversation active count is a stronger signal than global
    // online state once we have it. active_count includes the viewer,
    // so >1 means a peer is actively in the conversation right now.
    if (convPresence != null && convPresence.activeCount > 1) {
      if (convPresence.isBigGroup) {
        return '${convPresence.activeCount} active in chat';
      }
      final otherActive = (convPresence.activeUsers)
          .where((id) => id != currentUserId)
          .length;
      if (otherActive > 0) {
        return otherActive == 1
            ? 'Active in chat'
            : '$otherActive active in chat';
      }
    }
    if (conversation.type == 'group') {
      final participantCount = conversation.participantCountFor(currentUserId);
      if (participantCount <= 0) {
        return 'Group chat';
      }
      return '$participantCount participants';
    }
    return peerPresence?.maybeWhen(
          data: (isOnline) => isOnline ? 'Online' : 'Offline',
          orElse: () => 'Direct message',
        ) ??
        'Direct message';
  }

  String _titleFromConversationId(String rawId) {
    return rawId
        .split('-')
        .map(
          (word) => word.isNotEmpty
              ? '${word[0].toUpperCase()}${word.substring(1)}'
              : '',
        )
        .join(' ');
  }

  String _initials(String value) {
    final parts = value.split(' ').where((s) => s.isNotEmpty).toList();
    if (parts.isEmpty) return '?';
    return parts.length == 1
        ? parts[0][0].toUpperCase()
        : '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }
}

class _MessageBubble extends StatelessWidget {
  const _MessageBubble({
    required this.message,
    required this.isMine,
    required this.showSender,
  });

  final Message message;
  final bool isMine;
  final bool showSender;

  @override
  Widget build(BuildContext context) {
    final alignment = isMine ? Alignment.centerRight : Alignment.centerLeft;
    final bgColor = isMine ? null : Colors.white.withValues(alpha: 0.06);

    return Align(
      alignment: alignment,
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.76,
        ),
        child: Column(
          crossAxisAlignment: isMine
              ? CrossAxisAlignment.end
              : CrossAxisAlignment.start,
          children: [
            if (showSender && (message.senderName ?? '').isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 4, left: 4, right: 4),
                child: Text(
                  message.senderName!,
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              decoration: BoxDecoration(
                gradient: isMine ? AppColors.postbookGradient : null,
                color: bgColor,
                borderRadius: BorderRadius.only(
                  topLeft: const Radius.circular(18),
                  topRight: const Radius.circular(18),
                  bottomLeft: Radius.circular(isMine ? 18 : 6),
                  bottomRight: Radius.circular(isMine ? 6 : 18),
                ),
              ),
              child: _bubbleBody(),
            ),
            const SizedBox(height: 4),
            Text(
              _formatTime(message.createdAt),
              style: AppTextStyles.monoSmall.copyWith(color: AppColors.textDim),
            ),
          ],
        ),
      ),
    );
  }

  Widget _bubbleBody() {
    final text = message.previewText;
    if (message.mediaId != null && text.isEmpty) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.attach_file, size: 16, color: Colors.white70),
          const SizedBox(width: 6),
          Text(
            'Attachment',
            style: AppTextStyles.body.copyWith(color: Colors.white),
          ),
        ],
      );
    }
    return Text(text, style: AppTextStyles.body.copyWith(color: Colors.white));
  }

  String _formatTime(DateTime dt) {
    return '${dt.hour.toString().padLeft(2, '0')}:${dt.minute.toString().padLeft(2, '0')}';
  }
}

/// Three softly pulsing dots, staggered, used by the typing indicator.
/// Self-contained so it can be dropped beside any "is typing…" label.
class _TypingDots extends StatefulWidget {
  const _TypingDots();

  @override
  State<_TypingDots> createState() => _TypingDotsState();
}

class _TypingDotsState extends State<_TypingDots>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1100),
    )..repeat();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 30,
      height: 10,
      child: AnimatedBuilder(
        animation: _controller,
        builder: (context, _) {
          return Row(
            mainAxisSize: MainAxisSize.min,
            children: List.generate(3, (index) {
              // Stagger each dot by a third of the cycle.
              final phase = (_controller.value + index / 3) % 1.0;
              // Triangle wave 0..1..0 → smooth pulse.
              final pulse = phase < 0.5 ? phase * 2 : (1 - phase) * 2;
              final scale = 0.6 + pulse * 0.4;
              return Padding(
                padding: const EdgeInsets.symmetric(horizontal: 1.5),
                child: Transform.scale(
                  scale: scale,
                  child: Container(
                    width: 7,
                    height: 7,
                    decoration: BoxDecoration(
                      color: AppColors.postbookPrimary.withValues(
                        alpha: 0.45 + pulse * 0.45,
                      ),
                      shape: BoxShape.circle,
                    ),
                  ),
                ),
              );
            }),
          );
        },
      ),
    );
  }
}

class _ComposerActionButton extends StatelessWidget {
  const _ComposerActionButton({
    required this.icon,
    this.active = false,
    this.onTap,
  });

  final IconData icon;
  final bool active;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 42,
        height: 42,
        decoration: BoxDecoration(
          gradient: active ? AppColors.ctaGradient : null,
          color: active ? null : AppColors.bgCard,
          shape: BoxShape.circle,
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Icon(
          icon,
          color: active ? Colors.white : AppColors.textMuted,
          size: 20,
        ),
      ),
    );
  }
}

class _AttachmentMenu extends StatelessWidget {
  const _AttachmentMenu();

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: AppSpacing.pagePadding.copyWith(bottom: 10),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: const Row(
        mainAxisAlignment: MainAxisAlignment.spaceAround,
        children: [
          _AttachItem(Icons.camera_alt, 'Camera', AppColors.postbookPrimary),
          _AttachItem(Icons.image, 'Gallery', AppColors.postgramPrimary),
          _AttachItem(Icons.description, 'File', AppColors.posttubePrimary),
          _AttachItem(Icons.location_on, 'Location', AppColors.accentPurple),
        ],
      ),
    );
  }
}

class _AttachItem extends StatelessWidget {
  const _AttachItem(this.icon, this.label, this.color);

  final IconData icon;
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 42,
          height: 42,
          decoration: BoxDecoration(
            color: color.withValues(alpha: 0.18),
            borderRadius: BorderRadius.circular(14),
          ),
          child: Icon(icon, color: color, size: 20),
        ),
        const SizedBox(height: 5),
        Text(
          label,
          style: AppTextStyles.labelTiny.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
      ],
    );
  }
}
