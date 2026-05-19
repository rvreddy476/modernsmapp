import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/conversation.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/providers/chat_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Message Requests folder (spec §3.3 / §21 item 7).
///
/// Lists conversations flagged `is_request` and lets the user Accept or
/// Decline each one. Accepted requests are promoted into the main
/// conversation list; declined ones are removed.
class MessageRequestsScreen extends ConsumerWidget {
  const MessageRequestsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final requestsAsync = ref.watch(messageRequestsProvider);

    return Scaffold(
      backgroundColor: Colors.black,
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  GlassIconButton(
                    icon: Icons.arrow_back,
                    tooltip: 'Back',
                    onPressed: () => context.pop(),
                  ),
                  const SizedBox(width: 12),
                  Text('Message Requests', style: AppTextStyles.h1),
                ],
              ),
            ),
            Expanded(
              child: requestsAsync.when(
                loading: () =>
                    const Center(child: CircularProgressIndicator()),
                error: (err, stack) => Center(
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      const Icon(
                        Icons.error_outline,
                        size: 48,
                        color: Colors.white24,
                      ),
                      const SizedBox(height: 16),
                      Text(
                        'Could not load requests',
                        style: AppTextStyles.h3,
                      ),
                      const SizedBox(height: 8),
                      TextButton(
                        onPressed: () =>
                            ref.invalidate(messageRequestsProvider),
                        child: const Text(
                          'Retry',
                          style: TextStyle(color: AppColors.postbookPrimary),
                        ),
                      ),
                    ],
                  ),
                ),
                data: (requests) {
                  if (requests.isEmpty) {
                    return Center(
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          const Icon(
                            Icons.mark_email_unread_outlined,
                            size: 48,
                            color: Colors.white24,
                          ),
                          const SizedBox(height: 16),
                          Text(
                            'No message requests',
                            style: AppTextStyles.bodySmall,
                          ),
                        ],
                      ),
                    );
                  }

                  return RefreshIndicator(
                    color: AppColors.postbookPrimary,
                    onRefresh: () async =>
                        ref.invalidate(messageRequestsProvider),
                    child: ListView.builder(
                      physics: const AlwaysScrollableScrollPhysics(),
                      itemCount: requests.length,
                      padding: const EdgeInsets.symmetric(
                        horizontal: 16,
                        vertical: 4,
                      ),
                      itemBuilder: (context, index) => Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: _RequestTile(conversation: requests[index]),
                      ),
                    ),
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _RequestTile extends ConsumerStatefulWidget {
  const _RequestTile({required this.conversation});

  final Conversation conversation;

  @override
  ConsumerState<_RequestTile> createState() => _RequestTileState();
}

class _RequestTileState extends ConsumerState<_RequestTile> {
  bool _loading = false;

  Future<void> _accept() => _act(accept: true);

  Future<void> _decline() => _act(accept: false);

  Future<void> _act({required bool accept}) async {
    if (_loading) return;
    setState(() => _loading = true);
    try {
      final repo = ref.read(chatRepositoryProvider);
      if (accept) {
        await repo.acceptMessageRequest(widget.conversation.id);
      } else {
        await repo.declineMessageRequest(widget.conversation.id);
      }
      // An accepted request moves into the main list; both actions remove
      // it from the requests folder.
      ref.invalidate(messageRequestsProvider);
      ref.invalidate(chatConversationsProvider);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(accept ? 'Request accepted' : 'Request declined'),
        ),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            accept
                ? 'Could not accept request'
                : 'Could not decline request',
          ),
        ),
      );
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final currentUserId = ref.watch(authServiceProvider).userId;
    final convo = widget.conversation;
    final displayName = convo.displayNameFor(currentUserId);
    final initial = displayName.trim().isNotEmpty
        ? displayName.trim()[0].toUpperCase()
        : 'C';
    final preview = (convo.lastMessage ?? '').trim().isNotEmpty
        ? convo.lastMessage!.trim()
        : 'Wants to send you a message';

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Row(
            children: [
              CircleAvatar(
                radius: 24,
                backgroundColor: Colors.white10,
                child: Text(initial),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      displayName,
                      style: AppTextStyles.h3,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 4),
                    Text(
                      preview,
                      style: AppTextStyles.bodySmall,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          if (_loading)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 6),
              child: SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            )
          else
            Row(
              children: [
                Expanded(
                  child: ElevatedButton(
                    onPressed: _accept,
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppColors.postbookPrimary,
                      foregroundColor: Colors.white,
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(10),
                      ),
                    ),
                    child: const Text('Accept'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: OutlinedButton(
                    onPressed: _decline,
                    style: OutlinedButton.styleFrom(
                      foregroundColor: AppColors.textSecondary,
                      side: const BorderSide(color: AppColors.borderMedium),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(10),
                      ),
                    ),
                    child: const Text('Decline'),
                  ),
                ),
              ],
            ),
        ],
      ),
    );
  }
}
