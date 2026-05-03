// Commerce home (catalog) — Sprint 1.
//
// Top: search icon, cart badge.
// Body: scrollable category chips + product grid (2-col on phone).
// Pull-to-refresh + infinite scroll (offset paged off `productsProvider`).

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/features/commerce/home_recommendations.dart';
import 'package:atpost_app/features/commerce/widgets/wishlist_button.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:atpost_app/services/commerce_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CommerceHomeScreen extends ConsumerStatefulWidget {
  const CommerceHomeScreen({super.key, this.initialCategorySlug});

  /// When the user lands via `/commerce/category/:slug`, the matching
  /// category is preselected. Resolved against `categoriesProvider` once
  /// it loads.
  final String? initialCategorySlug;

  @override
  ConsumerState<CommerceHomeScreen> createState() => _CommerceHomeScreenState();
}

class _CommerceHomeScreenState extends ConsumerState<CommerceHomeScreen> {
  String? _categoryId;
  String? _searchQuery;
  final ScrollController _scroll = ScrollController();
  final List<Product> _accumulated = [];
  String? _lastCursor;
  bool _loadingMore = false;
  bool _exhausted = false;
  ProductsQuery? _queryInFlight;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scroll.removeListener(_onScroll);
    _scroll.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (!_scroll.hasClients) return;
    final pos = _scroll.position;
    if (pos.pixels >= pos.maxScrollExtent - 320 &&
        !_loadingMore &&
        !_exhausted) {
      _fetchMore();
    }
  }

  Future<void> _fetchMore() async {
    if (_loadingMore || _exhausted) return;
    setState(() => _loadingMore = true);
    final repo = ref.read(commerceRepositoryProvider);
    try {
      final page = await repo.listProducts(
        categoryId: _categoryId,
        q: _searchQuery,
        cursor: _lastCursor,
      );
      if (!mounted) return;
      setState(() {
        _accumulated.addAll(page.items);
        _lastCursor = page.nextOffset.toString();
        _exhausted = !page.hasMore || page.items.isEmpty;
      });
    } catch (_) {
      // swallow — initial page error surfaces via the AsyncValue UI.
    } finally {
      if (mounted) setState(() => _loadingMore = false);
    }
  }

  void _resetAndQuery({String? categoryId, String? q}) {
    setState(() {
      _categoryId = categoryId;
      _searchQuery = q;
      _accumulated.clear();
      _lastCursor = null;
      _exhausted = false;
    });
  }

  Future<void> _refresh() async {
    _resetAndQuery(categoryId: _categoryId, q: _searchQuery);
    ref.invalidate(productsProvider(_currentQuery));
    ref.invalidate(cartProvider);
  }

  ProductsQuery get _currentQuery =>
      ProductsQuery(categoryId: _categoryId, q: _searchQuery);

  @override
  Widget build(BuildContext context) {
    final query = _currentQuery;
    if (_queryInFlight != query) {
      _queryInFlight = query;
    }

    final pageAsync = ref.watch(productsProvider(query));

    // Resolve initial category slug from `categoriesProvider`.
    if (widget.initialCategorySlug != null && _categoryId == null) {
      ref.listen(categoriesProvider, (_, next) {
        next.whenData((cats) {
          for (final c in cats) {
            if (c.slug == widget.initialCategorySlug) {
              _resetAndQuery(categoryId: c.id);
              ref
                  .read(commerceTelemetryProvider)
                  .categoryViewed(categoryId: c.id);
              break;
            }
          }
        });
      });
    }

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Storefront', style: AppTextStyles.h2),
        actions: [
          IconButton(
            tooltip: 'Search',
            icon: const Icon(Icons.search, color: AppColors.textPrimary),
            onPressed: _openSearch,
          ),
          IconButton(
            tooltip: 'Wishlist',
            icon: const Icon(Icons.favorite_border,
                color: AppColors.textPrimary),
            onPressed: () =>
                GoRouter.of(context).push('/commerce/wishlist'),
          ),
          _CartBadgeButton(),
          const SizedBox(width: AppSpacing.s),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        color: AppColors.postbookPrimary,
        child: CustomScrollView(
          controller: _scroll,
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverToBoxAdapter(child: _CategoryStrip(
              selectedId: _categoryId,
              onSelect: (id) {
                _resetAndQuery(categoryId: id, q: _searchQuery);
                if (id != null) {
                  ref
                      .read(commerceTelemetryProvider)
                      .categoryViewed(categoryId: id);
                }
              },
            )),
            // Sprint 2: recommendations carousel below categories. Hidden
            // when a category filter is active so the surface stays
            // focused on the chosen category.
            if (_categoryId == null && (_searchQuery == null || _searchQuery!.isEmpty))
              const SliverToBoxAdapter(child: HomeRecommendations()),
            pageAsync.when(
              loading: () => const SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: CircularProgressIndicator(
                      color: AppColors.postbookPrimary),
                ),
              ),
              error: (e, _) => SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.xxl),
                    child: Text(
                      'Could not load products. Pull to retry.',
                      style: AppTextStyles.body,
                      textAlign: TextAlign.center,
                    ),
                  ),
                ),
              ),
              data: (page) {
                // Seed the accumulated list on first load only.
                if (_accumulated.isEmpty && _lastCursor == null) {
                  _accumulated.addAll(page.items);
                  _lastCursor = page.nextOffset.toString();
                  _exhausted = !page.hasMore;
                }
                if (_accumulated.isEmpty) {
                  return const SliverFillRemaining(
                    hasScrollBody: false,
                    child: _EmptyState(),
                  );
                }
                return SliverPadding(
                  padding: const EdgeInsets.fromLTRB(
                      AppSpacing.l, AppSpacing.s, AppSpacing.l, AppSpacing.xxl),
                  sliver: SliverGrid(
                    gridDelegate:
                        const SliverGridDelegateWithFixedCrossAxisCount(
                      crossAxisCount: 2,
                      mainAxisSpacing: AppSpacing.l,
                      crossAxisSpacing: AppSpacing.l,
                      childAspectRatio: 0.62,
                    ),
                    delegate: SliverChildBuilderDelegate(
                      (ctx, i) => _ProductCard(product: _accumulated[i]),
                      childCount: _accumulated.length,
                    ),
                  ),
                );
              },
            ),
            if (_loadingMore)
              const SliverToBoxAdapter(
                child: Padding(
                  padding: EdgeInsets.all(AppSpacing.xxl),
                  child: Center(
                    child: CircularProgressIndicator(
                      color: AppColors.postbookPrimary,
                      strokeWidth: 2,
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Future<void> _openSearch() async {
    // Sprint 2: full search screen (autocomplete + filters + sort).
    GoRouter.of(context).push('/commerce/search');
  }
}

class _CategoryStrip extends ConsumerWidget {
  const _CategoryStrip({required this.selectedId, required this.onSelect});

  final String? selectedId;
  final void Function(String? id) onSelect;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cats = ref.watch(categoriesProvider);
    return cats.when(
      loading: () => const SizedBox(height: 56),
      error: (_, _) => const SizedBox.shrink(),
      data: (list) {
        final tops = list.where((c) => c.parentId == null).toList();
        if (tops.isEmpty) return const SizedBox.shrink();
        return SizedBox(
          height: 56,
          child: ListView.separated(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.l, vertical: AppSpacing.m),
            itemCount: tops.length + 1,
            separatorBuilder: (_, _) => const SizedBox(width: AppSpacing.m),
            itemBuilder: (ctx, i) {
              if (i == 0) {
                return _Chip(
                  label: 'All',
                  selected: selectedId == null,
                  onTap: () => onSelect(null),
                );
              }
              final c = tops[i - 1];
              return _Chip(
                label: c.name,
                selected: selectedId == c.id,
                onTap: () => onSelect(c.id),
              );
            },
          ),
        );
      },
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      child: Container(
        padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.xxl, vertical: AppSpacing.m),
        decoration: BoxDecoration(
          color: selected ? AppColors.postbookPrimary : AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.bodySmall.copyWith(
            color: selected ? Colors.white : AppColors.textSecondary,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}

class _ProductCard extends StatelessWidget {
  const _ProductCard({required this.product});

  final Product product;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => GoRouter.of(context).push('/commerce/product/${product.id}'),
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
            // Sprint 2: image stack + small heart overlay.
            Stack(
              children: [
                AspectRatio(
                  aspectRatio: 1,
                  child: ClipRRect(
                    borderRadius: const BorderRadius.vertical(
                      top: Radius.circular(AppSpacing.radiusLarge),
                    ),
                    child: _ProductImage(url: product.primaryImageUrl),
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
                      productId: product.id,
                      snapshot: WishlistItemSnapshot(
                        title: product.title,
                        primaryImageUrl: product.primaryImageUrl,
                        sellingPrice: product.basePrice,
                        mrp: product.mrp,
                      ),
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
                    product.title,
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
                        'Rs. ${product.basePrice.toStringAsFixed(0)}',
                        style: AppTextStyles.h3,
                      ),
                      const SizedBox(width: AppSpacing.s),
                      if (product.discountPct != null)
                        Text(
                          'Rs. ${product.mrp.toStringAsFixed(0)}',
                          style: AppTextStyles.bodySmall.copyWith(
                            decoration: TextDecoration.lineThrough,
                            color: AppColors.textMuted,
                          ),
                        ),
                    ],
                  ),
                  if (product.discountPct != null) ...[
                    const SizedBox(height: AppSpacing.xs),
                    Text(
                      '${product.discountPct}% off',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.statusSuccess,
                      ),
                    ),
                  ],
                  const SizedBox(height: AppSpacing.s),
                  Row(
                    children: [
                      const Icon(Icons.star,
                          size: 12, color: AppColors.statusWarning),
                      const SizedBox(width: 4),
                      Text(
                        product.rating > 0
                            ? product.rating.toStringAsFixed(1)
                            : '—',
                        style: AppTextStyles.labelSmall,
                      ),
                      if (product.ratingCount > 0) ...[
                        const SizedBox(width: 4),
                        Text(
                          '(${product.ratingCount})',
                          style: AppTextStyles.labelSmall,
                        ),
                      ],
                    ],
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

class _ProductImage extends StatelessWidget {
  const _ProductImage({required this.url});

  final String? url;

  @override
  Widget build(BuildContext context) {
    if (url == null || url!.isEmpty) {
      return Container(
        color: AppColors.bgSecondary,
        child: const Icon(
          Icons.image_outlined,
          color: AppColors.textGhost,
          size: 36,
        ),
      );
    }
    return Image.network(
      url!,
      fit: BoxFit.cover,
      errorBuilder: (_, _, _) => Container(
        color: AppColors.bgSecondary,
        child: const Icon(
          Icons.broken_image_outlined,
          color: AppColors.textGhost,
          size: 32,
        ),
      ),
    );
  }
}

class _CartBadgeButton extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cartAsync = ref.watch(cartProvider);
    final count = cartAsync.asData?.value.itemCount ?? 0;
    return Stack(
      clipBehavior: Clip.none,
      children: [
        IconButton(
          tooltip: 'Cart',
          icon: const Icon(Icons.shopping_cart_outlined,
              color: AppColors.textPrimary),
          onPressed: () => GoRouter.of(context).push('/commerce/cart'),
        ),
        if (count > 0)
          Positioned(
            right: 4,
            top: 6,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary,
                borderRadius: BorderRadius.circular(999),
              ),
              constraints: const BoxConstraints(minWidth: 16, minHeight: 16),
              child: Text(
                '$count',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                ),
                textAlign: TextAlign.center,
              ),
            ),
          ),
      ],
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.storefront_outlined,
              size: 56, color: AppColors.textGhost),
          const SizedBox(height: AppSpacing.l),
          Text(
            'No products in this category yet.',
            style: AppTextStyles.body,
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }
}

