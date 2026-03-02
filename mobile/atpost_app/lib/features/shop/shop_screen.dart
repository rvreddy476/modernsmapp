import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/shop.dart';
import 'package:atpost_app/data/repositories/shop_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final shopProductsProvider = FutureProvider.autoDispose<List<Product>>((ref) async {
  final repo = ref.watch(shopRepositoryProvider);
  return repo.getProducts();
});

final shopCartProvider = FutureProvider.autoDispose<List<CartItem>>((ref) async {
  final repo = ref.watch(shopRepositoryProvider);
  return repo.getCart();
});

class ShopScreen extends ConsumerWidget {
  const ShopScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final productsAsync = ref.watch(shopProductsProvider);

    return SafeArea(
      child: Padding(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Shop', style: AppTextStyles.h1),
                Row(
                  children: [
                    IconButton(
                      icon: const Icon(Icons.search, color: AppColors.textPrimary),
                      onPressed: () {},
                    ),
                    IconButton(
                      icon: const Icon(Icons.shopping_cart_outlined, color: AppColors.textPrimary),
                      onPressed: () => _showCart(context, ref),
                    ),
                  ],
                ),
              ],
            ),
            const SizedBox(height: 12),

            // Category chips
            SizedBox(
              height: 36,
              child: ListView(
                scrollDirection: Axis.horizontal,
                children: const [
                  _CategoryChip(label: 'All', isSelected: true),
                  _CategoryChip(label: 'Electronics'),
                  _CategoryChip(label: 'Fashion'),
                  _CategoryChip(label: 'Home'),
                  _CategoryChip(label: 'Sports'),
                  _CategoryChip(label: 'Books'),
                ],
              ),
            ),
            const SizedBox(height: 16),

            // Product grid
            Expanded(
              child: productsAsync.when(
                data: (products) {
                  if (products.isEmpty) {
                    return Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.storefront, color: AppColors.textDim, size: 54),
                          const SizedBox(height: 12),
                          Text('No products yet', style: AppTextStyles.body.copyWith(color: AppColors.textDim)),
                        ],
                      ),
                    );
                  }
                  return GridView.builder(
                    gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                      crossAxisCount: 2,
                      childAspectRatio: 0.72,
                      crossAxisSpacing: 12,
                      mainAxisSpacing: 12,
                    ),
                    itemCount: products.length,
                    itemBuilder: (context, index) => _ProductCard(product: products[index]),
                  );
                },
                loading: () => const Center(child: CircularProgressIndicator()),
                error: (e, _) => Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.storefront, color: AppColors.textDim, size: 54),
                      const SizedBox(height: 12),
                      Text('Marketplace coming soon', style: AppTextStyles.body.copyWith(color: AppColors.textDim)),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _showCart(BuildContext context, WidgetRef ref) {
    showModalBottomSheet(
      context: context,
      backgroundColor: AppColors.bgCard,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (context) => Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Your Cart', style: AppTextStyles.h2),
            const SizedBox(height: 16),
            Consumer(
              builder: (context, ref, _) {
                final cartAsync = ref.watch(shopCartProvider);
                return cartAsync.when(
                  data: (items) {
                    if (items.isEmpty) {
                      return const Padding(
                        padding: EdgeInsets.symmetric(vertical: 24),
                        child: Center(child: Text('Cart is empty')),
                      );
                    }
                    return Column(
                      children: [
                        ...items.map((item) => ListTile(
                              title: Text(item.product?.title ?? 'Product'),
                              subtitle: Text('Qty: ${item.quantity}'),
                              trailing: Text(
                                '\$${((item.product?.price ?? 0) * item.quantity).toStringAsFixed(2)}',
                                style: AppTextStyles.body.copyWith(fontWeight: FontWeight.bold),
                              ),
                            )),
                        const SizedBox(height: 12),
                        SizedBox(
                          width: double.infinity,
                          child: ElevatedButton(
                            onPressed: () => Navigator.pop(context),
                            style: ElevatedButton.styleFrom(
                              backgroundColor: AppColors.postbookPrimary,
                              shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                            ),
                            child: const Text('Checkout'),
                          ),
                        ),
                      ],
                    );
                  },
                  loading: () => const Center(child: CircularProgressIndicator()),
                  error: (_, _) => const Padding(
                    padding: EdgeInsets.symmetric(vertical: 24),
                    child: Center(child: Text('Cart is empty')),
                  ),
                );
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _CategoryChip extends StatelessWidget {
  final String label;
  final bool isSelected;

  const _CategoryChip({required this.label, this.isSelected = false});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(right: 8),
      child: FilterChip(
        label: Text(label),
        selected: isSelected,
        onSelected: (_) {},
        backgroundColor: AppColors.bgCard,
        selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
        labelStyle: TextStyle(
          color: isSelected ? AppColors.postbookPrimary : AppColors.textSecondary,
          fontSize: 13,
        ),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(20),
          side: BorderSide(color: isSelected ? AppColors.postbookPrimary : AppColors.borderSubtle),
        ),
      ),
    );
  }
}

class _ProductCard extends StatelessWidget {
  final Product product;

  const _ProductCard({required this.product});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Product image placeholder
          Expanded(
            flex: 3,
            child: Container(
              decoration: BoxDecoration(
                color: AppColors.bgPrimary,
                borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
              ),
              child: const Center(
                child: Icon(Icons.image_outlined, color: AppColors.textDim, size: 40),
              ),
            ),
          ),
          // Product info
          Expanded(
            flex: 2,
            child: Padding(
              padding: const EdgeInsets.all(10),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    product.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.body.copyWith(fontSize: 13, fontWeight: FontWeight.w600),
                  ),
                  const Spacer(),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(
                        '\$${product.price.toStringAsFixed(2)}',
                        style: AppTextStyles.body.copyWith(
                          color: AppColors.postbookPrimary,
                          fontWeight: FontWeight.bold,
                        ),
                      ),
                      if (product.stock <= 5 && product.stock > 0)
                        Text(
                          '${product.stock} left',
                          style: AppTextStyles.labelSmall.copyWith(color: Colors.orange),
                        ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
