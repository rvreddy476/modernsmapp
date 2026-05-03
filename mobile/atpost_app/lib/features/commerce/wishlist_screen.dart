// Wishlist — Sprint 2.
//
// 2-column grid of saved products. Each card shows price, MRP strike,
// a heart that removes (with undo), and a "Move to cart" CTA which
// pushes the user into the PDP for variant selection (the wishlist row
// only stores product id — variants resolve at PDP).

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/features/commerce/widgets/wishlist_button.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class WishlistScreen extends ConsumerWidget {
  const WishlistScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final wishlistAsync = ref.watch(wishlistProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Wishlist', style: AppTextStyles.h2),
      ),
      body: wishlistAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load wishlist.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (items) {
          if (items.isEmpty) return const _EmptyWishlist();
          return RefreshIndicator(
            onRefresh: () => ref.read(wishlistProvider.notifier).refresh(),
            color: AppColors.postbookPrimary,
            child: GridView.builder(
              padding: const EdgeInsets.all(AppSpacing.l),
              gridDelegate:
                  const SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: 2,
                mainAxisSpacing: AppSpacing.l,
                crossAxisSpacing: AppSpacing.l,
                childAspectRatio: 0.62,
              ),
              itemCount: items.length,
              itemBuilder: (ctx, i) => _WishlistCard(item: items[i]),
            ),
          );
        },
      ),
    );
  }
}

class _EmptyWishlist extends StatelessWidget {
  const _EmptyWishlist();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(AppSpacing.xxl),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.favorite_border,
                size: 56, color: AppColors.textGhost),
            const SizedBox(height: AppSpacing.l),
            Text(
              'Your wishlist is empty.\nTap the heart on any product to save it.',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: AppSpacing.xxl),
            ElevatedButton(
              onPressed: () => GoRouter.of(context).push('/commerce'),
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
              ),
              child: const Text('Browse storefront'),
            ),
          ],
        ),
      ),
    );
  }
}

class _WishlistCard extends StatelessWidget {
  const _WishlistCard({required this.item});

  final WishlistItem item;

  @override
  Widget build(BuildContext context) {
    final s = item.productSnapshot;
    return InkWell(
      onTap: () =>
          GoRouter.of(context).push('/commerce/product/${item.productId}'),
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
            Stack(
              children: [
                AspectRatio(
                  aspectRatio: 1,
                  child: ClipRRect(
                    borderRadius: const BorderRadius.vertical(
                      top: Radius.circular(AppSpacing.radiusLarge),
                    ),
                    child: s.primaryImageUrl == null
                        ? Container(
                            color: AppColors.bgSecondary,
                            child: const Icon(Icons.image_outlined,
                                color: AppColors.textGhost),
                          )
                        : Image.network(
                            s.primaryImageUrl!,
                            fit: BoxFit.cover,
                            errorBuilder: (_, _, _) => Container(
                              color: AppColors.bgSecondary,
                              child: const Icon(
                                  Icons.broken_image_outlined,
                                  color: AppColors.textGhost),
                            ),
                          ),
                  ),
                ),
                Positioned(
                  top: 4,
                  right: 4,
                  child: Container(
                    decoration: const BoxDecoration(
                      color: AppColors.bgPrimary,
                      shape: BoxShape.circle,
                    ),
                    child: WishlistButton(
                      productId: item.productId,
                      snapshot: s,
                      size: 18,
                      padded: false,
                    ),
                  ),
                ),
              ],
            ),
            Padding(
              padding: const EdgeInsets.all(AppSpacing.l),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    s.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.textPrimary,
                    ),
                  ),
                  const SizedBox(height: AppSpacing.s),
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.baseline,
                    textBaseline: TextBaseline.alphabetic,
                    children: [
                      Text(
                        'Rs. ${s.sellingPrice.toStringAsFixed(0)}',
                        style: AppTextStyles.h3,
                      ),
                      const SizedBox(width: AppSpacing.s),
                      if (s.discountPct != null)
                        Text(
                          'Rs. ${s.mrp!.toStringAsFixed(0)}',
                          style: AppTextStyles.bodySmall.copyWith(
                            decoration: TextDecoration.lineThrough,
                            color: AppColors.textMuted,
                          ),
                        ),
                    ],
                  ),
                  const SizedBox(height: AppSpacing.m),
                  SizedBox(
                    width: double.infinity,
                    child: OutlinedButton(
                      onPressed: () => GoRouter.of(context)
                          .push('/commerce/product/${item.productId}'),
                      style: OutlinedButton.styleFrom(
                        side: const BorderSide(
                            color: AppColors.postbookPrimary),
                        padding: const EdgeInsets.symmetric(vertical: 8),
                      ),
                      child: Text(
                        'Move to cart',
                        style: AppTextStyles.labelSmall.copyWith(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
