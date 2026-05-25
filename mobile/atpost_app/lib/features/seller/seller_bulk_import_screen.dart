// Seller bulk SKU import — monitor + finalize on mobile. Uploads
// happen on web because Flutter's `file_picker` is not part of the
// mobile bundle yet; once we add it this screen can grow the upload
// CTA. For now the seller starts the job on desktop, then approves
// it from their phone after validation completes.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SellerBulkImportScreen extends ConsumerWidget {
  const SellerBulkImportScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final jobsAsync = ref.watch(bulkImportJobsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Bulk import', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        color: AppColors.postbookPrimary,
        onRefresh: () async {
          ref.invalidate(bulkImportJobsProvider);
          await ref.read(bulkImportJobsProvider.future);
        },
        child: jobsAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (e, _) => ListView(
            children: [
              SizedBox(
                height: MediaQuery.of(context).size.height * 0.5,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.xxl),
                    child: Text(
                      'Could not load import jobs.\n$e',
                      textAlign: TextAlign.center,
                      style: AppTextStyles.body,
                    ),
                  ),
                ),
              ),
            ],
          ),
          data: (jobs) {
            return ListView(
              padding: const EdgeInsets.all(AppSpacing.l),
              children: [
                Container(
                  padding: const EdgeInsets.all(AppSpacing.l),
                  decoration: BoxDecoration(
                    color: const Color(0xFFEFF6FF),
                    border: Border.all(color: const Color(0xFFBFDBFE)),
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusMedium),
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Icon(Icons.info_outline,
                          color: Color(0xFF1D4ED8), size: 20),
                      const SizedBox(width: AppSpacing.m),
                      Expanded(
                        child: Text(
                          'Upload a new CSV on the web dashboard. Once the job validates, you can approve + execute it from here.',
                          style: AppTextStyles.bodySmall.copyWith(
                            color: const Color(0xFF1E3A8A),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: AppSpacing.l),
                if (jobs.isEmpty)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 48),
                    child: Center(
                      child: Column(
                        children: [
                          const Icon(Icons.upload_file_outlined,
                              size: 56, color: AppColors.textGhost),
                          const SizedBox(height: AppSpacing.l),
                          Text('No import jobs yet', style: AppTextStyles.h3),
                        ],
                      ),
                    ),
                  )
                else
                  ...jobs.map((j) => Padding(
                        padding: const EdgeInsets.only(bottom: AppSpacing.s),
                        child: _JobRow(job: j),
                      )),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _JobRow extends ConsumerStatefulWidget {
  const _JobRow({required this.job});
  final BulkImportJob job;

  @override
  ConsumerState<_JobRow> createState() => _JobRowState();
}

class _JobRowState extends ConsumerState<_JobRow> {
  bool _executing = false;

  Future<void> _execute() async {
    setState(() => _executing = true);
    try {
      await ref
          .read(commerceRepositoryProvider)
          .executeBulkImport(widget.job.id);
      ref.invalidate(bulkImportJobsProvider);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Import started')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not start import: $e')),
      );
    } finally {
      if (mounted) setState(() => _executing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final j = widget.job;
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  j.filename.isEmpty ? 'Job ${j.id.substring(0, 8)}' : j.filename,
                  style: AppTextStyles.label,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              _StatusChip(status: j.status),
            ],
          ),
          if (j.createdAt != null) ...[
            const SizedBox(height: 2),
            Text(
              'Created ${_ago(j.createdAt!)}',
              style: AppTextStyles.bodySmall,
            ),
          ],
          const SizedBox(height: AppSpacing.s),
          _CountRow(label: 'Total rows', value: j.totalRows),
          _CountRow(label: 'Valid', value: j.validRows, accent: true),
          if (j.errorRows > 0)
            _CountRow(label: 'Errors', value: j.errorRows, danger: true),
          if (j.importedRows > 0)
            _CountRow(label: 'Imported', value: j.importedRows, accent: true),
          if (j.canExecute) ...[
            const SizedBox(height: AppSpacing.s),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  padding: const EdgeInsets.symmetric(vertical: 12),
                ),
                onPressed: _executing ? null : _execute,
                child: _executing
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : Text(
                        'Execute (${j.validRows} rows)',
                        style: const TextStyle(color: Colors.white),
                      ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _CountRow extends StatelessWidget {
  const _CountRow({
    required this.label,
    required this.value,
    this.accent = false,
    this.danger = false,
  });
  final String label;
  final int value;
  final bool accent;
  final bool danger;

  @override
  Widget build(BuildContext context) {
    final color = danger
        ? const Color(0xFFB91C1C)
        : accent
            ? const Color(0xFF047857)
            : AppColors.textSecondary;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 1),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Text(label, style: AppTextStyles.bodySmall.copyWith(color: color)),
          Text('$value', style: AppTextStyles.label.copyWith(color: color)),
        ],
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.status});
  final String status;

  @override
  Widget build(BuildContext context) {
    final (bg, fg) = switch (status) {
      'imported' || 'validated' =>
        (const Color(0xFFD1FAE5), const Color(0xFF047857)),
      'failed' =>
        (const Color(0xFFFFE4E6), const Color(0xFFB91C1C)),
      'importing' || 'validating' =>
        (const Color(0xFFDBEAFE), const Color(0xFF1D4ED8)),
      _ =>
        (const Color(0xFFFEF3C7), const Color(0xFF92400E)),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        status.toUpperCase(),
        style: AppTextStyles.labelTiny.copyWith(
          color: fg,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
  }
}

String _ago(DateTime t) {
  final diff = DateTime.now().difference(t);
  if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
  if (diff.inHours < 24) return '${diff.inHours}h ago';
  if (diff.inDays < 7) return '${diff.inDays}d ago';
  return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
}
