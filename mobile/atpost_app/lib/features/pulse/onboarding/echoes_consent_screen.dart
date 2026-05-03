import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/l10n/app_strings.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 1 — Pulse onboarding step B3: Echoes consent.
///
/// Single screen, single decision (Yes / Not yet). Persists via
/// PATCH `/v1/dating/profile` with field `echoes_consent: bool`. The backend
/// may not have wired this field yet (S1 backend); harmless if dropped.
///
/// Spec: PULSE_DATING_SPEC.md (Echoes section).
class EchoesConsentScreen extends ConsumerStatefulWidget {
  const EchoesConsentScreen({super.key});

  @override
  ConsumerState<EchoesConsentScreen> createState() =>
      _EchoesConsentScreenState();
}

class _EchoesConsentScreenState extends ConsumerState<EchoesConsentScreen> {
  bool _saving = false;
  String? _error;

  Future<void> _decide(bool consent) async {
    if (_saving) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await ref.read(pulseRepositoryProvider).updateEchoesConsent(consent);
      if (!mounted) return;
      // Forward into the existing identity/photos onboarding screen — that
      // screen routes to the discover feed once safety setup is complete.
      context.go('/pulse/onboarding');
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = AppStrings.t('pulse.echoes.error.save');
      });
    } finally {
      if (mounted) {
        setState(() => _saving = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: Text(AppStrings.t('pulse.app.name'), style: AppTextStyles.h2),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
      ),
      body: SafeArea(
        child: Padding(
          padding: AppSpacing.pagePadding,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SizedBox(height: AppSpacing.l),
              Text(
                AppStrings.t('pulse.echoes.title'),
                style: AppTextStyles.h1,
              ),
              const SizedBox(height: AppSpacing.l),
              Text(
                AppStrings.t('pulse.echoes.body'),
                style: AppTextStyles.body,
              ),
              const Spacer(),
              if (_error != null) ...[
                Text(
                  _error!,
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
                const SizedBox(height: AppSpacing.m),
              ],
              FilledButton(
                onPressed: _saving ? null : () => _decide(true),
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.postgramPrimary,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(
                      AppSpacing.radiusMedium,
                    ),
                  ),
                ),
                child: _saving
                    ? const SizedBox(
                        height: 18,
                        width: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : Text(
                        AppStrings.t('pulse.echoes.yes'),
                        style: AppTextStyles.bodyMedium.copyWith(
                          color: Colors.white,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
              ),
              const SizedBox(height: AppSpacing.l),
              OutlinedButton(
                onPressed: _saving ? null : () => _decide(false),
                style: OutlinedButton.styleFrom(
                  padding: const EdgeInsets.symmetric(vertical: 14),
                  side: BorderSide(color: AppColors.borderMedium),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(
                      AppSpacing.radiusMedium,
                    ),
                  ),
                ),
                child: Text(
                  AppStrings.t('pulse.echoes.no'),
                  style: AppTextStyles.bodyMedium,
                ),
              ),
              const SizedBox(height: AppSpacing.m),
              Text(
                AppStrings.t('pulse.echoes.warning'),
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.statusWarning,
                ),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: AppSpacing.l),
            ],
          ),
        ),
      ),
    );
  }
}
