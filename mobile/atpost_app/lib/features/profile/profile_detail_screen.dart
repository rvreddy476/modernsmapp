import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ProfileDetailScreen extends ConsumerStatefulWidget {
  const ProfileDetailScreen({super.key, required this.userId});

  final String userId;

  @override
  ConsumerState<ProfileDetailScreen> createState() => _ProfileDetailScreenState();
}

class _ProfileDetailScreenState extends ConsumerState<ProfileDetailScreen> {
  bool _following = false;

  @override
  Widget build(BuildContext context) {
    final userAsync = ref.watch(userProfileProvider(widget.userId));

    return Scaffold(
      body: SafeArea(
        child: userAsync.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('Could not load profile', style: AppTextStyles.bodySmall),
                const SizedBox(height: 12),
                TextButton(
                  onPressed: () => context.pop(),
                  child: const Text('Go back'),
                ),
              ],
            ),
          ),
          data: (user) => CustomScrollView(
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 0),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          IconButton(
                            onPressed: () => context.pop(),
                            icon: const Icon(Icons.arrow_back),
                            color: AppColors.textSecondary,
                          ),
                          const Spacer(),
                          const Icon(Icons.more_horiz, color: AppColors.textMuted),
                        ],
                      ),
                      const SizedBox(height: 8),
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Container(
                            width: 72,
                            height: 72,
                            decoration: BoxDecoration(
                              gradient: AppColors.postbookGradient,
                              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                            ),
                            child: Center(
                              child: Text(
                                user.displayName.isNotEmpty
                                    ? user.displayName[0].toUpperCase()
                                    : 'U',
                                style: AppTextStyles.h1.copyWith(color: Colors.white),
                              ),
                            ),
                          ),
                          const SizedBox(width: 16),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Row(
                                  children: [
                                    Flexible(
                                      child: Text(user.displayName, style: AppTextStyles.h2),
                                    ),
                                    if (user.isVerified) ...[
                                      const SizedBox(width: 4),
                                      const Icon(
                                        Icons.verified,
                                        size: 16,
                                        color: AppColors.postbookPrimary,
                                      ),
                                    ],
                                  ],
                                ),
                                const SizedBox(height: 2),
                                Text('@${user.username}', style: AppTextStyles.bodySmall),
                                if (user.profession != null &&
                                    user.profession!.isNotEmpty) ...[
                                  const SizedBox(height: 2),
                                  Text(
                                    user.profession!,
                                    style: AppTextStyles.bodySmall.copyWith(
                                      color: AppColors.textDim,
                                    ),
                                  ),
                                ],
                              ],
                            ),
                          ),
                        ],
                      ),
                      if (user.bio != null && user.bio!.isNotEmpty) ...[
                        const SizedBox(height: 12),
                        Text(
                          user.bio!,
                          style: AppTextStyles.bodySmall.copyWith(
                            color: AppColors.textSecondary,
                          ),
                        ),
                      ],
                      const SizedBox(height: 16),
                      Row(
                        children: [
                          _StatBadge(label: 'Followers', count: user.followerCount),
                          const SizedBox(width: 10),
                          _StatBadge(label: 'Following', count: user.followingCount),
                          const SizedBox(width: 10),
                          _StatBadge(label: 'Friends', count: user.friendCount),
                        ],
                      ),
                      const SizedBox(height: 16),
                      Row(
                        children: [
                          Expanded(
                            child: GestureDetector(
                              onTap: () => setState(() => _following = !_following),
                              child: AnimatedContainer(
                                duration: const Duration(milliseconds: 200),
                                padding: const EdgeInsets.symmetric(vertical: 12),
                                decoration: BoxDecoration(
                                  gradient: _following ? null : AppColors.postbookGradient,
                                  color: _following
                                      ? AppColors.bgCard
                                      : null,
                                  borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                                  border: Border.all(
                                    color: _following
                                        ? AppColors.borderSubtle
                                        : AppColors.postbookPrimary.withValues(alpha: 0.4),
                                  ),
                                ),
                                child: Center(
                                  child: Text(
                                    _following ? 'Following' : 'Follow',
                                    style: AppTextStyles.label.copyWith(
                                      color: _following ? AppColors.textSecondary : Colors.white,
                                    ),
                                  ),
                                ),
                              ),
                            ),
                          ),
                          const SizedBox(width: 10),
                          Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 18,
                              vertical: 12,
                            ),
                            decoration: BoxDecoration(
                              color: AppColors.bgCard,
                              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                              border: Border.all(color: AppColors.borderSubtle),
                            ),
                            child: Text('Message', style: AppTextStyles.label),
                          ),
                        ],
                      ),
                      const SizedBox(height: 20),
                    ],
                  ),
                ),
              ),
              SliverFillRemaining(
                child: Container(
                  margin: AppSpacing.pagePadding.copyWith(top: 0, bottom: 20),
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(
                          Icons.grid_on_outlined,
                          color: AppColors.textMuted,
                          size: 32,
                        ),
                        const SizedBox(height: 8),
                        Text(
                          'No posts yet',
                          style: AppTextStyles.bodySmall,
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _StatBadge extends StatelessWidget {
  const _StatBadge({required this.label, required this.count});

  final String label;
  final int count;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Text(_formatCount(count), style: AppTextStyles.h3),
          const SizedBox(height: 2),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }

  String _formatCount(int count) {
    if (count >= 1000000) return '${(count / 1000000).toStringAsFixed(1)}M';
    if (count >= 1000) return '${(count / 1000).toStringAsFixed(1)}K';
    return count.toString();
  }
}
