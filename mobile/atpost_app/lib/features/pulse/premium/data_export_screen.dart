// DPDP data export — Sprint 5.
//
// Lists past export requests + a CTA to request a new one. Backend rate-
// limits to one request per 7 days per user.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class DataExportScreen extends ConsumerWidget {
  const DataExportScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final exportsAsync = ref.watch(dataExportsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        title: Text('Data export', style: AppTextStyles.h2),
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        iconTheme: const IconThemeData(color: AppColors.textPrimary),
      ),
      body: exportsAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(
            color: AppColors.postbookPrimary,
          ),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Text(
              'Could not load your export history.',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (exports) => _Body(exports: exports),
      ),
    );
  }
}

class _Body extends ConsumerStatefulWidget {
  const _Body({required this.exports});
  final List<DataExportRecord> exports;

  @override
  ConsumerState<_Body> createState() => _BodyState();
}

class _BodyState extends ConsumerState<_Body> {
  bool _requesting = false;

  Future<void> _request() async {
    setState(() => _requesting = true);
    try {
      await ref.read(pulseRepositoryProvider).requestDataExport();
      if (!mounted) return;
      ref.invalidate(dataExportsProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Export requested. We\'ll notify you when it\'s ready.',
          ),
        ),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'Could not request an export. You may have requested one in '
            'the last 7 days.',
          ),
        ),
      );
    } finally {
      if (mounted) setState(() => _requesting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Container(
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Export your Pulse data',
                style: AppTextStyles.h3,
              ),
              const SizedBox(height: 8),
              Text(
                'Under India\'s DPDP Act you can request a copy of your '
                'Pulse profile, Tune, photos, sparks, matches, and messages. '
                'We\'ll prepare a JSON archive and email a download link '
                '(valid 7 days). One request per week per account.',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: 12),
              ElevatedButton(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                ),
                onPressed: _requesting ? null : _request,
                child: _requesting
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Text('Request a new export'),
              ),
            ],
          ),
        ),
        const SizedBox(height: 24),
        Text(
          'Past exports',
          style: AppTextStyles.h3,
        ),
        const SizedBox(height: 8),
        if (widget.exports.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 24),
            child: Text(
              'No exports yet.',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textTertiary,
              ),
            ),
          )
        else
          ...widget.exports.map((e) => _ExportRow(record: e)),
      ],
    );
  }
}

class _ExportRow extends StatelessWidget {
  const _ExportRow({required this.record});
  final DataExportRecord record;

  @override
  Widget build(BuildContext context) {
    final requested = record.requestedAt;
    final ready = record.isReady;
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(
            ready ? Icons.download_done_rounded : Icons.hourglass_top_rounded,
            color: ready
                ? AppColors.postbookPrimary
                : AppColors.textTertiary,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  record.status.toUpperCase(),
                  style: AppTextStyles.labelSmall,
                ),
                if (requested != null)
                  Text(
                    'Requested ${requested.toLocal()}',
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textTertiary,
                    ),
                  ),
              ],
            ),
          ),
          if (ready)
            TextButton(
              onPressed: () {
                // Real download is handled via the email link; in-app we
                // could deep-link to the URL but that needs a packaged web
                // view — punted to S6.
              },
              child: const Text(
                'Open',
                style: TextStyle(color: AppColors.postbookPrimary),
              ),
            ),
        ],
      ),
    );
  }
}
