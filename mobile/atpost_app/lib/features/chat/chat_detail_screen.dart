import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/providers/notification_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
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
  bool _attachmentOpen = false;
  bool _otherUserTyping = false;
  bool _loadingMessages = true;
  bool _sendingMessage = false;
  String? _loadError;
  List<_ChatMessage> _messages = [];

  bool get _hasText => _composerController.text.trim().isNotEmpty;
  bool get _isGroupChat =>
      widget.conversationId.contains('group') ||
      widget.conversationId.contains('room');

  @override
  void initState() {
    super.initState();
    _composerController.addListener(_composerListener);
    _loadMessages();
  }

  @override
  void dispose() {
    _composerController
      ..removeListener(_composerListener)
      ..dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final title = _titleFromConversationId(widget.conversationId);

    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: AppSpacing.pagePadding.copyWith(top: 10, bottom: 10),
              child: Row(
                children: [
                  GlassIconButton(
                    icon: Icons.arrow_back,
                    onPressed: () => context.pop(),
                  ),
                  const SizedBox(width: 10),
                  Container(
                    width: 42,
                    height: 42,
                    decoration: BoxDecoration(
                      gradient: _isGroupChat
                          ? AppColors.posttubeGradient
                          : AppColors.postbookGradient,
                      borderRadius: BorderRadius.circular(14),
                    ),
                    child: Center(
                      child: Text(
                        _initials(title),
                        style: AppTextStyles.label.copyWith(
                          color: Colors.white,
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(title, style: AppTextStyles.h2),
                        const SizedBox(height: 2),
                        Text(
                          _isGroupChat ? 'Group chat' : 'Chat',
                          style: AppTextStyles.bodySmall.copyWith(
                            color: AppColors.textSecondary,
                          ),
                        ),
                      ],
                    ),
                  ),
                  GlassIconButton(
                    icon: Icons.call_outlined,
                    onPressed: () => _startCall(CallType.audio, title),
                  ),
                  const SizedBox(width: 8),
                  GlassIconButton(
                    icon: Icons.videocam_outlined,
                    onPressed: () => _startCall(CallType.video, title),
                  ),
                ],
              ),
            ),
            Container(
              margin: AppSpacing.pagePadding.copyWith(bottom: 10),
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(999),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Text(
                'Messages are end-to-end encrypted',
                style: AppTextStyles.labelSmall,
              ),
            ),
            Padding(
              padding: AppSpacing.pagePadding.copyWith(bottom: 10),
              child: const _DateDivider(label: 'Today'),
            ),
            Expanded(
              child: _loadingMessages
                  ? const Center(child: CircularProgressIndicator())
                  : _loadError != null
                  ? Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(
                            _loadError!,
                            style: AppTextStyles.bodySmall.copyWith(
                              color: AppColors.textSecondary,
                            ),
                          ),
                          const SizedBox(height: 10),
                          TextButton(
                            onPressed: _loadMessages,
                            child: const Text('Retry'),
                          ),
                        ],
                      ),
                    )
                  : _messages.isEmpty
                  ? Center(
                      child: Text(
                        'No messages yet',
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textSecondary,
                        ),
                      ),
                    )
                  : ListView.builder(
                      padding: AppSpacing.pagePadding.copyWith(bottom: 14),
                      itemCount: _messages.length + (_otherUserTyping ? 1 : 0),
                      itemBuilder: (context, index) {
                        if (_otherUserTyping && index == _messages.length) {
                          return const Padding(
                            padding: EdgeInsets.only(top: 6, bottom: 6),
                            child: _TypingBubble(),
                          );
                        }
                        final message = _messages[index];
                        return Padding(
                          padding: const EdgeInsets.only(bottom: 10),
                          child: _MessageBubble(message: message),
                        );
                      },
                    ),
            ),
            AnimatedCrossFade(
              firstChild: const SizedBox(height: 0),
              secondChild: Padding(
                padding: AppSpacing.pagePadding.copyWith(bottom: 10),
                child: const _AttachmentMenu(),
              ),
              crossFadeState: _attachmentOpen
                  ? CrossFadeState.showSecond
                  : CrossFadeState.showFirst,
              duration: const Duration(milliseconds: 220),
            ),
            Padding(
              padding: AppSpacing.pagePadding.copyWith(bottom: 12),
              child: Row(
                children: [
                  _ComposerActionButton(
                    icon: Icons.add,
                    onTap: () =>
                        setState(() => _attachmentOpen = !_attachmentOpen),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Container(
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        borderRadius: BorderRadius.circular(
                          AppSpacing.radiusXL,
                        ),
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
                    icon: _hasText ? Icons.send : Icons.mic_none,
                    active: _hasText && !_sendingMessage,
                    onTap: _hasText && !_sendingMessage
                        ? _sendCurrentMessage
                        : null,
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _startCall(CallType type, String contactName) {
    ref
        .read(callProvider.notifier)
        .initiateCall(
          contactId: widget.conversationId,
          contactName: contactName,
          contactAvatar: '',
          type: type,
        );
  }

  void _composerListener() {
    if (!mounted) {
      return;
    }
    setState(() {});
  }

  Future<void> _loadMessages() async {
    setState(() {
      _loadingMessages = true;
      _loadError = null;
    });
    try {
      final repo = ref.read(chatRepositoryProvider);
      final apiMessages = await repo.getMessages(widget.conversationId);
      if (!mounted) return;

      final currentUserId = ref.read(authServiceProvider).userId;
      final sorted = [...apiMessages]
        ..sort((a, b) => a.createdAt.compareTo(b.createdAt));

      setState(() {
        _messages = sorted
            .map((message) => _fromApiMessage(message, currentUserId))
            .toList();
        _loadingMessages = false;
      });
      ref.invalidate(unreadChatCountProvider);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loadingMessages = false;
        _loadError = 'Could not load messages';
      });
    }
  }

  void _sendCurrentMessage() async {
    final text = _composerController.text.trim();
    if (text.isEmpty || _sendingMessage) {
      return;
    }

    _composerController.clear();
    setState(() {
      _sendingMessage = true;
      _attachmentOpen = false;
      _otherUserTyping = false;
    });

    try {
      final repo = ref.read(chatRepositoryProvider);
      final sent = await repo.sendMessage(widget.conversationId, text);
      if (!mounted) return;

      final currentUserId = ref.read(authServiceProvider).userId;
      setState(() {
        _messages = [..._messages, _fromApiMessage(sent, currentUserId)];
      });
      ref.invalidate(unreadChatCountProvider);
    } catch (_) {
      if (!mounted) return;
      _composerController
        ..text = text
        ..selection = TextSelection.collapsed(offset: text.length);
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Failed to send message')));
    } finally {
      if (mounted) {
        setState(() => _sendingMessage = false);
      }
    }
  }

  _ChatMessage _fromApiMessage(Message message, String? currentUserId) {
    final kind = switch (message.contentType) {
      'image' || 'video' => _MessageKind.media,
      'file' => _MessageKind.file,
      _ => _MessageKind.text,
    };

    return _ChatMessage(
      kind: kind,
      text: message.content,
      time: _formatMessageTime(message.createdAt),
      isMine: message.senderId == currentUserId,
      senderName: message.senderName,
      mediaCaption: kind == _MessageKind.media ? message.content : null,
      fileSize: kind == _MessageKind.file ? 'Attachment' : null,
    );
  }

  String _formatMessageTime(DateTime dateTime) {
    final hh = dateTime.hour.toString().padLeft(2, '0');
    final mm = dateTime.minute.toString().padLeft(2, '0');
    return '$hh:$mm';
  }

  String _titleFromConversationId(String rawId) {
    final words = rawId
        .split('-')
        .where((word) => word.isNotEmpty)
        .map((word) => '${word[0].toUpperCase()}${word.substring(1)}');
    return words.join(' ');
  }

  String _initials(String value) {
    final parts = value
        .split(' ')
        .where((segment) => segment.isNotEmpty)
        .toList();
    if (parts.length == 1) {
      return parts.first.substring(0, 1).toUpperCase();
    }
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }
}

class _DateDivider extends StatelessWidget {
  const _DateDivider({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        const Expanded(child: Divider(color: AppColors.borderSubtle)),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 10),
          child: Text(
            label,
            style: AppTextStyles.monoSmall.copyWith(color: AppColors.textDim),
          ),
        ),
        const Expanded(child: Divider(color: AppColors.borderSubtle)),
      ],
    );
  }
}

class _MessageBubble extends StatelessWidget {
  const _MessageBubble({required this.message});

  final _ChatMessage message;

  @override
  Widget build(BuildContext context) {
    final alignment = message.isMine
        ? Alignment.centerRight
        : Alignment.centerLeft;
    final radius = BorderRadius.only(
      topLeft: const Radius.circular(18),
      topRight: const Radius.circular(18),
      bottomLeft: Radius.circular(message.isMine ? 18 : 6),
      bottomRight: Radius.circular(message.isMine ? 6 : 18),
    );

    return Align(
      alignment: alignment,
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: MediaQuery.sizeOf(context).width * 0.76,
        ),
        child: Column(
          crossAxisAlignment: message.isMine
              ? CrossAxisAlignment.end
              : CrossAxisAlignment.start,
          children: [
            if (!message.isMine && message.senderName != null) ...[
              Padding(
                padding: const EdgeInsets.only(left: 10, bottom: 4),
                child: Text(
                  message.senderName!,
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.accentPurple,
                  ),
                ),
              ),
            ],
            Container(
              decoration: BoxDecoration(
                gradient: message.isMine ? AppColors.postbookGradient : null,
                color: message.isMine
                    ? null
                    : Colors.white.withValues(alpha: 0.06),
                borderRadius: radius,
                border: message.isMine
                    ? null
                    : Border.all(color: Colors.white.withValues(alpha: 0.08)),
              ),
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              child: _MessageContent(message: message),
            ),
            const SizedBox(height: 4),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 6),
              child: Text(
                message.time,
                style: AppTextStyles.monoSmall.copyWith(
                  color: AppColors.textDim,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _MessageContent extends StatelessWidget {
  const _MessageContent({required this.message});

  final _ChatMessage message;

  @override
  Widget build(BuildContext context) {
    final textColor = message.isMine ? Colors.white : AppColors.textSecondary;
    return switch (message.kind) {
      _MessageKind.text => Text(
        message.text,
        style: AppTextStyles.body.copyWith(color: textColor),
      ),
      _MessageKind.media => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            height: 124,
            width: double.infinity,
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(14),
              gradient: const LinearGradient(
                begin: Alignment.topLeft,
                end: Alignment.bottomRight,
                colors: [Color(0xFF2A1E3D), Color(0xFF1A1A26)],
              ),
            ),
            child: const Center(
              child: Icon(
                Icons.image_outlined,
                color: Colors.white70,
                size: 30,
              ),
            ),
          ),
          const SizedBox(height: 8),
          Text(
            message.mediaCaption ?? '',
            style: AppTextStyles.bodySmall.copyWith(color: textColor),
          ),
        ],
      ),
      _MessageKind.file => Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 32,
            height: 32,
            decoration: BoxDecoration(
              color: Colors.white.withValues(
                alpha: message.isMine ? 0.15 : 0.08,
              ),
              borderRadius: BorderRadius.circular(10),
            ),
            child: const Icon(
              Icons.insert_drive_file_outlined,
              size: 18,
              color: Colors.white,
            ),
          ),
          const SizedBox(width: 8),
          Flexible(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  message.text,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.bodySmall.copyWith(color: textColor),
                ),
                const SizedBox(height: 2),
                Text(
                  message.fileSize ?? '',
                  style: AppTextStyles.monoSmall.copyWith(
                    color: textColor.withValues(alpha: 0.78),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    };
  }
}

class _TypingBubble extends StatelessWidget {
  const _TypingBubble();

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: Colors.white.withValues(alpha: 0.06),
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(18),
            topRight: Radius.circular(18),
            bottomLeft: Radius.circular(6),
            bottomRight: Radius.circular(18),
          ),
          border: Border.all(color: Colors.white.withValues(alpha: 0.08)),
        ),
        child: const _TypingDots(size: 6),
      ),
    );
  }
}

class _TypingDots extends StatelessWidget {
  const _TypingDots({this.size = 6});

  final double size;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: List.generate(3, (index) {
        return Padding(
          padding: const EdgeInsets.only(right: 3),
          child:
              Container(
                    width: size,
                    height: size,
                    decoration: const BoxDecoration(
                      color: AppColors.onlineGreen,
                      shape: BoxShape.circle,
                    ),
                  )
                  .animate(onPlay: (controller) => controller.repeat())
                  .moveY(
                    begin: 0,
                    end: -4,
                    duration: 420.ms,
                    delay: (index * 150).ms,
                    curve: Curves.easeOut,
                  )
                  .moveY(
                    begin: -4,
                    end: 0,
                    duration: 420.ms,
                    curve: Curves.easeIn,
                  ),
        );
      }),
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
          boxShadow: active
              ? const [
                  BoxShadow(
                    color: Color(0x66FF6B35),
                    blurRadius: 16,
                    offset: Offset(0, 4),
                  ),
                ]
              : const [],
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

  static const List<_AttachmentOption> _options = [
    _AttachmentOption(
      label: 'Camera',
      icon: Icons.camera_alt,
      color: AppColors.postbookPrimary,
    ),
    _AttachmentOption(
      label: 'Gallery',
      icon: Icons.image,
      color: AppColors.postgramPrimary,
    ),
    _AttachmentOption(
      label: 'File',
      icon: Icons.description,
      color: AppColors.posttubePrimary,
    ),
    _AttachmentOption(
      label: 'Location',
      icon: Icons.location_on,
      color: AppColors.accentPurple,
    ),
    _AttachmentOption(
      label: 'Contact',
      icon: Icons.person,
      color: AppColors.postbookSecondary,
    ),
    _AttachmentOption(
      label: 'Poll',
      icon: Icons.poll,
      color: AppColors.postgramSecondary,
    ),
    _AttachmentOption(
      label: 'Audio',
      icon: Icons.music_note,
      color: AppColors.posttubeSecondary,
    ),
    _AttachmentOption(
      label: 'Reel',
      icon: Icons.movie,
      color: AppColors.liveRed,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: GridView.count(
        crossAxisCount: 4,
        childAspectRatio: 0.95,
        shrinkWrap: true,
        physics: const NeverScrollableScrollPhysics(),
        crossAxisSpacing: 10,
        mainAxisSpacing: 12,
        children: _options
            .map((option) => _AttachmentTile(option: option))
            .toList(),
      ),
    );
  }
}

class _AttachmentTile extends StatelessWidget {
  const _AttachmentTile({required this.option});

  final _AttachmentOption option;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 42,
          height: 42,
          decoration: BoxDecoration(
            color: option.color.withValues(alpha: 0.18),
            borderRadius: BorderRadius.circular(14),
          ),
          child: Icon(option.icon, color: option.color, size: 20),
        ),
        const SizedBox(height: 5),
        Text(
          option.label,
          style: AppTextStyles.labelTiny.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
      ],
    );
  }
}

class _AttachmentOption {
  const _AttachmentOption({
    required this.label,
    required this.icon,
    required this.color,
  });

  final String label;
  final IconData icon;
  final Color color;
}

enum _MessageKind { text, media, file }

class _ChatMessage {
  const _ChatMessage({
    required this.kind,
    required this.text,
    required this.time,
    required this.isMine,
    this.senderName,
    this.mediaCaption,
    this.fileSize,
  });

  final _MessageKind kind;
  final String text;
  final String time;
  final bool isMine;
  final String? senderName;
  final String? mediaCaption;
  final String? fileSize;
}
