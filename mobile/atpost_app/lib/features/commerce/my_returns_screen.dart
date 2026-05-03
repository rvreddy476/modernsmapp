// My returns — Sprint 2.
//
// Lists every return the customer has filed across all orders. Status
// states map to the backend enum:
//   requested → approved → picked_up → in_transit → received → refunded
// + rejected as a terminal state.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class MyReturnsScreen extends ConsumerWidget {
  const MyReturnsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final returnsAsync = ref.watch(myReturnsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('My returns', style: AppTextStyles.h2),
      ),
      body: returnsAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load returns.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (list) {
          if (list.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(AppSpacing.xxl),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.assignment_return_outlined,
                        size: 56, color: AppColors.textGhost),
                    const SizedBox(height: AppSpacing.l),
                    Text(
                      'You haven\'t filed any returns yet.',
                      style: AppTextStyles.body,
                      textAlign: TextAlign.center,
                    ),
                  ],
                ),
              ),
            );
          }
          return RefreshIndicator(
            onRefresh: () async => ref.invalidate(myReturnsProvider),
            color: AppColors.postbookPrimary,
            child: ListView.separated(
              padding: const EdgeInsets.all(AppSpacing.l),
              itemCount: list.length,
              separatorBuilder: (_, _) =>
                  const SizedBox(height: AppSpacing.l),
              itemBuilder: (ctx, i) => _ReturnRow(item: list[i]),
            ),
          );
        },
      ),
    );
  }
}

class _ReturnRow extends StatelessWidget {
  const _ReturnRow({required this.item});

  final ReturnRequest item;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () =>
          GoRouter.of(context).push('/commerce/returns/${item.id}'),
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: Container(
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
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Expanded(
                  child: Text(
                    'Order ${item.orderId.substring(0, item.orderId.length.clamp(0, 8))}',
                    style: AppTextStyles.label,
                  ),
                ),
                _StatusPill(status: item.status),
              ],
            ),
            const SizedBox(height: AppSpacing.s),
            Text('Reason: ${item.reason.label}',
                style: AppTextStyles.bodySmall),
            if (item.refundAmount != null) ...[
              const SizedBox(height: 2),
              Text(
                'Refund: Rs. ${item.refundAmount!.toStringAsFixed(0)}',
                style: AppTextStyles.label
                    .copyWith(color: AppColors.statusSuccess),
              ),
            ],
            const SizedBox(height: AppSpacing.s),
            Text(
              'Filed ${_fmtDate(item.createdAt)}',
              style: AppTextStyles.bodySmall,
            ),
          ],
        ),
      ),
    );
  }

  static String _fmtDate(DateTime d) {
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return '${d.day} ${months[d.month - 1]} ${d.year}';
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    Color color;
    switch (status) {
      case 'refunded':
        color = AppColors.statusSuccess;
        break;
      case 'rejected':
        color = AppColors.statusError;
        break;
      case 'approved':
      case 'picked_up':
      case 'in_transit':
      case 'received':
        color = AppColors.posttubePrimary;
        break;
      default:
        color = AppColors.statusWarning;
    }
    return Container(
      padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.m, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Text(
        _pretty(status),
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }

  static String _pretty(String s) {
    if (s.isEmpty) return 'Requested';
    return s
        .replaceAll('_', ' ')
        .split(' ')
        .map((w) => w.isEmpty ? w : w[0].toUpperCase() + w.substring(1))
        .join(' ');
  }
}
