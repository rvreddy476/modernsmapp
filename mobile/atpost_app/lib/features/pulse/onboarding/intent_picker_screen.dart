import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/l10n/app_strings.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 1 — Pulse onboarding step B1: relationship intent picker.
///
/// Three large tappable cards (Casual / Serious / Marriage). On selection we
/// PATCH `/v1/dating/profile/intent` and forward to the next onboarding step
/// (existing identity/photos screen at `/pulse/onboarding`, then Tune at
/// `/pulse/onboarding/tune`).
///
/// Spec: PULSE_DATING_SPEC.md §6.1.3.
class IntentPickerScreen extends ConsumerStatefulWidget {
  const IntentPickerScreen({super.key});

  @override
  ConsumerState<IntentPickerScreen> createState() =>
      _IntentPickerScreenState();
}

class _IntentPickerScreenState extends ConsumerState<IntentPickerScreen> {
  String? _selected;
  bool _saving = false;
  String? _error;

  static const _options = <_IntentOption>[
    _IntentOption(
      value: 'casual',
      icon: Icons.coffee_outlined,
      titleKey: 'pulse.intent.casual.title',
      descriptionKey: 'pulse.intent.casual.description',
      exampleKey: 'pulse.intent.casual.example',
    ),
    _IntentOption(
      value: 'serious',
      icon: Icons.favorite_outline,
      titleKey: 'pulse.intent.serious.title',
      descriptionKey: 'pulse.intent.serious.description',
      exampleKey: 'pulse.intent.serious.example',
    ),
    _IntentOption(
      value: 'marriage',
      icon: Icons.diversity_3_outlined,
      titleKey: 'pulse.intent.marriage.title',
      descriptionKey: 'pulse.intent.marriage.description',
      exampleKey: 'pulse.intent.marriage.example',
    ),
  ];

  Future<void> _continue() async {
    final selected = _selected;
    if (selected == null || _saving) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await ref.read(pulseRepositoryProvider).updateIntent(selected);
      if (!mounted) return;
      // Forward into the existing identity/photos onboarding screen.
      context.go('/pulse/onboarding');
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = AppStrings.t('pulse.intent.error.save');
      });
    } finally {
      if (mounted) {
        setState(() => _saving = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final canContinue = _selected != null && !_saving;
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
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const SizedBox(height: AppSpacing.l),
              Text(
                AppStrings.t('pulse.intent.title'),
                style: AppTextStyles.h1,
              ),
              const SizedBox(height: AppSpacing.m),
              Text(
                AppStrings.t('pulse.intent.subtitle'),
                style: AppTextStyles.body,
              ),
              const SizedBox(height: AppSpacing.xxl),
              Expanded(
                child: ListView.separated(
                  itemCount: _options.length,
                  separatorBuilder: (_, _) =>
                      const SizedBox(height: AppSpacing.l),
                  itemBuilder: (context, index) {
                    final option = _options[index];
                    return _IntentCard(
                      option: option,
                      selected: _selected == option.value,
                      onTap: _saving
                          ? null
                          : () => setState(() => _selected = option.value),
                    );
                  },
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: AppSpacing.m),
                Text(
                  _error!,
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
              ],
              const SizedBox(height: AppSpacing.l),
              Text(
                AppStrings.t('pulse.intent.footer'),
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: AppSpacing.l),
              SizedBox(
                width: double.infinity,
                child: FilledButton(
                  onPressed: canContinue ? _continue : null,
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
                          AppStrings.t('pulse.intent.continue'),
                          style: AppTextStyles.bodyMedium.copyWith(
                            color: Colors.white,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                ),
              ),
              const SizedBox(height: AppSpacing.l),
            ],
          ),
        ),
      ),
    );
  }
}

class _IntentOption {
  const _IntentOption({
    required this.value,
    required this.icon,
    required this.titleKey,
    required this.descriptionKey,
    required this.exampleKey,
  });
  final String value;
  final IconData icon;
  final String titleKey;
  final String descriptionKey;
  final String exampleKey;
}

class _IntentCard extends StatelessWidget {
  const _IntentCard({
    required this.option,
    required this.selected,
    required this.onTap,
  });

  final _IntentOption option;
  final bool selected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final borderColor = selected
        ? AppColors.postgramPrimary
        : AppColors.borderSubtle;
    return Material(
      color: AppColors.bgCard,
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: Container(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: borderColor, width: selected ? 2 : 1),
          ),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(
                option.icon,
                size: 32,
                color: selected
                    ? AppColors.postgramPrimary
                    : AppColors.textPrimary,
              ),
              const SizedBox(width: AppSpacing.l),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      AppStrings.t(option.titleKey),
                      style: AppTextStyles.h3,
                    ),
                    const SizedBox(height: AppSpacing.xs),
                    Text(
                      AppStrings.t(option.descriptionKey),
                      style: AppTextStyles.body,
                    ),
                    const SizedBox(height: AppSpacing.s),
                    Text(
                      AppStrings.t(option.exampleKey),
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              if (selected)
                const Icon(
                  Icons.check_circle,
                  color: AppColors.postgramPrimary,
                  size: 22,
                ),
            ],
          ),
        ),
      ),
    );
  }
}
