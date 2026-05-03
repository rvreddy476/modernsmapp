import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 4 — report sheet.
///
/// Categories track the spec's enum: harassment | inappropriate_photo |
/// impersonation | spam | other.
class ReportSheet extends ConsumerStatefulWidget {
  const ReportSheet({
    super.key,
    required this.targetUserId,
    this.targetName,
    this.context,
  });

  final String targetUserId;
  final String? targetName;

  /// Optional free-form context (e.g. "from message id abc123") that gets
  /// stitched onto `details` so support can find it later.
  final String? context;

  static Future<void> show(
    BuildContext context, {
    required String targetUserId,
    String? targetName,
    String? reportContext,
  }) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      builder: (_) => ReportSheet(
        targetUserId: targetUserId,
        targetName: targetName,
        context: reportContext,
      ),
    );
  }

  @override
  ConsumerState<ReportSheet> createState() => _ReportSheetState();
}

class _ReportSheetState extends ConsumerState<ReportSheet> {
  final _detailsController = TextEditingController();
  String _category = 'harassment';
  bool _busy = false;

  static const Map<String, String> _options = {
    'harassment': 'Harassment or abuse',
    'inappropriate_photo': 'Inappropriate photo',
    'impersonation': 'Impersonation / fake profile',
    'spam': 'Spam or scam',
    'other': 'Something else',
  };

  @override
  void dispose() {
    _detailsController.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    setState(() => _busy = true);
    try {
      final repo = ref.read(pulseRepositoryProvider);
      final composed = StringBuffer(_detailsController.text.trim());
      if (widget.context != null && widget.context!.isNotEmpty) {
        if (composed.isNotEmpty) composed.write('\n\n');
        composed.write('Context: ${widget.context}');
      }
      await repo.reportUser(
        targetUserId: widget.targetUserId,
        category: _category,
        details: composed.toString(),
      );
      // Sprint 5 telemetry: only the bucket, never the details body.
      ref.read(pulseTelemetryProvider).safetyReport(targetKind: 'user');
      if (!mounted) return;
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text('Report sent. Trust & Safety will review.')),
      );
    } catch (_) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not send report.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final viewInsets = MediaQuery.of(context).viewInsets;
    return Padding(
      padding: EdgeInsets.only(bottom: viewInsets.bottom),
      child: Container(
        padding: const EdgeInsets.fromLTRB(18, 14, 18, 22),
        decoration: const BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Center(
              child: Container(
                width: 44,
                height: 4,
                margin: const EdgeInsets.only(bottom: 14),
                decoration: BoxDecoration(
                  color: AppColors.borderMedium,
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusFull),
                ),
              ),
            ),
            Text(
              'Report ${widget.targetName ?? 'this profile'}',
              style: AppTextStyles.h2,
            ),
            const SizedBox(height: 8),
            Text(
              'Reports are confidential and reviewed by humans on the '
              'Trust & Safety team.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 14),
            Text('Category', style: AppTextStyles.label),
            const SizedBox(height: 6),
            ..._options.entries.map(
              (e) => RadioListTile<String>(
                value: e.key,
                groupValue: _category,
                onChanged: (v) =>
                    setState(() => _category = v ?? _category),
                title: Text(e.value, style: AppTextStyles.body),
                dense: true,
                contentPadding: EdgeInsets.zero,
                activeColor: AppColors.postbookPrimary,
              ),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _detailsController,
              maxLines: 4,
              maxLength: 500,
              style: AppTextStyles.body
                  .copyWith(color: AppColors.textPrimary),
              decoration: const InputDecoration(
                hintText: 'Add any details that will help us investigate.',
              ),
            ),
            const SizedBox(height: 8),
            FilledButton(
              onPressed: _busy ? null : _send,
              style: FilledButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                minimumSize: const Size.fromHeight(48),
              ),
              child: _busy
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: Colors.white),
                    )
                  : Text('Send report',
                      style:
                          AppTextStyles.h3.copyWith(color: Colors.white)),
            ),
          ],
        ),
      ),
    );
  }
}

/// Block confirmation. Returns `true` when the user blocked.
Future<bool> showBlockDialog(
  BuildContext context,
  WidgetRef ref, {
  required String targetUserId,
  String? targetName,
}) async {
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      backgroundColor: AppColors.bgSecondary,
      title: Text('Block ${targetName ?? 'this profile'}?',
          style: AppTextStyles.h2),
      content: Text(
        'They won\'t be able to see you on Pulse, message you, or appear '
        'in your matches. You can unblock from Settings later.',
        style: AppTextStyles.body,
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(true),
          style: FilledButton.styleFrom(
              backgroundColor: AppColors.statusError),
          child: const Text('Block'),
        ),
      ],
    ),
  );
  if (ok != true) return false;
  try {
    await ref.read(pulseRepositoryProvider).blockUser(targetUserId);
    if (!context.mounted) return true;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('User blocked.')),
    );
    return true;
  } catch (_) {
    if (!context.mounted) return false;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Could not block right now.')),
    );
    return false;
  }
}
