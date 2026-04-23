import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/postmatch.dart';
import 'package:atpost_app/data/repositories/postmatch_repository.dart';
import 'package:atpost_app/services/postmatch_auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PostMatchDiscoverScreen extends ConsumerStatefulWidget {
  const PostMatchDiscoverScreen({super.key});

  @override
  ConsumerState<PostMatchDiscoverScreen> createState() =>
      _PostMatchDiscoverScreenState();
}

class _PostMatchDiscoverScreenState
    extends ConsumerState<PostMatchDiscoverScreen> {
  bool _loading = true;
  bool _submitting = false;
  String _error = '';
  int _currentIndex = 0;
  List<PostMatchFeedItem> _cards = const [];
  PostMatchProfile? _profile;
  List<PostMatchPhoto> _photos = const [];
  bool _showInfo = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final auth = ref.read(postMatchAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    if (!auth.isReady) {
      context.go('/postmatch/onboarding');
      return;
    }

    setState(() {
      _loading = true;
      _error = '';
    });

    try {
      final repo = ref.read(postMatchRepositoryProvider);
      final results = await Future.wait([
        repo.getDiscoveryFeed(),
        repo.getProfile(),
        repo.getPhotos(),
      ]);
      if (!mounted) return;
      setState(() {
        _cards = results[0] as List<PostMatchFeedItem>;
        _profile = results[1] as PostMatchProfile?;
        _photos = results[2] as List<PostMatchPhoto>;
        _currentIndex = 0;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not load your discovery feed.';
        _loading = false;
      });
    }
  }

  Future<void> _handleDecision(String decision) async {
    final current = _currentCard;
    if (current == null || _submitting) return;
    setState(() => _submitting = true);
    try {
      final result = await ref
          .read(postMatchRepositoryProvider)
          .makeDecision(targetUserId: current.userId, decision: decision);
      if (!mounted) return;
      if (result.result == 'matched' && result.conversationId != null) {
        await showDialog<void>(
          context: context,
          builder: (context) => AlertDialog(
            backgroundColor: AppColors.bgSecondary,
            title: Text('It\'s a match', style: AppTextStyles.h2),
            content: Text(
              'You and ${current.firstName} liked each other.',
              style: AppTextStyles.body,
            ),
            actions: [
              TextButton(
                onPressed: () => context.pop(),
                child: Text(
                  'Keep Swiping',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ),
              TextButton(
                onPressed: () {
                  context.pop();
                  context.push('/postmatch/chat/${result.conversationId}');
                },
                child: Text(
                  'Send Message',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
            ],
          ),
        );
      }
      setState(() {
        _currentIndex += 1;
        _showInfo = false;
      });
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save your choice.')),
      );
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  PostMatchFeedItem? get _currentCard =>
      _currentIndex >= _cards.length ? null : _cards[_currentIndex];

  @override
  Widget build(BuildContext context) {
    final current = _currentCard;
    final avatarUrl = _photos
        .cast<PostMatchPhoto?>()
        .firstWhere((photo) => photo?.isPrimary ?? false, orElse: () => null)
        ?.mediaUrl;

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('PostMatch', style: AppTextStyles.h2),
        actions: [
          IconButton(
            onPressed: () => context.push('/postmatch/matches'),
            icon: const Icon(
              Icons.favorite_border,
              color: AppColors.textPrimary,
            ),
          ),
          IconButton(
            onPressed: () => context.push('/postmatch/profile'),
            icon: CircleAvatar(
              radius: 15,
              backgroundColor: AppColors.postbookPrimary.withValues(
                alpha: 0.18,
              ),
              backgroundImage: avatarUrl == null
                  ? null
                  : NetworkImage(avatarUrl),
              child: avatarUrl == null
                  ? Text(
                      (_profile?.firstName ?? 'P').substring(0, 1),
                      style: AppTextStyles.label.copyWith(
                        color: AppColors.postbookPrimary,
                      ),
                    )
                  : null,
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
          ? _ErrorState(message: _error, onRetry: _load)
          : current == null
          ? _EmptyState(onRefresh: _load)
          : Padding(
              padding: AppSpacing.pagePadding.copyWith(bottom: 24),
              child: Column(
                children: [
                  Expanded(
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                      child: Container(
                        color: AppColors.bgCard,
                        child: Column(
                          children: [
                            Expanded(
                              child: Stack(
                                fit: StackFit.expand,
                                children: [
                                  if (current.primaryPhoto?.url != null)
                                    Image.network(
                                      current.primaryPhoto!.url,
                                      fit: BoxFit.cover,
                                    )
                                  else
                                    Container(
                                      color: AppColors.bgTertiary,
                                      child: Center(
                                        child: Text(
                                          current.firstName.substring(0, 1),
                                          style: AppTextStyles.h1.copyWith(
                                            fontSize: 72,
                                          ),
                                        ),
                                      ),
                                    ),
                                  Positioned(
                                    left: 0,
                                    right: 0,
                                    bottom: 0,
                                    child: DecoratedBox(
                                      decoration: const BoxDecoration(
                                        gradient: LinearGradient(
                                          begin: Alignment.bottomCenter,
                                          end: Alignment.topCenter,
                                          colors: [
                                            Color(0xCC08080F),
                                            Color(0x0008080F),
                                          ],
                                        ),
                                      ),
                                      child: Padding(
                                        padding: const EdgeInsets.all(18),
                                        child: Column(
                                          crossAxisAlignment:
                                              CrossAxisAlignment.start,
                                          children: [
                                            Row(
                                              children: [
                                                Expanded(
                                                  child: Text(
                                                    '${current.firstName}, ${current.age}',
                                                    style: AppTextStyles.h1,
                                                  ),
                                                ),
                                                IconButton(
                                                  onPressed: () => setState(
                                                    () =>
                                                        _showInfo = !_showInfo,
                                                  ),
                                                  icon: const Icon(
                                                    Icons.info_outline,
                                                    color: Colors.white,
                                                  ),
                                                ),
                                              ],
                                            ),
                                            if (current.city != null)
                                              Text(
                                                current.city!,
                                                style: AppTextStyles.bodySmall
                                                    .copyWith(
                                                      color: Colors.white70,
                                                    ),
                                              ),
                                          ],
                                        ),
                                      ),
                                    ),
                                  ),
                                ],
                              ),
                            ),
                            AnimatedCrossFade(
                              duration: const Duration(milliseconds: 180),
                              crossFadeState: _showInfo
                                  ? CrossFadeState.showFirst
                                  : CrossFadeState.showSecond,
                              firstChild: Padding(
                                padding: const EdgeInsets.all(18),
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Wrap(
                                      spacing: 8,
                                      runSpacing: 8,
                                      children: [
                                        _InfoChip(
                                          label:
                                              '${current.compatibilityScore}% match',
                                          color: AppColors.postbookPrimary,
                                        ),
                                        _InfoChip(
                                          label: '${current.trustLevel} trust',
                                          color: AppColors.statusSuccess,
                                        ),
                                      ],
                                    ),
                                    if (current.relationshipIntent != null)
                                      Padding(
                                        padding: const EdgeInsets.only(top: 10),
                                        child: Text(
                                          current.relationshipIntent!
                                              .replaceAll('_', ' '),
                                          style: AppTextStyles.label.copyWith(
                                            color: AppColors.textSecondary,
                                          ),
                                        ),
                                      ),
                                    if (current.occupation != null)
                                      Padding(
                                        padding: const EdgeInsets.only(top: 6),
                                        child: Text(
                                          current.occupation!,
                                          style: AppTextStyles.bodySmall,
                                        ),
                                      ),
                                    if (current.bioPreview != null)
                                      Padding(
                                        padding: const EdgeInsets.only(top: 12),
                                        child: Text(
                                          current.bioPreview!,
                                          style: AppTextStyles.bodySmall,
                                        ),
                                      ),
                                  ],
                                ),
                              ),
                              secondChild: const SizedBox(height: 0),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(height: 16),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceEvenly,
                    children: [
                      _DecisionButton(
                        icon: Icons.close,
                        color: AppColors.statusError,
                        onTap: () => _handleDecision('pass'),
                      ),
                      _DecisionButton(
                        icon: Icons.star,
                        color: const Color(0xFF4EA8FF),
                        onTap: () => _handleDecision('super_like'),
                      ),
                      _DecisionButton(
                        icon: Icons.favorite,
                        color: AppColors.postbookPrimary,
                        onTap: () => _handleDecision('like'),
                        filled: true,
                      ),
                    ],
                  ),
                ],
              ),
            ),
    );
  }
}

class _DecisionButton extends StatelessWidget {
  const _DecisionButton({
    required this.icon,
    required this.color,
    required this.onTap,
    this.filled = false,
  });

  final IconData icon;
  final Color color;
  final VoidCallback onTap;
  final bool filled;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Ink(
        width: filled ? 68 : 58,
        height: filled ? 68 : 58,
        decoration: BoxDecoration(
          color: filled ? color : AppColors.bgCard,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: color, width: 2),
        ),
        child: Icon(icon, color: filled ? Colors.white : color),
      ),
    );
  }
}

class _InfoChip extends StatelessWidget {
  const _InfoChip({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }
}

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: AppSpacing.pagePadding,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(message, style: AppTextStyles.body),
            const SizedBox(height: 12),
            ElevatedButton(
              onPressed: onRetry,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
              ),
              child: const Text('Retry'),
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.onRefresh});

  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: AppSpacing.pagePadding,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('You have seen everyone for now.', style: AppTextStyles.h2),
            const SizedBox(height: 8),
            Text(
              'New profiles will appear as more people join.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 12),
            ElevatedButton(
              onPressed: onRefresh,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
              ),
              child: const Text('Refresh'),
            ),
          ],
        ),
      ),
    );
  }
}
