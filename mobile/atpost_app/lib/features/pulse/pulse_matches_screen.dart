import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// formerly PostMatchMatchesScreen
class PulseMatchesScreen extends ConsumerStatefulWidget {
  const PulseMatchesScreen({super.key});

  @override
  ConsumerState<PulseMatchesScreen> createState() =>
      _PulseMatchesScreenState();
}

class _PulseMatchesScreenState
    extends ConsumerState<PulseMatchesScreen> {
  bool _loading = true;
  String _error = '';
  List<PulseLikeReceived> _likes = const [];
  List<PulseMatch> _matches = const [];
  List<PulseConversation> _conversations = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final auth = ref.read(pulseAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    if (!auth.isReady) {
      context.go('/pulse/onboarding');
      return;
    }

    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      final repo = ref.read(pulseRepositoryProvider);
      final results = await Future.wait([
        repo.getLikesReceived(),
        repo.getMatches(),
        repo.getConversations(),
      ]);
      if (!mounted) return;
      setState(() {
        _likes = results[0] as List<PulseLikeReceived>;
        _matches = results[1] as List<PulseMatch>;
        _conversations = results[2] as List<PulseConversation>;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not load your matches right now.';
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Matches', style: AppTextStyles.h2),
        actions: [
          IconButton(
            onPressed: () => context.push('/pulse/discover'),
            icon: const Icon(
              Icons.explore_outlined,
              color: AppColors.textPrimary,
            ),
          ),
          IconButton(
            onPressed: () => context.push('/pulse/profile'),
            icon: const Icon(
              Icons.person_outline,
              color: AppColors.textPrimary,
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
          : RefreshIndicator(
              onRefresh: _load,
              color: AppColors.postbookPrimary,
              child: ListView(
                padding: AppSpacing.pagePadding.copyWith(bottom: 30),
                children: [
                  if (_likes.isNotEmpty) ...[
                    Text('Likes You', style: AppTextStyles.h3),
                    const SizedBox(height: 10),
                    SizedBox(
                      height: 108,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        itemCount: _likes.length,
                        separatorBuilder: (_, _) => const SizedBox(width: 10),
                        itemBuilder: (context, index) {
                          final like = _likes[index];
                          return _RoundProfileCard(
                            name: like.firstName,
                            imageUrl: like.photoUrl,
                            onTap: () => context.push('/pulse/discover'),
                          );
                        },
                      ),
                    ),
                    const SizedBox(height: 20),
                  ],
                  Text('New Matches', style: AppTextStyles.h3),
                  const SizedBox(height: 10),
                  if (_matches.isEmpty)
                    _EmptyCard(
                      message:
                          'No matches yet. Keep discovering to unlock conversations.',
                    )
                  else
                    SizedBox(
                      height: 110,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        itemCount: _matches.length,
                        separatorBuilder: (_, _) => const SizedBox(width: 10),
                        itemBuilder: (context, index) {
                          final match = _matches[index];
                          return _RoundProfileCard(
                            name: match.otherUser?.firstName ?? 'Match',
                            imageUrl: match.otherUser?.photoUrl,
                            onTap: () {
                              if (match.conversationId != null) {
                                context.push(
                                  '/pulse/chat/${match.conversationId}',
                                );
                              }
                            },
                          );
                        },
                      ),
                    ),
                  const SizedBox(height: 20),
                  Text('Conversations', style: AppTextStyles.h3),
                  const SizedBox(height: 10),
                  if (_conversations.isEmpty)
                    _EmptyCard(
                      message:
                          'No conversations yet. Match with someone first.',
                    )
                  else
                    ..._conversations.map(
                      (conversation) => Padding(
                        padding: const EdgeInsets.only(bottom: 10),
                        child: _ConversationTile(conversation: conversation),
                      ),
                    ),
                ],
              ),
            ),
    );
  }
}

class _RoundProfileCard extends StatelessWidget {
  const _RoundProfileCard({required this.name, this.imageUrl, this.onTap});

  final String name;
  final String? imageUrl;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(18),
      child: Ink(
        width: 88,
        child: Column(
          children: [
            CircleAvatar(
              radius: 34,
              backgroundColor: AppColors.postbookPrimary.withValues(
                alpha: 0.14,
              ),
              backgroundImage: imageUrl == null
                  ? null
                  : NetworkImage(imageUrl!),
              child: imageUrl == null
                  ? Text(
                      name.substring(0, 1),
                      style: AppTextStyles.h2.copyWith(
                        color: AppColors.postbookPrimary,
                      ),
                    )
                  : null,
            ),
            const SizedBox(height: 8),
            Text(
              name,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.label,
            ),
          ],
        ),
      ),
    );
  }
}

class _ConversationTile extends StatelessWidget {
  const _ConversationTile({required this.conversation});

  final PulseConversation conversation;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => context.push('/pulse/chat/${conversation.id}'),
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Ink(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            CircleAvatar(
              radius: 22,
              backgroundColor: AppColors.bgTertiary,
              child: Text(
                (conversation.otherUser?.firstName ?? 'U').substring(0, 1),
                style: AppTextStyles.label.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    conversation.otherUser?.firstName ?? 'Unknown',
                    style: AppTextStyles.label,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    conversation.lastMessage?.bodyText ??
                        'Start the conversation...',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyCard extends StatelessWidget {
  const _EmptyCard({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Text(message, style: AppTextStyles.bodySmall),
    );
  }
}
