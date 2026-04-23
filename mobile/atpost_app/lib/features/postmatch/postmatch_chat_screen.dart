import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/postmatch.dart';
import 'package:atpost_app/data/repositories/postmatch_repository.dart';
import 'package:atpost_app/services/postmatch_auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PostMatchChatScreen extends ConsumerStatefulWidget {
  const PostMatchChatScreen({super.key, required this.conversationId});

  final String conversationId;

  @override
  ConsumerState<PostMatchChatScreen> createState() =>
      _PostMatchChatScreenState();
}

class _PostMatchChatScreenState extends ConsumerState<PostMatchChatScreen> {
  final _messageController = TextEditingController();

  bool _loading = true;
  bool _sending = false;
  String _error = '';
  List<PostMatchMessage> _messages = const [];
  PostMatchConversation? _conversation;
  Timer? _poller;

  @override
  void initState() {
    super.initState();
    _load();
    _poller = Timer.periodic(
      const Duration(seconds: 5),
      (_) => _load(silent: true),
    );
  }

  @override
  void dispose() {
    _poller?.cancel();
    _messageController.dispose();
    super.dispose();
  }

  Future<void> _load({bool silent = false}) async {
    final auth = ref.read(postMatchAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    if (!auth.isReady) {
      context.go('/postmatch/onboarding');
      return;
    }

    if (!silent) {
      setState(() {
        _loading = true;
        _error = '';
      });
    }
    try {
      final repo = ref.read(postMatchRepositoryProvider);
      final results = await Future.wait([
        repo.getConversations(),
        repo.getMessages(widget.conversationId),
      ]);
      if (!mounted) return;
      final conversations = results[0] as List<PostMatchConversation>;
      setState(() {
        _conversation = conversations.firstWhere(
          (conversation) => conversation.id == widget.conversationId,
          orElse: () => PostMatchConversation(
            id: widget.conversationId,
            type: 'direct',
            status: 'active',
          ),
        );
        _messages = results[1] as List<PostMatchMessage>;
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
          .read(postMatchRepositoryProvider)
          .sendMessage(widget.conversationId, bodyText: text);
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
                      return Align(
                        alignment: isMine
                            ? Alignment.centerRight
                            : Alignment.centerLeft,
                        child: Container(
                          margin: const EdgeInsets.only(bottom: 10),
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
