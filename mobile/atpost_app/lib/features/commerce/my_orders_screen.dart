// My orders — Sprint 2.
//
// Tabs: All / Active / Delivered / Returned / Cancelled. Each row renders
// a thumbnail (or a placeholder when the order list endpoint doesn't
// embed item summaries — see COMMERCE_RECON; commerce-service today
// returns ids + totals + status). The "Track" CTA on active orders
// pushes the order detail screen.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class MyOrdersScreen extends ConsumerStatefulWidget {
  const MyOrdersScreen({super.key});

  @override
  ConsumerState<MyOrdersScreen> createState() => _MyOrdersScreenState();
}

enum _OrderTab { all, active, delivered, returned, cancelled }

class _MyOrdersScreenState extends ConsumerState<MyOrdersScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: _OrderTab.values.length, vsync: this);
  }

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  List<OrderListItem> _filter(List<OrderListItem> all, _OrderTab tab) {
    switch (tab) {
      case _OrderTab.all:
        return all;
      case _OrderTab.active:
        return all.where((o) => o.isActive).toList();
      case _OrderTab.delivered:
        return all.where((o) => o.isDelivered).toList();
      case _OrderTab.returned:
        return all.where((o) => o.isReturned).toList();
      case _OrderTab.cancelled:
        return all.where((o) => o.isCancelled).toList();
    }
  }

  @override
  Widget build(BuildContext context) {
    final ordersAsync = ref.watch(myOrdersProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('My orders', style: AppTextStyles.h2),
        bottom: TabBar(
          controller: _tabs,
          isScrollable: true,
          labelColor: AppColors.postbookPrimary,
          unselectedLabelColor: AppColors.textTertiary,
          indicatorColor: AppColors.postbookPrimary,
          labelStyle: AppTextStyles.label,
          tabs: const [
            Tab(text: 'All'),
            Tab(text: 'Active'),
            Tab(text: 'Delivered'),
            Tab(text: 'Returned'),
            Tab(text: 'Cancelled'),
          ],
        ),
      ),
      body: ordersAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load orders.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (all) {
          return TabBarView(
            controller: _tabs,
            children: _OrderTab.values.map((tab) {
              final list = _filter(all, tab);
              return RefreshIndicator(
                onRefresh: () async => ref.invalidate(myOrdersProvider),
                color: AppColors.postbookPrimary,
                child: list.isEmpty
                    ? _EmptyTab(tab: tab)
                    : ListView.separated(
                        padding: const EdgeInsets.all(AppSpacing.l),
                        itemCount: list.length,
                        separatorBuilder: (_, _) =>
                            const SizedBox(height: AppSpacing.l),
                        itemBuilder: (ctx, i) =>
                            _OrderRow(order: list[i]),
                      ),
              );
            }).toList(),
          );
        },
      ),
    );
  }
}

class _EmptyTab extends StatelessWidget {
  const _EmptyTab({required this.tab});

  final _OrderTab tab;

  @override
  Widget build(BuildContext context) {
    String label;
    switch (tab) {
      case _OrderTab.all:
        label = 'No orders yet. Browse the storefront to get started.';
        break;
      case _OrderTab.active:
        label = 'No orders are in transit right now.';
        break;
      case _OrderTab.delivered:
        label = 'No delivered orders yet.';
        break;
      case _OrderTab.returned:
        label = 'You haven\'t returned anything.';
        break;
      case _OrderTab.cancelled:
        label = 'No cancelled orders.';
        break;
    }
    return ListView(
      padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.xxl, vertical: AppSpacing.xxl * 2),
      children: [
        const Icon(Icons.receipt_long_outlined,
            size: 56, color: AppColors.textGhost),
        const SizedBox(height: AppSpacing.l),
        Text(
          label,
          style: AppTextStyles.body,
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: AppSpacing.xxl),
        Center(
          child: OutlinedButton(
            onPressed: () => GoRouter.of(context).push('/commerce'),
            child: const Text('Browse storefront'),
          ),
        ),
      ],
    );
  }
}

class _OrderRow extends StatelessWidget {
  const _OrderRow({required this.order});

  final OrderListItem order;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () =>
          GoRouter.of(context).push('/commerce/orders/${order.id}'),
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: Container(
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
                width: 64,
                height: 64,
                child: order.primaryThumbUrl == null
                    ? Container(
                        color: AppColors.bgSecondary,
                        child: const Icon(Icons.shopping_bag_outlined,
                            color: AppColors.textGhost),
                      )
                    : Image.network(
                        order.primaryThumbUrl!,
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
                    order.firstItemTitle ??
                        (order.itemCount > 0
                            ? '${order.itemCount} item${order.itemCount > 1 ? 's' : ''}'
                            : 'Order'),
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.textPrimary,
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    order.orderNumber.isEmpty
                        ? 'Order ${order.id}'
                        : 'Order ${order.orderNumber}',
                    style: AppTextStyles.bodySmall,
                  ),
                  const SizedBox(height: AppSpacing.s),
                  Row(
                    children: [
                      _StatusPill(status: order.status),
                      const SizedBox(width: AppSpacing.m),
                      Text(
                        _fmtDate(order.placedAt),
                        style: AppTextStyles.bodySmall,
                      ),
                    ],
                  ),
                  const SizedBox(height: AppSpacing.s),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(
                        'Rs. ${order.amountGrand.toStringAsFixed(0)}',
                        style: AppTextStyles.h3,
                      ),
                      if (order.isActive)
                        TextButton(
                          onPressed: () => GoRouter.of(context)
                              .push('/commerce/orders/${order.id}'),
                          child: const Text('Track'),
                        ),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
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
      case 'returned':
      case 'refunded':
        color = AppColors.accentPurple;
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
          horizontal: AppSpacing.m, vertical: 2),
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
