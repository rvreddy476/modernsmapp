import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/providers/orders_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class OrdersScreen extends ConsumerWidget {
  const OrdersScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ordersAsync = ref.watch(ordersProvider);

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        backgroundColor: AppColors.bgPrimary,
        appBar: AppBar(
          backgroundColor: AppColors.bgPrimary,
          foregroundColor: AppColors.textPrimary,
          title: Text('My Orders', style: AppTextStyles.h2),
          leading: IconButton(
            icon: const Icon(Icons.arrow_back),
            color: AppColors.textSecondary,
            onPressed: () => context.pop(),
          ),
          bottom: TabBar(
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textDim,
            indicatorColor: AppColors.postbookPrimary,
            labelStyle: AppTextStyles.label,
            tabs: const [
              Tab(text: 'Active'),
              Tab(text: 'Past'),
            ],
          ),
        ),
        body: ordersAsync.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Text(
              'Could not load orders',
              style: AppTextStyles.bodySmall,
            ),
          ),
          data: (orders) {
            final active = orders.where((o) => o.isActive).toList();
            final past = orders.where((o) => !o.isActive).toList();

            return TabBarView(
              children: [
                _OrderList(orders: active, emptyMessage: 'No active orders'),
                _OrderList(orders: past, emptyMessage: 'No past orders'),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _OrderList extends StatelessWidget {
  const _OrderList({required this.orders, required this.emptyMessage});

  final List<Order> orders;
  final String emptyMessage;

  @override
  Widget build(BuildContext context) {
    if (orders.isEmpty) {
      return Center(
        child: Text(emptyMessage, style: AppTextStyles.bodySmall),
      );
    }
    return ListView.builder(
      padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
      itemCount: orders.length,
      itemBuilder: (_, i) => _OrderTile(order: orders[i]),
    );
  }
}

class _OrderTile extends StatelessWidget {
  const _OrderTile({required this.order});

  final Order order;

  @override
  Widget build(BuildContext context) {
    final firstItemName =
        order.items.isNotEmpty ? order.items.first.productName : 'Order';

    return Card(
      color: AppColors.bgCard,
      margin: const EdgeInsets.only(bottom: 10),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        title: Text(firstItemName, style: AppTextStyles.h3),
        subtitle: Text(
          '${order.items.length} item(s) \u00b7 ${_formatDate(order.createdAt)}',
          style: AppTextStyles.bodySmall,
        ),
        trailing: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            _StatusBadge(status: order.status),
            const SizedBox(height: 4),
            Text(
              '\u20b9${order.total.toStringAsFixed(0)}',
              style: AppTextStyles.label,
            ),
          ],
        ),
        onTap: () => context.push('/orders/${order.id}'),
      ),
    );
  }

  String _formatDate(DateTime date) {
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return '${date.day} ${months[date.month - 1]} ${date.year}';
  }
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final color = _colorForStatus(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
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
