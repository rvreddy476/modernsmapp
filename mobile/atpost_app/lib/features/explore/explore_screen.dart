import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

class ExploreScreen extends StatelessWidget {
  const ExploreScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: SingleChildScrollView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Explore', style: AppTextStyles.h1),
            const SizedBox(height: 16),

            // Search bar placeholder
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Row(
                children: [
                  const Icon(Icons.search, color: AppColors.textDim, size: 20),
                  const SizedBox(width: 10),
                  Text('Search people, posts, tags...', style: AppTextStyles.body.copyWith(color: AppColors.textDim)),
                ],
              ),
            ),
            const SizedBox(height: 24),

            // Feature tiles
            Text('Features', style: AppTextStyles.h2),
            const SizedBox(height: 12),
            GridView.count(
              crossAxisCount: 2,
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              mainAxisSpacing: 12,
              crossAxisSpacing: 12,
              childAspectRatio: 1.4,
              children: [
                _FeatureTile(
                  icon: Icons.storefront,
                  label: 'Shop',
                  subtitle: 'Buy & sell',
                  color: AppColors.postbookPrimary,
                  onTap: () => context.push('/shop'),
                ),
                _FeatureTile(
                  icon: Icons.photo_album,
                  label: 'Memories',
                  subtitle: 'On this day',
                  color: AppColors.accentPurple,
                  onTap: () => context.push('/memories'),
                ),
                _FeatureTile(
                  icon: Icons.live_tv,
                  label: 'Live',
                  subtitle: 'Watch & go live',
                  color: AppColors.liveRed,
                  onTap: () => context.push('/live'),
                ),
                _FeatureTile(
                  icon: Icons.video_library,
                  label: 'PostTube',
                  subtitle: 'Long videos',
                  color: AppColors.posttubePrimary,
                  onTap: () => context.push('/posttube'),
                ),
              ],
            ),
            const SizedBox(height: 24),

            // Trending section placeholder
            Text('Trending', style: AppTextStyles.h2),
            const SizedBox(height: 12),
            Container(
              height: 160,
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: const Center(
                child: Icon(Icons.trending_up, color: AppColors.textDim, size: 40),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _FeatureTile extends StatelessWidget {
  final IconData icon;
  final String label;
  final String subtitle;
  final Color color;
  final VoidCallback onTap;

  const _FeatureTile({
    required this.icon,
    required this.label,
    required this.subtitle,
    required this.color,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: 36,
              height: 36,
              decoration: BoxDecoration(
                color: color.withValues(alpha: 0.2),
                borderRadius: BorderRadius.circular(10),
              ),
              child: Icon(icon, color: color, size: 20),
            ),
            const Spacer(),
            Text(label, style: AppTextStyles.h3),
            Text(subtitle, style: AppTextStyles.labelSmall.copyWith(color: AppColors.textDim)),
          ],
        ),
      ),
    );
  }
}
