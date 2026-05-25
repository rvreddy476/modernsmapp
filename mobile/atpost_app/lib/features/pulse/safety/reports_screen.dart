import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Phase 1 — "My Reports" list.
///
/// Fetches `GET /v1/dating/safety/reports/me` and shows one row per report
/// with category + submitted-at + status pill. When dating-service hasn't
/// shipped the list endpoint yet (HTTP 404/501), the repository returns
/// `endpointAvailable: false` and we render the legacy "pending endpoint"
/// banner so the surface is in place either way.
final myReportsProvider =
    FutureProvider.autoDispose<MyReportsResult>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getMyReports();
});

class MyReportsScreen extends ConsumerWidget {
  const MyReportsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final reports = ref.watch(myReportsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('My reports', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
        ),
      ),
      body: SafeArea(
        child: RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () async {
            ref.invalidate(myReportsProvider);
            await ref.read(myReportsProvider.future);
          },
          child: reports.when(
            loading: () => const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            ),
            error: (_, _) => ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: AppSpacing.pagePadding.copyWith(top: 18),
              children: [
                _InfoCard(
                  title: 'Could not load reports',
                  body: 'Something went wrong on our side. Pull to retry.',
                ),
              ],
            ),
            data: (result) {
              if (!result.endpointAvailable) {
                return ListView(
                  physics: const AlwaysScrollableScrollPhysics(),
                  padding: AppSpacing.pagePadding.copyWith(top: 18),
                  children: const [
                    _InfoCard(
                      title: 'Report history is coming soon',
                      body:
                          'Trust & Safety reviews every report. The list view '
                          'here is pending a backend follow-up; until then '
                          'you will receive an in-app notification when a '
                          'report is acted on.',
                    ),
                  ],
                );
              }
              if (result.items.isEmpty) {
                return ListView(
                  physics: const AlwaysScrollableScrollPhysics(),
                  padding: AppSpacing.pagePadding.copyWith(top: 18),
                  children: const [
                    _InfoCard(
                      title: 'No reports yet',
                      body:
                          'When you report someone the status will appear '
                          'here. Reports are confidential and reviewed by '
                          'humans on the Trust & Safety team.',
                    ),
                  ],
                );
              }
              return ListView.separated(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: AppSpacing.pagePadding.copyWith(top: 18),
                itemCount: result.items.length,
                separatorBuilder: (_, _) => const SizedBox(height: 10),
                itemBuilder: (_, i) => _ReportTile(entry: result.items[i]),
              );
            },
          ),
        ),
      ),
    );
  }
}

class _InfoCard extends StatelessWidget {
  const _InfoCard({required this.title, required this.body});

  final String title;
  final String body;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: AppTextStyles.h3),
          const SizedBox(height: 6),
          Text(body, style: AppTextStyles.bodySmall),
        ],
      ),
    );
  }
}

class _ReportTile extends StatelessWidget {
  const _ReportTile({required this.entry});

  final MyReportEntry entry;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  _categoryLabel(entry.category),
                  style: AppTextStyles.h3,
                ),
              ),
              _StatusPill(status: entry.status),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            _subtitle(entry),
            style: AppTextStyles.bodySmall,
          ),
          if (entry.resolutionNote != null &&
              entry.resolutionNote!.isNotEmpty) ...[
            const SizedBox(height: 8),
            Container(
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              ),
              child: Text(
                entry.resolutionNote!,
                style: AppTextStyles.bodySmall,
              ),
            ),
          ],
        ],
      ),
    );
  }

  String _categoryLabel(String c) {
    switch (c) {
      case 'harassment':
        return 'Harassment or abuse';
      case 'inappropriate_photo':
        return 'Inappropriate photo';
      case 'impersonation':
        return 'Impersonation / fake profile';
      case 'spam':
        return 'Spam or scam';
      case 'other':
        return 'Other';
      default:
        return c.isEmpty ? 'Report' : c;
    }
  }

  String _subtitle(MyReportEntry e) {
    final parts = <String>[];
    if (e.targetName != null && e.targetName!.isNotEmpty) {
      parts.add('Against ${e.targetName}');
    }
    final when = e.createdAt;
    if (when != null) {
      parts.add(_formatDate(when));
    }
    if (parts.isEmpty) return 'Submitted';
    return parts.join(' · ');
  }

  String _formatDate(DateTime when) {
    final local = when.toLocal();
    final y = local.year.toString().padLeft(4, '0');
    final m = local.month.toString().padLeft(2, '0');
    final d = local.day.toString().padLeft(2, '0');
    return '$y-$m-$d';
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = _statusStyle(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.18),
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }

  (String, Color) _statusStyle(String s) {
    switch (s) {
      case 'actioned':
      case 'resolved':
        return ('Actioned', AppColors.statusSuccess);
      case 'dismissed':
      case 'closed_no_action':
        return ('Closed', AppColors.textTertiary);
      case 'under_review':
      case 'investigating':
        return ('Under review', AppColors.statusWarning);
      case 'submitted':
      default:
        return ('Submitted', AppColors.posttubePrimary);
    }
  }
}
