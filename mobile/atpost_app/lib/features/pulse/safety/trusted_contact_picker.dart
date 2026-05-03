import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — trusted contact picker.
///
/// `flutter_contacts` is NOT in pubspec yet (documented as Sprint 5 work),
/// so this screen always renders the manual phone+name fallback. When that
/// dep lands, swap the manual form for a `flutter_contacts` picker.
class TrustedContactPicker extends ConsumerStatefulWidget {
  const TrustedContactPicker({super.key});

  @override
  ConsumerState<TrustedContactPicker> createState() =>
      _TrustedContactPickerState();
}

class _TrustedContactPickerState extends ConsumerState<TrustedContactPicker> {
  final _nameController = TextEditingController();
  final _phoneController = TextEditingController();
  final _relationshipController = TextEditingController();
  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    final existing = ref.read(trustedContactProvider);
    if (existing != null) {
      _nameController.text = existing.name;
      _phoneController.text = existing.phone;
      _relationshipController.text = existing.relationship ?? '';
    }
  }

  @override
  void dispose() {
    _nameController.dispose();
    _phoneController.dispose();
    _relationshipController.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _nameController.text.trim();
    final phone = _phoneController.text.trim();
    if (name.isEmpty || phone.length < 10) {
      setState(() => _error = 'Please enter a name and a 10-digit number.');
      return;
    }
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      // Persist locally so the safety center always has it cached.
      final id = 'local_${DateTime.now().millisecondsSinceEpoch}';
      await ref.read(trustedContactProvider.notifier).save(
            TrustedContact(
              id: id,
              name: name,
              phone: phone,
              relationship: _relationshipController.text.trim().isEmpty
                  ? null
                  : _relationshipController.text.trim(),
            ),
          );
      // Best-effort mirror to backend via existing profile patch.
      try {
        final repo = ref.read(pulseRepositoryProvider);
        await repo.updateProfile({
          'trusted_contact': {
            'name': name,
            'phone': phone,
            if (_relationshipController.text.trim().isNotEmpty)
              'relationship': _relationshipController.text.trim(),
          },
        });
      } catch (_) {
        // Local save already happened — the server can sync on next opportunity.
      }
      if (!mounted) return;
      Navigator.of(context).pop(true);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Trusted contact saved.')),
      );
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _error = 'Could not save. Please try again.';
      });
    }
  }

  Future<void> _clear() async {
    await ref.read(trustedContactProvider.notifier).clear();
    if (!mounted) return;
    _nameController.clear();
    _phoneController.clear();
    _relationshipController.clear();
    setState(() {});
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Trusted contact removed.')),
    );
  }

  @override
  Widget build(BuildContext context) {
    final existing = ref.watch(trustedContactProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Trusted contact', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
        ),
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(top: 14, bottom: 28),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(
                      AppSpacing.radiusLarge),
                  border: Border.all(
                      color: AppColors.posttubePrimary.withAlpha(80)),
                ),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Icon(Icons.shield_outlined,
                        color: AppColors.posttubePrimary, size: 20),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        'Pulse will share your live location with this '
                        'person ONLY when you choose to during a meet, '
                        'or when you tap the panic button.',
                        style: AppTextStyles.bodySmall,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 14),
              Text('Name', style: AppTextStyles.label),
              const SizedBox(height: 6),
              TextField(
                controller: _nameController,
                style: AppTextStyles.body
                    .copyWith(color: AppColors.textPrimary),
                decoration: const InputDecoration(
                  hintText: 'e.g. Priya (sister)',
                ),
              ),
              const SizedBox(height: 12),
              Text('Phone (10 digits)', style: AppTextStyles.label),
              const SizedBox(height: 6),
              TextField(
                controller: _phoneController,
                keyboardType: TextInputType.phone,
                style: AppTextStyles.body
                    .copyWith(color: AppColors.textPrimary),
                decoration: const InputDecoration(
                  hintText: '+91 9XXXXXXXXX',
                ),
              ),
              const SizedBox(height: 12),
              Text('Relationship (optional)', style: AppTextStyles.label),
              const SizedBox(height: 6),
              TextField(
                controller: _relationshipController,
                style: AppTextStyles.body
                    .copyWith(color: AppColors.textPrimary),
                decoration: const InputDecoration(
                  hintText: 'e.g. Sister, friend, flatmate',
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: 10),
                Text(_error!,
                    style: AppTextStyles.bodySmall
                        .copyWith(color: AppColors.statusError)),
              ],
              const SizedBox(height: 18),
              FilledButton(
                onPressed: _saving ? null : _save,
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  minimumSize: const Size.fromHeight(48),
                  shape: RoundedRectangleBorder(
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                ),
                child: _saving
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : Text('Save trusted contact',
                        style: AppTextStyles.h3
                            .copyWith(color: Colors.white)),
              ),
              if (existing != null) ...[
                const SizedBox(height: 10),
                TextButton(
                  onPressed: _clear,
                  child: Text('Remove trusted contact',
                      style: AppTextStyles.label
                          .copyWith(color: AppColors.statusError)),
                ),
              ],
              const SizedBox(height: 18),
              Text(
                'Sprint 5 follow-up: enable native contact picker via '
                'flutter_contacts (currently not in pubspec).',
                style: AppTextStyles.labelSmall,
                textAlign: TextAlign.center,
              ),
            ],
          ),
        ),
      ),
    );
  }
}
