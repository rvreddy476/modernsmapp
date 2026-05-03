// Checkout — Sprint 1.
//
// Flow:
//   1. Show selected address (default; tap "Change" → address-book picker).
//   2. Read-only cart review.
//   3. Payment-method picker (UPI highlighted; COD only when allowed).
//   4. Place order → repository.placeOrder → Razorpay stub on prepaid →
//      confirmOrderPayment → success screen.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:atpost_app/services/commerce_telemetry.dart';
import 'package:atpost_app/services/razorpay_commerce_stub.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CheckoutScreen extends ConsumerStatefulWidget {
  const CheckoutScreen({super.key});

  @override
  ConsumerState<CheckoutScreen> createState() => _CheckoutScreenState();
}

class _CheckoutScreenState extends ConsumerState<CheckoutScreen> {
  Address? _address;
  String _paymentMethod = 'upi';
  bool _placing = false;

  static const _methods = <_PaymentOption>[
    _PaymentOption(
      id: 'upi',
      label: 'UPI',
      subtitle: 'GPay / PhonePe / BHIM',
      icon: Icons.account_balance_wallet_outlined,
      recommended: true,
    ),
    _PaymentOption(
      id: 'card',
      label: 'Cards',
      subtitle: 'Credit / debit',
      icon: Icons.credit_card_outlined,
    ),
    _PaymentOption(
      id: 'netbanking',
      label: 'Net banking',
      subtitle: 'All major banks',
      icon: Icons.account_balance_outlined,
    ),
    _PaymentOption(
      id: 'wallet',
      label: 'Wallets',
      subtitle: 'Paytm / Mobikwik / Amazon Pay',
      icon: Icons.wallet_outlined,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    final cartAsync = ref.watch(cartProvider);
    final addrsAsync = ref.watch(addressesProvider);
    _address ??= ref.watch(defaultAddressProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Checkout', style: AppTextStyles.h2),
      ),
      body: cartAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text('Could not load checkout.\n$e',
                style: AppTextStyles.body, textAlign: TextAlign.center),
          ),
        ),
        data: (cart) {
          if (cart.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(AppSpacing.xxl),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text('Your cart is empty', style: AppTextStyles.h2),
                    const SizedBox(height: AppSpacing.s),
                    Text('Add items to checkout', style: AppTextStyles.body),
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
            );
          }
          final codAllowed = cart.grandTotal <= 5000;
          return ListView(
            padding: const EdgeInsets.all(AppSpacing.l),
            children: [
              _AddressCard(
                address: _address,
                addressesLoading: addrsAsync.isLoading,
                onChange: () async {
                  final picked = await GoRouter.of(context).push<String?>(
                    '/commerce/addresses?picker=1',
                  );
                  if (picked == null) return;
                  final list = ref.read(addressesProvider).asData?.value ??
                      const <Address>[];
                  for (final a in list) {
                    if (a.id == picked) {
                      setState(() => _address = a);
                      return;
                    }
                  }
                },
              ),
              const SizedBox(height: AppSpacing.xxl),
              Text('Items', style: AppTextStyles.h3),
              const SizedBox(height: AppSpacing.s),
              for (final item in cart.items) _ReviewItemTile(item: item),
              const SizedBox(height: AppSpacing.xxl),
              Text('Payment method', style: AppTextStyles.h3),
              const SizedBox(height: AppSpacing.s),
              for (final m in _methods)
                _MethodTile(
                  option: m,
                  selected: _paymentMethod == m.id,
                  onSelect: () => setState(() => _paymentMethod = m.id),
                ),
              if (codAllowed)
                _MethodTile(
                  option: const _PaymentOption(
                    id: 'cod',
                    label: 'Cash on delivery',
                    subtitle: 'Pay when you receive',
                    icon: Icons.payments_outlined,
                  ),
                  selected: _paymentMethod == 'cod',
                  onSelect: () => setState(() => _paymentMethod = 'cod'),
                )
              else
                Padding(
                  padding: const EdgeInsets.symmetric(
                      vertical: AppSpacing.s, horizontal: AppSpacing.s),
                  child: Text(
                    'Cash on delivery is unavailable for orders over ₹5,000.',
                    style: AppTextStyles.bodySmall,
                  ),
                ),
              const SizedBox(height: AppSpacing.xxl),
              _SummaryBlock(cart: cart),
            ],
          );
        },
      ),
      bottomNavigationBar: cartAsync.maybeWhen(
        data: (cart) => cart.isEmpty
            ? null
            : SafeArea(
                child: Container(
                  padding: const EdgeInsets.all(AppSpacing.l),
                  decoration: const BoxDecoration(
                    color: AppColors.bgPrimary,
                    border: Border(
                        top: BorderSide(color: AppColors.borderSubtle)),
                  ),
                  child: ElevatedButton(
                    onPressed: _placing || _address == null
                        ? null
                        : () => _placeOrder(cart),
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppColors.postbookPrimary,
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                    child: Text(
                      _placing
                          ? 'Placing order…'
                          : 'Place order · Rs. ${cart.grandTotal.toStringAsFixed(0)}',
                    ),
                  ),
                ),
              ),
        orElse: () => null,
      ),
    );
  }

  Future<void> _placeOrder(Cart cart) async {
    final addr = _address;
    if (addr == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Please select a delivery address')),
      );
      return;
    }
    setState(() => _placing = true);
    final repo = ref.read(commerceRepositoryProvider);
    final telemetry = ref.read(commerceTelemetryProvider);
    telemetry.checkoutStarted(grandTotal: cart.grandTotal);
    try {
      final order = await repo.placeOrder(
        addressId: addr.id,
        paymentMethod: _paymentMethod,
        idempotencyKey:
            'mobile_${DateTime.now().millisecondsSinceEpoch}_${addr.id}',
      );

      // Prepaid → open Razorpay stub. COD → straight to success.
      if (_paymentMethod != 'cod') {
        if (!mounted) return;
        final result = await RazorpayCommerceStub.open(
          context,
          args: CommerceStubArgs(
            orderId: order.id,
            amountInPaise: (order.amountGrand * 100).round(),
          ),
        );
        if (!result.confirmed) {
          if (!mounted) return;
          setState(() => _placing = false);
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text('Payment cancelled (${result.failureReason})'),
            ),
          );
          return;
        }
        await repo.confirmOrderPayment(
          order.id,
          razorpayOrderId: result.razorpayOrderId ?? '',
          razorpayPaymentId: result.razorpayPaymentId ?? '',
          razorpaySignature: result.razorpaySignature ?? '',
        );
      }

      telemetry.orderPlaced(
        orderId: order.id,
        paymentMethod: _paymentMethod,
      );
      ref.invalidate(cartProvider);
      if (!mounted) return;
      GoRouter.of(context).pushReplacement(
        '/commerce/orders/${order.id}?placed=1',
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not place order: $e')),
      );
    } finally {
      if (mounted) setState(() => _placing = false);
    }
  }
}

class _AddressCard extends StatelessWidget {
  const _AddressCard({
    required this.address,
    required this.addressesLoading,
    required this.onChange,
  });

  final Address? address;
  final bool addressesLoading;
  final VoidCallback onChange;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onChange,
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            const Icon(Icons.location_on_outlined,
                color: AppColors.postbookPrimary),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: address == null
                  ? Text(
                      addressesLoading
                          ? 'Loading addresses…'
                          : 'Add a delivery address',
                      style: AppTextStyles.body,
                    )
                  : Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '${address!.label} · ${address!.fullName}',
                          style: AppTextStyles.label,
                        ),
                        const SizedBox(height: 2),
                        Text(
                          [
                            address!.line1,
                            address!.city,
                            '${address!.state} ${address!.postalCode}',
                          ].join(', '),
                          style: AppTextStyles.bodySmall,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ],
                    ),
            ),
            const Icon(Icons.chevron_right, color: AppColors.textTertiary),
          ],
        ),
      ),
    );
  }
}

class _ReviewItemTile extends StatelessWidget {
  const _ReviewItemTile({required this.item});

  final CartItem item;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.s),
      child: Row(
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
            child: SizedBox(
              width: 48,
              height: 48,
              child: item.productSnapshot.primaryImageUrl == null
                  ? Container(
                      color: AppColors.bgSecondary,
                      child: const Icon(Icons.image_outlined,
                          size: 18, color: AppColors.textGhost),
                    )
                  : Image.network(
                      item.productSnapshot.primaryImageUrl!,
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) => Container(
                        color: AppColors.bgSecondary,
                        child: const Icon(Icons.broken_image_outlined,
                            size: 18, color: AppColors.textGhost),
                      ),
                    ),
            ),
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(item.productSnapshot.title,
                    style: AppTextStyles.label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis),
                Text('Qty ${item.qty}', style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          Text('Rs. ${item.lineTotal.toStringAsFixed(0)}',
              style: AppTextStyles.label),
        ],
      ),
    );
  }
}

class _PaymentOption {
  const _PaymentOption({
    required this.id,
    required this.label,
    required this.subtitle,
    required this.icon,
    this.recommended = false,
  });

  final String id;
  final String label;
  final String subtitle;
  final IconData icon;
  final bool recommended;
}

class _MethodTile extends StatelessWidget {
  const _MethodTile({
    required this.option,
    required this.selected,
    required this.onSelect,
  });

  final _PaymentOption option;
  final bool selected;
  final VoidCallback onSelect;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onSelect,
      borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 4),
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Row(
          children: [
            Icon(option.icon,
                color: selected
                    ? AppColors.postbookPrimary
                    : AppColors.textTertiary),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Text(option.label, style: AppTextStyles.label),
                      if (option.recommended) ...[
                        const SizedBox(width: AppSpacing.s),
                        Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 6, vertical: 1),
                          decoration: BoxDecoration(
                            color: AppColors.statusSuccess,
                            borderRadius: BorderRadius.circular(4),
                          ),
                          child: Text(
                            'Recommended',
                            style: AppTextStyles.labelTiny.copyWith(
                              color: Colors.white,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 2),
                  Text(option.subtitle, style: AppTextStyles.bodySmall),
                ],
              ),
            ),
            Radio<bool>(
              value: true,
              groupValue: selected,
              onChanged: (_) => onSelect(),
              activeColor: AppColors.postbookPrimary,
            ),
          ],
        ),
      ),
    );
  }
}

class _SummaryBlock extends StatelessWidget {
  const _SummaryBlock({required this.cart});

  final Cart cart;

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
        children: [
          _row('Subtotal', cart.subtotal),
          _row('Tax (GST)', cart.taxTotal),
          _row('Shipping', cart.shippingTotal),
          if (cart.discountTotal > 0) _row('Discount', -cart.discountTotal),
          const Divider(height: AppSpacing.xxl, color: AppColors.borderSubtle),
          _row('Total', cart.grandTotal, bold: true),
        ],
      ),
    );
  }

  Widget _row(String label, double value, {bool bold = false}) {
    final style = bold ? AppTextStyles.h3 : AppTextStyles.body;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Text(label, style: style),
          Text('Rs. ${value.toStringAsFixed(0)}', style: style),
        ],
      ),
    );
  }
}
