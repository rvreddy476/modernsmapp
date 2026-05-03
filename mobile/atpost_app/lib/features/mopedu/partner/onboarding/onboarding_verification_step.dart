// Onboarding step 8 — verification waiting room.
//
// Tells the partner the verification SLA and renders a status timeline
// based on the partner's profile + KYC + bank statuses. When status flips
// to `approved`, the screen pushes the partner to the dashboard.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class OnboardingVerificationStep extends ConsumerStatefulWidget {
  const OnboardingVerificationStep({super.key});

  @override
  ConsumerState<OnboardingVerificationStep> createState() =>
      _OnboardingVerificationStepState();
}

class _OnboardingVerificationStepState
    extends ConsumerState<OnboardingVerificationStep> {
  @override
  void initState() {
    super.initState();
    // Refresh the partner profile on entry so the timeline reflects the
    // verification queue's most recent decision.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.invalidate(myPartnerProfileProvider);
    });
  }

  @override
  Widget build(BuildContext context) {
    final asyncProfile = ref.watch(myPartnerProfileProvider);
    return asyncProfile.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (_, _) => Center(
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Text(
            'Could not load verification status. Pull to refresh.',
            style: AppTextStyles.body,
            textAlign: TextAlign.center,
          ),
        ),
      ),
      data: (partner) {
        if (partner != null && partner.status == PartnerStatus.approved) {
          // Approved — punt to dashboard.
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (mounted) context.go('/mopedu/partner/dashboard');
          });
        }
        return _Body(partner: partner);
      },
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.partner});
  final RiderPartner? partner;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Container(
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            gradient: AppColors.ctaGradient,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Icon(Icons.hourglass_top, color: Colors.white, size: 28),
              const SizedBox(height: 12),
              Text(
                'We are reviewing your documents',
                style: AppTextStyles.h1.copyWith(color: Colors.white),
              ),
              const SizedBox(height: 6),
              Text(
                'Typical SLA: 24 hours. We will notify you the moment the '
                'verification team is done.',
                style: AppTextStyles.body.copyWith(color: Colors.white),
              ),
            ],
          ),
        ),
        const SizedBox(height: 20),
        Text('Status timeline', style: AppTextStyles.h2),
        const SizedBox(height: 8),
        _TimelineRow(
          label: 'Profile created',
          done: partner != null,
        ),
        _TimelineRow(
          label: 'KYC documents submitted',
          done: partner != null &&
              partner!.kycStatus != VerificationStatus.draft,
        ),
        _TimelineRow(
          label: 'KYC approved',
          done: partner?.kycStatus == VerificationStatus.approved,
        ),
        _TimelineRow(
          label: 'Bank details approved',
          done: partner?.bankStatus == VerificationStatus.approved,
          optional: true,
        ),
        _TimelineRow(
          label: 'Profile approved',
          done: partner?.status == PartnerStatus.approved,
        ),
        const SizedBox(height: 16),
        OutlinedButton.icon(
          onPressed: () {},
          icon: const Icon(Icons.refresh, size: 16),
          label: const Text('Refresh status'),
        ),
      ],
    );
  }
}

class _TimelineRow extends StatelessWidget {
  const _TimelineRow({
    required this.label,
    required this.done,
    this.optional = false,
  });

  final String label;
  final bool done;
  final bool optional;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Row(
        children: [
          Icon(
            done ? Icons.check_circle : Icons.radio_button_unchecked,
            color: done ? AppColors.statusSuccess : AppColors.textTertiary,
            size: 20,
          ),
          const SizedBox(width: 10),
          Text(label, style: AppTextStyles.body),
          if (optional) ...[
            const SizedBox(width: 6),
            Text('(optional)', style: AppTextStyles.labelSmall),
          ],
        ],
      ),
    );
  }
}
