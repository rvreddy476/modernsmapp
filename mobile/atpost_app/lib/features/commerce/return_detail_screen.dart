// Return detail — Sprint 2.
//
// Backend doesn't expose `GET /v1/commerce/returns/:id` directly today;
// we fetch the user's full returns list and locate the one we want by id.
// When a single-return endpoint ships this becomes a one-liner.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ReturnDetailScreen extends ConsumerWidget {
  const ReturnDetailScreen({super.key, required this.returnId});

  final String returnId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final returnsAsync = ref.watch(myReturnsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Return', style: AppTextStyles.h2),
      ),
      body: returnsAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load return.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (list) {
          ReturnRequest? match;
          for (final r in list) {
            if (r.id == returnId) {
              match = r;
              break;
            }
          }
          if (match == null) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(AppSpacing.xxl),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text('Return not found.', style: AppTextStyles.body),
                    const SizedBox(height: AppSpacing.l),
                    ElevatedButton(
                      onPressed: () =>
                          GoRouter.of(context).go('/commerce/returns'),
                      child: const Text('Back to my returns'),
                    ),
                  ],
                ),
              ),
            );
          }
          return _Body(item: match);
        },
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.item});

  final ReturnRequest item;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(AppSpacing.l),
      children: [
        _Card(
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Return id', style: AppTextStyles.bodySmall),
                Text(
                  item.id.substring(0, item.id.length.clamp(0, 12)),
                  style: AppTextStyles.label,
                ),
              ],
            ),
            const SizedBox(height: AppSpacing.s),
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Order', style: AppTextStyles.bodySmall),
                Text(
                  item.orderId.substring(0, item.orderId.length.clamp(0, 12)),
                  style: AppTextStyles.label,
                ),
              ],
            ),
            const SizedBox(height: AppSpacing.s),
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Reason', style: AppTextStyles.bodySmall),
                Text(item.reason.label, style: AppTextStyles.label),
              ],
            ),
            if (item.reasonDescription != null) ...[
              const SizedBox(height: AppSpacing.s),
              Text(item.reasonDescription!, style: AppTextStyles.bodySmall),
            ],
          ],
        ),
        const SizedBox(height: AppSpacing.l),
        _Card(
          children: [
            Text('Timeline', style: AppTextStyles.h3),
            const SizedBox(height: AppSpacing.l),
            _Timeline(item: item),
          ],
        ),
        const SizedBox(height: AppSpacing.l),
        if (item.pickupAddress != null)
          _Card(
            children: [
              Text('Pickup', style: AppTextStyles.label),
              const SizedBox(height: AppSpacing.s),
              Text(
                '${item.pickupAddress!.fullName} · ${item.pickupAddress!.phone}',
                style: AppTextStyles.body,
              ),
              Text(
                '${item.pickupAddress!.line1}, ${item.pickupAddress!.city} ${item.pickupAddress!.postalCode}',
                style: AppTextStyles.bodySmall,
              ),
              if (item.pickupScheduledAt != null) ...[
                const SizedBox(height: AppSpacing.s),
                Text(
                  'Scheduled: ${_fmtDateTime(item.pickupScheduledAt!)}',
                  style: AppTextStyles.label
                      .copyWith(color: AppColors.posttubePrimary),
                ),
              ],
            ],
          ),
        const SizedBox(height: AppSpacing.l),
        if (item.refundedAt != null && item.refundAmount != null)
          _Card(
            children: [
              Text('Refund', style: AppTextStyles.label),
              const SizedBox(height: AppSpacing.s),
              Text(
                'Rs. ${item.refundAmount!.toStringAsFixed(0)}',
                style: AppTextStyles.h2
                    .copyWith(color: AppColors.statusSuccess),
              ),
              Text(
                'Issued ${_fmtDateTime(item.refundedAt!)}',
                style: AppTextStyles.bodySmall,
              ),
            ],
          ),
      ],
    );
  }

  static String _fmtDateTime(DateTime d) {
    return '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')} '
        '${d.hour.toString().padLeft(2, '0')}:${d.minute.toString().padLeft(2, '0')}';
  }
}

class _Timeline extends StatelessWidget {
  const _Timeline({required this.item});

  final ReturnRequest item;

  static const _steps = [
    ('requested', 'Requested'),
    ('approved', 'Approved'),
    ('picked_up', 'Picked up'),
    ('in_transit', 'In transit'),
    ('received', 'Received'),
    ('refunded', 'Refunded'),
  ];

  int _currentIndex() {
    if (item.status == 'rejected') return -1;
    for (var i = _steps.length - 1; i >= 0; i--) {
      if (_steps[i].$1 == item.status) return i;
    }
    return 0;
  }

  @override
  Widget build(BuildContext context) {
    final current = _currentIndex();
    final rejected = item.status == 'rejected';
    return Column(
      children: [
        if (rejected)
          Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.statusError.withValues(alpha: 0.12),
              borderRadius:
                  BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            child: Text(
              'Return rejected.',
              style: AppTextStyles.label
                  .copyWith(color: AppColors.statusError),
            ),
          ),
        if (!rejected)
          for (var i = 0; i < _steps.length; i++)
            _TimelineStep(
              label: _steps[i].$2,
              done: i <= current,
              isLast: i == _steps.length - 1,
            ),
      ],
    );
  }
}

class _TimelineStep extends StatelessWidget {
  const _TimelineStep({
    required this.label,
    required this.done,
    required this.isLast,
  });

  final String label;
  final bool done;
  final bool isLast;

  @override
  Widget build(BuildContext context) {
    final dotColor =
        done ? AppColors.postbookPrimary : AppColors.borderSubtle;
    return IntrinsicHeight(
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Column(
            children: [
              Container(
                width: 14,
                height: 14,
                decoration: BoxDecoration(
                  color: dotColor,
                  shape: BoxShape.circle,
                ),
              ),
              if (!isLast)
                Expanded(
                  child: Container(width: 2, color: dotColor),
                ),
            ],
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.only(bottom: AppSpacing.l),
              child: Text(
                label,
                style: AppTextStyles.body.copyWith(
                  color: done
                      ? AppColors.textPrimary
                      : AppColors.textMuted,
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _Card extends StatelessWidget {
  const _Card({required this.children});

  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: children,
      ),
    );
  }
}
