import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/shop.dart';
import 'package:atpost_app/data/repositories/shop_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final shopProductsProvider = FutureProvider.autoDispose<List<Product>>((
  ref,
) async {
  final repo = ref.watch(shopRepositoryProvider);
  return repo.getProducts();
});

final shopCartProvider = FutureProvider.autoDispose<List<CartItem>>((
  ref,
) async {
  final repo = ref.watch(shopRepositoryProvider);
  return repo.getCart();
});

class ShopScreen extends ConsumerStatefulWidget {
  const ShopScreen({super.key});

  @override
  ConsumerState<ShopScreen> createState() => _ShopScreenState();
}

class _ShopScreenState extends ConsumerState<ShopScreen> {
  final TextEditingController _searchController = TextEditingController();

  bool _loadingProducts = true;
  String? _productsError;
  List<Product> _products = const [];
  String _selectedCategory = 'All';
  final Set<String> _addingProductIds = <String>{};

  @override
  void initState() {
    super.initState();
    _loadProducts();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _loadProducts({bool showLoader = true}) async {
    if (showLoader) {
      setState(() {
        _loadingProducts = true;
        _productsError = null;
      });
    }

    try {
      final repo = ref.read(shopRepositoryProvider);
      final products = await repo.getProducts(limit: 60);
      if (!mounted) return;
      setState(() {
        _products = products;
        _loadingProducts = false;
        _productsError = null;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loadingProducts = false;
        _productsError = 'Could not load products right now.';
      });
    }
  }

  List<String> get _categories {
    final set =
        _products
            .map((p) => p.category.trim())
            .where((v) => v.isNotEmpty)
            .toSet()
            .toList()
          ..sort((a, b) => a.compareTo(b));
    return ['All', ...set];
  }

  List<Product> get _visibleProducts {
    final query = _searchController.text.trim().toLowerCase();
    return _products.where((product) {
      final inCategory =
          _selectedCategory == 'All' ||
          product.category.toLowerCase() == _selectedCategory.toLowerCase();
      if (!inCategory) return false;
      if (query.isEmpty) return true;

      return product.title.toLowerCase().contains(query) ||
          product.description.toLowerCase().contains(query) ||
          product.category.toLowerCase().contains(query);
    }).toList();
  }

  Future<void> _addToCart(Product product) async {
    if (_addingProductIds.contains(product.id)) return;
    setState(() => _addingProductIds.add(product.id));

    try {
      await ref.read(shopRepositoryProvider).addToCart(product.id);
      ref.invalidate(shopCartProvider);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('${product.title} added to cart.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not add item to cart.')),
      );
    } finally {
      if (mounted) {
        setState(() => _addingProductIds.remove(product.id));
      }
    }
  }

  void _showCartSheet() {
    final pageContext = context;

    showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(22)),
      ),
      builder: (context) {
        return SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
            child: Consumer(
              builder: (context, ref, _) {
                final cartAsync = ref.watch(shopCartProvider);
                return cartAsync.when(
                  loading: () => const SizedBox(
                    height: 260,
                    child: Center(
                      child: CircularProgressIndicator(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  ),
                  error: (_, _) => SizedBox(
                    height: 240,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text('Your Cart', style: AppTextStyles.h2),
                        const SizedBox(height: 16),
                        Text(
                          'Could not load your cart.',
                          style: AppTextStyles.bodySmall,
                        ),
                      ],
                    ),
                  ),
                  data: (items) {
                    final total = items.fold<double>(
                      0,
                      (sum, item) =>
                          sum + ((item.product?.price ?? 0) * item.quantity),
                    );

                    if (items.isEmpty) {
                      return SizedBox(
                        height: 240,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text('Your Cart', style: AppTextStyles.h2),
                            const SizedBox(height: 18),
                            Container(
                              width: double.infinity,
                              padding: const EdgeInsets.all(18),
                              decoration: BoxDecoration(
                                color: AppColors.bgCard,
                                borderRadius: BorderRadius.circular(16),
                                border: Border.all(
                                  color: AppColors.borderSubtle,
                                ),
                              ),
                              child: Text(
                                'Your cart is empty. Add products from the marketplace.',
                                style: AppTextStyles.bodySmall,
                              ),
                            ),
                          ],
                        ),
                      );
                    }

                    return Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Text('Your Cart', style: AppTextStyles.h2),
                            const Spacer(),
                            Text(
                              '${items.length} item${items.length == 1 ? '' : 's'}',
                              style: AppTextStyles.labelSmall,
                            ),
                          ],
                        ),
                        const SizedBox(height: 10),
                        ConstrainedBox(
                          constraints: BoxConstraints(
                            maxHeight:
                                MediaQuery.of(context).size.height * 0.35,
                          ),
                          child: ListView.separated(
                            shrinkWrap: true,
                            itemCount: items.length,
                            separatorBuilder: (_, _) => const Divider(
                              height: 14,
                              color: AppColors.borderSubtle,
                            ),
                            itemBuilder: (context, index) {
                              final item = items[index];
                              final product = item.product;
                              final title = product?.title ?? 'Product';
                              final subtotal =
                                  ((product?.price ?? 0) * item.quantity)
                                      .toStringAsFixed(2);

                              return Row(
                                children: [
                                  Container(
                                    width: 42,
                                    height: 42,
                                    decoration: BoxDecoration(
                                      color: AppColors.bgCard,
                                      borderRadius: BorderRadius.circular(10),
                                    ),
                                    child: const Icon(
                                      Icons.shopping_bag_outlined,
                                      color: AppColors.textSecondary,
                                    ),
                                  ),
                                  const SizedBox(width: 10),
                                  Expanded(
                                    child: Column(
                                      crossAxisAlignment:
                                          CrossAxisAlignment.start,
                                      children: [
                                        Text(
                                          title,
                                          maxLines: 1,
                                          overflow: TextOverflow.ellipsis,
                                          style: AppTextStyles.label,
                                        ),
                                        Text(
                                          'Qty ${item.quantity}',
                                          style: AppTextStyles.labelSmall,
                                        ),
                                      ],
                                    ),
                                  ),
                                  Text(
                                    '\$$subtotal',
                                    style: AppTextStyles.label.copyWith(
                                      color: AppColors.postbookPrimary,
                                    ),
                                  ),
                                  IconButton(
                                    tooltip: 'Remove',
                                    onPressed: () async {
                                      try {
                                        await ref
                                            .read(shopRepositoryProvider)
                                            .removeFromCart(item.productId);
                                        ref.invalidate(shopCartProvider);
                                      } catch (_) {
                                        if (!pageContext.mounted) return;
                                        ScaffoldMessenger.of(
                                          pageContext,
                                        ).showSnackBar(
                                          const SnackBar(
                                            content: Text(
                                              'Could not remove item.',
                                            ),
                                          ),
                                        );
                                      }
                                    },
                                    icon: const Icon(
                                      Icons.delete_outline,
                                      color: AppColors.textMuted,
                                    ),
                                  ),
                                ],
                              );
                            },
                          ),
                        ),
                        const SizedBox(height: 12),
                        Container(
                          width: double.infinity,
                          padding: const EdgeInsets.all(12),
                          decoration: BoxDecoration(
                            color: AppColors.bgCard,
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: AppColors.borderSubtle),
                          ),
                          child: Row(
                            children: [
                              Text('Total', style: AppTextStyles.h3),
                              const Spacer(),
                              Text(
                                '\$${total.toStringAsFixed(2)}',
                                style: AppTextStyles.h3.copyWith(
                                  color: AppColors.postbookPrimary,
                                ),
                              ),
                            ],
                          ),
                        ),
                        const SizedBox(height: 12),
                        SizedBox(
                          width: double.infinity,
                          child: ElevatedButton.icon(
                            onPressed: () async {
                              try {
                                final order = await ref
                                    .read(shopRepositoryProvider)
                                    .checkout();
                                ref.invalidate(shopCartProvider);
                                if (!pageContext.mounted) return;
                                Navigator.of(context).pop();
                                pageContext.push('/orders/${order.id}');
                              } catch (_) {
                                if (!pageContext.mounted) return;
                                ScaffoldMessenger.of(pageContext).showSnackBar(
                                  const SnackBar(
                                    content: Text(
                                      'Checkout failed. Please retry.',
                                    ),
                                  ),
                                );
                              }
                            },
                            style: ElevatedButton.styleFrom(
                              backgroundColor: AppColors.postbookPrimary,
                              foregroundColor: Colors.white,
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(12),
                              ),
                              padding: const EdgeInsets.symmetric(vertical: 14),
                            ),
                            icon: const Icon(Icons.payments_outlined),
                            label: const Text('Checkout'),
                          ),
                        ),
                      ],
                    );
                  },
                );
              },
            ),
          ),
        );
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final visibleProducts = _visibleProducts;

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () => _loadProducts(showLoader: false),
          child: CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 8),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Container(
                        width: double.infinity,
                        padding: const EdgeInsets.all(16),
                        decoration: BoxDecoration(
                          borderRadius: BorderRadius.circular(
                            AppSpacing.radiusXL,
                          ),
                          gradient: const LinearGradient(
                            colors: [Color(0x33FF6B35), Color(0x334ECDC4)],
                            begin: Alignment.topLeft,
                            end: Alignment.bottomRight,
                          ),
                          border: Border.all(color: AppColors.borderMedium),
                        ),
                        child: Row(
                          children: [
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text('Marketplace', style: AppTextStyles.h1),
                                  const SizedBox(height: 4),
                                  Text(
                                    'Discover products from creators and small brands.',
                                    style: AppTextStyles.bodySmall,
                                  ),
                                ],
                              ),
                            ),
                            IconButton(
                              onPressed: _loadProducts,
                              icon: const Icon(
                                Icons.refresh_rounded,
                                color: AppColors.textPrimary,
                              ),
                            ),
                            IconButton(
                              onPressed: _showCartSheet,
                              icon: const Icon(
                                Icons.shopping_cart_outlined,
                                color: AppColors.textPrimary,
                              ),
                            ),
                          ],
                        ),
                      ),
                      const SizedBox(height: 12),
                      Container(
                        decoration: BoxDecoration(
                          color: AppColors.bgCard,
                          borderRadius: BorderRadius.circular(
                            AppSpacing.radiusLarge,
                          ),
                          border: Border.all(color: AppColors.borderSubtle),
                        ),
                        child: TextField(
                          controller: _searchController,
                          onChanged: (_) => setState(() {}),
                          decoration: InputDecoration(
                            border: InputBorder.none,
                            hintText: 'Search products',
                            hintStyle: AppTextStyles.bodySmall,
                            prefixIcon: const Icon(
                              Icons.search,
                              color: AppColors.textMuted,
                            ),
                            suffixIcon: _searchController.text.isEmpty
                                ? null
                                : IconButton(
                                    onPressed: () {
                                      _searchController.clear();
                                      setState(() {});
                                    },
                                    icon: const Icon(
                                      Icons.close,
                                      color: AppColors.textMuted,
                                    ),
                                  ),
                          ),
                        ),
                      ),
                      const SizedBox(height: 12),
                      SizedBox(
                        height: 40,
                        child: ListView.separated(
                          scrollDirection: Axis.horizontal,
                          itemCount: _categories.length,
                          separatorBuilder: (_, _) => const SizedBox(width: 8),
                          itemBuilder: (context, index) {
                            final category = _categories[index];
                            final selected = category == _selectedCategory;
                            return ChoiceChip(
                              label: Text(category),
                              selected: selected,
                              onSelected: (_) {
                                setState(() => _selectedCategory = category);
                              },
                              selectedColor: AppColors.postbookPrimary
                                  .withValues(alpha: 0.22),
                              backgroundColor: AppColors.bgCard,
                              side: BorderSide(
                                color: selected
                                    ? AppColors.postbookPrimary
                                    : AppColors.borderSubtle,
                              ),
                              labelStyle: AppTextStyles.label.copyWith(
                                color: selected
                                    ? AppColors.postbookPrimary
                                    : AppColors.textSecondary,
                              ),
                            );
                          },
                        ),
                      ),
                      const SizedBox(height: 12),
                      Text(
                        '${visibleProducts.length} results',
                        style: AppTextStyles.labelSmall,
                      ),
                    ],
                  ),
                ),
              ),
              if (_loadingProducts)
                const SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: CircularProgressIndicator(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                )
              else if (_productsError != null)
                SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: Padding(
                      padding: AppSpacing.pagePadding,
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(
                            Icons.storefront_outlined,
                            size: 42,
                            color: AppColors.textMuted,
                          ),
                          const SizedBox(height: 10),
                          Text(_productsError!, style: AppTextStyles.bodySmall),
                          const SizedBox(height: 8),
                          TextButton(
                            onPressed: _loadProducts,
                            child: Text(
                              'Retry',
                              style: AppTextStyles.label.copyWith(
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                )
              else if (visibleProducts.isEmpty)
                const SliverFillRemaining(
                  hasScrollBody: false,
                  child: Center(
                    child: Padding(
                      padding: EdgeInsets.all(18),
                      child: Text('No products match your search or category.'),
                    ),
                  ),
                )
              else
                SliverPadding(
                  padding: AppSpacing.pagePadding.copyWith(bottom: 110),
                  sliver: SliverLayoutBuilder(
                    builder: (context, constraints) {
                      final width = constraints.crossAxisExtent;
                      final crossAxisCount = width >= 900
                          ? 4
                          : width >= 650
                          ? 3
                          : 2;

                      return SliverGrid(
                        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                          crossAxisCount: crossAxisCount,
                          childAspectRatio: 0.72,
                          mainAxisSpacing: 10,
                          crossAxisSpacing: 10,
                        ),
                        delegate: SliverChildBuilderDelegate((context, index) {
                          final product = visibleProducts[index];
                          return _ProductCard(
                            product: product,
                            adding: _addingProductIds.contains(product.id),
                            onAdd: () => _addToCart(product),
                          );
                        }, childCount: visibleProducts.length),
                      );
                    },
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ProductCard extends StatelessWidget {
  const _ProductCard({
    required this.product,
    required this.adding,
    required this.onAdd,
  });

  final Product product;
  final bool adding;
  final VoidCallback onAdd;

  @override
  Widget build(BuildContext context) {
    final lowStock = product.stock > 0 && product.stock <= 5;

    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            flex: 3,
            child: Container(
              width: double.infinity,
              decoration: const BoxDecoration(
                borderRadius: BorderRadius.vertical(
                  top: Radius.circular(16),
                ),
                gradient: LinearGradient(
                  colors: [
                    Color(0x3325B2FF),
                    Color(0x335350E6),
                    Color(0x33FF6B35),
                  ],
                  begin: Alignment.topLeft,
                  end: Alignment.bottomRight,
                ),
              ),
              child: Stack(
                children: [
                  // Product image from first media ID, or fallback icon.
                  if (product.mediaIds.isNotEmpty)
                    Positioned.fill(
                      child: ClipRRect(
                        borderRadius: const BorderRadius.vertical(
                          top: Radius.circular(16),
                        ),
                        child: Image.network(
                          '${Environment.apiBaseUrl}/v1/media/${product.mediaIds.first}/serve',
                          fit: BoxFit.cover,
                          width: double.infinity,
                          height: double.infinity,
                          loadingBuilder: (context, child, loadingProgress) {
                            if (loadingProgress == null) return child;
                            return const Center(
                              child: SizedBox(
                                width: 24,
                                height: 24,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  color: AppColors.postbookPrimary,
                                ),
                              ),
                            );
                          },
                          errorBuilder: (_, _, _) => const Center(
                            child: Icon(
                              Icons.shopping_bag_outlined,
                              size: 40,
                              color: AppColors.textSecondary,
                            ),
                          ),
                        ),
                      ),
                    )
                  else
                    const Center(
                      child: Icon(
                        Icons.shopping_bag_outlined,
                        size: 40,
                        color: AppColors.textSecondary,
                      ),
                    ),
                  // Category badge.
                  Positioned(
                    top: 8,
                    left: 8,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 4,
                      ),
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: 0.35),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Text(
                        product.category.isEmpty ? 'General' : product.category,
                        style: AppTextStyles.labelSmall.copyWith(
                          color: Colors.white,
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
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
                    style: AppTextStyles.label,
                  ),
                  const Spacer(),
                  Row(
                    children: [
                      Text(
                        '${_currencySymbol(product.currency)}${product.price.toStringAsFixed(2)}',
                        style: AppTextStyles.h3.copyWith(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                      const Spacer(),
                      if (lowStock)
                        Text(
                          '${product.stock} left',
                          style: AppTextStyles.labelSmall.copyWith(
                            color: Colors.orange,
                          ),
                        ),
                    ],
                  ),
                  const SizedBox(height: 6),
                  SizedBox(
                    width: double.infinity,
                    child: ElevatedButton(
                      onPressed: adding ? null : onAdd,
                      style: ElevatedButton.styleFrom(
                        backgroundColor: AppColors.postbookPrimary,
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(vertical: 8),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(10),
                        ),
                      ),
                      child: adding
                          ? const SizedBox(
                              width: 14,
                              height: 14,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : const Text('Add'),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  String _currencySymbol(String currency) {
    switch (currency.toUpperCase()) {
      case 'INR':
        return 'Rs ';
      case 'EUR':
        return 'EUR ';
      case 'GBP':
        return 'GBP ';
      case 'USD':
      default:
        return r'$';
    }
  }
}
