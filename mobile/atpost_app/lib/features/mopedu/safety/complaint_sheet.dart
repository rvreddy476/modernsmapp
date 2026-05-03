// Mopedu — complaint submission sheet.
//
// Sprint 3. Triggered from the ride summary screen "Issue with this ride?"
// link. Category radio + 200-char description. On submit, fires
// `submitComplaint` and refreshes the customer's complaint list.
//
// PRIVACY: telemetry carries the category bucket only — never the
// description, ride id, or partner identifiers.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ComplaintSheet extends ConsumerStatefulWidget {
  const ComplaintSheet({super.key, required this.rideId});

  final String rideId;

  @override
  ConsumerState<ComplaintSheet> createState() => _ComplaintSheetState();
}

class _ComplaintSheetState extends ConsumerState<ComplaintSheet> {
  ComplaintCategory _category = ComplaintCategory.driverBehavior;
  final _description = TextEditingController();
  bool _submitting = false;
  String? _error;

  static const int _maxChars = 200;

  @override
  void dispose() {
    _description.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      await repo.submitComplaint(
        widget.rideId,
        category: _category,
        description: _description.text.trim().isEmpty
            ? null
            : _description.text.trim(),
      );
      // PRIVACY: category only.
      ref.read(mopeduTelemetryProvider).mopeduComplaintSubmitted(
            category: _category.wire,
          );
      ref.invalidate(myComplaintsProvider);
      if (!mounted) return;
      Navigator.of(context).pop(true);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text("Thanks. We'll get back to you within 24 hours."),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = 'Could not submit. Please try again.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final padding = MediaQuery.of(context).viewInsets.bottom;
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
            Text('Report an issue', style: AppTextStyles.h2),
            const SizedBox(height: 4),
            Text(
              'Pick the closest category and (optionally) tell us more.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 14),
            for (final c in ComplaintCategory.values)
              _CategoryRow(
                category: c,
                selected: _category == c,
                onTap: () => setState(() => _category = c),
              ),
            const SizedBox(height: 12),
            TextField(
              controller: _description,
              maxLines: 4,
              maxLength: _maxChars,
              style: AppTextStyles.body,
              decoration: InputDecoration(
                hintText: 'Optional — what happened?',
                hintStyle: AppTextStyles.bodySmall,
                filled: true,
                fillColor: AppColors.bgTertiary,
                border: OutlineInputBorder(
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide: BorderSide.none,
                ),
                contentPadding: const EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 12,
                ),
                counterStyle: AppTextStyles.labelSmall,
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
            const SizedBox(height: 12),
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
                onPressed: _submitting ? null : _submit,
                child: _submitting
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(
                          color: Colors.white,
                          strokeWidth: 2,
                        ),
                      )
                    : const Text('Submit'),
              ),
            ),
            const SizedBox(height: 6),
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
          ],
        ),
      ),
    );
  }
}

class _CategoryRow extends StatelessWidget {
  const _CategoryRow({
    required this.category,
    required this.selected,
    required this.onTap,
  });

  final ComplaintCategory category;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.1)
              : AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Row(
          children: [
            Icon(
              selected
                  ? Icons.radio_button_checked
                  : Icons.radio_button_unchecked,
              color: selected
                  ? AppColors.postbookPrimary
                  : AppColors.textTertiary,
              size: 18,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(category.label, style: AppTextStyles.label),
            ),
          ],
        ),
      ),
    );
  }
}
