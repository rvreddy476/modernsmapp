// Phase F4 mobile — buyer RFQ inbox. Lists every RFQ the user has
// sent, newest first. Tapping a row opens the detail screen where
// the buyer can accept / reject the seller's quote.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/b2b_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RFQListScreen extends ConsumerWidget {
  const RFQListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rfqsAsync = ref.watch(myRFQsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('My RFQs', style: AppTextStyles.h2),
      ),
      body: rfqsAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load RFQs.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (rfqs) {
          if (rfqs.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(AppSpacing.xxl),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(
                      Icons.request_quote_outlined,
                      size: 48,
                      color: AppColors.textTertiary,
                    ),
                    const SizedBox(height: AppSpacing.l),
                    Text('No quote requests yet', style: AppTextStyles.h2),
                    const SizedBox(height: AppSpacing.s),
                    Text(
                      'On any product page, tap "Request a custom quote" to ask the seller for volume pricing.',
                      style: AppTextStyles.body,
                      textAlign: TextAlign.center,
                    ),
                  ],
                ),
              ),
            );
          }
          return RefreshIndicator(
            onRefresh: () async => ref.invalidate(myRFQsProvider),
            color: AppColors.postbookPrimary,
            child: ListView.separated(
              padding: const EdgeInsets.all(AppSpacing.l),
              itemCount: rfqs.length,
              separatorBuilder: (_, _) =>
                  const SizedBox(height: AppSpacing.s),
              itemBuilder: (_, i) {
                final r = rfqs[i];
                final color = _statusColor(r.status);
                return InkWell(
                  onTap: () => GoRouter.of(context).push('/rfq/${r.id}'),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                  child: Container(
                    padding: const EdgeInsets.all(AppSpacing.l),
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusMedium),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: Row(
                      children: [
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                'RFQ ${r.id.substring(0, 8)}…',
                                style: AppTextStyles.h3,
                              ),
                              const SizedBox(height: AppSpacing.xs),
                              Text(
                                'Requested ${_fmtDate(r.requestedAt)} · Expires ${_fmtDate(r.expiresAt)}',
                                style: AppTextStyles.bodySmall,
                              ),
                            ],
                          ),
                        ),
                        Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: AppSpacing.m, vertical: 2),
                          decoration: BoxDecoration(
                            color: color.withValues(alpha: 0.16),
                            borderRadius:
                                BorderRadius.circular(AppSpacing.radiusFull),
                            border: Border.all(
                              color: color.withValues(alpha: 0.4),
                            ),
                          ),
                          child: Text(
                            r.status,
                            style: AppTextStyles.labelSmall
                                .copyWith(color: color),
                          ),
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
          );
        },
      ),
    );
  }

  static Color _statusColor(String s) {
    switch (s) {
      case 'requested':
        return AppColors.statusWarning;
      case 'quoted':
        return AppColors.posttubePrimary;
      case 'accepted':
        return AppColors.statusSuccess;
      case 'rejected':
      case 'expired':
      case 'cancelled':
        return AppColors.textTertiary;
      default:
        return AppColors.statusWarning;
    }
  }

  static String _fmtDate(DateTime d) {
    final mm = d.month.toString().padLeft(2, '0');
    final dd = d.day.toString().padLeft(2, '0');
    return '$dd/$mm/${d.year}';
  }
}
