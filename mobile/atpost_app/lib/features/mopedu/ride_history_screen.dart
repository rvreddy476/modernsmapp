// Mopedu — ride history with All / Completed / Cancelled tabs.
//
// Infinite-scroll the paged `myRidesProvider`. Each row routes to the
// summary screen.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RideHistoryScreen extends ConsumerStatefulWidget {
  const RideHistoryScreen({super.key});

  @override
  ConsumerState<RideHistoryScreen> createState() => _RideHistoryScreenState();
}

class _RideHistoryScreenState extends ConsumerState<RideHistoryScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: 3, vsync: this);
  }

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('My rides', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/mopedu'),
        ),
        bottom: TabBar(
          controller: _tabs,
          labelColor: AppColors.postbookPrimary,
          unselectedLabelColor: AppColors.textTertiary,
          indicatorColor: AppColors.postbookPrimary,
          tabs: const [
            Tab(text: 'All'),
            Tab(text: 'Completed'),
            Tab(text: 'Cancelled'),
          ],
        ),
      ),
      body: TabBarView(
        controller: _tabs,
        children: const [
          _RideList(filter: _Filter.all),
          _RideList(filter: _Filter.completed),
          _RideList(filter: _Filter.cancelled),
        ],
      ),
    );
  }
}

enum _Filter { all, completed, cancelled }

class _RideList extends ConsumerStatefulWidget {
  const _RideList({required this.filter});

  final _Filter filter;

  @override
  ConsumerState<_RideList> createState() => _RideListState();
}

class _RideListState extends ConsumerState<_RideList> {
  final _ctl = ScrollController();
  final _items = <Ride>[];
  String? _cursor;
  bool _loading = false;
  bool _exhausted = false;
  Object? _error;

  @override
  void initState() {
    super.initState();
    _ctl.addListener(_maybeMore);
    WidgetsBinding.instance.addPostFrameCallback((_) => _loadMore());
  }

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  void _maybeMore() {
    if (!_ctl.hasClients) return;
    if (_ctl.position.pixels >= _ctl.position.maxScrollExtent - 400) {
      _loadMore();
    }
  }

  Future<void> _loadMore() async {
    if (_loading || _exhausted) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final page = await ref.read(
        myRidesProvider(MyRidesQuery(cursor: _cursor)).future,
      );
      setState(() {
        _items.addAll(page.items);
        _cursor = page.nextCursor;
        if (_cursor == null) _exhausted = true;
        _loading = false;
      });
    } catch (e) {
      setState(() {
        _error = e;
        _loading = false;
      });
    }
  }

  Future<void> _refresh() async {
    setState(() {
      _items.clear();
      _cursor = null;
      _exhausted = false;
    });
    await _loadMore();
  }

  Iterable<Ride> get _filtered {
    switch (widget.filter) {
      case _Filter.all:
        return _items;
      case _Filter.completed:
        return _items.where((r) => r.status == RideStatus.completed);
      case _Filter.cancelled:
        return _items.where((r) => r.status.startsWith('cancelled_'));
    }
  }

  @override
  Widget build(BuildContext context) {
    final visible = _filtered.toList();
    return RefreshIndicator(
      onRefresh: _refresh,
      child: ListView.separated(
        controller: _ctl,
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
        itemCount: visible.length + 1,
        separatorBuilder: (_, _) => const SizedBox(height: 8),
        itemBuilder: (context, i) {
          if (i == visible.length) {
            if (_loading) {
              return const Padding(
                padding: EdgeInsets.all(16),
                child: Center(child: CircularProgressIndicator()),
              );
            }
            if (_error != null) {
              return Padding(
                padding: const EdgeInsets.all(16),
                child: Text(
                  'Could not load rides.',
                  style: AppTextStyles.bodySmall,
                ),
              );
            }
            if (_exhausted && visible.isEmpty) {
              return Padding(
                padding: const EdgeInsets.all(24),
                child: Center(
                  child: Text(
                    'No rides yet.',
                    style: AppTextStyles.bodySmall,
                  ),
                ),
              );
            }
            return const SizedBox.shrink();
          }
          return _RideRow(ride: visible[i]);
        },
      ),
    );
  }
}

class _RideRow extends StatelessWidget {
  const _RideRow({required this.ride});

  final Ride ride;

  static const _icons = <String, IconData>{
    VehicleType.bike: Icons.two_wheeler,
    VehicleType.auto: Icons.electric_rickshaw,
    VehicleType.miniCab: Icons.directions_car,
    VehicleType.sedan: Icons.directions_car_filled,
    VehicleType.suv: Icons.airport_shuttle,
    VehicleType.premium: Icons.local_taxi,
  };

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: () => context.push('/mopedu/rides/${ride.id}'),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            Container(
              width: 40,
              height: 40,
              decoration: BoxDecoration(
                color: AppColors.bgTertiary,
                borderRadius: BorderRadius.circular(10),
              ),
              child: Icon(
                _icons[ride.vehicleType] ?? Icons.directions_car,
                color: AppColors.postbookPrimary,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    ride.drop.displayName,
                    style: AppTextStyles.label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    ride.requestedAt.toLocal().toString().split('.').first,
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  formatRupees(
                    ride.finalFarePaise ?? ride.fareEstimatePaise,
                  ),
                  style: AppTextStyles.label,
                ),
                const SizedBox(height: 2),
                _StatusChip(status: ride.status),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = _styleFor(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(99),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }

  (String, Color) _styleFor(String s) {
    if (s == RideStatus.completed) {
      return ('Completed', AppColors.statusSuccess);
    }
    if (s.startsWith('cancelled_')) {
      return ('Cancelled', AppColors.statusError);
    }
    if (s == RideStatus.expired) {
      return ('Expired', AppColors.statusError);
    }
    if (s == RideStatus.failed) {
      return ('Failed', AppColors.statusError);
    }
    return ('In progress', AppColors.posttubePrimary);
  }
}
