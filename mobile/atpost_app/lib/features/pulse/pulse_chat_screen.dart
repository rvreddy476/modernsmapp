import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/features/pulse/safety/chat_safety_sheet.dart';
import 'package:atpost_app/features/pulse/safety/panic_sheet.dart';
import 'package:atpost_app/features/pulse/safety/report_block_sheet.dart';
import 'package:atpost_app/features/pulse/safety/share_location_banner.dart';
import 'package:atpost_app/features/pulse/widgets/match_context_banner.dart';
import 'package:atpost_app/features/pulse/widgets/moderation_banner.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:atpost_app/services/pulse_chat_outbox.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:atpost_app/services/realtime_service.dart';
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

  /// Per-message reveal toggle for moderation-blocked messages. Premium-only
  /// gating is enforced by the moderation banner widget itself.
  final Set<String> _revealed = <String>{};

  /// Pending outbound messages this screen is currently optimistic about.
  /// Keyed by idempotency_key. Rendered as ghost bubbles until the server
  /// echo lands (or the queue marks them failed).
  List<PulseOutboxEntry> _outbox = const [];

  /// WS subscription for `message` events filtered to this conversation
  /// (P0-4 acceptance test A — live receive without refresh).
  StreamSubscription<RealtimeEvent>? _wsSub;

  /// Outbox subscription — keeps the optimistic bubble list in sync with
  /// the queue state machine.
  StreamSubscription<List<PulseOutboxEntry>>? _outboxSub;

  /// Idempotency keys of messages we already merged into [_messages] so a
  /// late WS echo (or a polling fallback) doesn't double-paint the bubble.
  final Set<String> _seenIdempotencyKeys = <String>{};

  /// Server-side message ids we've already merged. Same idea as
  /// [_seenIdempotencyKeys] but keyed by id for in-bound messages from
  /// the other party (they don't carry our idempotency key).
  final Set<String> _seenMessageIds = <String>{};

  @override
  void initState() {
    super.initState();
    PulseBreadcrumbs.conversationOpen(conversationId: widget.conversationId);
    _load();
  }

  @override
  void dispose() {
    PulseBreadcrumbs.conversationClose(conversationId: widget.conversationId);
    _wsSub?.cancel();
    _outboxSub?.cancel();
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
      final messages = results[1] as List<PulseMessage>;
      setState(() {
        _conversation = conversations.firstWhere(
          (conversation) => conversation.id == widget.conversationId,
          orElse: () => PulseConversation(
            id: widget.conversationId,
            type: 'direct',
            status: 'active',
          ),
        );
        _messages = messages;
        _seenMessageIds.addAll(messages.map((m) => m.id));
        _loading = false;
      });
      _ensureSubscriptions();
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not load this conversation.';
        _loading = false;
      });
    }
  }

  /// Idempotent — first call wires the WS + outbox listeners, subsequent
  /// calls are no-ops. Done lazily so we only subscribe once the
  /// conversation actually loaded (otherwise events for the wrong
  /// conversation can race in during the initial fetch).
  void _ensureSubscriptions() {
    _wsSub ??= ref.read(realtimeServiceProvider).events.listen(_onWsEvent);
    if (_outboxSub == null) {
      final outbox = ref.read(pulseChatOutboxProvider);
      // Push the existing snapshot immediately so we don't flicker.
      _outbox = outbox.entriesFor(widget.conversationId);
      _outboxSub = outbox.changes.listen((_) {
        if (!mounted) return;
        setState(() {
          _outbox = outbox.entriesFor(widget.conversationId);
        });
      });
    }
  }

  void _onWsEvent(RealtimeEvent event) {
    if (event is! ChatMessageEvent) return;
    if (event.conversationId != widget.conversationId) return;
    if (!mounted) return;
    // The shared realtime_event wraps the chat-service's `message.new`
    // wire type as `ChatMessageEvent` (see realtime_event.dart switch).
    final messageId = event.messageId;
    if (messageId.isNotEmpty && _seenMessageIds.contains(messageId)) {
      return; // Already merged via REST history or a prior WS echo.
    }

    // Build a PulseMessage from the wire payload. We mirror the
    // dating-service message shape; fields the wire didn't include
    // fall back to sensible defaults.
    final payload = event.payload as Map<String, dynamic>;
    final message = PulseMessage(
      id: messageId.isEmpty
          ? 'ws-${DateTime.now().microsecondsSinceEpoch}'
          : messageId,
      conversationId: event.conversationId,
      senderUserId: event.senderId,
      messageType: event.messageType,
      bodyText: event.text.isEmpty ? null : event.text,
      mediaKey: event.mediaId,
      moderationStatus: (payload['moderation_status'] ?? 'approved').toString(),
      createdAt: payload['created_at']?.toString() ??
          event.createdAt.toIso8601String(),
      moderation: payload['moderation'] is Map
          ? Map<String, dynamic>.from(payload['moderation'] as Map)
          : null,
    );

    final idem = payload['idempotency_key']?.toString();
    if (idem != null && idem.isNotEmpty) {
      if (_seenIdempotencyKeys.contains(idem)) return;
      _seenIdempotencyKeys.add(idem);
    }
    if (messageId.isNotEmpty) _seenMessageIds.add(messageId);

    setState(() {
      _messages = [..._messages, message];
    });
  }

  Future<void> _send() async {
    final text = _messageController.text.trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    _messageController.clear();
    try {
      final outbox = ref.read(pulseChatOutboxProvider);
      // Enqueue first; the outbox immediately tries to flush. If the
      // device is offline (or the API errors out on transport) the
      // entry stays queued and we surface it as a ghost bubble.
      final entry = await outbox.enqueue(
        conversationId: widget.conversationId,
        bodyText: text,
      );
      _seenIdempotencyKeys.add(entry.idempotencyKey);
      ref.read(pulseTelemetryProvider).messageSent(
            matchId: widget.conversationId,
          );
      PulseBreadcrumbs.conversationSend(
        conversationId: widget.conversationId,
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text('Could not queue message. Try again.')),
      );
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
                    itemCount: _messages.length + _outbox.length,
                    itemBuilder: (context, index) {
                      if (index >= _messages.length) {
                        final entry = _outbox[index - _messages.length];
                        return _OutboxBubble(
                          entry: entry,
                          onRetry: () => ref
                              .read(pulseChatOutboxProvider)
                              .drain(),
                          onDiscard: () => ref
                              .read(pulseChatOutboxProvider)
                              .discard(entry.id),
                        );
                      }
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

/// Renders a ghost bubble for an outbound message that is queued,
/// sending, or failed. Failed bubbles expose retry / discard actions.
class _OutboxBubble extends StatelessWidget {
  const _OutboxBubble({
    required this.entry,
    required this.onRetry,
    required this.onDiscard,
  });

  final PulseOutboxEntry entry;
  final VoidCallback onRetry;
  final VoidCallback onDiscard;

  @override
  Widget build(BuildContext context) {
    final isFailed = entry.state == PulseOutboxState.failed;
    final isQueued = entry.state == PulseOutboxState.queued;
    final isSending = entry.state == PulseOutboxState.sending;

    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        margin: const EdgeInsets.only(bottom: 10),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.78,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Container(
              padding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 10,
              ),
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary.withValues(
                  alpha: isFailed ? 0.45 : 0.65,
                ),
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusLarge),
              ),
              child: Text(
                entry.bodyText,
                style: AppTextStyles.bodySmall.copyWith(color: Colors.white),
              ),
            ),
            const SizedBox(height: 4),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (isQueued)
                  _StateLabel(
                    icon: Icons.schedule,
                    label: 'Queued',
                    color: AppColors.textTertiary,
                    error: entry.errorMessage,
                  ),
                if (isSending)
                  _StateLabel(
                    icon: Icons.sync,
                    label: 'Sending',
                    color: AppColors.posttubePrimary,
                  ),
                if (isFailed) ...[
                  _StateLabel(
                    icon: Icons.error_outline,
                    label: 'Failed',
                    color: AppColors.statusError,
                    error: entry.errorMessage,
                  ),
                  TextButton(
                    onPressed: onRetry,
                    child: Text(
                      'Retry',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  ),
                  TextButton(
                    onPressed: onDiscard,
                    child: Text(
                      'Discard',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.statusError,
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _StateLabel extends StatelessWidget {
  const _StateLabel({
    required this.icon,
    required this.label,
    required this.color,
    this.error,
  });

  final IconData icon;
  final String label;
  final Color color;
  final String? error;

  @override
  Widget build(BuildContext context) {
    final text = (error != null && error!.isNotEmpty) ? '$label · $error' : label;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 12, color: color),
          const SizedBox(width: 4),
          Text(
            text,
            style: AppTextStyles.labelSmall.copyWith(color: color),
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
