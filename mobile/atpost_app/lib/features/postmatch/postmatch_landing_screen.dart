import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/postmatch_auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PostMatchLandingScreen extends ConsumerStatefulWidget {
  const PostMatchLandingScreen({super.key});

  @override
  ConsumerState<PostMatchLandingScreen> createState() =>
      _PostMatchLandingScreenState();
}

class _PostMatchLandingScreenState
    extends ConsumerState<PostMatchLandingScreen> {
  bool _loading = true;
  String _ctaRoute = '/postmatch/onboarding';

  static const _features = [
    (
      icon: Icons.verified_user_outlined,
      title: 'Trust-first matching',
      subtitle: 'Intent, lifestyle, and profile quality shape every card.',
    ),
    (
      icon: Icons.location_on_outlined,
      title: 'Nearby discovery',
      subtitle: 'Location helps surface people you can realistically meet.',
    ),
    (
      icon: Icons.mark_chat_unread_outlined,
      title: 'Match to chat',
      subtitle: 'Conversations unlock only after a mutual like.',
    ),
    (
      icon: Icons.shield_outlined,
      title: 'Safety tools',
      subtitle: 'Block, report, and moderation controls from day one.',
    ),
  ];

  @override
  void initState() {
    super.initState();
    _resolveCtaRoute();
  }

  Future<void> _resolveCtaRoute() async {
    final auth = ref.read(postMatchAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    setState(() {
      _ctaRoute = auth.isReady
          ? '/postmatch/discover'
          : '/postmatch/onboarding';
      _loading = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: CustomScrollView(
          slivers: [
            SliverToBoxAdapter(
              child: Padding(
                padding: AppSpacing.pagePadding.copyWith(top: 18, bottom: 110),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    _LandingHero(
                      onPressed: _loading
                          ? null
                          : () => context.push(_ctaRoute),
                    ),
                    const SizedBox(height: 22),
                    Text('Why PostMatch', style: AppTextStyles.h2),
                    const SizedBox(height: 10),
                    ..._features.map(
                      (feature) => Padding(
                        padding: const EdgeInsets.only(bottom: 10),
                        child: _FeatureCard(
                          icon: feature.icon,
                          title: feature.title,
                          subtitle: feature.subtitle,
                        ),
                      ),
                    ),
                    const SizedBox(height: 22),
                    Container(
                      width: double.infinity,
                      padding: const EdgeInsets.all(18),
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        borderRadius: BorderRadius.circular(
                          AppSpacing.radiusXL,
                        ),
                        border: Border.all(color: AppColors.borderSubtle),
                      ),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text('4-step flow', style: AppTextStyles.h3),
                          SizedBox(height: 10),
                          _FlowStep(
                            number: '01',
                            title: 'Create your profile',
                            subtitle:
                                'Basics, date of birth, gender, intent, and who you want to meet.',
                          ),
                          _FlowStep(
                            number: '02',
                            title: 'Upload photos',
                            subtitle:
                                'Add up to six profile photos and a live selfie.',
                          ),
                          _FlowStep(
                            number: '03',
                            title: 'Allow location',
                            subtitle:
                                'Optional, but useful for sorting nearby matches.',
                          ),
                          _FlowStep(
                            number: '04',
                            title: 'Start discovering',
                            subtitle:
                                'Like, pass, super-like, and chat after a match.',
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _LandingHero extends StatelessWidget {
  const _LandingHero({this.onPressed});

  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(22),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0x33FF6B35), Color(0x33FF3366), Color(0x1AFFFFFF)],
        ),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 54,
            height: 54,
            decoration: BoxDecoration(
              gradient: AppColors.ctaGradient,
              borderRadius: BorderRadius.circular(18),
            ),
            child: const Icon(Icons.favorite, color: Colors.white, size: 28),
          ),
          const SizedBox(height: 18),
          Text(
            'Find someone who actually gets you.',
            style: AppTextStyles.h1.copyWith(fontSize: 32, height: 1.08),
          ),
          const SizedBox(height: 8),
          Text(
            'Smart compatibility, clear intent matching, and safety-first design.',
            style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
          ),
          const SizedBox(height: 18),
          ElevatedButton(
            onPressed: onPressed,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              foregroundColor: Colors.white,
              disabledBackgroundColor: AppColors.postbookPrimary.withValues(
                alpha: 0.5,
              ),
              padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: Text('Create Your Profile', style: AppTextStyles.label),
          ),
        ],
      ),
    );
  }
}

class _FeatureCard extends StatelessWidget {
  const _FeatureCard({
    required this.icon,
    required this.title,
    required this.subtitle,
  });

  final IconData icon;
  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 42,
            height: 42,
            decoration: BoxDecoration(
              color: AppColors.postbookPrimary.withValues(alpha: 0.14),
              borderRadius: BorderRadius.circular(14),
            ),
            child: Icon(icon, color: AppColors.postbookPrimary),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppTextStyles.h3),
                const SizedBox(height: 2),
                Text(
                  subtitle,
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _FlowStep extends StatelessWidget {
  const _FlowStep({
    required this.number,
    required this.title,
    required this.subtitle,
  });

  final String number;
  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            number,
            style: AppTextStyles.h2.copyWith(color: AppColors.postbookPrimary),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppTextStyles.label),
                const SizedBox(height: 2),
                Text(
                  subtitle,
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
