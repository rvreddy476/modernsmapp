// Onboarding step 3 — KYC documents.
//
// Surfaces five document slots:
//   1. Aadhaar via DigiLocker — reuses Pulse's DPDP disclosure copy.
//   2. PAN — text input + a "submit" button (file upload deferred to S3
//      when the media-service Flutter wrapper lands).
//   3. Driving licence — image picker if `image_picker` plugin allows;
//      else a "Coming soon" placeholder.
//   4. Profile photo — same pattern as DL.
//   5. Police verification — optional; placeholder.
//
// PRIVACY: this surface NEVER routes Aadhaar number, PAN number, or
// any document number into telemetry. The plain-text strings stay in
// memory only until the (future) media-service upload completes.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// DPDP-aware copy reused across every Aadhaar surface in AtPost. We
/// re-state it here (rather than importing the Pulse constant) so this
/// feature has no dependency on Pulse's package layout.
const String kPartnerAadhaarDpdpDisclosure =
    'We use DigiLocker. We never store your Aadhaar number. '
    'Only a verification token + the document type. You can revoke anytime.';

class OnboardingKycStep extends ConsumerStatefulWidget {
  const OnboardingKycStep({super.key});

  @override
  ConsumerState<OnboardingKycStep> createState() => _OnboardingKycStepState();
}

class _OnboardingKycStepState extends ConsumerState<OnboardingKycStep> {
  bool _aadhaarStarted = false;
  bool _aadhaarCompleted = false;
  bool _panSubmitted = false;
  bool _dlSubmitted = false;
  bool _photoSubmitted = false;
  String _panNumber = '';
  bool _busy = false;

  bool get _allRequiredDone =>
      _aadhaarCompleted && _panSubmitted && _dlSubmitted && _photoSubmitted;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text('KYC documents', style: AppTextStyles.h2),
              const SizedBox(height: 4),
              Text(
                'Required by Indian regulators. Verification typically '
                'takes under 24 hours.',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: 16),
              _AadhaarTile(
                started: _aadhaarStarted,
                completed: _aadhaarCompleted,
                onStart: _onStartAadhaar,
              ),
              const SizedBox(height: 10),
              _PanTile(
                submitted: _panSubmitted,
                value: _panNumber,
                onChanged: (v) => setState(() => _panNumber = v),
                onSubmit: _onSubmitPan,
              ),
              const SizedBox(height: 10),
              _DocTile(
                title: PartnerDocumentType.label(
                    PartnerDocumentType.drivingLicense),
                subtitle: 'Photo of your DL (front & back).',
                submitted: _dlSubmitted,
                onTap: _onPickDL,
              ),
              const SizedBox(height: 10),
              _DocTile(
                title: PartnerDocumentType.label(
                    PartnerDocumentType.profilePhoto),
                subtitle: 'A clear photo of your face.',
                submitted: _photoSubmitted,
                onTap: _onPickPhoto,
              ),
              const SizedBox(height: 10),
              _DocTile(
                title: PartnerDocumentType.label(
                    PartnerDocumentType.policeVerification),
                subtitle: 'Optional in v1 — speeds up approval if you '
                    'already have one.',
                submitted: false,
                onTap: () => _toast('Optional — skip for now.'),
                optional: true,
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

  Future<void> _onStartAadhaar() async {
    setState(() {
      _aadhaarStarted = true;
      _busy = true;
    });
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      final flow = await repo.startAadhaarKYC();
      // In a fully wired build we would push a WebView here (see
      // pulse/verification/aadhaar_verification_screen.dart for the
      // pattern). For Sprint 2 we simulate completion via a callback
      // sheet so the partner can finish the flow against a stub backend.
      if (!mounted) return;
      final code = await _showAadhaarSimSheet(flow.authorizeUrl);
      if (code == null) {
        setState(() {
          _aadhaarStarted = false;
          _busy = false;
        });
        return;
      }
      await repo.completeAadhaarKYC(code: code, state: flow.state);
      setState(() {
        _aadhaarCompleted = true;
        _busy = false;
      });
    } catch (e) {
      setState(() {
        _aadhaarStarted = false;
        _busy = false;
      });
      _toast('DigiLocker is unreachable right now. Please retry.');
    }
  }

  Future<String?> _showAadhaarSimSheet(String url) async {
    return showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) {
        return Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('DigiLocker', style: AppTextStyles.h2),
              const SizedBox(height: 8),
              Text(kPartnerAadhaarDpdpDisclosure, style: AppTextStyles.body),
              const SizedBox(height: 12),
              Text('Authorize URL:', style: AppTextStyles.labelSmall),
              const SizedBox(height: 4),
              Text(
                url,
                style: AppTextStyles.mono,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: 16),
              ElevatedButton(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                ),
                onPressed: () =>
                    Navigator.of(context).pop('mopedu_sim_${DateTime.now().millisecondsSinceEpoch}'),
                child: const Text('I have completed the Aadhaar consent'),
              ),
              const SizedBox(height: 6),
              TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Cancel'),
              ),
            ],
          ),
        );
      },
    );
  }

  Future<void> _onSubmitPan() async {
    if (_panNumber.trim().length < 8) {
      _toast('Enter a valid PAN.');
      return;
    }
    setState(() => _busy = true);
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      await repo.submitKYCDocument(
        documentType: PartnerDocumentType.pan,
        documentNumber: _panNumber.trim().toUpperCase(),
        // No file in Sprint 2 — backend accepts a placeholder pointer that
        // the verification queue will reject if no actual document exists.
        fileUrl: 'pending://pan',
      );
      setState(() {
        _panSubmitted = true;
        _busy = false;
      });
    } catch (_) {
      setState(() => _busy = false);
      _toast('Could not submit PAN. Please retry.');
    }
  }

  Future<void> _onPickDL() async {
    // The `image_picker` plugin is in pubspec but we deliberately do NOT
    // wire it here — Sprint 2 spec says use the placeholder if the
    // media-service upload pattern hasn't been ported. We mark the doc as
    // "submitted" against a placeholder URL so the verification queue
    // surfaces it for ops review.
    setState(() => _busy = true);
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      await repo.submitKYCDocument(
        documentType: PartnerDocumentType.drivingLicense,
        fileUrl: 'pending://dl',
      );
      setState(() {
        _dlSubmitted = true;
        _busy = false;
      });
      _toast('Driving licence noted. Upload a real photo from settings later.');
    } catch (_) {
      setState(() => _busy = false);
      _toast('Could not save. Please retry.');
    }
  }

  Future<void> _onPickPhoto() async {
    setState(() => _busy = true);
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      await repo.submitKYCDocument(
        documentType: PartnerDocumentType.profilePhoto,
        fileUrl: 'pending://photo',
      );
      setState(() {
        _photoSubmitted = true;
        _busy = false;
      });
    } catch (_) {
      setState(() => _busy = false);
      _toast('Could not save. Please retry.');
    }
  }

  void _toast(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }
}

class _AadhaarTile extends StatelessWidget {
  const _AadhaarTile({
    required this.started,
    required this.completed,
    required this.onStart,
  });

  final bool started;
  final bool completed;
  final VoidCallback onStart;

  @override
  Widget build(BuildContext context) {
    final color = completed
        ? AppColors.statusSuccess
        : (started ? AppColors.statusWarning : AppColors.textTertiary);
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                completed ? Icons.verified : Icons.fingerprint,
                color: color,
                size: 22,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text('Aadhaar (via DigiLocker)', style: AppTextStyles.h3),
              ),
              if (completed)
                Text(
                  'Done',
                  style: AppTextStyles.labelSmall.copyWith(color: color),
                ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            kPartnerAadhaarDpdpDisclosure,
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 10),
          if (!completed)
            ElevatedButton.icon(
              onPressed: started ? null : onStart,
              icon: const Icon(Icons.lock_outline, size: 16),
              label: const Text('Continue with DigiLocker'),
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                foregroundColor: Colors.white,
              ),
            ),
        ],
      ),
    );
  }
}

class _PanTile extends StatelessWidget {
  const _PanTile({
    required this.submitted,
    required this.value,
    required this.onChanged,
    required this.onSubmit,
  });

  final bool submitted;
  final String value;
  final ValueChanged<String> onChanged;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                submitted ? Icons.verified : Icons.credit_card,
                color: submitted
                    ? AppColors.statusSuccess
                    : AppColors.textTertiary,
                size: 22,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  PartnerDocumentType.label(PartnerDocumentType.pan),
                  style: AppTextStyles.h3,
                ),
              ),
              if (submitted)
                Text(
                  'Done',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.statusSuccess,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 8),
          if (!submitted) ...[
            TextField(
              onChanged: onChanged,
              textCapitalization: TextCapitalization.characters,
              maxLength: 10,
              style: AppTextStyles.body,
              decoration: InputDecoration(
                hintText: 'PAN (e.g. ABCDE1234F)',
                hintStyle: AppTextStyles.bodySmall,
                filled: true,
                fillColor: AppColors.bgTertiary,
                counterText: '',
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide: BorderSide.none,
                ),
                contentPadding: const EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 12,
                ),
              ),
            ),
            const SizedBox(height: 8),
            ElevatedButton(
              onPressed: value.trim().length >= 8 ? onSubmit : null,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                foregroundColor: Colors.white,
              ),
              child: const Text('Submit PAN'),
            ),
          ],
        ],
      ),
    );
  }
}

class _DocTile extends StatelessWidget {
  const _DocTile({
    required this.title,
    required this.subtitle,
    required this.submitted,
    required this.onTap,
    this.optional = false,
  });

  final String title;
  final String subtitle;
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
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Text(title, style: AppTextStyles.h3),
                      if (optional) ...[
                        const SizedBox(width: 6),
                        Text(
                          '(optional)',
                          style: AppTextStyles.labelSmall,
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 2),
                  Text(subtitle, style: AppTextStyles.bodySmall),
                ],
              ),
            ),
            const SizedBox(width: 8),
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
