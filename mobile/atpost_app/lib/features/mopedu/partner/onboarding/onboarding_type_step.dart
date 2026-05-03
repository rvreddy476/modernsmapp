// Onboarding step 1 — partner type picker.
//
// Radios: Individual driver / Owner-driver / Fleet driver / Fleet owner.
// Selection is held in the `partnerOnboardingNotifier`. "Continue" only
// advances; nothing is sent to the backend yet.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class OnboardingTypeStep extends ConsumerWidget {
  const OnboardingTypeStep({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final st = ref.watch(partnerOnboardingNotifier);
    final notifier = ref.read(partnerOnboardingNotifier.notifier);

    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text(
                'How do you plan to drive on Mopedu?',
                style: AppTextStyles.h2,
              ),
              const SizedBox(height: 8),
              Text(
                'You can change this later from your partner profile.',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: 16),
              for (final t in PartnerType.values)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: _TypeCard(
                    type: t,
                    selected: st.partnerType == t,
                    onTap: () => notifier.selectType(t),
                  ),
                ),
            ],
          ),
        ),
        _ContinueBar(
          enabled: st.partnerType != null,
          onTap: () => notifier.next(),
        ),
      ],
    );
  }
}

class _TypeCard extends StatelessWidget {
  const _TypeCard({
    required this.type,
    required this.selected,
    required this.onTap,
  });

  final PartnerType type;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.12)
              : AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
            width: selected ? 1.5 : 1,
          ),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(
              selected ? Icons.radio_button_checked : Icons.radio_button_off,
              color: selected
                  ? AppColors.postbookPrimary
                  : AppColors.textTertiary,
              size: 20,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(type.label, style: AppTextStyles.h3),
                  const SizedBox(height: 4),
                  Text(type.description, style: AppTextStyles.bodySmall),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ContinueBar extends StatelessWidget {
  const _ContinueBar({required this.enabled, required this.onTap});

  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
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
            backgroundColor: enabled
                ? AppColors.postbookPrimary
                : AppColors.bgTertiary,
            foregroundColor: Colors.white,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
          ),
          onPressed: enabled ? onTap : null,
          child: Text(
            'Continue',
            style: AppTextStyles.h3.copyWith(color: Colors.white),
          ),
        ),
      ),
    );
  }
}
