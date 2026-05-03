// Onboarding step 5 — vehicle documents.
//
// RC + Insurance + PUC are required. Permit + Fitness are optional.
// In Sprint 2 we mark each as "submitted" against a placeholder URL —
// real photo uploads land when the media-service Flutter wrapper ships.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class OnboardingVehicleDocsStep extends ConsumerStatefulWidget {
  const OnboardingVehicleDocsStep({super.key});

  @override
  ConsumerState<OnboardingVehicleDocsStep> createState() =>
      _OnboardingVehicleDocsStepState();
}

class _OnboardingVehicleDocsStepState
    extends ConsumerState<OnboardingVehicleDocsStep> {
  final _submitted = <String>{};
  bool _busy = false;

  bool get _allRequiredDone =>
      _submitted.contains(VehicleDocumentType.rc) &&
      _submitted.contains(VehicleDocumentType.insurance) &&
      _submitted.contains(VehicleDocumentType.pollutionCert);

  @override
  Widget build(BuildContext context) {
    final st = ref.watch(partnerOnboardingNotifier);
    final v = st.vehicle;
    if (v == null) {
      return const Center(child: Text('No vehicle on file. Go back.'));
    }
    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text('Vehicle documents', style: AppTextStyles.h2),
              const SizedBox(height: 4),
              Text(
                'For ${v.make} ${v.model} (${v.registrationNumber}).',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: 16),
              for (final t in [
                VehicleDocumentType.rc,
                VehicleDocumentType.insurance,
                VehicleDocumentType.pollutionCert,
                VehicleDocumentType.permit,
                VehicleDocumentType.fitnessCert,
              ])
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: _DocTile(
                    title: VehicleDocumentType.label(t),
                    submitted: _submitted.contains(t),
                    optional: t == VehicleDocumentType.permit ||
                        t == VehicleDocumentType.fitnessCert,
                    onTap: () => _onPick(v.id, t),
                  ),
                ),
            ],
          ),
        ),
        _ContinueBar(
          enabled: _allRequiredDone && !_busy,
          busy: _busy,
          onTap: () => ref.read(partnerOnboardingNotifier.notifier).next(),
        ),
      ],
    );
  }

  Future<void> _onPick(String vehicleId, String docType) async {
    setState(() => _busy = true);
    try {
      await ref.read(mopeduRepositoryProvider).submitVehicleDocument(
            vehicleId,
            documentType: docType,
            fileUrl: 'pending://$docType',
          );
      setState(() {
        _submitted.add(docType);
        _busy = false;
      });
    } catch (_) {
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save. Please retry.')),
      );
    }
  }
}

class _DocTile extends StatelessWidget {
  const _DocTile({
    required this.title,
    required this.submitted,
    required this.onTap,
    this.optional = false,
  });

  final String title;
  final bool submitted;
  final VoidCallback onTap;
  final bool optional;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            Icon(
              submitted ? Icons.verified : Icons.upload_file,
              color: submitted
                  ? AppColors.statusSuccess
                  : AppColors.textTertiary,
              size: 22,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Row(
                children: [
                  Text(title, style: AppTextStyles.h3),
                  if (optional) ...[
                    const SizedBox(width: 6),
                    Text('(optional)', style: AppTextStyles.labelSmall),
                  ],
                ],
              ),
            ),
            Icon(
              submitted ? Icons.check : Icons.chevron_right,
              color: submitted
                  ? AppColors.statusSuccess
                  : AppColors.textTertiary,
            ),
          ],
        ),
      ),
    );
  }
}

class _ContinueBar extends StatelessWidget {
  const _ContinueBar({
    required this.enabled,
    required this.busy,
    required this.onTap,
  });

  final bool enabled;
  final bool busy;
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
            backgroundColor:
                enabled ? AppColors.postbookPrimary : AppColors.bgTertiary,
            foregroundColor: Colors.white,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
          ),
          onPressed: enabled ? onTap : null,
          child: busy
              ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(
                    color: Colors.white,
                    strokeWidth: 2,
                  ),
                )
              : Text(
                  'Continue',
                  style: AppTextStyles.h3.copyWith(color: Colors.white),
                ),
        ),
      ),
    );
  }
}
