import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/features/pulse/safety/chat_safety_sheet.dart';
import 'package:atpost_app/features/pulse/safety/panic_sheet.dart';
import 'package:atpost_app/features/pulse/safety/report_block_sheet.dart';
import 'package:atpost_app/features/pulse/safety/share_location_banner.dart';
import 'package:atpost_app/features/pulse/widgets/match_context_banner.dart';
import 'package:atpost_app/features/pulse/widgets/moderation_banner.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// formerly PostMatchChatScreen
class PulseChatScreen extends ConsumerStatefulWidget {
  const PulseChatScreen({super.key, required this.conversationId});

  final String conversationId;

  @override
  ConsumerState<PulseChatScreen> createState() =>
      _PulseChatScreenState();
}

class _PulseChatScreenState extends ConsumerState<PulseChatScreen> {
  final _messageController = TextEditingController();

  bool _loading = true;
  bool _sending = false;
  String _error = '';
  List<PulseMessage> _messages = const [];
  PulseConversation? _conversation;
  Timer? _poller;

  /// Per-message reveal toggle for moderation-blocked messages. Premium-only
  /// gating is enforced by the moderation banner widget itself.
  final Set<String> _revealed = <String>{};

  @override
  void initState() {
    super.initState();
    PulseBreadcrumbs.conversationOpen(conversationId: widget.conversationId);
    _load();
    _poller = Timer.periodic(
      const Duration(seconds: 5),
      (_) => _load(silent: true),
    );
  }

  @override
  void dispose() {
    PulseBreadcrumbs.conversationClose(conversationId: widget.conversationId);
    _poller?.cancel();
    _messageController.dispose();
    super.dispose();
  }

  Future<void> _load({bool silent = false}) async {
    final auth = ref.read(pulseAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    if (!auth.isReady) {
      context.go('/pulse/onboarding');
      return;
    }

    if (!silent) {
      setState(() {
        _loading = true;
        _error = '';
      });
    }
    try {
      final repo = ref.read(pulseRepositoryProvider);
      final results = await Future.wait([
        repo.getConversations(),
        repo.getMessages(widget.conversationId),
      ]);
      if (!mounted) return;
      final conversations = results[0] as List<PulseConversation>;
      setState(() {
        _conversation = conversations.firstWhere(
          (conversation) => conversation.id == widget.conversationId,
          orElse: () => PulseConversation(
            id: widget.conversationId,
            type: 'direct',
            status: 'active',
          ),
        );
        _messages = results[1] as List<PulseMessage>;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not load this conversation.';
        _loading = false;
      });
    }
  }

  Future<void> _send() async {
    final text = _messageController.text.trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      final message = await ref
          .read(pulseRepositoryProvider)
          .sendMessage(widget.conversationId, bodyText: text);
      // Sprint 5 telemetry: count only — never the message body.
      ref.read(pulseTelemetryProvider).messageSent(
            matchId: widget.conversationId,
          );
      PulseBreadcrumbs.conversationSend(
        conversationId: widget.conversationId,
      );
      if (!mounted) return;
      _messageController.clear();
      setState(() => _messages = [..._messages, message]);
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Could not send message.')));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text(
          _conversation?.otherUser?.firstName ?? 'Conversation',
          style: AppTextStyles.h2,
        ),
        actions: [
          GestureDetector(
            onLongPress: () => PanicSheet.show(context),
            child: IconButton(
              tooltip: 'Safety options (long-press for panic)',
              icon: const Icon(
                Icons.shield_outlined,
                color: AppColors.posttubePrimary,
              ),
              onPressed: () {
                final other = _conversation?.otherUser;
                if (other == null) return;
                ChatSafetySheet.show(
                  context,
                  otherUserId: other.userId,
                  otherUserName: other.firstName,
                );
              },
            ),
          ),
        ],
      ),
      body: _loading
          ? const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            )
          : _error.isNotEmpty
          ? Center(child: Text(_error, style: AppTextStyles.body))
          : Column(
              children: [
                const ShareLocationBanner(),
                _ChatBannerSlot(
                  conversationId: widget.conversationId,
                  onSuggestOpener: (text) {
                    _messageController.text = text;
                    _messageController.selection =
                        TextSelection.fromPosition(
                      TextPosition(offset: _messageController.text.length),
                    );
                  },
                ),
                Expanded(
                  child: ListView.builder(
                    reverse: false,
                    padding: AppSpacing.pagePadding.copyWith(top: 12),
                    itemCount: _messages.length,
                    itemBuilder: (context, index) {
                      final message = _messages[index];
                      final isMine =
                          message.senderUserId !=
                          (_conversation?.otherUser?.userId ?? '');
                      final decision = PulseModerationDecision.tryParse(
                        message.moderation,
                      );
                      final isHeld = decision != null && decision.isBlock;
                      final isWarn = decision != null && decision.isWarn;
                      final revealed = _revealed.contains(message.id);

                      return Align(
                        alignment: isMine
                            ? Alignment.centerRight
                            : Alignment.centerLeft,
                        child: GestureDetector(
                          onLongPress: isMine
                              ? null
                              : () {
                                  final other = _conversation?.otherUser;
                                  if (other == null) return;
                                  ReportSheet.show(
                                    context,
                                    targetUserId: other.userId,
                                    targetName: other.firstName,
                                    reportContext: 'message_id=${message.id}',
                                  );
                                },
                          child: Container(
                            margin: const EdgeInsets.only(bottom: 10),
                            constraints: BoxConstraints(
                              maxWidth: MediaQuery.of(context).size.width *
                                  0.78,
                            ),
                            child: Column(
                              crossAxisAlignment: isMine
                                  ? CrossAxisAlignment.end
                                  : CrossAxisAlignment.start,
                              children: [
                                if (isHeld)
                                  ModerationHeldPlaceholder(
                                    isPremium: false,
                                    revealed: revealed,
                                    onReveal: () => setState(() {
                                      _revealed.add(message.id);
                                    }),
                                    body: message.bodyText,
                                  )
                                else
                                  Container(
                                    padding: const EdgeInsets.symmetric(
                                      horizontal: 14,
                                      vertical: 10,
                                    ),
                                    decoration: BoxDecoration(
                                      color: isMine
                                          ? AppColors.postbookPrimary
                                          : AppColors.bgCard,
                                      borderRadius: BorderRadius.circular(
                                        AppSpacing.radiusLarge,
                                      ),
                                    ),
                                    child: Text(
                                      message.bodyText ?? '',
                                      style: AppTextStyles.bodySmall.copyWith(
                                        color: isMine
                                            ? Colors.white
                                            : AppColors.textPrimary,
                                      ),
                                    ),
                                  ),
                                if (isWarn)
                                  ModerationWarnBanner(
                                    suggestion: decision.suggestion,
                                  ),
                              ],
                            ),
                          ),
                        ),
                      );
                    },
                  ),
                ),
                SafeArea(
                  top: false,
                  child: Padding(
                    padding: AppSpacing.pagePadding.copyWith(
                      top: 8,
                      bottom: 16,
                    ),
                    child: Row(
                      children: [
                        Expanded(
                          child: TextField(
                            controller: _messageController,
                            style: AppTextStyles.body.copyWith(
                              color: AppColors.textPrimary,
                            ),
                            decoration: InputDecoration(
                              hintText: 'Type a message',
                              hintStyle: AppTextStyles.bodySmall.copyWith(
                                color: AppColors.textMuted,
                              ),
                              filled: true,
                              fillColor: AppColors.bgCard,
                              border: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(
                                  AppSpacing.radiusLarge,
                                ),
                                borderSide: BorderSide(
                                  color: AppColors.borderSubtle,
                                ),
                              ),
                              enabledBorder: OutlineInputBorder(
                                borderRadius: BorderRadius.circular(
                                  AppSpacing.radiusLarge,
                                ),
                                borderSide: BorderSide(
                                  color: AppColors.borderSubtle,
                                ),
                              ),
                            ),
                          ),
                        ),
                        const SizedBox(width: 8),
                        IconButton.filled(
                          onPressed: _sending ? null : _send,
                          style: IconButton.styleFrom(
                            backgroundColor: AppColors.postbookPrimary,
                          ),
                          icon: _sending
                              ? const SizedBox(
                                  width: 18,
                                  height: 18,
                                  child: CircularProgressIndicator(
                                    strokeWidth: 2,
                                    color: Colors.white,
                                  ),
                                )
                              : const Icon(Icons.send),
                        ),
                      ],
                    ),
                  ),
                ),
              ],
            ),
    );
  }
}

/// Sprint 3 — resolves the match attached to this conversation (if any) and
/// renders the `MatchContextBanner` above the chat. Renders nothing when
/// the conversation isn't a Pulse match.
class _ChatBannerSlot extends ConsumerWidget {
  const _ChatBannerSlot({
    required this.conversationId,
    required this.onSuggestOpener,
  });

  final String conversationId;
  final ValueChanged<String> onSuggestOpener;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(pulseMatchByConversationProvider(conversationId));
    return async.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (match) {
        if (match == null) return const SizedBox.shrink();
        return MatchContextBanner(
          matchId: match.id,
          onSuggestOpener: onSuggestOpener,
        );
      },
    );
  }
}
