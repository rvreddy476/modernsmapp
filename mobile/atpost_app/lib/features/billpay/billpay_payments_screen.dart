// Bill-pay payments history — Phase 2.
//
// Tabbed list (All / Recharges / Bills / Failed) with infinite scroll
// grouped by month. Each row shows provider, masked identifier, amount,
// date, and a status pill.
//
// "Recharges" filter: payments whose `accountId` is null (recharges have no
// saved account). "Bills" filter: payments with an `accountId`.
//
// PRIVACY: rows render `payment.maskedIdentifier`, never the raw identifier.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/repositories/billpay_repository.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

enum _Filter { all, recharges, bills, failed }

class BillPayPaymentsScreen extends ConsumerStatefulWidget {
  const BillPayPaymentsScreen({super.key});

  @override
  ConsumerState<BillPayPaymentsScreen> createState() =>
      _BillPayPaymentsScreenState();
}

class _BillPayPaymentsScreenState extends ConsumerState<BillPayPaymentsScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabController;
  final ScrollController _scrollController = ScrollController();
  final List<BillPayment> _items = [];
  String? _nextCursor;
  bool _loading = false;
  bool _initialLoaded = false;
  Object? _error;
  _Filter _filter = _Filter.all;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 4, vsync: this);
    _tabController.addListener(_onTabChanged);
    _scrollController.addListener(_onScroll);
    _load(reset: true);
  }

  @override
  void dispose() {
    _tabController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _onTabChanged() {
    if (!_tabController.indexIsChanging &&
        _tabController.index != _tabController.previousIndex) {
      _filter = _Filter.values[_tabController.index];
      _load(reset: true);
    } else {
      // Trigger filter on every settle.
      final next = _Filter.values[_tabController.index];
      if (next != _filter) {
        _filter = next;
        _load(reset: true);
      }
    }
  }

  void _onScroll() {
    if (_loading || _nextCursor == null) return;
    if (_scrollController.position.pixels >=
        _scrollController.position.maxScrollExtent - 200) {
      _load();
    }
  }

  Future<void> _load({bool reset = false}) async {
    if (_loading) return;
    setState(() {
      _loading = true;
      _error = null;
      if (reset) {
        _items.clear();
        _nextCursor = null;
        _initialLoaded = false;
      }
    });
    try {
      final repo = ref.read(billpayRepositoryProvider);
      final page = await repo.getPayments(
        limit: 20,
        cursor: reset ? null : _nextCursor,
        status: _filter == _Filter.failed ? 'failed' : null,
      );
      if (!mounted) return;
      setState(() {
        _items.addAll(page.items);
        _nextCursor = page.nextCursor;
        _loading = false;
        _initialLoaded = true;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e;
        _initialLoaded = true;
      });
    }
  }

  List<BillPayment> _filtered() {
    switch (_filter) {
      case _Filter.recharges:
        return _items
            .where((p) => p.accountId == null || p.accountId!.isEmpty)
            .toList();
      case _Filter.bills:
        return _items
            .where((p) => p.accountId != null && p.accountId!.isNotEmpty)
            .toList();
      case _Filter.failed:
        return _items.where((p) => p.isFailed).toList();
      case _Filter.all:
        return _items;
    }
  }

  @override
  Widget build(BuildContext context) {
    final filtered = _filtered();
    final groups = _groupByMonth(filtered);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Payment history', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.pop(),
        ),
        bottom: TabBar(
          controller: _tabController,
          indicatorColor: AppColors.postbookPrimary,
          labelColor: AppColors.textPrimary,
          unselectedLabelColor: AppColors.textTertiary,
          tabs: const [
            Tab(text: 'All'),
            Tab(text: 'Recharges'),
            Tab(text: 'Bills'),
            Tab(text: 'Failed'),
          ],
        ),
      ),
      body: RefreshIndicator(
        onRefresh: () => _load(reset: true),
        color: AppColors.postbookPrimary,
        child: !_initialLoaded
            ? const Center(
                child: CircularProgressIndicator(
                  color: AppColors.postbookPrimary,
                ),
              )
            : _error != null
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(AppSpacing.xxl),
                      child: Text(
                        'Could not load payments.',
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.statusError,
                        ),
                      ),
                    ),
                  )
                : filtered.isEmpty
                    ? Center(
                        child: Text(
                          'No payments here yet.',
                          style: AppTextStyles.bodySmall,
                        ),
                      )
                    : ListView.builder(
                        controller: _scrollController,
                        itemCount: groups.length + (_nextCursor != null ? 1 : 0),
                        itemBuilder: (_, idx) {
                          if (idx == groups.length) {
                            return const Padding(
                              padding: EdgeInsets.all(AppSpacing.xxl),
                              child: Center(
                                child: CircularProgressIndicator(
                                  color: AppColors.postbookPrimary,
                                ),
                              ),
                            );
                          }
                          final g = groups[idx];
                          return _MonthSection(
                            title: g.title,
                            items: g.items,
                          );
                        },
                      ),
      ),
    );
  }
}

class _MonthGroup {
  _MonthGroup(this.title, this.items);
  final String title;
  final List<BillPayment> items;
}

List<_MonthGroup> _groupByMonth(List<BillPayment> items) {
  const months = [
    'January', 'February', 'March', 'April', 'May', 'June',
    'July', 'August', 'September', 'October', 'November', 'December',
  ];
  final map = <String, List<BillPayment>>{};
  for (final p in items) {
    final key = '${p.createdAt.year}-${p.createdAt.month}';
    map.putIfAbsent(key, () => []).add(p);
  }
  final keys = map.keys.toList()
    ..sort((a, b) {
      final ap = a.split('-').map(int.parse).toList();
      final bp = b.split('-').map(int.parse).toList();
      if (ap[0] != bp[0]) return bp[0].compareTo(ap[0]);
      return bp[1].compareTo(ap[1]);
    });
  return [
    for (final k in keys)
      _MonthGroup(
        '${months[int.parse(k.split('-')[1]) - 1]} ${k.split('-')[0]}',
        map[k]!,
      ),
  ];
}

class _MonthSection extends StatelessWidget {
  const _MonthSection({required this.title, required this.items});

  final String title;
  final List<BillPayment> items;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(
            AppSpacing.l,
            AppSpacing.l,
            AppSpacing.l,
            AppSpacing.s,
          ),
          child: Text(
            title,
            style: AppTextStyles.labelSmall.copyWith(
              color: AppColors.textTertiary,
            ),
          ),
        ),
        for (final p in items) _PaymentRow(payment: p),
      ],
    );
  }
}

class _PaymentRow extends StatelessWidget {
  const _PaymentRow({required this.payment});

  final BillPayment payment;

  Color _statusColor() {
    if (payment.isSucceeded) return AppColors.statusSuccess;
    if (payment.isFailed) return AppColors.statusError;
    if (payment.isRefunded) return AppColors.accentPurple;
    return AppColors.statusWarning;
  }

  String _statusLabel() {
    if (payment.isSucceeded) return 'Paid';
    if (payment.isFailed) return 'Failed';
    if (payment.isRefunded) return 'Refunded';
    return 'Pending';
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => context.push('/billpay/payments/${payment.id}'),
      child: Padding(
        padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.l,
          vertical: AppSpacing.l,
        ),
        child: Row(
          children: [
            Container(
              width: 36,
              height: 36,
              decoration: BoxDecoration(
                color: AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(8),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: const Icon(
                Icons.receipt_long_rounded,
                color: AppColors.textTertiary,
                size: 18,
              ),
            ),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    payment.providerName,
                    style: AppTextStyles.h3,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    payment.maskedIdentifier ?? _fmtDate(payment.createdAt),
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  formatRupees(payment.amountPaise),
                  style: AppTextStyles.h3,
                ),
                const SizedBox(height: 2),
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: AppSpacing.s,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: _statusColor().withAlpha(40),
                    borderRadius: BorderRadius.circular(6),
                  ),
                  child: Text(
                    _statusLabel(),
                    style: AppTextStyles.labelTiny.copyWith(
                      color: _statusColor(),
                    ),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

String _fmtDate(DateTime d) {
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  return '${d.day} ${months[d.month - 1]}';
}
