// Mopedu — Partner onboarding wizard (Sprint 2).
//
// Multi-step shell that hosts the per-step children. The notifier
// (`partnerOnboardingNotifier`) drives navigation; each step widget reads
// state via Riverpod and calls into the notifier when ready.
//
// Telemetry: each step entry fires `mopedu.partner.onboarding.step` from
// the notifier itself.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_kyc_step.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_payment_step.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_plan_step.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_profile_step.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_type_step.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_vehicle_docs_step.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_vehicle_step.dart';
import 'package:atpost_app/features/mopedu/partner/onboarding/onboarding_verification_step.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PartnerOnboardingScreen extends ConsumerStatefulWidget {
  const PartnerOnboardingScreen({super.key});

  @override
  ConsumerState<PartnerOnboardingScreen> createState() =>
      _PartnerOnboardingScreenState();
}

class _PartnerOnboardingScreenState
    extends ConsumerState<PartnerOnboardingScreen> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      // Fire entry telemetry for the first step. We do NOT preselect a
      // partner type — the user picks that on the type step.
      ref
          .read(mopeduTelemetryProvider)
          .mopeduPartnerOnboardingStep(stepName: 'type');
    });
  }

  @override
  Widget build(BuildContext context) {
    final st = ref.watch(partnerOnboardingNotifier);
    final progress = (st.currentStep.index + 1) /
        PartnerOnboardingStep.values.length.toDouble();

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
          onPressed: () => _onBack(context, st.currentStep),
        ),
        title: Text(_titleFor(st.currentStep), style: AppTextStyles.h2),
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(4),
          child: LinearProgressIndicator(
            value: progress,
            minHeight: 3,
            backgroundColor: AppColors.bgTertiary,
            valueColor: const AlwaysStoppedAnimation<Color>(
              AppColors.postbookPrimary,
            ),
          ),
        ),
      ),
      body: SafeArea(child: _bodyFor(st.currentStep)),
    );
  }

  String _titleFor(PartnerOnboardingStep s) {
    switch (s) {
      case PartnerOnboardingStep.type:
        return 'Choose your role';
      case PartnerOnboardingStep.profile:
        return 'Your details';
      case PartnerOnboardingStep.kyc:
        return 'KYC documents';
      case PartnerOnboardingStep.vehicle:
        return 'Vehicle details';
      case PartnerOnboardingStep.vehicleDocs:
        return 'Vehicle documents';
      case PartnerOnboardingStep.plan:
        return 'Choose a plan';
      case PartnerOnboardingStep.payment:
        return 'Payment';
      case PartnerOnboardingStep.verification:
        return 'Verification';
    }
  }

  Widget _bodyFor(PartnerOnboardingStep s) {
    switch (s) {
      case PartnerOnboardingStep.type:
        return const OnboardingTypeStep();
      case PartnerOnboardingStep.profile:
        return const OnboardingProfileStep();
      case PartnerOnboardingStep.kyc:
        return const OnboardingKycStep();
      case PartnerOnboardingStep.vehicle:
        return const OnboardingVehicleStep();
      case PartnerOnboardingStep.vehicleDocs:
        return const OnboardingVehicleDocsStep();
      case PartnerOnboardingStep.plan:
        return const OnboardingPlanStep();
      case PartnerOnboardingStep.payment:
        return const OnboardingPaymentStep();
      case PartnerOnboardingStep.verification:
        return const OnboardingVerificationStep();
    }
  }

  void _onBack(BuildContext context, PartnerOnboardingStep s) {
    if (s == PartnerOnboardingStep.type) {
      if (context.canPop()) {
        context.pop();
      } else {
        context.go('/mopedu/partner');
      }
      return;
    }
    ref.read(partnerOnboardingNotifier.notifier).back();
  }
}
