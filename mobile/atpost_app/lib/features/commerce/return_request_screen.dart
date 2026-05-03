// Return request flow — Sprint 2.
//
// Multi-step:
//   1. Pick items + qty.
//   2. Pick reason (radio list of `ReturnReason`).
//   3. Pick pickup address (default = original shipping address; user can
//      change to any saved address).
//   4. Review + submit. On success → confirmation snackbar +
//      `pushReplacement` to /commerce/returns/:id.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:atpost_app/services/commerce_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ReturnRequestScreen extends ConsumerStatefulWidget {
  const ReturnRequestScreen({super.key, required this.orderId});

  final String orderId;

  @override
  ConsumerState<ReturnRequestScreen> createState() =>
      _ReturnRequestScreenState();
}

class _ReturnRequestScreenState extends ConsumerState<ReturnRequestScreen> {
  int _step = 0;

  // Step 1 state — qty per order item id (0 = not selected).
  final Map<String, int> _qtyByItem = {};

  // Step 2.
  ReturnReason _reason = ReturnReason.defective;
  final TextEditingController _otherCtrl = TextEditingController();

  // Step 3.
  String? _pickupAddressId;

  bool _busy = false;

  @override
  void dispose() {
    _otherCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final orderAsync = ref.watch(orderDetailProvider(widget.orderId));
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Return request', style: AppTextStyles.h2),
      ),
      body: orderAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text('Could not load order.\n$e',
                style: AppTextStyles.body, textAlign: TextAlign.center),
          ),
        ),
        data: (order) {
          // Seed pickup address from the order's shipping address once.
          _pickupAddressId ??= order.shippingAddress?.id;
          return _StepContent(
            order: order,
            step: _step,
            qtyByItem: _qtyByItem,
            reason: _reason,
            otherCtrl: _otherCtrl,
            pickupAddressId: _pickupAddressId,
            busy: _busy,
            onAdjustQty: (itemId, delta, max) {
              setState(() {
                final current = _qtyByItem[itemId] ?? 0;
                final next = (current + delta).clamp(0, max);
                if (next == 0) {
                  _qtyByItem.remove(itemId);
                } else {
                  _qtyByItem[itemId] = next;
                }
              });
            },
            onPickReason: (r) => setState(() => _reason = r),
            onPickAddress: (id) => setState(() => _pickupAddressId = id),
            onNext: _onNext,
            onBack: () => setState(() => _step = (_step - 1).clamp(0, 3)),
            onSubmit: () => _submit(order),
          );
        },
      ),
    );
  }

  void _onNext() {
    if (_step == 0 && _qtyByItem.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Pick at least one item to return')),
      );
      return;
    }
    if (_step == 1 &&
        _reason == ReturnReason.other &&
        _otherCtrl.text.trim().isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Tell us a bit more about the issue')),
      );
      return;
    }
    if (_step == 2 && (_pickupAddressId == null || _pickupAddressId!.isEmpty)) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Pick a pickup address')),
      );
      return;
    }
    setState(() => _step = (_step + 1).clamp(0, 3));
  }

  Future<void> _submit(Order order) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      // Build the items list. Find seller id from the first matching order
      // item; commerce-service requires it on the wire.
      final items = <ReturnItem>[];
      String? sellerId;
      for (final entry in _qtyByItem.entries) {
        final orderItem = order.items.firstWhere(
          (oi) => oi.id == entry.key,
          orElse: () => order.items.first,
        );
        sellerId ??= orderItem.sellerId;
        items.add(ReturnItem(
          orderItemId: entry.key,
          qty: entry.value,
          reason: _reason,
        ));
      }
      if (sellerId == null || sellerId.isEmpty) {
        throw StateError('Seller id missing on order items');
      }
      final ret = await ref.read(commerceRepositoryProvider).requestReturn(
            orderId: order.id,
            items: items,
            pickupAddressId: _pickupAddressId!,
            sellerId: sellerId,
            reasonDescription: _reason == ReturnReason.other
                ? _otherCtrl.text.trim()
                : null,
          );
      ref.read(commerceTelemetryProvider).returnRequested(
            orderId: order.id,
            reason: _reason.wireValue,
          );
      ref.invalidate(myReturnsProvider);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Return requested. We\'ll be in touch.')),
      );
      GoRouter.of(context).pushReplacement('/commerce/returns/${ret.id}');
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not request return: $e')),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }
}

class _StepContent extends ConsumerWidget {
  const _StepContent({
    required this.order,
    required this.step,
    required this.qtyByItem,
    required this.reason,
    required this.otherCtrl,
    required this.pickupAddressId,
    required this.busy,
    required this.onAdjustQty,
    required this.onPickReason,
    required this.onPickAddress,
    required this.onNext,
    required this.onBack,
    required this.onSubmit,
  });

  final Order order;
  final int step;
  final Map<String, int> qtyByItem;
  final ReturnReason reason;
  final TextEditingController otherCtrl;
  final String? pickupAddressId;
  final bool busy;
  final void Function(String itemId, int delta, int max) onAdjustQty;
  final void Function(ReturnReason) onPickReason;
  final void Function(String addressId) onPickAddress;
  final VoidCallback onNext;
  final VoidCallback onBack;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final body = switch (step) {
      0 => _stepItems(),
      1 => _stepReason(),
      2 => _stepPickup(ref),
      _ => _stepReview(ref),
    };
    return Column(
      children: [
        const SizedBox(height: AppSpacing.l),
        _StepDots(current: step, total: 4),
        const SizedBox(height: AppSpacing.l),
        Expanded(child: body),
        SafeArea(
          child: Container(
            padding: const EdgeInsets.fromLTRB(
                AppSpacing.l, AppSpacing.m, AppSpacing.l, AppSpacing.m),
            decoration: const BoxDecoration(
              color: AppColors.bgPrimary,
              border:
                  Border(top: BorderSide(color: AppColors.borderSubtle)),
            ),
            child: Row(
              children: [
                if (step > 0)
                  Expanded(
                    child: OutlinedButton(
                      onPressed: busy ? null : onBack,
                      child: const Text('Back'),
                    ),
                  ),
                if (step > 0) const SizedBox(width: AppSpacing.l),
                Expanded(
                  child: ElevatedButton(
                    onPressed: busy ? null : (step == 3 ? onSubmit : onNext),
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppColors.postbookPrimary,
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                    child: busy
                        ? const SizedBox(
                            height: 18,
                            width: 18,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              color: Colors.white,
                            ),
                          )
                        : Text(step == 3 ? 'Submit' : 'Continue'),
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }

  Widget _stepItems() {
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
      children: [
        Text('Pick items to return', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.l),
        for (final i in order.items)
          Container(
            margin: const EdgeInsets.only(bottom: AppSpacing.l),
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        i.productSnapshot.title,
                        style: AppTextStyles.label.copyWith(
                          color: AppColors.textPrimary,
                        ),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                      Text('Ordered: ${i.qty}',
                          style: AppTextStyles.bodySmall),
                    ],
                  ),
                ),
                _QtyStepper(
                  qty: qtyByItem[i.id] ?? 0,
                  max: i.qty,
                  onChange: (d) => onAdjustQty(i.id, d, i.qty),
                ),
              ],
            ),
          ),
      ],
    );
  }

  Widget _stepReason() {
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
      children: [
        Text('Why are you returning?', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.l),
        for (final r in ReturnReason.values)
          RadioListTile<ReturnReason>(
            value: r,
            groupValue: reason,
            onChanged: (v) {
              if (v != null) onPickReason(v);
            },
            title: Text(r.label, style: AppTextStyles.body),
            activeColor: AppColors.postbookPrimary,
          ),
        if (reason == ReturnReason.other) ...[
          const SizedBox(height: AppSpacing.l),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
            child: TextField(
              controller: otherCtrl,
              maxLines: 4,
              maxLength: 500,
              style: AppTextStyles.body,
              decoration: InputDecoration(
                hintText: 'Tell us more (max 500 chars)',
                hintStyle:
                    AppTextStyles.body.copyWith(color: AppColors.textMuted),
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
            ),
          ),
        ],
      ],
    );
  }

  Widget _stepPickup(WidgetRef ref) {
    final addressesAsync = ref.watch(addressesProvider);
    return addressesAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (e, _) => Center(
        child: Padding(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          child: Text('Could not load addresses.\n$e',
              style: AppTextStyles.body, textAlign: TextAlign.center),
        ),
      ),
      data: (list) {
        if (list.isEmpty) {
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(AppSpacing.xxl),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'You don\'t have any saved addresses yet.',
                    style: AppTextStyles.body,
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: AppSpacing.l),
                  ElevatedButton(
                    onPressed: () =>
                        GoRouter.of(ref.context).push('/commerce/addresses/new'),
                    child: const Text('Add address'),
                  ),
                ],
              ),
            ),
          );
        }
        return ListView(
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
          children: [
            Text('Pickup address', style: AppTextStyles.h3),
            const SizedBox(height: AppSpacing.l),
            for (final a in list)
              RadioListTile<String>(
                value: a.id,
                groupValue: pickupAddressId,
                onChanged: (v) {
                  if (v != null) onPickAddress(v);
                },
                title: Text('${a.label} · ${a.fullName}',
                    style: AppTextStyles.label),
                subtitle: Text(
                  '${a.line1}, ${a.city} ${a.postalCode}',
                  style: AppTextStyles.bodySmall,
                ),
                activeColor: AppColors.postbookPrimary,
              ),
          ],
        );
      },
    );
  }

  Widget _stepReview(WidgetRef ref) {
    final addressesAsync = ref.watch(addressesProvider);
    final addrs = addressesAsync.asData?.value ?? const <Address>[];
    Address? addr;
    for (final a in addrs) {
      if (a.id == pickupAddressId) {
        addr = a;
        break;
      }
    }
    return ListView(
      padding: const EdgeInsets.all(AppSpacing.l),
      children: [
        Text('Review your return', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.l),
        _Card(
          children: [
            Text('Items', style: AppTextStyles.label),
            const SizedBox(height: AppSpacing.s),
            for (final entry in qtyByItem.entries)
              _itemSummaryRow(order, entry.key, entry.value),
          ],
        ),
        const SizedBox(height: AppSpacing.l),
        _Card(
          children: [
            Text('Reason', style: AppTextStyles.label),
            const SizedBox(height: AppSpacing.s),
            Text(reason.label, style: AppTextStyles.body),
            if (reason == ReturnReason.other &&
                otherCtrl.text.trim().isNotEmpty) ...[
              const SizedBox(height: AppSpacing.s),
              Text(otherCtrl.text.trim(),
                  style: AppTextStyles.bodySmall),
            ],
          ],
        ),
        const SizedBox(height: AppSpacing.l),
        if (addr != null)
          _Card(
            children: [
              Text('Pickup', style: AppTextStyles.label),
              const SizedBox(height: AppSpacing.s),
              Text('${addr.fullName} · ${addr.phone}',
                  style: AppTextStyles.body),
              Text(
                '${addr.line1}, ${addr.city} ${addr.postalCode}',
                style: AppTextStyles.bodySmall,
              ),
            ],
          ),
      ],
    );
  }

  Widget _itemSummaryRow(Order order, String itemId, int qty) {
    final item = order.items.firstWhere(
      (i) => i.id == itemId,
      orElse: () => order.items.first,
    );
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Expanded(
            child: Text('${item.productSnapshot.title} × $qty',
                style: AppTextStyles.body, overflow: TextOverflow.ellipsis),
          ),
          Text(
            'Rs. ${(item.unitPrice * qty).toStringAsFixed(0)}',
            style: AppTextStyles.label,
          ),
        ],
      ),
    );
  }
}

class _StepDots extends StatelessWidget {
  const _StepDots({required this.current, required this.total});

  final int current;
  final int total;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: List.generate(total, (i) {
        final on = i <= current;
        return AnimatedContainer(
          duration: const Duration(milliseconds: 180),
          margin: const EdgeInsets.symmetric(horizontal: 3),
          width: on ? 22 : 8,
          height: 4,
          decoration: BoxDecoration(
            color: on
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
            borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          ),
        );
      }),
    );
  }
}

class _QtyStepper extends StatelessWidget {
  const _QtyStepper({
    required this.qty,
    required this.max,
    required this.onChange,
  });

  final int qty;
  final int max;
  final void Function(int delta) onChange;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        IconButton(
          icon: const Icon(Icons.remove_circle_outline,
              color: AppColors.textSecondary),
          onPressed: qty > 0 ? () => onChange(-1) : null,
        ),
        Text('$qty', style: AppTextStyles.label),
        IconButton(
          icon: const Icon(Icons.add_circle_outline,
              color: AppColors.textSecondary),
          onPressed: qty < max ? () => onChange(1) : null,
        ),
      ],
    );
  }
}

class _Card extends StatelessWidget {
  const _Card({required this.children});

  final List<Widget> children;

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
        crossAxisAlignment: CrossAxisAlignment.start,
        children: children,
      ),
    );
  }
}
