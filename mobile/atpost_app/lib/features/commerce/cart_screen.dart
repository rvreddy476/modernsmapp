// Cart — Sprint 1.
//
// Reads the live cart via `cartProvider`. Each item shows image / title /
// variant label / unit price / qty stepper / remove. Coupon input is a
// placeholder until the backend coupon endpoint is wired into the
// repository (see TODO inline). Sticky bottom shows the totals + checkout.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CartScreen extends ConsumerStatefulWidget {
  const CartScreen({super.key});

  @override
  ConsumerState<CartScreen> createState() => _CartScreenState();
}

class _CartScreenState extends ConsumerState<CartScreen> {
  final TextEditingController _couponCtrl = TextEditingController();
  // appliedCoupon is the code the user pressed Apply on; the
  // previewProvider watches this rather than the raw text so we don't
  // hammer the backend on every keystroke.
  String _appliedCoupon = '';

  @override
  void dispose() {
    _couponCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final cartAsync = ref.watch(cartProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Your cart', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        onRefresh: () => ref.read(cartProvider.notifier).refresh(),
        color: AppColors.postbookPrimary,
        child: cartAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(
              color: AppColors.postbookPrimary,
            ),
          ),
          error: (e, _) => ListView(
            children: [
              SizedBox(
                height: MediaQuery.of(context).size.height * 0.6,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.xxl),
                    child: Text(
                      'Could not load cart.\n$e',
                      style: AppTextStyles.body,
                      textAlign: TextAlign.center,
                    ),
                  ),
                ),
              ),
            ],
          ),
          data: (cart) {
            if (cart.isEmpty) return _EmptyCart();
            return _Loaded(
              cart: cart,
              couponCtrl: _couponCtrl,
              appliedCoupon: _appliedCoupon,
              onApply: (code) => setState(() => _appliedCoupon = code),
              onClear: () {
                _couponCtrl.clear();
                setState(() => _appliedCoupon = '');
              },
            );
          },
        ),
      ),
      bottomNavigationBar: cartAsync.maybeWhen(
        data: (cart) => cart.isEmpty ? null : _CheckoutBar(cart: cart),
        orElse: () => null,
      ),
    );
  }
}

class _Loaded extends ConsumerWidget {
  const _Loaded({
    required this.cart,
    required this.couponCtrl,
    required this.appliedCoupon,
    required this.onApply,
    required this.onClear,
  });

  final Cart cart;
  final TextEditingController couponCtrl;
  final String appliedCoupon;
  final ValueChanged<String> onApply;
  final VoidCallback onClear;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // previewAsync is null when no coupon has been pressed Apply on.
    // The provider returns an empty/applied=false envelope for empty
    // input so we can render a stable empty state without branching.
    final preview = appliedCoupon.isEmpty
        ? null
        : ref.watch(couponPreviewProvider(appliedCoupon));
    return ListView(
      padding: const EdgeInsets.all(AppSpacing.l),
      children: [
        for (final item in cart.items)
          Padding(
            padding: const EdgeInsets.only(bottom: AppSpacing.m),
            child: _CartItemRow(item: item),
          ),
        const SizedBox(height: AppSpacing.l),
        _CouponBlock(
          controller: couponCtrl,
          applied: cart.appliedCouponCode,
          appliedCoupon: appliedCoupon,
          preview: preview,
          onApply: onApply,
          onClear: onClear,
        ),
        const SizedBox(height: AppSpacing.xxl),
        _TotalsBlock(cart: cart, preview: preview?.valueOrNull),
        const SizedBox(height: AppSpacing.xxl),
      ],
    );
  }
}

class _CartItemRow extends ConsumerWidget {
  const _CartItemRow({required this.item});

  final CartItem item;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
            child: SizedBox(
              width: 72,
              height: 72,
              child: item.productSnapshot.primaryImageUrl == null
                  ? Container(
                      color: AppColors.bgSecondary,
                      child: const Icon(Icons.image_outlined,
                          color: AppColors.textGhost),
                    )
                  : Image.network(
                      item.productSnapshot.primaryImageUrl!,
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) => Container(
                        color: AppColors.bgSecondary,
                        child: const Icon(Icons.broken_image_outlined,
                            color: AppColors.textGhost),
                      ),
                    ),
            ),
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  item.productSnapshot.title,
                  style: AppTextStyles.label,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
                if (item.productSnapshot.variantLabel != null) ...[
                  const SizedBox(height: 2),
                  Text(
                    item.productSnapshot.variantLabel!,
                    style: AppTextStyles.bodySmall,
                  ),
                ],
                const SizedBox(height: AppSpacing.s),
                Text(
                  'Rs. ${item.unitPrice.toStringAsFixed(0)}',
                  style: AppTextStyles.h3,
                ),
                const SizedBox(height: AppSpacing.s),
                Row(
                  children: [
                    _QtyStepper(
                      qty: item.qty,
                      onDelta: (delta) async {
                        final newQty = item.qty + delta;
                        await ref
                            .read(cartProvider.notifier)
                            .updateItem(item.variantId, newQty,
                                productId: item.productId);
                      },
                    ),
                    const Spacer(),
                    IconButton(
                      tooltip: 'Remove',
                      icon: const Icon(Icons.close,
                          color: AppColors.textTertiary),
                      onPressed: () => ref
                          .read(cartProvider.notifier)
                          .removeItem(item.variantId),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _QtyStepper extends StatelessWidget {
  const _QtyStepper({required this.qty, required this.onDelta});

  final int qty;
  final void Function(int delta) onDelta;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            icon: const Icon(Icons.remove, size: 16),
            onPressed: qty <= 1 ? null : () => onDelta(-1),
            color: AppColors.textPrimary,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            padding: EdgeInsets.zero,
          ),
          SizedBox(
            width: 24,
            child: Text(
              '$qty',
              textAlign: TextAlign.center,
              style: AppTextStyles.label,
            ),
          ),
          IconButton(
            icon: const Icon(Icons.add, size: 16),
            onPressed: () => onDelta(1),
            color: AppColors.textPrimary,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            padding: EdgeInsets.zero,
          ),
        ],
      ),
    );
  }
}

class _CouponBlock extends StatelessWidget {
  const _CouponBlock({
    required this.controller,
    required this.applied,
    required this.appliedCoupon,
    required this.preview,
    required this.onApply,
    required this.onClear,
  });

  final TextEditingController controller;
  final String? applied;
  final String appliedCoupon;
  final AsyncValue<CouponPreview>? preview;
  final ValueChanged<String> onApply;
  final VoidCallback onClear;

  @override
  Widget build(BuildContext context) {
    final hasApplied = appliedCoupon.isNotEmpty;
    final loading = preview?.isLoading ?? false;
    final hasError = preview?.hasError ?? false;
    final data = preview?.valueOrNull;
    final accepted = data?.applied ?? false;

    String? message;
    Color? messageColor;
    if (hasError) {
      message = 'Coupon could not be applied.';
      messageColor = const Color(0xFFB91C1C);
    } else if (data != null && accepted) {
      message =
          'Coupon ${data.couponCode} applied — you save Rs. ${data.couponDiscount.toStringAsFixed(0)}.';
      messageColor = const Color(0xFF047857);
    } else if (data != null && !accepted && hasApplied) {
      message = "This coupon doesn't apply to your current cart.";
      messageColor = const Color(0xFF92400E);
    }

    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const Icon(Icons.local_offer_outlined,
                  color: AppColors.textTertiary),
              const SizedBox(width: AppSpacing.m),
              Expanded(
                child: TextField(
                  controller: controller,
                  style: AppTextStyles.body,
                  textCapitalization: TextCapitalization.characters,
                  onSubmitted: (v) => onApply(v.trim()),
                  decoration: InputDecoration(
                    hintText: applied ?? 'Coupon code',
                    hintStyle: AppTextStyles.body.copyWith(
                      color: AppColors.textMuted,
                    ),
                    border: InputBorder.none,
                    isDense: true,
                  ),
                ),
              ),
              if (hasApplied && controller.text.trim() == appliedCoupon)
                TextButton(
                  onPressed: onClear,
                  child: const Text('Clear'),
                )
              else
                TextButton(
                  onPressed: loading
                      ? null
                      : () => onApply(controller.text.trim()),
                  child: loading
                      ? const SizedBox(
                          width: 14,
                          height: 14,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Text('Apply'),
                ),
            ],
          ),
          if (message != null) ...[
            const SizedBox(height: AppSpacing.s),
            Text(
              message,
              style: AppTextStyles.bodySmall.copyWith(color: messageColor),
            ),
          ],
        ],
      ),
    );
  }
}

class _TotalsBlock extends StatelessWidget {
  const _TotalsBlock({required this.cart, this.preview});

  final Cart cart;
  final CouponPreview? preview;

  @override
  Widget build(BuildContext context) {
    final extraDiscount = (preview?.applied ?? false)
        ? preview!.couponDiscount
        : 0.0;
    final grandTotal = (preview?.applied ?? false)
        ? preview!.grandTotal
        : cart.grandTotal;
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          _TotalsRow(label: 'Subtotal', value: cart.subtotal),
          _TotalsRow(label: 'Tax (GST)', value: cart.taxTotal),
          _TotalsRow(label: 'Shipping', value: cart.shippingTotal),
          if (cart.discountTotal > 0)
            _TotalsRow(label: 'Discount', value: -cart.discountTotal),
          if (extraDiscount > 0)
            _TotalsRow(
              label: 'Coupon (${preview!.couponCode})',
              value: -extraDiscount,
            ),
          const Divider(height: AppSpacing.xxl, color: AppColors.borderSubtle),
          _TotalsRow(label: 'Total', value: grandTotal, bold: true),
        ],
      ),
    );
  }
}

class _TotalsRow extends StatelessWidget {
  const _TotalsRow({
    required this.label,
    required this.value,
    this.bold = false,
  });

  final String label;
  final double value;
  final bool bold;

  @override
  Widget build(BuildContext context) {
    final style = bold ? AppTextStyles.h3 : AppTextStyles.body;
    final amount = 'Rs. ${value.toStringAsFixed(0)}';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Text(label, style: style),
          Text(amount, style: style),
        ],
      ),
    );
  }
}

class _CheckoutBar extends StatelessWidget {
  const _CheckoutBar({required this.cart});

  final Cart cart;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: const BoxDecoration(
          color: AppColors.bgPrimary,
          border: Border(top: BorderSide(color: AppColors.borderSubtle)),
        ),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'Total',
                    style: AppTextStyles.bodySmall,
                  ),
                  Text(
                    'Rs. ${cart.grandTotal.toStringAsFixed(0)}',
                    style: AppTextStyles.h2,
                  ),
                ],
              ),
            ),
            ElevatedButton(
              onPressed: () =>
                  GoRouter.of(context).push('/commerce/checkout'),
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                padding: const EdgeInsets.symmetric(
                    horizontal: AppSpacing.xxl, vertical: 14),
              ),
              child: const Text('Proceed to checkout'),
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyCart extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return ListView(
      children: [
        SizedBox(
          height: MediaQuery.of(context).size.height * 0.7,
          child: Center(
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(Icons.shopping_cart_outlined,
                    size: 56, color: AppColors.textGhost),
                const SizedBox(height: AppSpacing.l),
                Text(
                  'Your cart is empty',
                  style: AppTextStyles.h2,
                ),
                const SizedBox(height: AppSpacing.s),
                Text(
                  'Browse products to start shopping',
                  style: AppTextStyles.body,
                ),
                const SizedBox(height: AppSpacing.xxl),
                ElevatedButton(
                  onPressed: () => GoRouter.of(context).go('/commerce'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                  ),
                  child: const Text('Browse products'),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}
