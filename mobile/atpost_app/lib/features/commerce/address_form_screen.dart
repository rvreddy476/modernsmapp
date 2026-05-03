// Address form — Sprint 1.
//
// Used for both "add new" and "edit" — when an `Address` is passed via
// `GoRouter.push(extra: existingAddress)`, the form pre-fills and the
// submit calls `updateAddress` rather than `createAddress`.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class AddressFormScreen extends ConsumerStatefulWidget {
  const AddressFormScreen({super.key, this.existing});

  final Address? existing;

  @override
  ConsumerState<AddressFormScreen> createState() => _AddressFormScreenState();
}

class _AddressFormScreenState extends ConsumerState<AddressFormScreen> {
  final _formKey = GlobalKey<FormState>();
  late final TextEditingController _fullName;
  late final TextEditingController _phone;
  late final TextEditingController _pincode;
  late final TextEditingController _city;
  late final TextEditingController _state;
  late final TextEditingController _line1;
  late final TextEditingController _line2;
  late final TextEditingController _landmark;
  String _label = 'Home';
  bool _isDefault = false;
  bool _busy = false;

  static const _labels = ['Home', 'Work', 'Other'];

  @override
  void initState() {
    super.initState();
    final e = widget.existing;
    _fullName = TextEditingController(text: e?.fullName ?? '');
    _phone = TextEditingController(text: e?.phone ?? '');
    _pincode = TextEditingController(text: e?.postalCode ?? '');
    _city = TextEditingController(text: e?.city ?? '');
    _state = TextEditingController(text: e?.state ?? '');
    _line1 = TextEditingController(text: e?.line1 ?? '');
    _line2 = TextEditingController(text: e?.line2 ?? '');
    _landmark = TextEditingController(text: e?.landmark ?? '');
    _label = e?.label ?? 'Home';
    if (!_labels.contains(_label)) _label = 'Other';
    _isDefault = e?.isDefault ?? false;

    _pincode.addListener(_onPincodeChanged);
  }

  @override
  void dispose() {
    _pincode.removeListener(_onPincodeChanged);
    _fullName.dispose();
    _phone.dispose();
    _pincode.dispose();
    _city.dispose();
    _state.dispose();
    _line1.dispose();
    _line2.dispose();
    _landmark.dispose();
    super.dispose();
  }

  void _onPincodeChanged() {
    // Backend doesn't expose state-by-pincode lookup yet; we use a static
    // first-digit table as a "best guess" pre-fill. The user can edit it.
    final p = _pincode.text;
    if (p.length != 6) return;
    if (_state.text.isNotEmpty) return;
    final guess = _stateForPincode(p);
    if (guess != null) {
      _state.text = guess;
    }
  }

  static String? _stateForPincode(String p) {
    if (p.length < 2) return null;
    final prefix = int.tryParse(p.substring(0, 2));
    if (prefix == null) return null;
    if (prefix >= 11 && prefix <= 11) return 'Delhi';
    if (prefix >= 12 && prefix <= 13) return 'Haryana';
    if (prefix >= 14 && prefix <= 19) return 'Punjab';
    if (prefix >= 20 && prefix <= 28) return 'Uttar Pradesh';
    if (prefix >= 30 && prefix <= 34) return 'Rajasthan';
    if (prefix >= 36 && prefix <= 39) return 'Gujarat';
    if (prefix >= 40 && prefix <= 44) return 'Maharashtra';
    if (prefix >= 45 && prefix <= 48) return 'Madhya Pradesh';
    if (prefix >= 50 && prefix <= 53) return 'Telangana';
    if (prefix >= 56 && prefix <= 59) return 'Karnataka';
    if (prefix >= 60 && prefix <= 64) return 'Tamil Nadu';
    if (prefix >= 67 && prefix <= 69) return 'Kerala';
    if (prefix >= 70 && prefix <= 74) return 'West Bengal';
    if (prefix >= 75 && prefix <= 77) return 'Odisha';
    if (prefix >= 78 && prefix <= 78) return 'Assam';
    if (prefix >= 80 && prefix <= 85) return 'Bihar';
    return null;
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() => _busy = true);
    final addr = Address(
      id: widget.existing?.id ?? '',
      label: _label,
      fullName: _fullName.text.trim(),
      phone: _phone.text.trim(),
      line1: _line1.text.trim(),
      line2: _line2.text.trim().isEmpty ? null : _line2.text.trim(),
      landmark: _landmark.text.trim().isEmpty ? null : _landmark.text.trim(),
      city: _city.text.trim(),
      state: _state.text.trim(),
      postalCode: _pincode.text.trim(),
      country: widget.existing?.country ?? 'IN',
      isDefault: _isDefault,
    );
    final repo = ref.read(commerceRepositoryProvider);
    try {
      if (widget.existing != null) {
        await repo.updateAddress(widget.existing!.id, addr);
      } else {
        await repo.createAddress(addr);
      }
      ref.invalidate(addressesProvider);
      if (!mounted) return;
      GoRouter.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not save address: $e')),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isEdit = widget.existing != null;
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text(isEdit ? 'Edit address' : 'Add address',
            style: AppTextStyles.h2),
      ),
      body: Form(
        key: _formKey,
        child: ListView(
          padding: const EdgeInsets.all(AppSpacing.l),
          children: [
            DropdownButtonFormField<String>(
              initialValue: _label,
              decoration: _decoration('Label'),
              dropdownColor: AppColors.bgSecondary,
              style: AppTextStyles.body,
              items: _labels
                  .map((l) => DropdownMenuItem(value: l, child: Text(l)))
                  .toList(),
              onChanged: (v) => setState(() => _label = v ?? 'Home'),
            ),
            const SizedBox(height: AppSpacing.l),
            _field(_fullName, 'Full name', required: true),
            const SizedBox(height: AppSpacing.l),
            _field(
              _phone,
              'Phone',
              required: true,
              keyboard: TextInputType.phone,
              formatters: [
                FilteringTextInputFormatter.digitsOnly,
                LengthLimitingTextInputFormatter(10),
              ],
            ),
            const SizedBox(height: AppSpacing.l),
            _field(
              _pincode,
              'Pincode',
              required: true,
              keyboard: TextInputType.number,
              formatters: [
                FilteringTextInputFormatter.digitsOnly,
                LengthLimitingTextInputFormatter(6),
              ],
              validate: (v) =>
                  v == null || v.length != 6 ? 'Enter a 6-digit pincode' : null,
            ),
            const SizedBox(height: AppSpacing.l),
            Row(
              children: [
                Expanded(child: _field(_city, 'City', required: true)),
                const SizedBox(width: AppSpacing.l),
                Expanded(child: _field(_state, 'State', required: true)),
              ],
            ),
            const SizedBox(height: AppSpacing.l),
            _field(_line1, 'Address line 1', required: true),
            const SizedBox(height: AppSpacing.l),
            _field(_line2, 'Address line 2 (optional)'),
            const SizedBox(height: AppSpacing.l),
            _field(_landmark, 'Landmark (optional)'),
            const SizedBox(height: AppSpacing.l),
            SwitchListTile(
              value: _isDefault,
              onChanged: (v) => setState(() => _isDefault = v),
              title: Text('Set as default', style: AppTextStyles.label),
              activeThumbColor: AppColors.postbookPrimary,
              contentPadding: EdgeInsets.zero,
            ),
            const SizedBox(height: AppSpacing.xxl),
            ElevatedButton(
              onPressed: _busy ? null : _submit,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              child: Text(_busy ? 'Saving…' : 'Save address'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _field(
    TextEditingController c,
    String label, {
    bool required = false,
    TextInputType? keyboard,
    List<TextInputFormatter>? formatters,
    String? Function(String?)? validate,
  }) {
    return TextFormField(
      controller: c,
      keyboardType: keyboard,
      inputFormatters: formatters,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: _decoration(label),
      validator: (v) {
        if (validate != null) return validate(v);
        if (required && (v == null || v.trim().isEmpty)) {
          return 'Required';
        }
        return null;
      },
    );
  }

  InputDecoration _decoration(String label) {
    return InputDecoration(
      labelText: label,
      labelStyle: AppTextStyles.bodySmall,
      filled: true,
      fillColor: AppColors.bgCard,
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
        borderSide: const BorderSide(color: AppColors.postbookPrimary),
      ),
    );
  }
}
