// Onboarding step 4 — vehicle details.
//
// Collects vehicle type, make, model, year, color, registration number.
// On submit calls `submitVehicle()`.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class OnboardingVehicleStep extends ConsumerStatefulWidget {
  const OnboardingVehicleStep({super.key});

  @override
  ConsumerState<OnboardingVehicleStep> createState() =>
      _OnboardingVehicleStepState();
}

class _OnboardingVehicleStepState extends ConsumerState<OnboardingVehicleStep> {
  String _vehicleType = VehicleType.auto;
  late final TextEditingController _make;
  late final TextEditingController _model;
  late final TextEditingController _year;
  late final TextEditingController _color;
  late final TextEditingController _reg;

  @override
  void initState() {
    super.initState();
    _make = TextEditingController();
    _model = TextEditingController();
    _year = TextEditingController();
    _color = TextEditingController();
    _reg = TextEditingController();
  }

  @override
  void dispose() {
    _make.dispose();
    _model.dispose();
    _year.dispose();
    _color.dispose();
    _reg.dispose();
    super.dispose();
  }

  bool get _formValid {
    return _make.text.trim().length >= 2 &&
        _model.text.trim().length >= 1 &&
        (int.tryParse(_year.text.trim()) ?? 0) > 1990 &&
        _color.text.trim().isNotEmpty &&
        _reg.text.trim().length >= 6;
  }

  Future<void> _submit() async {
    final ok = await ref
        .read(partnerOnboardingNotifier.notifier)
        .submitVehicle(
          vehicleType: _vehicleType,
          make: _make.text.trim(),
          model: _model.text.trim(),
          year: int.tryParse(_year.text.trim()) ?? 0,
          color: _color.text.trim(),
          registrationNumber: _reg.text.trim().toUpperCase(),
        );
    if (!ok && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save vehicle. Please retry.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final st = ref.watch(partnerOnboardingNotifier);
    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text('Vehicle details', style: AppTextStyles.h2),
              const SizedBox(height: 4),
              Text(
                'Enter the vehicle you will drive on Mopedu.',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: 16),
              _TypeRow(
                selected: _vehicleType,
                onChange: (v) => setState(() => _vehicleType = v),
              ),
              const SizedBox(height: 16),
              _Field(
                controller: _make,
                label: 'Make (e.g. Bajaj, Maruti)',
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 12),
              _Field(
                controller: _model,
                label: 'Model (e.g. Swift, RE)',
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: _Field(
                      controller: _year,
                      label: 'Year',
                      keyboard: TextInputType.number,
                      onChanged: (_) => setState(() {}),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: _Field(
                      controller: _color,
                      label: 'Color',
                      onChanged: (_) => setState(() {}),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              _Field(
                controller: _reg,
                label: 'Registration number',
                onChanged: (_) => setState(() {}),
              ),
            ],
          ),
        ),
        _ContinueBar(
          enabled: _formValid && !st.busy,
          busy: st.busy,
          onTap: _submit,
        ),
      ],
    );
  }
}

class _TypeRow extends StatelessWidget {
  const _TypeRow({required this.selected, required this.onChange});

  final String selected;
  final ValueChanged<String> onChange;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 44,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: VehicleType.all.length,
        separatorBuilder: (_, _) => const SizedBox(width: 8),
        itemBuilder: (_, i) {
          final t = VehicleType.all[i];
          final isSel = t == selected;
          return InkWell(
            borderRadius: BorderRadius.circular(99),
            onTap: () => onChange(t),
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
              decoration: BoxDecoration(
                color: isSel
                    ? AppColors.postbookPrimary.withValues(alpha: 0.18)
                    : AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(99),
                border: Border.all(
                  color: isSel
                      ? AppColors.postbookPrimary
                      : AppColors.borderSubtle,
                  width: isSel ? 1.5 : 1,
                ),
              ),
              child: Text(
                VehicleType.label(t),
                style: AppTextStyles.label.copyWith(
                  color: isSel
                      ? AppColors.postbookPrimary
                      : AppColors.textSecondary,
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}

class _Field extends StatelessWidget {
  const _Field({
    required this.controller,
    required this.label,
    this.keyboard,
    this.onChanged,
  });

  final TextEditingController controller;
  final String label;
  final TextInputType? keyboard;
  final ValueChanged<String>? onChanged;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 4, bottom: 6),
          child: Text(label, style: AppTextStyles.labelSmall),
        ),
        TextField(
          controller: controller,
          keyboardType: keyboard,
          onChanged: onChanged,
          style: AppTextStyles.body,
          decoration: InputDecoration(
            filled: true,
            fillColor: AppColors.bgSecondary,
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              borderSide: const BorderSide(color: AppColors.borderSubtle),
            ),
            enabledBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              borderSide: const BorderSide(color: AppColors.borderSubtle),
            ),
            contentPadding: const EdgeInsets.symmetric(
              horizontal: 12,
              vertical: 14,
            ),
          ),
        ),
      ],
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
                  'Save and continue',
                  style: AppTextStyles.h3.copyWith(color: Colors.white),
                ),
        ),
      ),
    );
  }
}
