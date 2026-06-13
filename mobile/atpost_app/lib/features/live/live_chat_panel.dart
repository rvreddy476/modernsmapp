// LiveChatPanel — shared chat overlay for the live-v2 viewer +
// broadcaster screens. Wraps the liveChatProvider StateNotifier which
// owns the replay buffer + RealtimeService subscription lifecycle.
//
// Layout-agnostic: consumer chooses width + height via SizedBox or
// Expanded. Auto-scrolls to bottom on new messages.

import 'dart:async';
import 'package:atpost_app/providers/live_streams_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class LiveChatPanel extends ConsumerStatefulWidget {
  final String streamId;
  const LiveChatPanel({super.key, required this.streamId});

  @override
  ConsumerState<LiveChatPanel> createState() => _LiveChatPanelState();
}

class _LiveChatPanelState extends ConsumerState<LiveChatPanel> {
  final _scrollCtrl = ScrollController();
  final _inputCtrl = TextEditingController();
  bool _sending = false;
  DateTime _lastSentAt = DateTime.fromMillisecondsSinceEpoch(0);
  static const _sendThrottle = Duration(milliseconds: 1500);
  static const _maxChars = 500;

  @override
  void dispose() {
    _scrollCtrl.dispose();
    _inputCtrl.dispose();
    super.dispose();
  }

  void _scrollToBottom() {
    // Defer one frame so the new message is in the list before we
    // measure its size.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollCtrl.hasClients) return;
      _scrollCtrl.animateTo(
        _scrollCtrl.position.maxScrollExtent,
        duration: const Duration(milliseconds: 150),
        curve: Curves.easeOut,
      );
    });
  }

  Future<void> _send() async {
    final text = _inputCtrl.text.trim();
    if (text.isEmpty || _sending) return;
    final now = DateTime.now();
    if (now.difference(_lastSentAt) < _sendThrottle) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Slow down a moment.')),
      );
      return;
    }
    _lastSentAt = now;
    setState(() => _sending = true);
    final ok = await ref
        .read(liveChatProvider(widget.streamId).notifier)
        .send(text);
    if (!mounted) return;
    setState(() => _sending = false);
    if (ok) {
      _inputCtrl.clear();
      _scrollToBottom();
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(liveChatProvider(widget.streamId));

    // Auto-scroll when the message count grows.
    ref.listen(liveChatProvider(widget.streamId), (prev, next) {
      if (prev == null) return;
      if (next.messages.length > prev.messages.length) {
        _scrollToBottom();
      }
    });

    return Container(
      decoration: BoxDecoration(
        color: Theme.of(context).cardColor,
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(16),
      ),
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            child: Row(
              children: const [
                Text('Live chat', style: TextStyle(fontWeight: FontWeight.w600)),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: !state.loaded
                ? const Center(child: SizedBox(
                    width: 18, height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ))
                : state.messages.isEmpty
                    ? const Center(
                        child: Padding(
                          padding: EdgeInsets.all(12),
                          child: Text(
                            'No messages yet. Say hi 👋',
                            style: TextStyle(fontSize: 12, color: Colors.grey),
                          ),
                        ),
                      )
                    : ListView.builder(
                        controller: _scrollCtrl,
                        padding: const EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 6,
                        ),
                        itemCount: state.messages.length,
                        itemBuilder: (_, i) {
                          final m = state.messages[i];
                          return Padding(
                            padding: const EdgeInsets.symmetric(vertical: 2),
                            child: RichText(
                              text: TextSpan(
                                style: DefaultTextStyle.of(context).style,
                                children: [
                                  TextSpan(
                                    text: 'user ${m.userId.substring(0, 6)}: ',
                                    style: const TextStyle(
                                      fontWeight: FontWeight.w600,
                                    ),
                                  ),
                                  TextSpan(text: m.text),
                                ],
                              ),
                            ),
                          );
                        },
                      ),
          ),
          if (state.errorMessage != null)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
              child: Text(
                state.errorMessage!,
                style: const TextStyle(color: Colors.red, fontSize: 11),
              ),
            ),
          const Divider(height: 1),
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 6, 8, 8),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _inputCtrl,
                    maxLength: _maxChars,
                    decoration: const InputDecoration(
                      counterText: '',
                      hintText: 'Type a message…',
                      isDense: true,
                      contentPadding: EdgeInsets.symmetric(
                        horizontal: 12,
                        vertical: 10,
                      ),
                      border: OutlineInputBorder(),
                    ),
                    onSubmitted: (_) => _send(),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: _sending ? null : _send,
                  child: const Text('Send'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
