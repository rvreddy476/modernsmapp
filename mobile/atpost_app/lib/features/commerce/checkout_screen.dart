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
import 'package:atpost_app/data/models/b2b.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/b2b_repository.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:atpost_app/services/commerce_telemetry.dart';
import 'package:atpost_app/services/razorpay_commerce.dart';
import 'package:atpost_app/services/razorpay_commerce_stub.dart';
import 'package:flutter/foundation.dart' show kDebugMode;
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

  // Phase F4 mobile — B2B context. Null _selectedOrg means a retail
  // checkout; selecting an org unlocks PO / cost-center / invoice-
  // email fields + the "Pay on invoice (Net N)" payment method when
  // the org has credit_terms_days > 0.
  Organization? _selectedOrg;
  final _poController = TextEditingController();
  final _costCenterController = TextEditingController();
  final _invoiceEmailController = TextEditingController();

  @override
  void dispose() {
    _poController.dispose();
    _costCenterController.dispose();
    _invoiceEmailController.dispose();
    super.dispose();
  }

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
              // Phase F4 mobile — B2B context. Only renders when the
              // buyer belongs to at least one organization; pure retail
              // buyers see the same checkout they always have.
              const SizedBox(height: AppSpacing.xxl),
              _B2BContextCard(
                selected: _selectedOrg,
                poController: _poController,
                costCenterController: _costCenterController,
                invoiceEmailController: _invoiceEmailController,
                onOrgChanged: (org) {
                  setState(() {
                    _selectedOrg = org;
                    // If the buyer switches away from a credit-eligible
                    // org, fall the payment method back to UPI.
                    if (_paymentMethod == 'credit' &&
                        (org == null || !org.hasCreditTerms)) {
                      _paymentMethod = 'upi';
                    }
                  });
                },
                cartGrandTotal: cart.grandTotal,
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
              // Phase F4 mobile — Net-N invoice payment surfaces only
              // when the buyer has picked an org with credit terms
              // configured. Backend confirms the order immediately
              // and stamps payment_due_date.
              if (_selectedOrg?.hasCreditTerms ?? false)
                _MethodTile(
                  option: _PaymentOption(
                    id: 'credit',
                    label: 'Pay on invoice',
                    subtitle:
                        'Net ${_selectedOrg!.creditTermsDays} days — billed to ${_selectedOrg!.name}',
                    icon: Icons.receipt_long_outlined,
                  ),
                  selected: _paymentMethod == 'credit',
                  onSelect: () => setState(() => _paymentMethod = 'credit'),
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
        // Phase F4 — B2B context is optional. The org selector only
        // renders when the buyer belongs to ≥1 org, so retail
        // checkouts pass nulls and the backend treats the order as
        // pure B2C.
        organizationId: _selectedOrg?.id,
        poNumber: _poController.text.trim().isEmpty
            ? null
            : _poController.text.trim(),
        costCenter: _costCenterController.text.trim().isEmpty
            ? null
            : _costCenterController.text.trim(),
        invoiceEmail: _invoiceEmailController.text.trim().isEmpty
            ? null
            : _invoiceEmailController.text.trim(),
      );

      // Phase F4 — B2B-aware short-circuits:
      //   * awaiting_approval: order is parked until an org approver
      //     signs off. No gateway call; the seller's approver is
      //     notified server-side. Send the buyer to the order page
      //     where they can see the status pill.
      //   * credit: backend already confirmed the order with a
      //     payment_due_date; invoice will be paid Net-N. No gateway.
      if (order.awaitingApproval) {
        ref.invalidate(cartProvider);
        if (!mounted) return;
        GoRouter.of(context).pushReplacement(
          '/commerce/orders/${order.id}?placed=1',
        );
        return;
      }
      if (_paymentMethod == 'credit') {
        ref.invalidate(cartProvider);
        if (!mounted) return;
        GoRouter.of(context).pushReplacement(
          '/commerce/orders/${order.id}?placed=1',
        );
        return;
      }

      // Prepaid → real Razorpay (or the stub if dev + opted in). COD
      // skips the gateway. Phase 1.4: replaces the stub-only flow with
      // payments-service intent + razorpay_flutter SDK + the secure
      // confirm body the Phase-0.1 backend requires.
      if (_paymentMethod != 'cod') {
        final amountMinor = (order.amountGrand * 100).round();
        final intent = await repo.createPaymentIntent(
          orderId: order.id,
          amount: order.amountGrand,
        );
        if (intent.providerRef.isEmpty) {
          if (!mounted) return;
          setState(() => _placing = false);
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              content: Text('Payment unavailable: gateway not configured'),
            ),
          );
          return;
        }

        if (!mounted) return;
        final stubArgs = CommerceStubArgs(
          orderId: order.id,
          amountInPaise: amountMinor,
          razorpayOrderId: intent.providerRef,
        );

        // Release builds always use the real SDK. Debug builds opt into
        // the stub with --dart-define=ENABLE_STUB_PAYMENTS=true so QA can
        // exercise the flow without Razorpay's hosted sheet.
        const useStub = kDebugMode &&
            String.fromEnvironment('ENABLE_STUB_PAYMENTS') == 'true';
        CommerceStubResult result;
        if (useStub) {
          result = await RazorpayCommerceStub.open(context, args: stubArgs);
        } else {
          const keyId = String.fromEnvironment('RAZORPAY_KEY_ID');
          result = await RazorpayCommerce.open(
            context,
            args: stubArgs,
            razorpayKeyId: keyId,
          );
        }

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
          paymentIntentId: intent.intentId,
          razorpayOrderId: result.razorpayOrderId ?? intent.providerRef,
          razorpayPaymentId: result.razorpayPaymentId ?? '',
          razorpaySignature: result.razorpaySignature ?? '',
          amountMinor: amountMinor,
          gateway: useStub ? 'stub' : 'razorpay',
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

// Phase F4 mobile — Buying-for-org context. Hides itself entirely when
// the user belongs to no organizations (the most common case — pure
// B2C buyers should never see this card).
class _B2BContextCard extends ConsumerWidget {
  const _B2BContextCard({
    required this.selected,
    required this.poController,
    required this.costCenterController,
    required this.invoiceEmailController,
    required this.onOrgChanged,
    required this.cartGrandTotal,
  });

  final Organization? selected;
  final TextEditingController poController;
  final TextEditingController costCenterController;
  final TextEditingController invoiceEmailController;
  final ValueChanged<Organization?> onOrgChanged;
  final double cartGrandTotal;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final orgsAsync = ref.watch(myOrganizationsProvider);
    return orgsAsync.when(
      // Network blip = render nothing rather than block checkout.
      // Buyer can still complete a retail checkout while we retry in
      // the background.
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (orgs) {
        if (orgs.isEmpty) return const SizedBox.shrink();
        // Approval threshold warning is computed against the live cart
        // total — switching orgs while editing the cart re-evaluates.
        final needsApproval = selected != null &&
            selected!.approvalThreshold != null &&
            cartGrandTotal >= selected!.approvalThreshold!;
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
              Text('Buying for', style: AppTextStyles.h3),
              const SizedBox(height: AppSpacing.xs),
              Text(
                'Bill the company, attach a PO / cost center, or pay on invoice.',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: AppSpacing.s),
              DropdownButtonFormField<String>(
                initialValue: selected?.id ?? '',
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
                items: [
                  const DropdownMenuItem(
                    value: '',
                    child: Text('Personal (no organization)'),
                  ),
                  for (final o in orgs)
                    DropdownMenuItem(
                      value: o.id,
                      child: Text(
                        o.gstin != null && o.gstin!.isNotEmpty
                            ? '${o.name} · GSTIN ${o.gstin}'
                            : o.name,
                      ),
                    ),
                ],
                onChanged: (id) {
                  if (id == null || id.isEmpty) {
                    onOrgChanged(null);
                    return;
                  }
                  for (final o in orgs) {
                    if (o.id == id) {
                      onOrgChanged(o);
                      return;
                    }
                  }
                },
              ),
              if (selected != null) ...[
                const SizedBox(height: AppSpacing.l),
                _B2BField(
                  controller: poController,
                  label: 'PO Number',
                  hint: 'Purchase order ref',
                ),
                const SizedBox(height: AppSpacing.s),
                _B2BField(
                  controller: costCenterController,
                  label: 'Cost Center',
                  hint: 'Department or project',
                ),
                const SizedBox(height: AppSpacing.s),
                _B2BField(
                  controller: invoiceEmailController,
                  label: 'Invoice Email',
                  hint: selected!.billingEmail ?? 'finance@company.com',
                  keyboardType: TextInputType.emailAddress,
                ),
                if (needsApproval) ...[
                  const SizedBox(height: AppSpacing.s),
                  Container(
                    padding: const EdgeInsets.all(AppSpacing.s),
                    decoration: BoxDecoration(
                      color: Colors.amber.withValues(alpha: 0.15),
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusSmall),
                    ),
                    child: Text(
                      '⚠ Orders ≥ ₹${selected!.approvalThreshold!.toStringAsFixed(0)} require an approver sign-off before payment.',
                      style: AppTextStyles.bodySmall,
                    ),
                  ),
                ],
                if (selected!.hasCreditTerms) ...[
                  const SizedBox(height: AppSpacing.s),
                  Text(
                    'Credit terms: Net ${selected!.creditTermsDays} days available.',
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                ],
              ],
            ],
          ),
        );
      },
    );
  }
}

class _B2BField extends StatelessWidget {
  const _B2BField({
    required this.controller,
    required this.label,
    this.hint,
    this.keyboardType,
  });

  final TextEditingController controller;
  final String label;
  final String? hint;
  final TextInputType? keyboardType;

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: controller,
      keyboardType: keyboardType,
      decoration: InputDecoration(
        labelText: label,
        hintText: hint,
        border: const OutlineInputBorder(),
        isDense: true,
      ),
    );
  }
}
