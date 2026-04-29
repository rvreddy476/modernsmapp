import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

class QaReportResult {
  final String reason;
  final String details;
  const QaReportResult({required this.reason, required this.details});
}

const _reasons = <String>[
  'Spam',
  'Off-topic',
  'Harassment',
  'Hate speech',
  'Misinformation',
  'Other',
];

/// Shows a modal bottom sheet for collecting a report. Returns null if cancelled.
Future<QaReportResult?> showQaReportSheet(BuildContext context) {
  return showModalBottomSheet<QaReportResult>(
    context: context,
    backgroundColor: AppColors.bgCard,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (ctx) => Padding(
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(ctx).viewInsets.bottom,
      ),
      child: const _QaReportSheetBody(),
    ),
  );
}

class _QaReportSheetBody extends StatefulWidget {
  const _QaReportSheetBody();

  @override
  State<_QaReportSheetBody> createState() => _QaReportSheetBodyState();
}

class _QaReportSheetBodyState extends State<_QaReportSheetBody> {
  String _reason = _reasons.first;
  final _detailsController = TextEditingController();

  @override
  void dispose() {
    _detailsController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Report content', style: AppTextStyles.h2),
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            initialValue: _reason,
            decoration: const InputDecoration(
              labelText: 'Reason',
              border: OutlineInputBorder(),
            ),
            items: _reasons
                .map((r) => DropdownMenuItem(value: r, child: Text(r)))
                .toList(),
            onChanged: (val) {
              if (val != null) setState(() => _reason = val);
            },
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _detailsController,
            minLines: 3,
            maxLines: 6,
            maxLength: 500,
            decoration: const InputDecoration(
              labelText: 'Details (optional)',
              border: OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 12),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Cancel'),
              ),
              const SizedBox(width: 8),
              ElevatedButton(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                ),
                onPressed: () {
                  Navigator.of(context).pop(
                    QaReportResult(
                      reason: _reason,
                      details: _detailsController.text.trim(),
                    ),
                  );
                },
                child: const Text('Submit'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
