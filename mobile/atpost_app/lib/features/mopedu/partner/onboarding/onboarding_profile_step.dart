// Onboarding step 2 — partner profile.
//
// Collects full name, phone (auto-filled if available), optional email,
// and city. On submit calls `submitProfile()` which POSTs to
// `/v1/rider/partners` and advances on success.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class OnboardingProfileStep extends ConsumerStatefulWidget {
  const OnboardingProfileStep({super.key});

  @override
  ConsumerState<OnboardingProfileStep> createState() =>
      _OnboardingProfileStepState();
}

class _OnboardingProfileStepState extends ConsumerState<OnboardingProfileStep> {
  late final TextEditingController _name;
  late final TextEditingController _phone;
  late final TextEditingController _email;
  String? _cityId;

  @override
  void initState() {
    super.initState();
    final st = ref.read(partnerOnboardingNotifier);
    _name = TextEditingController(text: st.fullName ?? '');
    _phone = TextEditingController(text: st.phone ?? '');
    _email = TextEditingController(text: st.email ?? '');
    _cityId = st.cityId ?? ref.read(selectedCityProvider);
    // Best-effort prefill of name from current user.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final u = ref.read(currentUserProvider).value;
      if (u != null && _name.text.isEmpty) {
        _name.text = u.displayName;
      }
    });
  }

  @override
  void dispose() {
    _name.dispose();
    _phone.dispose();
    _email.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final st = ref.watch(partnerOnboardingNotifier);
    final cities = ref.watch(riderCitiesProvider);

    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text('Tell us about you', style: AppTextStyles.h2),
              const SizedBox(height: 4),
              Text(
                'We will share these with our verification team only.',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: 16),
              _Field(
                controller: _name,
                label: 'Full name (as on Aadhaar)',
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 12),
              _Field(
                controller: _phone,
                label: 'Phone number',
                keyboard: TextInputType.phone,
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 12),
              _Field(
                controller: _email,
                label: 'Email (optional)',
                keyboard: TextInputType.emailAddress,
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 12),
              cities.when(
                data: (list) => _CityDropdown(
                  cities: list,
                  selected: _cityId,
                  onChanged: (v) => setState(() => _cityId = v),
                ),
                loading: () => const Padding(
                  padding: EdgeInsets.all(16),
                  child: Center(child: CircularProgressIndicator()),
                ),
                error: (_, _) => Text(
                  'Could not load cities. Pull to retry.',
                  style: AppTextStyles.bodySmall,
                ),
              ),
              if (st.error != null) ...[
                const SizedBox(height: 12),
                Text(
                  'Could not save. Please retry.',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
              ],
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

  bool get _formValid {
    return _name.text.trim().length >= 3 &&
        _phone.text.trim().length >= 10 &&
        _cityId != null &&
        _cityId!.isNotEmpty;
  }

  Future<void> _submit() async {
    final ok = await ref
        .read(partnerOnboardingNotifier.notifier)
        .submitProfile(
          fullName: _name.text.trim(),
          phone: _phone.text.trim(),
          email: _email.text.trim().isEmpty ? null : _email.text.trim(),
          cityId: _cityId!,
        );
    if (!ok && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save profile. Please retry.')),
      );
    }
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
          style: AppTextStyles.body,
          onChanged: onChanged,
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

class _CityDropdown extends StatelessWidget {
  const _CityDropdown({
    required this.cities,
    required this.selected,
    required this.onChanged,
  });

  final List<RiderCity> cities;
  final String? selected;
  final ValueChanged<String?> onChanged;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 4, bottom: 6),
          child: Text('City', style: AppTextStyles.labelSmall),
        ),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: DropdownButtonHideUnderline(
            child: DropdownButton<String>(
              isExpanded: true,
              value: selected,
              dropdownColor: AppColors.bgSecondary,
              hint: Text('Select city', style: AppTextStyles.bodySmall),
              style: AppTextStyles.body,
              items: [
                for (final c in cities.where((c) => c.isActive))
                  DropdownMenuItem(value: c.id, child: Text(c.name)),
              ],
              onChanged: onChanged,
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
