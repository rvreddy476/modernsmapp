import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/order.dart';
import 'package:atpost_app/providers/orders_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class OrdersScreen extends ConsumerStatefulWidget {
  const OrdersScreen({super.key});

  @override
  ConsumerState<OrdersScreen> createState() => _OrdersScreenState();
}

class _OrdersScreenState extends ConsumerState<OrdersScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabController;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  String _formatDate(DateTime date) {
    const months = [
      'Jan',
      'Feb',
      'Mar',
      'Apr',
      'May',
      'Jun',
      'Jul',
      'Aug',
      'Sep',
      'Oct',
      'Nov',
      'Dec',
    ];
    return '${date.day} ${months[date.month - 1]} ${date.year}';
  }

  String _currency(String currency) {
    switch (currency.toUpperCase()) {
      case 'USD':
        return r'$';
      case 'EUR':
        return 'EUR ';
      case 'INR':
      default:
        return 'Rs ';
    }
  }

  @override
  Widget build(BuildContext context) {
    final ordersAsync = ref.watch(ordersProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: ordersAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: _InlineStateCard(
              icon: Icons.receipt_long_outlined,
              message: 'Could not load orders.',
              action: 'Retry',
              onTap: () => ref.invalidate(ordersProvider),
            ),
          ),
          data: (orders) {
            final active = orders.where((order) => order.isActive).toList();
            final past = orders.where((order) => !order.isActive).toList();
            final totalSpent = orders.fold<double>(
              0,
              (sum, order) => sum + order.total,
            );

            return Column(
              children: [
                Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 10),
                  child: _OrdersHero(
                    orderCount: orders.length,
                    activeCount: active.length,
                    totalSpent: totalSpent,
                    currency: _currency(
                      orders.isNotEmpty ? orders.first.currency : 'INR',
                    ),
                    onBack: () => context.pop(),
                    onRefresh: () => ref.invalidate(ordersProvider),
                  ),
                ),
                const SizedBox(height: 12),
                Padding(
                  padding: AppSpacing.pagePadding,
                  child: Container(
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge,
                      ),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: TabBar(
                      controller: _tabController,
                      labelColor: AppColors.postbookPrimary,
                      unselectedLabelColor: AppColors.textDim,
                      indicatorColor: AppColors.postbookPrimary,
                      tabs: [
                        Tab(text: 'Active (${active.length})'),
                        Tab(text: 'Past (${past.length})'),
                      ],
                    ),
                  ),
                ),
                Expanded(
                  child: TabBarView(
                    controller: _tabController,
                    children: [
                      _OrderList(
                        orders: active,
                        emptyMessage: 'No active orders right now.',
                        formatDate: _formatDate,
                        currency: _currency,
                      ),
                      _OrderList(
                        orders: past,
                        emptyMessage: 'No past orders yet.',
                        formatDate: _formatDate,
                        currency: _currency,
                      ),
                    ],
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _OrdersHero extends StatelessWidget {
  const _OrdersHero({
    required this.orderCount,
    required this.activeCount,
    required this.totalSpent,
    required this.currency,
    required this.onBack,
    required this.onRefresh,
  });

  final int orderCount;
  final int activeCount;
  final double totalSpent;
  final String currency;
  final VoidCallback onBack;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          colors: [Color(0x33FF6B35), Color(0x334ECDC4), Color(0x337B68EE)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Column(
        children: [
          Row(
            children: [
              IconButton(
                onPressed: onBack,
                icon: const Icon(
                  Icons.arrow_back_ios_new_rounded,
                  size: 18,
                  color: AppColors.textPrimary,
                ),
              ),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  'My Orders',
                  style: AppTextStyles.h1.copyWith(fontSize: 30),
                ),
              ),
              IconButton(
                onPressed: onRefresh,
                icon: const Icon(
                  Icons.refresh_rounded,
                  color: AppColors.textPrimary,
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              _MetricPill(label: 'Orders', value: '$orderCount'),
              const SizedBox(width: 8),
              _MetricPill(label: 'Active', value: '$activeCount'),
              const SizedBox(width: 8),
              Expanded(
                child: _MetricPill(
                  label: 'Spent',
                  value: '$currency${totalSpent.toStringAsFixed(0)}',
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _MetricPill extends StatelessWidget {
  const _MetricPill({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(value, style: AppTextStyles.h3),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _OrderList extends StatelessWidget {
  const _OrderList({
    required this.orders,
    required this.emptyMessage,
    required this.formatDate,
    required this.currency,
  });

  final List<Order> orders;
  final String emptyMessage;
  final String Function(DateTime) formatDate;
  final String Function(String) currency;

  @override
  Widget build(BuildContext context) {
    if (orders.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          SizedBox(
            height: 320,
            child: Center(
              child: _InlineStateCard(
                icon: Icons.shopping_bag_outlined,
                message: emptyMessage,
                action: 'Explore Shop',
                onTap: () => context.push('/shop'),
              ),
            ),
          ),
        ],
      );
    }

    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 110),
      itemCount: orders.length,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, index) {
        final order = orders[index];
        final firstItem = order.items.isNotEmpty ? order.items.first : null;

        return Material(
          color: Colors.transparent,
          child: InkWell(
            onTap: () => context.push('/orders/${order.id}'),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            child: Ink(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Row(
                children: [
                  Container(
                    width: 52,
                    height: 52,
                    decoration: BoxDecoration(
                      borderRadius: BorderRadius.circular(14),
                      gradient: const LinearGradient(
                        colors: [Color(0x334ECDC4), Color(0x33FF6B35)],
                      ),
                    ),
                    child: const Icon(
                      Icons.inventory_2_outlined,
                      color: AppColors.textPrimary,
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          firstItem?.productTitle ?? 'Order',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: AppTextStyles.h3,
                        ),
                        const SizedBox(height: 2),
                        Text(
                          '${order.items.length} item(s)  |  ${formatDate(order.createdAt)}',
                          style: AppTextStyles.labelSmall,
                        ),
                        const SizedBox(height: 6),
                        _StatusBadge(status: order.status),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  Column(
                    crossAxisAlignment: CrossAxisAlignment.end,
                    children: [
                      Text(
                        '${currency(order.currency)}${order.total.toStringAsFixed(0)}',
                        style: AppTextStyles.label.copyWith(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                      const SizedBox(height: 6),
                      const Icon(
                        Icons.chevron_right_rounded,
                        color: AppColors.textMuted,
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        );
      },
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
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: color.withValues(alpha: 0.35)),
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

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({
    required this.icon,
    required this.message,
    required this.action,
    required this.onTap,
  });

  final IconData icon;
  final String message;
  final String action;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: AppSpacing.pagePadding,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textMuted),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
          TextButton(
            onPressed: onTap,
            child: Text(
              action,
              style: AppTextStyles.label.copyWith(
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
