import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/data/repositories/orders_repository.dart';
import 'package:atpost_app/providers/orders_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class OrderDetailScreen extends ConsumerWidget {
  const OrderDetailScreen({super.key, required this.orderId});

  final String orderId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final orderAsync = ref.watch(orderDetailProvider(orderId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: Text('Order Details', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          color: AppColors.textSecondary,
          onPressed: () => context.pop(),
        ),
      ),
      body: orderAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, _) => Center(
          child: Text('Could not load order', style: AppTextStyles.bodySmall),
        ),
        data: (order) => _OrderDetailBody(
          order: order,
          orderId: orderId,
        ),
      ),
    );
  }
}

class _OrderDetailBody extends ConsumerWidget {
  const _OrderDetailBody({required this.order, required this.orderId});

  final Order order;
  final String orderId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return SingleChildScrollView(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 40),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Order ID + status
          Row(
            children: [
              Expanded(
                child: Text(
                  'Order #${order.id.length > 8 ? order.id.substring(0, 8).toUpperCase() : order.id.toUpperCase()}',
                  style: AppTextStyles.h3,
                ),
              ),
              _StatusBadge(status: order.status),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            _formatDate(order.createdAt),
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 20),

          // Items
          Text('Items', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          Container(
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              children: order.items.asMap().entries.map((entry) {
                final i = entry.key;
                final item = entry.value;
                final isLast = i == order.items.length - 1;
                return Column(
                  children: [
                    ListTile(
                      title: Text(item.productName, style: AppTextStyles.h3),
                      subtitle: Text(
                        'Qty: ${item.quantity}',
                        style: AppTextStyles.bodySmall,
                      ),
                      trailing: Text(
                        '\u20b9${item.price.toStringAsFixed(0)}',
                        style: AppTextStyles.label,
                      ),
                    ),
                    if (!isLast)
                      const Divider(
                        height: 1,
                        thickness: 1,
                        color: AppColors.borderSubtle,
                        indent: 16,
                        endIndent: 16,
                      ),
                  ],
                );
              }).toList(),
            ),
          ),

          const SizedBox(height: 16),

          // Total row
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Total', style: AppTextStyles.h3),
                Text(
                  '\u20b9${order.total.toStringAsFixed(0)}',
                  style: AppTextStyles.h3.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ],
            ),
          ),

          // Shipping address
          if (order.shippingAddress != null &&
              order.shippingAddress!.isNotEmpty) ...[
            const SizedBox(height: 16),
            Text('Shipping Address', style: AppTextStyles.h3),
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Text(
                order.shippingAddress!,
                style: AppTextStyles.bodySmall,
              ),
            ),
          ],

          // Estimated delivery
          if (order.estimatedDelivery != null) ...[
            const SizedBox(height: 16),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              decoration: BoxDecoration(
                color: Colors.green.withValues(alpha: 0.1),
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                border: Border.all(color: Colors.green.withValues(alpha: 0.3)),
              ),
              child: Row(
                children: [
                  const Icon(Icons.local_shipping_outlined,
                      color: Colors.green, size: 18),
                  const SizedBox(width: 10),
                  Text(
                    'Est. delivery: ${_formatDate(order.estimatedDelivery!)}',
                    style: AppTextStyles.label.copyWith(color: Colors.green),
                  ),
                ],
              ),
            ),
          ],

          const SizedBox(height: 24),

          // Status timeline
          Text('Order Status', style: AppTextStyles.h3),
          const SizedBox(height: 12),
          _StatusTimeline(currentStatus: order.status),

          // Cancel button
          if (order.status == 'pending') ...[
            const SizedBox(height: 24),
            SizedBox(
              width: double.infinity,
              child: OutlinedButton(
                onPressed: () => _confirmCancel(context, ref),
                style: OutlinedButton.styleFrom(
                  foregroundColor: Colors.red,
                  side: const BorderSide(color: Colors.red),
                  padding: const EdgeInsets.symmetric(vertical: 14),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                ),
                child: Text('Cancel Order', style: AppTextStyles.label.copyWith(color: Colors.red)),
              ),
            ),
          ],
        ],
      ),
    );
  }

  Future<void> _confirmCancel(BuildContext context, WidgetRef ref) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Cancel Order', style: AppTextStyles.h3),
        content: Text(
          'Are you sure you want to cancel this order?',
          style: AppTextStyles.bodySmall,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text('No', style: AppTextStyles.label),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(
              'Yes, Cancel',
              style: AppTextStyles.label.copyWith(color: Colors.red),
            ),
          ),
        ],
      ),
    );

    if (confirmed == true) {
      try {
        await ref.read(ordersRepositoryProvider).cancelOrder(orderId);
        ref.invalidate(ordersProvider);
        if (context.mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Order cancelled.')),
          );
          context.pop();
        }
      } catch (_) {
        if (context.mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Could not cancel order.')),
          );
        }
      }
    }
  }

  String _formatDate(DateTime date) {
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return '${date.day} ${months[date.month - 1]} ${date.year}';
  }
}

class _StatusTimeline extends StatelessWidget {
  const _StatusTimeline({required this.currentStatus});

  final String currentStatus;

  static const _steps = [
    ('ordered', 'Order Placed'),
    ('confirmed', 'Confirmed'),
    ('shipped', 'Shipped'),
    ('delivered', 'Delivered'),
  ];

  int get _currentIndex {
    switch (currentStatus) {
      case 'pending':
        return 0;
      case 'confirmed':
        return 1;
      case 'shipped':
        return 2;
      case 'delivered':
        return 3;
      default:
        return -1; // cancelled
    }
  }

  @override
  Widget build(BuildContext context) {
    if (currentStatus == 'cancelled') {
      return Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: Colors.red.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: Colors.red.withValues(alpha: 0.3)),
        ),
        child: Row(
          children: [
            const Icon(Icons.cancel_outlined, color: Colors.red, size: 20),
            const SizedBox(width: 10),
            Text(
              'Order Cancelled',
              style: AppTextStyles.label.copyWith(color: Colors.red),
            ),
          ],
        ),
      );
    }

    final idx = _currentIndex;
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: _steps.asMap().entries.map((entry) {
          final stepIdx = entry.key;
          final label = entry.value.$2;
          final isDone = stepIdx <= idx;
          final isLast = stepIdx == _steps.length - 1;

          return Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Column(
                children: [
                  Container(
                    width: 24,
                    height: 24,
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      color: isDone
                          ? AppColors.postbookPrimary
                          : AppColors.bgSecondary,
                      border: Border.all(
                        color: isDone
                            ? AppColors.postbookPrimary
                            : AppColors.borderSubtle,
                      ),
                    ),
                    child: isDone
                        ? const Icon(Icons.check, color: Colors.white, size: 14)
                        : null,
                  ),
                  if (!isLast)
                    Container(
                      width: 2,
                      height: 24,
                      color: isDone
                          ? AppColors.postbookPrimary
                          : AppColors.borderSubtle,
                    ),
                ],
              ),
              const SizedBox(width: 12),
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text(
                  label,
                  style: AppTextStyles.label.copyWith(
                    color: isDone
                        ? AppColors.textPrimary
                        : AppColors.textDim,
                  ),
                ),
              ),
            ],
          );
        }).toList(),
      ),
    );
  }
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final color = _colorForStatus(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Text(
        _labelForStatus(status),
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }

  Color _colorForStatus(String status) {
    switch (status) {
      case 'pending':
        return Colors.orange;
      case 'confirmed':
        return Colors.blue;
      case 'shipped':
        return Colors.purple;
      case 'delivered':
        return Colors.green;
      case 'cancelled':
        return Colors.red;
      default:
        return AppColors.textMuted;
    }
  }

  String _labelForStatus(String status) {
    switch (status) {
      case 'pending':
        return 'Pending';
      case 'confirmed':
        return 'Confirmed';
      case 'shipped':
        return 'Shipped';
      case 'delivered':
        return 'Delivered';
      case 'cancelled':
        return 'Cancelled';
      default:
        return status;
    }
  }
}
