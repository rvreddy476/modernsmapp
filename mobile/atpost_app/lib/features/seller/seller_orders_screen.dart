// Seller orders / fulfillment queue. Read-only on mobile for this
// slice — actual shipment booking + status updates stay on web until
// a fulfillment-actions screen ships. Stage filter chips switch the
// active provider family member so each tab caches independently.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

const _stages = <(String, String)>[
  ('all', 'All'),
  ('unshipped', 'Unshipped'),
  ('in_transit', 'In transit'),
  ('delivered', 'Delivered'),
  ('cancelled', 'Cancelled'),
];

class SellerOrdersScreen extends ConsumerStatefulWidget {
  const SellerOrdersScreen({super.key});

  @override
  ConsumerState<SellerOrdersScreen> createState() => _SellerOrdersScreenState();
}

class _SellerOrdersScreenState extends ConsumerState<SellerOrdersScreen> {
  String _stage = 'all';

  @override
  Widget build(BuildContext context) {
    final ordersAsync = ref.watch(sellerOrdersProvider(_stage));
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Orders', style: AppTextStyles.h2),
      ),
      body: Column(
        children: [
          SizedBox(
            height: 44,
            child: ListView.separated(
              padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
              scrollDirection: Axis.horizontal,
              itemCount: _stages.length,
              separatorBuilder: (_, _) => const SizedBox(width: AppSpacing.s),
              itemBuilder: (_, i) {
                final (key, label) = _stages[i];
                final active = key == _stage;
                return ChoiceChip(
                  label: Text(label),
                  selected: active,
                  onSelected: (_) => setState(() => _stage = key),
                  selectedColor: AppColors.postbookPrimary,
                  backgroundColor: AppColors.bgCard,
                  labelStyle: AppTextStyles.label.copyWith(
                    color: active ? Colors.white : AppColors.textPrimary,
                    fontWeight: FontWeight.w700,
                  ),
                  side: BorderSide(color: AppColors.borderSubtle),
                );
              },
            ),
          ),
          Expanded(
            child: RefreshIndicator(
              color: AppColors.postbookPrimary,
              onRefresh: () async {
                ref.invalidate(sellerOrdersProvider(_stage));
                await ref.read(sellerOrdersProvider(_stage).future);
              },
              child: ordersAsync.when(
                loading: () => const Center(
                  child: CircularProgressIndicator(color: AppColors.postbookPrimary),
                ),
                error: (e, _) => ListView(
                  children: [
                    SizedBox(
                      height: MediaQuery.of(context).size.height * 0.5,
                      child: Center(
                        child: Padding(
                          padding: const EdgeInsets.all(AppSpacing.xxl),
                          child: Text(
                            'Could not load orders.\n$e',
                            textAlign: TextAlign.center,
                            style: AppTextStyles.body,
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
                data: (orders) {
                  if (orders.isEmpty) {
                    return ListView(
                      children: [
                        SizedBox(
                          height: MediaQuery.of(context).size.height * 0.5,
                          child: Center(
                            child: Column(
                              mainAxisAlignment: MainAxisAlignment.center,
                              children: [
                                const Icon(Icons.inbox_outlined,
                                    size: 56, color: AppColors.textGhost),
                                const SizedBox(height: AppSpacing.l),
                                Text(
                                  _stage == 'all'
                                      ? 'No orders yet'
                                      : 'Nothing in this queue',
                                  style: AppTextStyles.h2,
                                ),
                              ],
                            ),
                          ),
                        ),
                      ],
                    );
                  }
                  return ListView.separated(
                    padding: const EdgeInsets.all(AppSpacing.l),
                    itemCount: orders.length,
                    separatorBuilder: (_, _) =>
                        const SizedBox(height: AppSpacing.s),
                    itemBuilder: (_, i) => _OrderRow(card: orders[i]),
                  );
                },
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _OrderRow extends StatelessWidget {
  const _OrderRow({required this.card});
  final SellerOrderCard card;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: () => GoRouter.of(context)
          .push('/commerce/orders/${card.orderId}'),
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(card.orderNumber, style: AppTextStyles.label),
                ),
                _StatusChip(label: _displayStatus(card)),
              ],
            ),
            const SizedBox(height: 2),
            Text(
              '${card.itemCount} item${card.itemCount == 1 ? '' : 's'} · Rs. ${card.sellerSubtotal.toStringAsFixed(0)}',
              style: AppTextStyles.bodySmall,
            ),
            if (card.trackingNumber != null) ...[
              const SizedBox(height: 4),
              Text(
                'AWB ${card.trackingNumber}',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textMuted,
                  fontFamily: 'monospace',
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _displayStatus(SellerOrderCard c) {
    if (c.shipmentStatus != null && c.shipmentStatus!.isNotEmpty) {
      return c.shipmentStatus!.toUpperCase();
    }
    return c.status.toUpperCase();
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.label});
  final String label;

  @override
  Widget build(BuildContext context) {
    final (bg, fg) = _toneFor(label);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelTiny.copyWith(
          color: fg,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
  }

  (Color, Color) _toneFor(String label) {
    switch (label) {
      case 'DELIVERED':
        return (const Color(0xFFD1FAE5), const Color(0xFF047857));
      case 'CANCELLED':
        return (const Color(0xFFFFE4E6), const Color(0xFFB91C1C));
      case 'IN_TRANSIT':
      case 'SHIPPED':
      case 'OUT_FOR_DELIVERY':
        return (const Color(0xFFDBEAFE), const Color(0xFF1D4ED8));
      default:
        return (const Color(0xFFFEF3C7), const Color(0xFF92400E));
    }
  }
}
