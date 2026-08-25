import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/monetization/widgets/tier_picker_sheet.dart';
import 'package:atpost_app/features/monetization/widgets/tip_sheet.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ProfileDetailScreen extends ConsumerStatefulWidget {
  const ProfileDetailScreen({super.key, required this.userId});

  final String userId;

  @override
  ConsumerState<ProfileDetailScreen> createState() =>
      _ProfileDetailScreenState();
}

class _ProfileDetailScreenState extends ConsumerState<ProfileDetailScreen> {
  bool _following = false;
  bool _subscribed = false;
  bool _openingConversation = false;

  Future<void> _openConversation() async {
    if (_openingConversation) return;
    setState(() => _openingConversation = true);
    try {
      final conversation = await ref
          .read(chatRepositoryProvider)
          .createDirectConversation(widget.userId);
      if (!mounted) return;
      context.push('/chat/${conversation.id}');
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text("Couldn't open the conversation")),
      );
    } finally {
      if (mounted) setState(() => _openingConversation = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final userAsync = ref.watch(userProfileProvider(widget.userId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: userAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, __) => const Center(child: Text('Profile unavailable')),
        data: (user) => CustomScrollView(
          slivers: [
            SliverToBoxAdapter(child: _buildHeader(user)),
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 20),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(user.displayName, style: AppTextStyles.h2.copyWith(fontSize: 24)),
                              Text('@${user.username}', style: AppTextStyles.body.copyWith(color: AppColors.textMuted)),
                            ],
                          ),
                        ),
                        _buildActionButtons(user),
                      ],
                    ),
                    if (user.bio != null) ...[
                      const SizedBox(height: 12),
                      Text(user.bio!, style: AppTextStyles.body),
                    ],
                    const SizedBox(height: 16),
                    _buildStatsRow(user),
                  ],
                ),
              ),
            ),
            const SliverToBoxAdapter(child: SizedBox(height: 20)),
            SliverFillRemaining(
              hasScrollBody: false,
              child: _buildEmptyState(),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildHeader(User user) {
    final topPadding = MediaQuery.of(context).padding.top;
    return Column(
      children: [
        Stack(
          clipBehavior: Clip.none,
          children: [
            Container(
              height: 180 + topPadding,
              width: double.infinity,
              decoration: BoxDecoration(
                gradient: AppColors.ctaGradient,
                image: user.hasCover ? DecorationImage(
                  image: NetworkImage(user.coverUrl!),
                  fit: BoxFit.cover,
                ) : null,
              ),
              child: Container(color: Colors.black26),
            ),
            Positioned(
              top: topPadding + 10,
              left: 16,
              child: _CircleAction(icon: Icons.arrow_back_ios_new_rounded, onTap: () => context.pop()),
            ),
            Positioned(
              bottom: -40,
              left: 20,
              child: Container(
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  border: Border.all(color: AppColors.bgPrimary, width: 4),
                ),
                child: CircleAvatar(
                  radius: 46,
                  backgroundColor: AppColors.bgTertiary,
                  backgroundImage: user.hasAvatar ? NetworkImage(user.avatarUrl) : null,
                  child: !user.hasAvatar ? Text(user.displayName[0].toUpperCase(), style: AppTextStyles.h1) : null,
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 48),
      ],
    );
  }

  Widget _buildActionButtons(User user) {
    return Row(
      children: [
        IconButton(
          onPressed: _openConversation,
          icon: const Icon(Icons.mail_outline_rounded, color: Colors.white),
          style: IconButton.styleFrom(backgroundColor: AppColors.bgCard),
        ),
        const SizedBox(width: 8),
        _FollowButton(userId: user.id),
      ],
    );
  }

  Widget _buildStatsRow(User user) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.start,
      children: [
        _buildStatItem('Followers', _compactCount(user.followerCount)),
        const SizedBox(width: 24),
        _buildStatItem('Following', _compactCount(user.followingCount)),
        const SizedBox(width: 24),
        _buildStatItem('Posts', user.postCount.toString()),
      ],
    );
  }

  Widget _buildStatItem(String label, String value) {
    return Row(
      children: [
        Text(value, style: AppTextStyles.label.copyWith(fontWeight: FontWeight.bold)),
        const SizedBox(width: 4),
        Text(label, style: AppTextStyles.bodySmall.copyWith(color: AppColors.textMuted)),
      ],
    );
  }

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.grid_off_rounded, size: 48, color: AppColors.textDim),
          const SizedBox(height: 12),
          Text('No posts yet', style: AppTextStyles.bodySmall),
        ],
      ),
    );
  }

  String _compactCount(int n) {
    if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}K';
    return n.toString();
  }
}

class _CircleAction extends StatelessWidget {
  final IconData icon;
  final VoidCallback onTap;
  const _CircleAction({required this.icon, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(8),
        decoration: const BoxDecoration(color: Colors.black45, shape: BoxShape.circle),
        child: Icon(icon, color: Colors.white, size: 20),
      ),
    );
  }
}

class _FollowButton extends ConsumerWidget {
  final String userId;
  const _FollowButton({required this.userId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Basic follow logic for UI demo
    return ElevatedButton(
      onPressed: () {},
      style: ElevatedButton.styleFrom(
        backgroundColor: AppColors.postbookPrimary,
        foregroundColor: Colors.white,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
      ),
      child: const Text('Follow'),
    );
  }
}
