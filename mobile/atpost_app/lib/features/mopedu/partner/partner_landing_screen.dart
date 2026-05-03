// Mopedu — Partner landing screen (Sprint 2).
//
// First touch for any user who taps "Become a Mopedu Partner". Pure
// marketing surface — no API calls, no telemetry on mount (the home
// screen will fire `mopedu.opened` already; arriving on this screen is
// captured downstream when the user taps "Get started" via the
// onboarding step telemetry).

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

class PartnerLandingScreen extends StatelessWidget {
  const PartnerLandingScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.canPop() ? context.pop() : context.go('/'),
        ),
        title: Text('Mopedu partners', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 120),
        children: const [
          _Hero(),
          SizedBox(height: 24),
          _BenefitsRow(),
          SizedBox(height: 24),
          _PlanTeaser(),
          SizedBox(height: 24),
          _LegalNote(),
        ],
      ),
      bottomSheet: const _GetStartedCta(),
    );
  }
}

class _Hero extends StatelessWidget {
  const _Hero();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        gradient: AppColors.ctaGradient,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.local_taxi, color: Colors.white, size: 28),
          const SizedBox(height: 12),
          Text(
            'Become a Mopedu Partner',
            style: AppTextStyles.h1.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 8),
          Text(
            'Drive on your own terms. Pay a flat subscription, '
            'keep 100% of your fares — no commission cut.',
            style: AppTextStyles.body.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _BenefitsRow extends StatelessWidget {
  const _BenefitsRow();

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: const [
        Expanded(
          child: _BenefitCard(
            icon: Icons.trending_up,
            title: 'Earn more',
            body: 'No commission. Flat subscription only.',
          ),
        ),
        SizedBox(width: 10),
        Expanded(
          child: _BenefitCard(
            icon: Icons.payments_outlined,
            title: 'Lower commission',
            body: 'Keep 100% of your fares — flat plan only.',
          ),
        ),
        SizedBox(width: 10),
        Expanded(
          child: _BenefitCard(
            icon: Icons.schedule,
            title: 'Be your own boss',
            body: 'Go online when you want. No targets.',
          ),
        ),
      ],
    );
  }
}

class _BenefitCard extends StatelessWidget {
  const _BenefitCard({
    required this.icon,
    required this.title,
    required this.body,
  });

  final IconData icon;
  final String title;
  final String body;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, color: AppColors.posttubePrimary, size: 22),
          const SizedBox(height: 10),
          Text(title, style: AppTextStyles.h3),
          const SizedBox(height: 4),
          Text(body, style: AppTextStyles.bodySmall),
        ],
      ),
    );
  }
}

class _PlanTeaser extends StatelessWidget {
  const _PlanTeaser();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Plans from ₹199 to ₹999', style: AppTextStyles.h2),
          const SizedBox(height: 8),
          Text(
            'Start with a 7-day free Trial (10 leads). Upgrade to Basic, '
            'Plus, Pro, or Elite for more leads and higher priority.',
            style: AppTextStyles.body,
          ),
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: const [
              _PillTag(label: 'Trial — Free 7d'),
              _PillTag(label: 'Basic ₹199'),
              _PillTag(label: 'Plus ₹299'),
              _PillTag(label: 'Pro ₹499'),
              _PillTag(label: 'Elite ₹999'),
            ],
          ),
        ],
      ),
    );
  }
}

class _PillTag extends StatelessWidget {
  const _PillTag({required this.label});
  final String label;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.bgTertiary,
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Text(label, style: AppTextStyles.labelSmall),
    );
  }
}

class _LegalNote extends StatelessWidget {
  const _LegalNote();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Text(
        'You will need: Aadhaar (via DigiLocker), PAN, Driving licence, '
        'a vehicle photo + RC. We never store your Aadhaar number.',
        style: AppTextStyles.bodySmall,
      ),
    );
  }
}

class _GetStartedCta extends StatelessWidget {
  const _GetStartedCta();

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
        decoration: const BoxDecoration(
          color: AppColors.bgPrimary,
          border: Border(
            top: BorderSide(color: AppColors.borderSubtle, width: 0.5),
          ),
        ),
        child: SizedBox(
          width: double.infinity,
          height: 50,
          child: ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              foregroundColor: Colors.white,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            onPressed: () => context.push('/mopedu/partner/onboarding'),
            child: Text(
              'Get started',
              style: AppTextStyles.h3.copyWith(color: Colors.white),
            ),
          ),
        ),
      ),
    );
  }
}
