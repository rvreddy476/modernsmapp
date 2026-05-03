// Mopedu — trusted-contact picker sheet.
//
// Sprint 3. Bottom-sheet form. `flutter_contacts` is NOT in pubspec, so
// the "Pick from contacts" button surfaces a snackbar fallback. When the
// dep lands, swap that callback for a real contact-picker call.
//
// PRIVACY: phone, name, and relationship NEVER enter telemetry. Only the
// boolean "trusted contact set" event fires.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

const _relationshipOptions = <String>[
  'Spouse',
  'Parent',
  'Sibling',
  'Friend',
  'Other',
];

class TrustedContactPickerSheet extends ConsumerStatefulWidget {
  const TrustedContactPickerSheet({super.key, this.existing});

  final TrustedContact? existing;

  @override
  ConsumerState<TrustedContactPickerSheet> createState() =>
      _TrustedContactPickerSheetState();
}

class _TrustedContactPickerSheetState
    extends ConsumerState<TrustedContactPickerSheet> {
  late final TextEditingController _name;
  late final TextEditingController _phone;
  String? _relationship;
  bool _shareOnSos = true;
  bool _saving = false;
  bool _removing = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    final ex = widget.existing;
    _name = TextEditingController(text: ex?.name ?? '');
    final raw = ex?.phone ?? '';
    // Strip a leading +91 if present, the field shows the local part.
    final stripped = raw.startsWith('+91') ? raw.substring(3).trim() : raw;
    _phone = TextEditingController(text: stripped);
    _relationship = ex?.relationship;
    _shareOnSos = ex?.shareLocationOnSos ?? true;
  }

  @override
  void dispose() {
    _name.dispose();
    _phone.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _name.text.trim();
    final phoneLocal = _phone.text.trim();
    if (name.isEmpty) {
      setState(() => _error = 'Please enter a name.');
      return;
    }
    if (phoneLocal.length < 10) {
      setState(() => _error = 'Enter a 10-digit Indian mobile number.');
      return;
    }
    final phone = '+91$phoneLocal';
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      await repo.setTrustedContact(
        TrustedContact(
          name: name,
          phone: phone,
          relationship: _relationship,
          shareLocationOnSos: _shareOnSos,
        ),
      );
      // PRIVACY: never log phone/name. Single counter event.
      ref.read(mopeduTelemetryProvider).mopeduSafetyTrustedContactSet();
      ref.invalidate(trustedContactProvider);
      if (!mounted) return;
      Navigator.of(context).pop(true);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Trusted contact saved.')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _error = 'Could not save. Please try again.';
      });
    }
  }

  Future<void> _remove() async {
    setState(() {
      _removing = true;
      _error = null;
    });
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      // Send empty string fields to indicate removal — backend treats an
      // empty phone as a clear. Local-only fallback below if it errors.
      await repo.setTrustedContact(
        const TrustedContact(name: '', phone: '', shareLocationOnSos: false),
      );
      ref.invalidate(trustedContactProvider);
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _removing = false;
        _error = 'Could not remove. Please try again.';
      });
    }
  }

  void _pickFromContacts() {
    // `flutter_contacts` not yet in pubspec. Surface a clear snackbar so
    // the user knows the manual form is the path today.
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text(
          'Contact picker coming soon. Please enter the name and number '
          'manually for now.',
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final padding = MediaQuery.of(context).viewInsets.bottom;
    final hasExisting = widget.existing != null;

    return Padding(
      padding: EdgeInsets.fromLTRB(20, 20, 20, 20 + padding),
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Center(
              child: Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: AppColors.borderMedium,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: 14),
            Text(
              hasExisting ? 'Edit trusted contact' : 'Add trusted contact',
              style: AppTextStyles.h2,
            ),
            const SizedBox(height: 4),
            Text(
              'They will be notified with a live ride link when you '
              'press SOS.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 16),
            OutlinedButton.icon(
              onPressed: _pickFromContacts,
              icon: const Icon(Icons.contacts_outlined, size: 18),
              label: const Text('Pick from contacts'),
            ),
            const SizedBox(height: 14),
            _LabeledField(
              label: 'Contact name',
              child: TextField(
                controller: _name,
                style: AppTextStyles.body,
                decoration: _inputDecoration('e.g. Priya'),
              ),
            ),
            const SizedBox(height: 12),
            _LabeledField(
              label: 'Contact phone (India)',
              child: Row(
                children: [
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 14,
                    ),
                    decoration: BoxDecoration(
                      color: AppColors.bgTertiary,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusMedium,
                      ),
                    ),
                    child: Text('+91', style: AppTextStyles.label),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: _phone,
                      style: AppTextStyles.body,
                      keyboardType: TextInputType.phone,
                      inputFormatters: [
                        FilteringTextInputFormatter.digitsOnly,
                        LengthLimitingTextInputFormatter(10),
                      ],
                      decoration: _inputDecoration('10-digit number'),
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 12),
            _LabeledField(
              label: 'Relationship',
              child: DropdownButtonFormField<String>(
                initialValue: _relationship,
                isExpanded: true,
                style: AppTextStyles.body,
                dropdownColor: AppColors.bgTertiary,
                decoration: _inputDecoration('Select relationship'),
                items: [
                  for (final r in _relationshipOptions)
                    DropdownMenuItem(value: r, child: Text(r)),
                ],
                onChanged: (v) => setState(() => _relationship = v),
              ),
            ),
            const SizedBox(height: 12),
            SwitchListTile.adaptive(
              contentPadding: EdgeInsets.zero,
              value: _shareOnSos,
              activeColor: AppColors.posttubePrimary,
              onChanged: (v) => setState(() => _shareOnSos = v),
              title: Text(
                'Share my live location with this contact when I press SOS',
                style: AppTextStyles.label,
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: 8),
              Text(
                _error!,
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.statusError,
                ),
              ),
            ],
            const SizedBox(height: 14),
            SizedBox(
              height: 48,
              child: ElevatedButton(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                  shape: RoundedRectangleBorder(
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                ),
                onPressed: _saving ? null : _save,
                child: _saving
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(
                          color: Colors.white,
                          strokeWidth: 2,
                        ),
                      )
                    : const Text('Save'),
              ),
            ),
            if (hasExisting) ...[
              const SizedBox(height: 8),
              TextButton(
                onPressed: _removing ? null : _remove,
                child: Text(
                  _removing ? 'Removing...' : 'Remove contact',
                  style: TextStyle(color: AppColors.statusError),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  InputDecoration _inputDecoration(String hint) {
    return InputDecoration(
      hintText: hint,
      hintStyle: AppTextStyles.bodySmall,
      filled: true,
      fillColor: AppColors.bgTertiary,
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        borderSide: BorderSide.none,
      ),
      contentPadding: const EdgeInsets.symmetric(
        horizontal: 12,
        vertical: 12,
      ),
    );
  }
}

class _LabeledField extends StatelessWidget {
  const _LabeledField({required this.label, required this.child});

  final String label;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 4, bottom: 6),
          child: Text(label, style: AppTextStyles.labelSmall),
        ),
        child,
      ],
    );
  }
}
