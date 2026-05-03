// Wallet transactions list — Phase 2 Sprint 1.
//
// Tabs: All / Top-ups / Sends / Received / Merchant. Infinite scroll
// grouped by day. A search-filter input matches against label, phone, or
// counterparty.
//
// PRIVACY: search runs client-side over the rows already on the device —
// the query never hits the network.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/data/repositories/wallet_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class WalletTransactionsScreen extends ConsumerStatefulWidget {
  const WalletTransactionsScreen({super.key});

  @override
  ConsumerState<WalletTransactionsScreen> createState() =>
      _WalletTransactionsScreenState();
}

class _TxnTab {
  const _TxnTab({required this.label, this.type, this.direction});

  final String label;
  final String? type;
  final String? direction;
}

const _tabs = <_TxnTab>[
  _TxnTab(label: 'All'),
  _TxnTab(label: 'Top-ups', type: 'top_up'),
  _TxnTab(label: 'Sends', type: 'send'),
  _TxnTab(label: 'Received', type: 'receive'),
  _TxnTab(label: 'Merchant', type: 'merchant_pay'),
];

class _WalletTransactionsScreenState
    extends ConsumerState<WalletTransactionsScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tab;
  String _search = '';

  @override
  void initState() {
    super.initState();
    _tab = TabController(length: _tabs.length, vsync: this);
    _tab.addListener(() {
      if (_tab.indexIsChanging) return;
      setState(() {});
    });
  }

  @override
  void dispose() {
    _tab.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final activeTab = _tabs[_tab.index];
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Transactions', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
          onPressed: () => context.pop(),
        ),
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(96),
          child: Column(
            children: [
              Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: AppSpacing.l,
                  vertical: AppSpacing.s,
                ),
                child: TextField(
                  onChanged: (v) => setState(() => _search = v.trim()),
                  style: AppTextStyles.body,
                  decoration: InputDecoration(
                    prefixIcon: const Icon(Icons.search,
                        color: AppColors.textTertiary),
                    hintText: 'Search transactions',
                    hintStyle: AppTextStyles.body
                        .copyWith(color: AppColors.textGhost),
                    filled: true,
                    fillColor: AppColors.bgCard,
                    isDense: true,
                    border: OutlineInputBorder(
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusMedium),
                      borderSide:
                          const BorderSide(color: AppColors.borderSubtle),
                    ),
                  ),
                ),
              ),
              TabBar(
                controller: _tab,
                isScrollable: true,
                tabAlignment: TabAlignment.start,
                labelColor: AppColors.posttubePrimary,
                unselectedLabelColor: AppColors.textTertiary,
                indicatorColor: AppColors.posttubePrimary,
                tabs: [for (final t in _tabs) Tab(text: t.label)],
              ),
            ],
          ),
        ),
      ),
      body: _TxnList(
        key: ValueKey('${activeTab.type}_${activeTab.direction}'),
        type: activeTab.type,
        direction: activeTab.direction,
        search: _search,
      ),
    );
  }
}

// ─── Paged list with day-grouping + infinite scroll ─────────────────────

class _TxnList extends ConsumerStatefulWidget {
  const _TxnList({
    super.key,
    required this.type,
    required this.direction,
    required this.search,
  });

  final String? type;
  final String? direction;
  final String search;

  @override
  ConsumerState<_TxnList> createState() => _TxnListState();
}

class _TxnListState extends ConsumerState<_TxnList> {
  final _items = <WalletTransaction>[];
  final _scroll = ScrollController();
  String? _cursor;
  bool _loading = false;
  bool _exhausted = false;
  Object? _error;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
    _loadMore(initial: true);
  }

  @override
  void didUpdateWidget(covariant _TxnList old) {
    super.didUpdateWidget(old);
    // Search changes filter client-side; tab/dir changes (handled by
    // ValueKey re-mount) reset entirely.
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (_scroll.position.pixels >
        _scroll.position.maxScrollExtent - 200) {
      _loadMore();
    }
  }

  Future<void> _loadMore({bool initial = false}) async {
    if (_loading || _exhausted) return;
    setState(() => _loading = true);
    try {
      final repo = ref.read(walletRepositoryProvider);
      final page = await repo.getTransactions(
        limit: 30,
        cursor: initial ? null : _cursor,
        type: widget.type,
        direction: widget.direction,
      );
      if (!mounted) return;
      setState(() {
        _items.addAll(page.items);
        _cursor = page.nextCursor;
        _exhausted = page.nextCursor == null;
        _loading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e;
      });
    }
  }

  Future<void> _refresh() async {
    setState(() {
      _items.clear();
      _cursor = null;
      _exhausted = false;
      _error = null;
    });
    await _loadMore(initial: true);
  }

  Iterable<WalletTransaction> get _filtered {
    if (widget.search.isEmpty) return _items;
    final q = widget.search.toLowerCase();
    return _items.where((t) {
      return (t.counterpartyLabel ?? '').toLowerCase().contains(q) ||
          (t.counterpartyPhone ?? '').contains(q) ||
          (t.merchantService ?? '').toLowerCase().contains(q) ||
          t.type.contains(q);
    });
  }

  Map<String, List<WalletTransaction>> _groupByDay() {
    final groups = <String, List<WalletTransaction>>{};
    for (final t in _filtered) {
      final d = t.createdAt.toLocal();
      final key = '${d.year}-${d.month.toString().padLeft(2, '0')}-'
          '${d.day.toString().padLeft(2, '0')}';
      groups.putIfAbsent(key, () => <WalletTransaction>[]).add(t);
    }
    return groups;
  }

  @override
  Widget build(BuildContext context) {
    if (_error != null && _items.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text('Could not load.', style: AppTextStyles.h3),
              const SizedBox(height: AppSpacing.s),
              Text('$_error', style: AppTextStyles.bodySmall),
              const SizedBox(height: AppSpacing.l),
              ElevatedButton(
                onPressed: _refresh,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                ),
                child: const Text('Retry'),
              ),
            ],
          ),
        ),
      );
    }
    if (_items.isEmpty && !_loading) {
      return Center(
        child: Text('No transactions yet.', style: AppTextStyles.bodySmall),
      );
    }

    final groups = _groupByDay();
    final keys = groups.keys.toList()..sort((a, b) => b.compareTo(a));

    return RefreshIndicator(
      color: AppColors.postbookPrimary,
      onRefresh: _refresh,
      child: ListView.builder(
        controller: _scroll,
        padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.l,
          vertical: AppSpacing.s,
        ),
        itemCount: keys.length + 1,
        itemBuilder: (context, index) {
          if (index == keys.length) {
            return Padding(
              padding: const EdgeInsets.all(AppSpacing.l),
              child: Center(
                child: _loading
                    ? const CircularProgressIndicator(
                        color: AppColors.postbookPrimary,
                      )
                    : _exhausted
                        ? Text(
                            'No more transactions.',
                            style: AppTextStyles.bodySmall,
                          )
                        : const SizedBox.shrink(),
              ),
            );
          }
          final dayKey = keys[index];
          final dayItems = groups[dayKey]!;
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.symmetric(
                  vertical: AppSpacing.s,
                ),
                child: Text(
                  _formatDayHeader(dayKey),
                  style: AppTextStyles.labelSmall,
                ),
              ),
              for (final t in dayItems) _TxnRow(txn: t),
            ],
          );
        },
      ),
    );
  }

  String _formatDayHeader(String key) {
    final parts = key.split('-');
    final y = int.parse(parts[0]);
    final m = int.parse(parts[1]);
    final d = int.parse(parts[2]);
    final today = DateTime.now();
    if (today.year == y && today.month == m && today.day == d) return 'Today';
    final yest = today.subtract(const Duration(days: 1));
    if (yest.year == y && yest.month == m && yest.day == d) return 'Yesterday';
    const months = [
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return '$d ${months[m - 1]} $y';
  }
}

class _TxnRow extends StatelessWidget {
  const _TxnRow({required this.txn});

  final WalletTransaction txn;

  @override
  Widget build(BuildContext context) {
    final credit = txn.isCredit;
    return InkWell(
      onTap: () => GoRouter.of(context).push('/wallet/transactions/${txn.id}'),
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: AppSpacing.m),
        child: Row(
          children: [
            CircleAvatar(
              radius: 20,
              backgroundColor: AppColors.bgSecondary,
              child: Icon(
                _icon(txn.type),
                color: AppColors.textSecondary,
                size: 18,
              ),
            ),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    _label(txn),
                    style: AppTextStyles.label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    _statusBadge(txn.status),
                    style: AppTextStyles.bodySmall.copyWith(
                      color: _statusColor(txn.status),
                    ),
                  ),
                ],
              ),
            ),
            Text(
              '${credit ? '+' : '-'}${formatRupees(txn.amountPaise)}',
              style: AppTextStyles.label.copyWith(
                color: credit
                    ? AppColors.statusSuccess
                    : AppColors.textPrimary,
              ),
            ),
          ],
        ),
      ),
    );
  }

  IconData _icon(String t) {
    switch (t) {
      case 'top_up':
        return Icons.account_balance_wallet_outlined;
      case 'send':
        return Icons.north_east;
      case 'receive':
        return Icons.south_west;
      case 'merchant_pay':
        return Icons.shopping_bag_outlined;
      case 'refund':
        return Icons.replay_outlined;
      case 'reversal':
        return Icons.undo;
      default:
        return Icons.swap_horiz;
    }
  }

  String _label(WalletTransaction t) {
    switch (t.type) {
      case 'top_up':
        return 'Added to wallet';
      case 'send':
        return 'Sent to ${t.counterpartyLabel ?? t.counterpartyPhone ?? 'recipient'}';
      case 'receive':
        return 'Received from ${t.counterpartyLabel ?? t.counterpartyPhone ?? 'sender'}';
      case 'merchant_pay':
        return 'Paid to ${t.merchantService ?? 'merchant'}';
      case 'refund':
        return 'Refund';
      case 'reversal':
        return 'Reversed';
      default:
        return t.type;
    }
  }

  String _statusBadge(String s) {
    switch (s) {
      case 'succeeded':
        return 'Success';
      case 'pending':
        return 'Pending';
      case 'failed':
        return 'Failed';
      case 'reversed':
        return 'Reversed';
      default:
        return s;
    }
  }

  Color _statusColor(String s) {
    switch (s) {
      case 'succeeded':
        return AppColors.statusSuccess;
      case 'pending':
        return AppColors.statusWarning;
      case 'failed':
      case 'reversed':
        return AppColors.statusError;
      default:
        return AppColors.textTertiary;
    }
  }
}
