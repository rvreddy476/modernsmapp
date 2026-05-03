// "Recommended for you" horizontal carousel — Sprint 2.
//
// Backend has no real recommender today (COMMERCE_RECON §I.12). The
// repository's `getRecommendations()` falls back to a trending-products
// query so the UX ships now and Sprint 3 can wire the real ranker (or
// `suggestion-service`) without a screen change.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class HomeRecommendations extends ConsumerWidget {
  const HomeRecommendations({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final recoAsync = ref.watch(recommendationsProvider);
    return recoAsync.when(
      loading: () => const SizedBox(height: 220),
      error: (_, _) => const SizedBox.shrink(),
      data: (list) {
        if (list.isEmpty) return const SizedBox.shrink();
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: AppSpacing.l),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.symmetric(
                    horizontal: AppSpacing.l),
                child: Text(
                  'Recommended for you',
                  style: AppTextStyles.h3,
                ),
              ),
              const SizedBox(height: AppSpacing.s),
              SizedBox(
                height: 220,
                child: ListView.separated(
                  scrollDirection: Axis.horizontal,
                  padding: const EdgeInsets.symmetric(
                      horizontal: AppSpacing.l),
                  itemCount: list.length,
                  separatorBuilder: (_, _) =>
                      const SizedBox(width: AppSpacing.l),
                  itemBuilder: (ctx, i) =>
                      _RecoTile(product: list[i]),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _RecoTile extends StatelessWidget {
  const _RecoTile({required this.product});

  final Product product;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 140,
      child: InkWell(
        onTap: () =>
            GoRouter.of(context).push('/commerce/product/${product.id}'),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: Container(
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              AspectRatio(
                aspectRatio: 1,
                child: ClipRRect(
                  borderRadius: const BorderRadius.vertical(
                    top: Radius.circular(AppSpacing.radiusLarge),
                  ),
                  child: product.primaryImageUrl == null
                      ? Container(
                          color: AppColors.bgSecondary,
                          child: const Icon(Icons.image_outlined,
                              color: AppColors.textGhost),
                        )
                      : Image.network(
                          product.primaryImageUrl!,
                          fit: BoxFit.cover,
                          errorBuilder: (_, _, _) => Container(
                            color: AppColors.bgSecondary,
                            child: const Icon(Icons.broken_image_outlined,
                                color: AppColors.textGhost),
                          ),
                        ),
                ),
              ),
              Padding(
                padding: const EdgeInsets.all(AppSpacing.m),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      product.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      'Rs. ${product.basePrice.toStringAsFixed(0)}',
                      style: AppTextStyles.label,
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
