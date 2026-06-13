// Seller returns inbox. Read-only on mobile for now — approve / reject
// + label-generation actions stay web until a dedicated returns-action
// screen ships. Each row shows the return state, reason, refund amount,
// and refund status.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SellerReturnsScreen extends ConsumerWidget {
  const SellerReturnsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final returnsAsync = ref.watch(sellerReturnsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Returns', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        color: AppColors.postbookPrimary,
        onRefresh: () async {
          ref.invalidate(sellerReturnsProvider);
          await ref.read(sellerReturnsProvider.future);
        },
        child: returnsAsync.when(
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
                      'Could not load returns.\n$e',
                      textAlign: TextAlign.center,
                      style: AppTextStyles.body,
                    ),
                  ),
                ),
              ),
            ],
          ),
          data: (returns) {
            if (returns.isEmpty) {
              return ListView(
                children: [
                  SizedBox(
                    height: MediaQuery.of(context).size.height * 0.5,
                    child: Center(
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          const Icon(Icons.assignment_return_outlined,
                              size: 56, color: AppColors.textGhost),
                          const SizedBox(height: AppSpacing.l),
                          Text('No returns yet', style: AppTextStyles.h2),
                          const SizedBox(height: AppSpacing.s),
                          Padding(
                            padding: const EdgeInsets.symmetric(
                                horizontal: AppSpacing.xxl),
                            child: Text(
                              "Customer return requests appear here. Approve / reject from the web dashboard for now.",
                              textAlign: TextAlign.center,
                              style: AppTextStyles.body,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.all(AppSpacing.l),
              itemCount: returns.length,
              separatorBuilder: (_, _) => const SizedBox(height: AppSpacing.s),
              itemBuilder: (_, i) => _ReturnRow(card: returns[i]),
            );
          },
        ),
      ),
    );
  }
}

class _ReturnRow extends StatelessWidget {
  const _ReturnRow({required this.card});
  final SellerReturnCard card;

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
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  card.orderNumber.isEmpty
                      ? 'Return #${card.id.substring(0, 6)}'
                      : card.orderNumber,
                  style: AppTextStyles.label,
                ),
              ),
              _Chip(text: card.status.toUpperCase(), tone: _statusTone(card.status)),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            'Reason: ${card.reasonCode}',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 4),
          Row(
            children: [
              Text(
                'Refund: Rs. ${card.refundAmount.toStringAsFixed(0)}',
                style: AppTextStyles.bodySmall.copyWith(
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(width: AppSpacing.s),
              _Chip(
                text: card.refundStatus.toUpperCase(),
                tone: _refundTone(card.refundStatus),
              ),
            ],
          ),
        ],
      ),
    );
  }

  ({Color bg, Color fg}) _statusTone(String s) {
    switch (s) {
      case 'approved':
      case 'completed':
        return (bg: const Color(0xFFD1FAE5), fg: const Color(0xFF047857));
      case 'rejected':
        return (bg: const Color(0xFFFFE4E6), fg: const Color(0xFFB91C1C));
      case 'picked_up':
      case 'in_transit':
        return (bg: const Color(0xFFDBEAFE), fg: const Color(0xFF1D4ED8));
      default:
        return (bg: const Color(0xFFFEF3C7), fg: const Color(0xFF92400E));
    }
  }

  ({Color bg, Color fg}) _refundTone(String s) {
    switch (s) {
      case 'succeeded':
      case 'manual':
        return (bg: const Color(0xFFD1FAE5), fg: const Color(0xFF047857));
      case 'failed':
        return (bg: const Color(0xFFFFE4E6), fg: const Color(0xFFB91C1C));
      default:
        return (bg: const Color(0xFFFEF3C7), fg: const Color(0xFF92400E));
    }
  }
}

class _Chip extends StatelessWidget {
  const _Chip({required this.text, required this.tone});
  final String text;
  final ({Color bg, Color fg}) tone;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: tone.bg,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        text,
        style: AppTextStyles.labelTiny.copyWith(
          color: tone.fg,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
  }
}
