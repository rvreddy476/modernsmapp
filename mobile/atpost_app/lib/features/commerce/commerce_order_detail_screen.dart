// Commerce order detail — Sprint 2 (full).
//
// Sections (top → bottom):
//   * "Order placed!" success banner (when ?placed=1).
//   * Header card — order #, status pill, total, placed date.
//   * Status timeline — Placed → Paid → Packed → Shipped → Out for
//     delivery → Delivered. Reflects order timestamps + status.
//   * Live tracking — courier + AWB + last 3 tracking events. Polls the
//     shipment endpoint every 60s while shipped/out-for-delivery
//     (Sprint 2 §6).
//   * Items — image, title, qty, line total. Each has "Buy again" +
//     "Return" (only if status == delivered AND within window).
//     Delivered items also show a "Rate this product" CTA.
//   * Shipping address card.
//   * Payment summary — itemised + grand total + payment method/status.
//   * Invoice — "Download invoice" CTA (status >= shipped).
//   * Need help? — chat-with-seller stub, report issue, cancel order.

import 'dart:async';
import 'dart:typed_data';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CommerceOrderDetailScreen extends ConsumerStatefulWidget {
  const CommerceOrderDetailScreen({
    super.key,
    required this.orderId,
    this.justPlaced = false,
  });

  final String orderId;

  /// When true, the screen shows the "Order placed!" success header.
  final bool justPlaced;

  @override
  ConsumerState<CommerceOrderDetailScreen> createState() =>
      _CommerceOrderDetailScreenState();
}

class _CommerceOrderDetailScreenState
    extends ConsumerState<CommerceOrderDetailScreen>
    with WidgetsBindingObserver {
  Timer? _poller;
  bool _foreground = true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poller?.cancel();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
  }

  /// Starts polling the shipment endpoint every 60s while the order is
  /// shipped/out-for-delivery. Stops on delivered/cancelled.
  void _ensurePoller(Order order) {
    final shouldPoll = order.status == 'shipped' ||
        order.status == 'out_for_delivery';
    if (!shouldPoll) {
      _poller?.cancel();
      _poller = null;
      return;
    }
    _poller ??= Timer.periodic(const Duration(seconds: 60), (_) {
      if (!mounted || !_foreground) return;
      ref.invalidate(orderShipmentProvider(widget.orderId));
    });
  }

  @override
  Widget build(BuildContext context) {
    final orderAsync = ref.watch(orderDetailProvider(widget.orderId));
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Order', style: AppTextStyles.h2),
      ),
      body: orderAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load order.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (order) {
          _ensurePoller(order);
          return RefreshIndicator(
            onRefresh: () async {
              ref.invalidate(orderDetailProvider(widget.orderId));
              ref.invalidate(orderShipmentProvider(widget.orderId));
            },
            color: AppColors.postbookPrimary,
            child: ListView(
              padding: const EdgeInsets.all(AppSpacing.l),
              children: [
                if (widget.justPlaced)
                  _SuccessBanner(),
                if (widget.justPlaced)
                  const SizedBox(height: AppSpacing.l),
                _HeaderCard(order: order),
                const SizedBox(height: AppSpacing.l),
                _StatusTimelineCard(order: order),
                const SizedBox(height: AppSpacing.l),
                if (_isShippedOrLater(order))
                  _LiveTrackingCard(orderId: widget.orderId),
                if (_isShippedOrLater(order))
                  const SizedBox(height: AppSpacing.l),
                _ItemsCard(order: order),
                const SizedBox(height: AppSpacing.l),
                if (order.shippingAddress != null)
                  _AddressCard(address: order.shippingAddress!),
                if (order.shippingAddress != null)
                  const SizedBox(height: AppSpacing.l),
                _PaymentSummaryCard(order: order),
                const SizedBox(height: AppSpacing.l),
                if (_isShippedOrLater(order))
                  _InvoiceCard(orderId: widget.orderId),
                if (_isShippedOrLater(order))
                  const SizedBox(height: AppSpacing.l),
                _NeedHelpCard(order: order),
                const SizedBox(height: AppSpacing.xxl),
              ],
            ),
          );
        },
      ),
    );
  }

  static bool _isShippedOrLater(Order order) {
    switch (order.status) {
      case 'shipped':
      case 'out_for_delivery':
      case 'delivered':
        return true;
      default:
        return order.shippedAt != null || order.deliveredAt != null;
    }
  }
}

// ─── Success banner ─────────────────────────────────────────────────────

class _SuccessBanner extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.statusSuccess.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.statusSuccess),
      ),
      child: Row(
        children: [
          const Icon(Icons.check_circle, color: AppColors.statusSuccess),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Order placed!', style: AppTextStyles.h3),
                Text(
                  'We\'ll notify you as it ships.',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Header ─────────────────────────────────────────────────────────────

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({required this.order});

  final Order order;

  @override
  Widget build(BuildContext context) {
    return _Card(
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    order.orderNumber.isEmpty
                        ? 'Order'
                        : 'Order ${order.orderNumber}',
                    style: AppTextStyles.h3,
                  ),
                  Text(
                    'Placed ${_fmtDate(order.placedAt)}',
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            _StatusPill(status: order.status),
          ],
        ),
        const SizedBox(height: AppSpacing.l),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text('Total', style: AppTextStyles.label),
            Text(
              'Rs. ${order.amountGrand.toStringAsFixed(0)}',
              style: AppTextStyles.h2,
            ),
          ],
        ),
      ],
    );
  }

  static String _fmtDate(DateTime d) {
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return '${d.day} ${months[d.month - 1]} ${d.year}';
  }
}

// ─── Status timeline ────────────────────────────────────────────────────

class _StatusTimelineCard extends StatelessWidget {
  const _StatusTimelineCard({required this.order});

  final Order order;

  static const _steps = [
    ('placed', 'Placed'),
    ('paid', 'Paid'),
    ('packed', 'Packed'),
    ('shipped', 'Shipped'),
    ('out_for_delivery', 'Out for delivery'),
    ('delivered', 'Delivered'),
  ];

  int _currentIndex() {
    final s = order.status;
    if (s == 'cancelled' || s == 'refunded') return -1;
    if (s == 'delivered') return 5;
    if (s == 'out_for_delivery') return 4;
    if (s == 'shipped') return 3;
    if (s == 'packed' || s == 'processing' || s == 'confirmed') return 2;
    if (s == 'paid' ||
        (order.paymentStatus == 'paid' && order.paidAt != null)) {
      return 1;
    }
    return 0;
  }

  DateTime? _timestampFor(int i) {
    switch (i) {
      case 0:
        return order.placedAt;
      case 1:
        return order.paidAt;
      case 3:
        return order.shippedAt;
      case 5:
        return order.deliveredAt;
      default:
        return null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final cancelled =
        order.status == 'cancelled' || order.status == 'refunded';
    final current = _currentIndex();
    return _Card(
      children: [
        Text('Status', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.l),
        if (cancelled)
          Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.statusError.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            child: Text(
              order.status == 'refunded'
                  ? 'Order refunded.'
                  : 'Order cancelled.',
              style:
                  AppTextStyles.label.copyWith(color: AppColors.statusError),
            ),
          ),
        if (!cancelled)
          for (var i = 0; i < _steps.length; i++)
            _TimelineRow(
              label: _steps[i].$2,
              done: i <= current,
              isLast: i == _steps.length - 1,
              timestamp: _timestampFor(i),
            ),
      ],
    );
  }
}

class _TimelineRow extends StatelessWidget {
  const _TimelineRow({
    required this.label,
    required this.done,
    required this.isLast,
    this.timestamp,
  });

  final String label;
  final bool done;
  final bool isLast;
  final DateTime? timestamp;

  @override
  Widget build(BuildContext context) {
    final dotColor =
        done ? AppColors.postbookPrimary : AppColors.borderSubtle;
    return IntrinsicHeight(
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Column(
            children: [
              Container(
                width: 14,
                height: 14,
                decoration: BoxDecoration(
                  color: dotColor,
                  shape: BoxShape.circle,
                ),
              ),
              if (!isLast)
                Expanded(
                  child: Container(width: 2, color: dotColor),
                ),
            ],
          ),
          const SizedBox(width: AppSpacing.l),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.only(bottom: AppSpacing.l),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    label,
                    style: AppTextStyles.body.copyWith(
                      color: done
                          ? AppColors.textPrimary
                          : AppColors.textMuted,
                    ),
                  ),
                  if (timestamp != null && done)
                    Text(
                      _fmtDateTime(timestamp!),
                      style: AppTextStyles.bodySmall,
                    ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  static String _fmtDateTime(DateTime d) {
    return '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')} '
        '${d.hour.toString().padLeft(2, '0')}:${d.minute.toString().padLeft(2, '0')}';
  }
}

// ─── Live tracking ──────────────────────────────────────────────────────

class _LiveTrackingCard extends ConsumerWidget {
  const _LiveTrackingCard({required this.orderId});

  final String orderId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final shipmentAsync = ref.watch(orderShipmentProvider(orderId));
    return _Card(
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text('Live tracking', style: AppTextStyles.h3),
            IconButton(
              tooltip: 'Refresh',
              icon: const Icon(Icons.refresh,
                  size: 18, color: AppColors.textSecondary),
              onPressed: () =>
                  ref.invalidate(orderShipmentProvider(orderId)),
            ),
          ],
        ),
        shipmentAsync.when(
          loading: () => const Padding(
            padding: EdgeInsets.symmetric(vertical: AppSpacing.l),
            child: LinearProgressIndicator(
              minHeight: 2,
              color: AppColors.postbookPrimary,
            ),
          ),
          error: (_, _) => Padding(
            padding: const EdgeInsets.symmetric(vertical: AppSpacing.l),
            child: Text(
              'Tracking not available yet.',
              style: AppTextStyles.bodySmall,
            ),
          ),
          data: (shipment) {
            if (shipment.id.isEmpty) {
              return Padding(
                padding: const EdgeInsets.symmetric(vertical: AppSpacing.l),
                child: Text(
                  'Shipment hasn\'t been booked yet.',
                  style: AppTextStyles.bodySmall,
                ),
              );
            }
            final recent = shipment.events.length > 3
                ? shipment.events.sublist(shipment.events.length - 3)
                : shipment.events;
            return Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const SizedBox(height: AppSpacing.s),
                Text(
                  '${shipment.courier.isEmpty ? 'Courier' : shipment.courier}'
                  ' · AWB ${shipment.awb.isEmpty ? '—' : shipment.awb}',
                  style: AppTextStyles.label,
                ),
                if (shipment.trackingUrl != null) ...[
                  const SizedBox(height: 2),
                  Text(
                    'Track on courier site',
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.posttubePrimary,
                      decoration: TextDecoration.underline,
                    ),
                  ),
                ],
                const SizedBox(height: AppSpacing.l),
                if (recent.isEmpty)
                  Text(
                    'No tracking events yet.',
                    style: AppTextStyles.bodySmall,
                  ),
                for (final e in recent.reversed)
                  Padding(
                    padding:
                        const EdgeInsets.symmetric(vertical: AppSpacing.s),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Container(
                          width: 8,
                          height: 8,
                          margin: const EdgeInsets.only(top: 6),
                          decoration: const BoxDecoration(
                            color: AppColors.posttubePrimary,
                            shape: BoxShape.circle,
                          ),
                        ),
                        const SizedBox(width: AppSpacing.m),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(e.status, style: AppTextStyles.label),
                              Text(
                                _fmtDateTime(e.occurredAt),
                                style: AppTextStyles.bodySmall,
                              ),
                              if (e.location != null)
                                Text(e.location!,
                                    style: AppTextStyles.bodySmall),
                              if (e.remark != null)
                                Text(e.remark!,
                                    style: AppTextStyles.bodySmall),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            );
          },
        ),
      ],
    );
  }

  static String _fmtDateTime(DateTime d) {
    return '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')} '
        '${d.hour.toString().padLeft(2, '0')}:${d.minute.toString().padLeft(2, '0')}';
  }
}

// ─── Items ──────────────────────────────────────────────────────────────

class _ItemsCard extends ConsumerWidget {
  const _ItemsCard({required this.order});

  final Order order;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return _Card(
      children: [
        Text('Items', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        for (final item in order.items)
          _ItemRow(order: order, item: item),
      ],
    );
  }
}

class _ItemRow extends StatelessWidget {
  const _ItemRow({required this.order, required this.item});

  final Order order;
  final OrderItem item;

  bool get _canReturn {
    if (order.status != 'delivered') return false;
    final delivered = order.deliveredAt;
    if (delivered == null) return true;
    // Default 7-day window when product policy isn't on the order item
    // payload — the real gate is server-side anyway.
    final ageDays = DateTime.now().difference(delivered).inDays;
    return ageDays <= 30;
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.s),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                child: SizedBox(
                  width: 56,
                  height: 56,
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
                            child: const Icon(
                                Icons.broken_image_outlined,
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
                      style: AppTextStyles.label.copyWith(
                          color: AppColors.textPrimary),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (item.productSnapshot.variantLabel != null)
                      Text(item.productSnapshot.variantLabel!,
                          style: AppTextStyles.bodySmall),
                    Text(
                      'Qty ${item.qty} · Rs. ${item.lineTotal.toStringAsFixed(0)}',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: AppSpacing.s),
          Wrap(
            spacing: AppSpacing.m,
            runSpacing: AppSpacing.s,
            children: [
              OutlinedButton(
                onPressed: () => GoRouter.of(context)
                    .push('/commerce/product/${item.productId}'),
                style: OutlinedButton.styleFrom(
                  side: const BorderSide(color: AppColors.borderMedium),
                  padding: const EdgeInsets.symmetric(
                      horizontal: AppSpacing.l, vertical: 6),
                ),
                child: Text('Buy again', style: AppTextStyles.labelSmall),
              ),
              if (_canReturn)
                OutlinedButton(
                  onPressed: () => GoRouter.of(context)
                      .push('/commerce/orders/${order.id}/return'),
                  style: OutlinedButton.styleFrom(
                    side:
                        const BorderSide(color: AppColors.postbookPrimary),
                    padding: const EdgeInsets.symmetric(
                        horizontal: AppSpacing.l, vertical: 6),
                  ),
                  child: Text(
                    'Return',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                ),
              if (order.status == 'delivered')
                OutlinedButton(
                  onPressed: () => GoRouter.of(context).push(
                    '/commerce/products/${item.productId}/review',
                    extra: <String, String>{
                      'seller_id': item.sellerId,
                      'order_item_id': item.id,
                      'product_title': item.productSnapshot.title,
                    },
                  ),
                  style: OutlinedButton.styleFrom(
                    side: const BorderSide(color: AppColors.statusWarning),
                    padding: const EdgeInsets.symmetric(
                        horizontal: AppSpacing.l, vertical: 6),
                  ),
                  child: Text(
                    'Rate this product',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: AppColors.statusWarning),
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

// ─── Address ────────────────────────────────────────────────────────────

class _AddressCard extends StatelessWidget {
  const _AddressCard({required this.address});

  final Address address;

  @override
  Widget build(BuildContext context) {
    return _Card(
      children: [
        Text('Ship to', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        Text(address.fullName, style: AppTextStyles.label),
        Text(
          [
            address.line1,
            if (address.line2 != null) address.line2,
            address.city,
            '${address.state} ${address.postalCode}',
          ].whereType<String>().join(', '),
          style: AppTextStyles.bodySmall,
        ),
        Text('Phone: ${address.phone}', style: AppTextStyles.bodySmall),
      ],
    );
  }
}

// ─── Payment summary ────────────────────────────────────────────────────

class _PaymentSummaryCard extends StatelessWidget {
  const _PaymentSummaryCard({required this.order});

  final Order order;

  @override
  Widget build(BuildContext context) {
    return _Card(
      children: [
        Text('Payment', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        _row('Subtotal', 'Rs. ${order.amountSubtotal.toStringAsFixed(0)}'),
        _row('Tax', 'Rs. ${order.amountTax.toStringAsFixed(0)}'),
        _row('Shipping', 'Rs. ${order.amountShipping.toStringAsFixed(0)}'),
        if (order.amountDiscount > 0)
          _row('Discount', '- Rs. ${order.amountDiscount.toStringAsFixed(0)}'),
        const Divider(color: AppColors.borderSubtle, height: 24),
        _row(
          'Grand total',
          'Rs. ${order.amountGrand.toStringAsFixed(0)}',
          bold: true,
        ),
        const SizedBox(height: AppSpacing.s),
        if (order.paymentMethod != null)
          _row('Method', order.paymentMethod!.toUpperCase()),
        _row('Payment status', order.paymentStatus),
      ],
    );
  }

  Widget _row(String l, String v, {bool bold = false}) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Text(l,
              style: bold ? AppTextStyles.label : AppTextStyles.bodySmall),
          Text(
            v,
            style: bold
                ? AppTextStyles.h3
                : AppTextStyles.label
                    .copyWith(color: AppColors.textPrimary),
          ),
        ],
      ),
    );
  }
}

// ─── Invoice ────────────────────────────────────────────────────────────

class _InvoiceCard extends ConsumerStatefulWidget {
  const _InvoiceCard({required this.orderId});

  final String orderId;

  @override
  ConsumerState<_InvoiceCard> createState() => _InvoiceCardState();
}

class _InvoiceCardState extends ConsumerState<_InvoiceCard> {
  bool _busy = false;

  Future<void> _download() async {
    setState(() => _busy = true);
    try {
      final Uint8List bytes = await ref
          .read(commerceRepositoryProvider)
          .getOrderInvoice(widget.orderId);
      AppLogger.info(
        'Invoice fetched (${bytes.length} bytes)',
        tag: 'Invoice',
      );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            bytes.isEmpty
                ? 'Invoice not available yet.'
                : 'Invoice ready (${bytes.length} bytes). Save / share coming soon.',
          ),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not fetch invoice: $e')),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return _Card(
      children: [
        Text('Invoice', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        Text(
          'Tax-compliant invoice with HSN + GST split.',
          style: AppTextStyles.bodySmall,
        ),
        const SizedBox(height: AppSpacing.s),
        OutlinedButton.icon(
          onPressed: _busy ? null : _download,
          icon: _busy
              ? const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: AppColors.postbookPrimary,
                  ),
                )
              : const Icon(Icons.file_download_outlined,
                  color: AppColors.postbookPrimary),
          label: Text(
            _busy ? 'Fetching…' : 'Download invoice',
            style: AppTextStyles.label
                .copyWith(color: AppColors.postbookPrimary),
          ),
        ),
      ],
    );
  }
}

// ─── Need help ──────────────────────────────────────────────────────────

class _NeedHelpCard extends ConsumerWidget {
  const _NeedHelpCard({required this.order});

  final Order order;

  bool get _cancellable {
    switch (order.status) {
      case 'created':
      case 'confirmed':
      case 'paid':
      case 'pending':
      case 'processing':
      case 'packed':
        return true;
      default:
        return false;
    }
  }

  Future<void> _cancel(BuildContext context, WidgetRef ref) async {
    final reason = await showDialog<String>(
      context: context,
      builder: (ctx) {
        final ctrl = TextEditingController();
        return AlertDialog(
          backgroundColor: AppColors.bgCard,
          title: Text('Cancel order?', style: AppTextStyles.h3),
          content: TextField(
            controller: ctrl,
            decoration: const InputDecoration(hintText: 'Reason (optional)'),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: const Text('Keep order'),
            ),
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(ctrl.text.trim()),
              child: const Text('Cancel order'),
            ),
          ],
        );
      },
    );
    if (reason == null) return;
    try {
      await ref
          .read(commerceRepositoryProvider)
          .cancelOrder(order.id, reason);
      ref.invalidate(orderDetailProvider(order.id));
      ref.invalidate(myOrdersProvider);
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Order cancelled')),
      );
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not cancel: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return _Card(
      children: [
        Text('Need help?', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        ListTile(
          dense: true,
          contentPadding: EdgeInsets.zero,
          leading: const Icon(Icons.chat_bubble_outline,
              color: AppColors.textSecondary),
          title: Text('Chat with seller',
              style: AppTextStyles.body),
          subtitle: Text('Coming soon', style: AppTextStyles.bodySmall),
          onTap: null,
        ),
        ListTile(
          dense: true,
          contentPadding: EdgeInsets.zero,
          leading: const Icon(Icons.report_outlined,
              color: AppColors.textSecondary),
          title: Text('Report an issue', style: AppTextStyles.body),
          subtitle: Text('Coming soon', style: AppTextStyles.bodySmall),
          onTap: null,
        ),
        if (_cancellable)
          ListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            leading: const Icon(Icons.cancel_outlined,
                color: AppColors.statusError),
            title: Text(
              'Cancel order',
              style:
                  AppTextStyles.body.copyWith(color: AppColors.statusError),
            ),
            onTap: () => _cancel(context, ref),
          ),
      ],
    );
  }
}

// ─── Shared bits ────────────────────────────────────────────────────────

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    Color color;
    switch (status) {
      case 'delivered':
        color = AppColors.statusSuccess;
        break;
      case 'cancelled':
      case 'failed':
        color = AppColors.statusError;
        break;
      case 'shipped':
      case 'out_for_delivery':
        color = AppColors.posttubePrimary;
        break;
      default:
        color = AppColors.statusWarning;
    }
    return Container(
      padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.l, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Text(
        _pretty(status),
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }

  static String _pretty(String s) {
    if (s.isEmpty) return 'Pending';
    return s
        .replaceAll('_', ' ')
        .split(' ')
        .map((w) => w.isEmpty ? w : w[0].toUpperCase() + w.substring(1))
        .join(' ');
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
