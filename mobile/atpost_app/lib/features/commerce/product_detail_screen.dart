// Product detail (PDP) — Sprint 1.
//
// Sections from top:
//   1. Image gallery (PageView with dot indicators).
//   2. Title + rating link.
//   3. Price block (MRP strike + sale price + % off).
//   4. Variant selector — chips per attribute (size / color / …).
//   5. Pincode serviceability check.
//   6. Description (collapsible).
//   7. Tax + HSN small-print.
//   8. Reviews preview (top 3 + "See all").
//   9. Sticky bottom — "Add to cart" + "Buy now".

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/features/commerce/widgets/wishlist_button.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ProductDetailScreen extends ConsumerStatefulWidget {
  const ProductDetailScreen({super.key, required this.productId});

  final String productId;

  @override
  ConsumerState<ProductDetailScreen> createState() =>
      _ProductDetailScreenState();
}

class _ProductDetailScreenState extends ConsumerState<ProductDetailScreen> {
  ProductVariant? _selectedVariant;
  final TextEditingController _pincodeCtrl = TextEditingController();
  String? _pincodeChecked;
  bool _descExpanded = false;
  final PageController _galleryCtrl = PageController();
  int _galleryIndex = 0;

  @override
  void dispose() {
    _pincodeCtrl.dispose();
    _galleryCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final productAsync = ref.watch(productProvider(widget.productId));
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back, color: AppColors.textPrimary),
          onPressed: () => GoRouter.of(context).pop(),
        ),
        actions: [
          // Sprint 2: heart toggle for wishlist add/remove. Snapshot is
          // best-effort — when the product is loaded we feed it through
          // so the wishlist tile renders price + image without an extra
          // round-trip.
          productAsync.maybeWhen(
            data: (p) => WishlistButton(
              productId: p.id,
              snapshot: WishlistItemSnapshot(
                title: p.title,
                primaryImageUrl: p.primaryImageUrl,
                sellingPrice: p.basePrice,
                mrp: p.mrp,
              ),
            ),
            orElse: () => WishlistButton(productId: widget.productId),
          ),
          IconButton(
            icon: const Icon(Icons.shopping_cart_outlined,
                color: AppColors.textPrimary),
            onPressed: () => GoRouter.of(context).push('/commerce/cart'),
          ),
        ],
      ),
      body: productAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load product.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (product) {
          _selectedVariant ??= product.defaultVariant;
          return _Body(
            product: product,
            selectedVariant: _selectedVariant,
            onVariantSelect: (v) => setState(() => _selectedVariant = v),
            pincodeCtrl: _pincodeCtrl,
            checkedPincode: _pincodeChecked,
            onCheckPincode: () {
              final p = _pincodeCtrl.text.trim();
              if (p.length == 6) {
                setState(() => _pincodeChecked = p);
              }
            },
            descExpanded: _descExpanded,
            toggleDesc: () =>
                setState(() => _descExpanded = !_descExpanded),
            galleryCtrl: _galleryCtrl,
            galleryIndex: _galleryIndex,
            onGalleryChange: (i) => setState(() => _galleryIndex = i),
          );
        },
      ),
      bottomNavigationBar: productAsync.maybeWhen(
        data: (product) => _StickyActions(
          product: product,
          variant: _selectedVariant ?? product.defaultVariant,
        ),
        orElse: () => null,
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({
    required this.product,
    required this.selectedVariant,
    required this.onVariantSelect,
    required this.pincodeCtrl,
    required this.checkedPincode,
    required this.onCheckPincode,
    required this.descExpanded,
    required this.toggleDesc,
    required this.galleryCtrl,
    required this.galleryIndex,
    required this.onGalleryChange,
  });

  final Product product;
  final ProductVariant? selectedVariant;
  final void Function(ProductVariant) onVariantSelect;
  final TextEditingController pincodeCtrl;
  final String? checkedPincode;
  final VoidCallback onCheckPincode;
  final bool descExpanded;
  final VoidCallback toggleDesc;
  final PageController galleryCtrl;
  final int galleryIndex;
  final void Function(int) onGalleryChange;

  @override
  Widget build(BuildContext context) {
    final images = <String>[
      if (product.primaryImageUrl != null) product.primaryImageUrl!,
      ...product.imageUrls,
    ];
    final showPrice = selectedVariant?.sellingPrice ?? product.basePrice;
    final showMrp = selectedVariant?.mrp ?? product.mrp;
    final showDiscount =
        selectedVariant?.discountPct ?? product.discountPct;

    return ListView(
      padding: const EdgeInsets.only(bottom: AppSpacing.xxl),
      children: [
        // Gallery.
        AspectRatio(
          aspectRatio: 1,
          child: Stack(
            children: [
              Positioned.fill(
                child: images.isEmpty
                    ? Container(
                        color: AppColors.bgSecondary,
                        child: const Icon(Icons.image_outlined,
                            size: 64, color: AppColors.textGhost),
                      )
                    : PageView.builder(
                        controller: galleryCtrl,
                        onPageChanged: onGalleryChange,
                        itemCount: images.length,
                        itemBuilder: (_, i) => Image.network(
                          images[i],
                          fit: BoxFit.cover,
                          errorBuilder: (_, _, _) => Container(
                            color: AppColors.bgSecondary,
                            child: const Icon(Icons.broken_image_outlined,
                                size: 48, color: AppColors.textGhost),
                          ),
                        ),
                      ),
              ),
              if (images.length > 1)
                Positioned(
                  bottom: AppSpacing.l,
                  left: 0,
                  right: 0,
                  child: Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: List.generate(images.length, (i) {
                      final on = i == galleryIndex;
                      return AnimatedContainer(
                        duration: const Duration(milliseconds: 180),
                        margin: const EdgeInsets.symmetric(horizontal: 3),
                        width: on ? 18 : 6,
                        height: 6,
                        decoration: BoxDecoration(
                          color: on ? Colors.white : Colors.white54,
                          borderRadius:
                              BorderRadius.circular(AppSpacing.radiusFull),
                        ),
                      );
                    }),
                  ),
                ),
            ],
          ),
        ),
        const SizedBox(height: AppSpacing.l),
        // Title + rating.
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(product.title, style: AppTextStyles.h2),
              const SizedBox(height: AppSpacing.s),
              InkWell(
                onTap: () => GoRouter.of(context).push(
                  '/commerce/product/${product.id}/reviews',
                ),
                child: Row(
                  children: [
                    const Icon(Icons.star,
                        size: 16, color: AppColors.statusWarning),
                    const SizedBox(width: 4),
                    Text(
                      product.rating > 0
                          ? product.rating.toStringAsFixed(1)
                          : '—',
                      style: AppTextStyles.label,
                    ),
                    const SizedBox(width: AppSpacing.s),
                    Text(
                      product.ratingCount > 0
                          ? '${product.ratingCount} reviews'
                          : 'No reviews yet',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.posttubePrimary,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: AppSpacing.xxl),
        // Price block.
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.baseline,
            textBaseline: TextBaseline.alphabetic,
            children: [
              Text(
                'Rs. ${showPrice.toStringAsFixed(0)}',
                style: AppTextStyles.h1,
              ),
              const SizedBox(width: AppSpacing.l),
              if (showDiscount != null) ...[
                Text(
                  'Rs. ${showMrp.toStringAsFixed(0)}',
                  style: AppTextStyles.label.copyWith(
                    decoration: TextDecoration.lineThrough,
                    color: AppColors.textMuted,
                  ),
                ),
                const SizedBox(width: AppSpacing.m),
                Text(
                  '$showDiscount% off',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.statusSuccess,
                  ),
                ),
              ],
            ],
          ),
        ),
        const SizedBox(height: AppSpacing.xxl),
        // Variants.
        if (product.variants.isNotEmpty)
          _VariantSelector(
            product: product,
            selected: selectedVariant,
            onSelect: onVariantSelect,
          ),
        const SizedBox(height: AppSpacing.xxl),
        // Pincode.
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
          child: _PincodeBlock(
            controller: pincodeCtrl,
            checked: checkedPincode,
            onCheck: onCheckPincode,
          ),
        ),
        const SizedBox(height: AppSpacing.xxl),
        // Description.
        if (product.description != null && product.description!.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Description', style: AppTextStyles.h3),
                const SizedBox(height: AppSpacing.s),
                Text(
                  product.description!,
                  style: AppTextStyles.body,
                  maxLines: descExpanded ? null : 5,
                  overflow: descExpanded
                      ? TextOverflow.visible
                      : TextOverflow.ellipsis,
                ),
                TextButton(
                  onPressed: toggleDesc,
                  child: Text(descExpanded ? 'Show less' : 'Read more'),
                ),
              ],
            ),
          ),
        const SizedBox(height: AppSpacing.l),
        // Tax + HSN small print.
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
          child: _SmallPrint(product: product),
        ),
        const SizedBox(height: AppSpacing.xxl),
        // Reviews.
        _ReviewsBlock(productId: product.id),
      ],
    );
  }
}

class _VariantSelector extends StatelessWidget {
  const _VariantSelector({
    required this.product,
    required this.selected,
    required this.onSelect,
  });

  final Product product;
  final ProductVariant? selected;
  final void Function(ProductVariant) onSelect;

  @override
  Widget build(BuildContext context) {
    // Group variants by attribute key (size, color, …) so each attribute
    // becomes a row of chips. Tapping a chip selects the first variant
    // matching all currently-selected values.
    final attrKeys = <String>{};
    for (final v in product.variants) {
      attrKeys.addAll(v.attributes.keys);
    }
    if (attrKeys.isEmpty) return const SizedBox.shrink();

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (final key in attrKeys) ...[
            Text(_capitalize(key), style: AppTextStyles.h3),
            const SizedBox(height: AppSpacing.s),
            Wrap(
              spacing: AppSpacing.m,
              runSpacing: AppSpacing.m,
              children: _valuesFor(product.variants, key).map((value) {
                final variant = _firstMatch(product.variants, key, value);
                final isSelected = selected?.attributes[key] == value;
                return _Chip(
                  label: value,
                  selected: isSelected,
                  onTap: variant == null ? null : () => onSelect(variant),
                );
              }).toList(),
            ),
            const SizedBox(height: AppSpacing.l),
          ],
          if (selected != null && selected!.stockQty > 0 && selected!.stockQty <= 3)
            Text(
              'Only ${selected!.stockQty} left',
              style: AppTextStyles.label.copyWith(
                color: AppColors.statusWarning,
              ),
            ),
          if (selected != null && selected!.stockQty == 0)
            Text(
              'Out of stock',
              style: AppTextStyles.label.copyWith(
                color: AppColors.statusError,
              ),
            ),
        ],
      ),
    );
  }

  static String _capitalize(String s) =>
      s.isEmpty ? s : s[0].toUpperCase() + s.substring(1);

  static List<String> _valuesFor(List<ProductVariant> variants, String key) {
    final out = <String>{};
    for (final v in variants) {
      final val = v.attributes[key];
      if (val != null) out.add(val);
    }
    return out.toList();
  }

  static ProductVariant? _firstMatch(
      List<ProductVariant> variants, String key, String value) {
    for (final v in variants) {
      if (v.attributes[key] == value) return v;
    }
    return null;
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
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
      child: Container(
        padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.l, vertical: AppSpacing.m),
        decoration: BoxDecoration(
          color: selected ? AppColors.postbookPrimary : AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.bodySmall.copyWith(
            color: selected ? Colors.white : AppColors.textPrimary,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}

class _PincodeBlock extends ConsumerWidget {
  const _PincodeBlock({
    required this.controller,
    required this.checked,
    required this.onCheck,
  });

  final TextEditingController controller;
  final String? checked;
  final VoidCallback onCheck;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Delivery', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        Row(
          children: [
            Expanded(
              child: TextField(
                controller: controller,
                keyboardType: TextInputType.number,
                style: AppTextStyles.body,
                decoration: InputDecoration(
                  hintText: 'Enter 6-digit pincode',
                  hintStyle: AppTextStyles.body.copyWith(
                    color: AppColors.textMuted,
                  ),
                  filled: true,
                  fillColor: AppColors.bgCard,
                  border: OutlineInputBorder(
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusSmall),
                    borderSide:
                        const BorderSide(color: AppColors.borderSubtle),
                  ),
                  enabledBorder: OutlineInputBorder(
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusSmall),
                    borderSide:
                        const BorderSide(color: AppColors.borderSubtle),
                  ),
                ),
                maxLength: 6,
              ),
            ),
            const SizedBox(width: AppSpacing.m),
            ElevatedButton(
              onPressed: onCheck,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
              ),
              child: const Text('Check'),
            ),
          ],
        ),
        if (checked != null) _PincodeResult(pincode: checked!),
      ],
    );
  }
}

class _PincodeResult extends ConsumerWidget {
  const _PincodeResult({required this.pincode});

  final String pincode;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final res = ref.watch(pincodeServiceabilityProvider(pincode));
    return res.when(
      loading: () => const Padding(
        padding: EdgeInsets.symmetric(vertical: AppSpacing.s),
        child: LinearProgressIndicator(
          minHeight: 2,
          color: AppColors.postbookPrimary,
        ),
      ),
      error: (_, _) => Padding(
        padding: const EdgeInsets.symmetric(vertical: AppSpacing.s),
        child: Text(
          'Could not check pincode. Try again.',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.statusError,
          ),
        ),
      ),
      data: (data) {
        if (!data.deliverable) {
          return Padding(
            padding: const EdgeInsets.only(top: AppSpacing.s),
            child: Text(
              data.message ?? 'Not deliverable to this pincode',
              style: AppTextStyles.label.copyWith(
                color: AppColors.statusError,
              ),
            ),
          );
        }
        final eta = DateTime.now().add(Duration(days: data.etaDays));
        return Padding(
          padding: const EdgeInsets.only(top: AppSpacing.s),
          child: Text(
            'Delivery by ${_fmtDate(eta)}',
            style: AppTextStyles.label.copyWith(
              color: AppColors.statusSuccess,
            ),
          ),
        );
      },
    );
  }

  static String _fmtDate(DateTime d) {
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return '${d.day} ${months[d.month - 1]}';
  }
}

class _SmallPrint extends StatelessWidget {
  const _SmallPrint({required this.product});

  final Product product;

  @override
  Widget build(BuildContext context) {
    final lines = <String>[];
    lines.add('Inclusive of all taxes');
    if (product.taxRatePct > 0) {
      lines.add('GST ${product.taxRatePct.toStringAsFixed(0)}%');
    }
    if (product.hsnCode != null) {
      lines.add('HSN ${product.hsnCode}');
    }
    if (product.isReturnable && product.returnWindowDays > 0) {
      lines.add('${product.returnWindowDays}-day return');
    }
    return Text(
      lines.join(' · '),
      style: AppTextStyles.bodySmall.copyWith(color: AppColors.textMuted),
    );
  }
}

class _ReviewsBlock extends ConsumerWidget {
  const _ReviewsBlock({required this.productId});

  final String productId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncReviews = ref.watch(productReviewsProvider(productId));
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text('Reviews', style: AppTextStyles.h3),
              TextButton(
                onPressed: () => GoRouter.of(context)
                    .push('/commerce/product/$productId/reviews'),
                child: const Text('See all'),
              ),
            ],
          ),
          asyncReviews.when(
            loading: () => const Padding(
              padding: EdgeInsets.symmetric(vertical: AppSpacing.l),
              child: LinearProgressIndicator(
                minHeight: 2,
                color: AppColors.postbookPrimary,
              ),
            ),
            error: (_, _) => Text(
              'Could not load reviews.',
              style: AppTextStyles.bodySmall,
            ),
            data: (list) {
              if (list.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.symmetric(vertical: AppSpacing.l),
                  child: Text(
                    'No reviews yet. Be the first.',
                    style: AppTextStyles.body,
                  ),
                );
              }
              return Column(
                children: list.take(3).map(_ReviewTile.new).toList(),
              );
            },
          ),
        ],
      ),
    );
  }
}

class _ReviewTile extends StatelessWidget {
  const _ReviewTile(this.review);

  final ProductReview review;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.s),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              for (var i = 0; i < 5; i++)
                Icon(
                  i < review.rating ? Icons.star : Icons.star_outline,
                  size: 14,
                  color: AppColors.statusWarning,
                ),
              const SizedBox(width: AppSpacing.s),
              Text(review.buyerName, style: AppTextStyles.label),
            ],
          ),
          if (review.title != null) ...[
            const SizedBox(height: 2),
            Text(review.title!, style: AppTextStyles.label),
          ],
          if (review.body != null) ...[
            const SizedBox(height: 2),
            Text(
              review.body!,
              style: AppTextStyles.bodySmall,
              maxLines: 3,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ],
      ),
    );
  }
}

class _StickyActions extends ConsumerStatefulWidget {
  const _StickyActions({required this.product, required this.variant});

  final Product product;
  final ProductVariant? variant;

  @override
  ConsumerState<_StickyActions> createState() => _StickyActionsState();
}

class _StickyActionsState extends ConsumerState<_StickyActions> {
  bool _busy = false;

  Future<bool> _addToCart() async {
    final variant = widget.variant;
    if (variant == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('No variant available for this product')),
      );
      return false;
    }
    setState(() => _busy = true);
    try {
      await ref.read(cartProvider.notifier).addToCart(
            productId: widget.product.id,
            variantId: variant.id,
            qty: 1,
          );
      return true;
    } catch (e) {
      if (!mounted) return false;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not add to cart: $e')),
      );
      return false;
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final disabled = widget.variant == null ||
        widget.variant!.stockQty == 0 ||
        _busy;
    return SafeArea(
      child: Container(
        padding: const EdgeInsets.fromLTRB(
            AppSpacing.l, AppSpacing.m, AppSpacing.l, AppSpacing.m),
        decoration: const BoxDecoration(
          color: AppColors.bgPrimary,
          border: Border(top: BorderSide(color: AppColors.borderSubtle)),
        ),
        child: Row(
          children: [
            Expanded(
              child: OutlinedButton(
                onPressed: disabled
                    ? null
                    : () async {
                        final ok = await _addToCart();
                        if (!ok || !mounted) return;
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: const Text('Added to cart'),
                            action: SnackBarAction(
                              label: 'View cart',
                              onPressed: () => GoRouter.of(context)
                                  .push('/commerce/cart'),
                            ),
                          ),
                        );
                      },
                style: OutlinedButton.styleFrom(
                  side: const BorderSide(color: AppColors.postbookPrimary),
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                child: const Text('Add to cart'),
              ),
            ),
            const SizedBox(width: AppSpacing.m),
            Expanded(
              child: ElevatedButton(
                onPressed: disabled
                    ? null
                    : () async {
                        final ok = await _addToCart();
                        if (!ok || !mounted) return;
                        GoRouter.of(context).push('/commerce/checkout');
                      },
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                child: const Text('Buy now'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
