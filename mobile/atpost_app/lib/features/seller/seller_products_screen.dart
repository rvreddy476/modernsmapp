// Seller products list — read-only inventory of the seller's catalog.
// Tapping a product opens the variants management screen. Drafts +
// changes_requested products get an inline "Submit for review" action.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SellerProductsScreen extends ConsumerWidget {
  const SellerProductsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final productsAsync = ref.watch(mySellerProductsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Products', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(mySellerProductsProvider);
          await ref.read(mySellerProductsProvider.future);
        },
        color: AppColors.postbookPrimary,
        child: productsAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (e, _) => ListView(
            children: [
              SizedBox(
                height: MediaQuery.of(context).size.height * 0.6,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.xxl),
                    child: Text(
                      'Could not load products.\n$e',
                      textAlign: TextAlign.center,
                      style: AppTextStyles.body,
                    ),
                  ),
                ),
              ),
            ],
          ),
          data: (products) {
            if (products.isEmpty) {
              return ListView(
                children: [
                  SizedBox(
                    height: MediaQuery.of(context).size.height * 0.6,
                    child: Center(
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          const Icon(Icons.inventory_2_outlined,
                              size: 56, color: AppColors.textGhost),
                          const SizedBox(height: AppSpacing.l),
                          Text('No products yet', style: AppTextStyles.h2),
                          const SizedBox(height: AppSpacing.s),
                          Text(
                            'New products are created from the web seller dashboard.',
                            textAlign: TextAlign.center,
                            style: AppTextStyles.body,
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
              itemCount: products.length,
              separatorBuilder: (_, _) => const SizedBox(height: AppSpacing.s),
              itemBuilder: (_, i) => _ProductRow(product: products[i]),
            );
          },
        ),
      ),
    );
  }
}

class _ProductRow extends ConsumerStatefulWidget {
  const _ProductRow({required this.product});
  final SellerProductSummary product;

  @override
  ConsumerState<_ProductRow> createState() => _ProductRowState();
}

class _ProductRowState extends ConsumerState<_ProductRow> {
  bool _submitting = false;

  Future<void> _submitForReview() async {
    setState(() => _submitting = true);
    try {
      await ref
          .read(commerceRepositoryProvider)
          .submitProductForReview(widget.product.id);
      if (!mounted) return;
      ref.invalidate(mySellerProductsProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Submitted for review')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not submit: $e')),
      );
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = widget.product;
    final canSubmit =
        p.approvalStatus == 'draft' || p.approvalStatus == 'changes_requested';
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: () => GoRouter.of(context).push('/seller/products/${p.id}/variants'),
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
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(p.title, style: AppTextStyles.label, maxLines: 2),
                      const SizedBox(height: 2),
                      Text(
                        p.slug,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textMuted,
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(width: AppSpacing.s),
                _ApprovalChip(status: p.approvalStatus),
              ],
            ),
            const SizedBox(height: AppSpacing.s),
            Row(
              children: [
                if (p.createdAt != null)
                  Text(
                    _ago(p.createdAt!),
                    style: AppTextStyles.bodySmall,
                  ),
                const Spacer(),
                if (canSubmit)
                  TextButton(
                    onPressed: _submitting ? null : _submitForReview,
                    child: _submitting
                        ? const SizedBox(
                            width: 14,
                            height: 14,
                            child:
                                CircularProgressIndicator(strokeWidth: 2),
                          )
                        : Text(
                            p.approvalStatus == 'changes_requested'
                                ? 'Resubmit'
                                : 'Submit for review',
                          ),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _ApprovalChip extends StatelessWidget {
  const _ApprovalChip({required this.status});
  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, bg, fg) = _toneFor(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelTiny.copyWith(
          color: fg,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
  }

  (String, Color, Color) _toneFor(String status) {
    switch (status) {
      case 'approved':
      case 'live':
        return ('LIVE', const Color(0xFFD1FAE5), const Color(0xFF047857));
      case 'submitted':
      case 'under_review':
        return ('REVIEW', const Color(0xFFFEF3C7), const Color(0xFF92400E));
      case 'rejected':
        return ('REJECTED', const Color(0xFFFFE4E6), const Color(0xFFB91C1C));
      case 'changes_requested':
        return ('CHANGES', const Color(0xFFFFEDD5), const Color(0xFF9A3412));
      case 'hidden':
      case 'archived':
        return (
          status.toUpperCase(),
          AppColors.bgSecondary,
          AppColors.textTertiary,
        );
      default:
        return ('DRAFT', AppColors.bgSecondary, AppColors.textSecondary);
    }
  }
}

String _ago(DateTime t) {
  final diff = DateTime.now().difference(t);
  if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
  if (diff.inHours < 24) return '${diff.inHours}h ago';
  if (diff.inDays < 7) return '${diff.inDays}d ago';
  return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
}
