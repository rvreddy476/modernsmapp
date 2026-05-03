// Mopedu — my complaints list.
//
// Sprint 3. Reachable via /mopedu/complaints. Lists every complaint the
// customer has filed; tapping a row opens a modal with the full
// description and the admin's resolution note (when present).

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class MyComplaintsScreen extends ConsumerWidget {
  const MyComplaintsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final list = ref.watch(myComplaintsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('My complaints', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.canPop()
              ? context.pop()
              : context.go('/mopedu/safety'),
        ),
      ),
      body: RefreshIndicator(
        onRefresh: () async => ref.invalidate(myComplaintsProvider),
        child: list.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (e, _) => Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                'Could not load complaints.\n$e',
                textAlign: TextAlign.center,
                style: AppTextStyles.bodySmall,
              ),
            ),
          ),
          data: (items) {
            if (items.isEmpty) {
              return ListView(
                padding: const EdgeInsets.all(24),
                children: [
                  const SizedBox(height: 80),
                  const Icon(
                    Icons.flag_outlined,
                    color: AppColors.textTertiary,
                    size: 48,
                  ),
                  const SizedBox(height: 12),
                  Text(
                    'No complaints filed yet.',
                    textAlign: TextAlign.center,
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
              itemCount: items.length,
              separatorBuilder: (_, _) => const SizedBox(height: 8),
              itemBuilder: (context, i) =>
                  _ComplaintRow(complaint: items[i]),
            );
          },
        ),
      ),
    );
  }
}

class _ComplaintRow extends StatelessWidget {
  const _ComplaintRow({required this.complaint});

  final Complaint complaint;

  @override
  Widget build(BuildContext context) {
    final dateLabel =
        complaint.createdAt.toLocal().toString().split('.').first;
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: () => _openDetail(context),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(
                  Icons.flag_outlined,
                  color: AppColors.statusWarning,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    complaint.category.label,
                    style: AppTextStyles.label,
                  ),
                ),
                _StatusPill(status: complaint.status),
              ],
            ),
            const SizedBox(height: 6),
            Text(dateLabel, style: AppTextStyles.labelSmall),
            if (complaint.description != null &&
                complaint.description!.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(
                complaint.description!,
                style: AppTextStyles.bodySmall,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ],
          ],
        ),
      ),
    );
  }

  Future<void> _openDetail(BuildContext context) async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => _ComplaintDetailModal(complaint: complaint),
    );
  }
}

class _ComplaintDetailModal extends StatelessWidget {
  const _ComplaintDetailModal({required this.complaint});
  final Complaint complaint;

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
            Row(
              children: [
                Expanded(
                  child: Text(
                    complaint.category.label,
                    style: AppTextStyles.h2,
                  ),
                ),
                _StatusPill(status: complaint.status),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              'Filed on ${complaint.createdAt.toLocal().toString().split('.').first}',
              style: AppTextStyles.labelSmall,
            ),
            const SizedBox(height: 14),
            if (complaint.description != null &&
                complaint.description!.isNotEmpty) ...[
              Text('Your description', style: AppTextStyles.h3),
              const SizedBox(height: 4),
              Text(complaint.description!, style: AppTextStyles.body),
              const SizedBox(height: 14),
            ],
            if (complaint.resolutionNote != null &&
                complaint.resolutionNote!.isNotEmpty) ...[
              Text("Mopedu's response", style: AppTextStyles.h3),
              const SizedBox(height: 4),
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: AppColors.statusSuccess.withValues(alpha: 0.08),
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusMedium),
                  border: Border.all(
                    color: AppColors.statusSuccess.withValues(alpha: 0.3),
                  ),
                ),
                child: Text(
                  complaint.resolutionNote!,
                  style: AppTextStyles.body,
                ),
              ),
              if (complaint.resolvedAt != null) ...[
                const SizedBox(height: 6),
                Text(
                  'Resolved ${complaint.resolvedAt!.toLocal().toString().split('.').first}',
                  style: AppTextStyles.labelSmall,
                ),
              ],
              const SizedBox(height: 14),
            ] else if (!ComplaintStatus.isTerminal(complaint.status)) ...[
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: AppColors.statusWarning.withValues(alpha: 0.08),
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusMedium),
                  border: Border.all(
                    color: AppColors.statusWarning.withValues(alpha: 0.3),
                  ),
                ),
                child: Text(
                  "Our team is reviewing this complaint. We'll get "
                  'back to you within 24 hours.',
                  style: AppTextStyles.bodySmall,
                ),
              ),
              const SizedBox(height: 14),
            ],
            SizedBox(
              height: 44,
              child: OutlinedButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Close'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});
  final String status;

  @override
  Widget build(BuildContext context) {
    final color = ComplaintStatus.isTerminal(status)
        ? AppColors.statusSuccess
        : AppColors.statusWarning;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Text(
        ComplaintStatus.label(status),
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }
}
