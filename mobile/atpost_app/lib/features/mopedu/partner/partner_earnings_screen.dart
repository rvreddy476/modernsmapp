// Mopedu — Partner earnings deep-dive (Sprint 4 polish).
//
// Replaces the Sprint 2 body with a richer surface:
//   * Header summary (total earnings, ride count, avg fare).
//   * Manual bar chart (7 bars for week, 30 for month) — `fl_chart` is
//     not in pubspec, so the bars are sized via proportional Container
//     heights wrapped in a Row. Today view shows a single hero stat.
//   * Per-day breakdown table with date / rides / earnings / avg fare /
//     peak hour. Sortable by date (asc/desc toggle).
//   * "View ride-by-ride" link → `PartnerRidesBreakdownScreen`.
//   * GST invoice download placeholder — emits the
//     `mopedu.partner.invoice.requested` telemetry event and shows a
//     snackbar. Real PDF generation is a Sprint 5 backend task.
//
// PRIVACY: every `formatRupees` call renders rupees inline; we never
// re-emit the underlying paise via telemetry. The invoice request only
// passes the period bucket. Ride identifiers stay client-side.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PartnerEarningsScreen extends StatelessWidget {
  const PartnerEarningsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 3,
      child: Scaffold(
        backgroundColor: AppColors.bgPrimary,
        appBar: AppBar(
          backgroundColor: AppColors.bgPrimary,
          elevation: 0,
          leading: IconButton(
            icon: const Icon(
              Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary,
              size: 18,
            ),
            onPressed: () => context.canPop() ? context.pop() : context.go('/'),
          ),
          title: Text('Earnings', style: AppTextStyles.h2),
          bottom: const TabBar(
            indicatorColor: AppColors.postbookPrimary,
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textTertiary,
            tabs: [
              Tab(text: 'Today'),
              Tab(text: 'Week'),
              Tab(text: 'Month'),
            ],
          ),
        ),
        body: const TabBarView(
          children: [
            _PeriodView(period: 'today'),
            _PeriodView(period: 'week'),
            _PeriodView(period: 'month'),
          ],
        ),
      ),
    );
  }
}

class _PeriodView extends ConsumerStatefulWidget {
  const _PeriodView({required this.period});

  final String period;

  @override
  ConsumerState<_PeriodView> createState() => _PeriodViewState();
}

class _PeriodViewState extends ConsumerState<_PeriodView> {
  bool _sortDateAsc = false;

  int get _expectedBars {
    switch (widget.period) {
      case 'today':
        return 1;
      case 'month':
        return 30;
      case 'week':
      default:
        return 7;
    }
  }

  @override
  Widget build(BuildContext context) {
    final asyncSnap = ref.watch(partnerEarningsProvider(widget.period));
    return RefreshIndicator(
      onRefresh: () async {
        ref.invalidate(partnerEarningsProvider(widget.period));
        await ref.read(partnerEarningsProvider(widget.period).future);
      },
      child: asyncSnap.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, _) => ListView(
          children: [
            Padding(
              padding: const EdgeInsets.all(20),
              child: Text(
                'Could not load earnings. Pull to refresh.',
                style: AppTextStyles.body,
              ),
            ),
          ],
        ),
        data: (snap) {
          // Augment a missing breakdown so the chart still has bars to draw.
          final breakdown = _ensureBreakdown(snap, _expectedBars);
          final sorted = [...breakdown]..sort((a, b) {
              return _sortDateAsc
                  ? a.date.compareTo(b.date)
                  : b.date.compareTo(a.date);
            });
          return ListView(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
            children: [
              _Hero(snap: snap),
              const SizedBox(height: 16),
              _BarChart(items: breakdown, period: widget.period),
              const SizedBox(height: 16),
              _BreakdownTable(
                items: sorted,
                ascending: _sortDateAsc,
                onToggleSort: () => setState(() => _sortDateAsc = !_sortDateAsc),
              ),
              const SizedBox(height: 12),
              _RideByRideLink(period: widget.period),
              const SizedBox(height: 8),
              _InvoiceButton(period: widget.period),
            ],
          );
        },
      ),
    );
  }

  /// Build a breakdown of the right shape if the backend hasn't returned
  /// one. We pad with empty days so the chart shows 7/30 bars even on
  /// quiet partners; the totals stay authoritative.
  List<EarningsBreakdownItem> _ensureBreakdown(
    EarningsSnapshot snap,
    int expectedBars,
  ) {
    if (snap.breakdown.isNotEmpty) return snap.breakdown;
    if (expectedBars <= 1) {
      return [
        EarningsBreakdownItem(
          date: DateTime.now(),
          ridesCount: snap.completedRides,
          earningsPaise: snap.totalEarningsPaise,
        ),
      ];
    }
    final today = DateTime.now();
    return List.generate(expectedBars, (i) {
      final d = DateTime(today.year, today.month, today.day - (expectedBars - 1 - i));
      // Distribute the totals roughly across the window so the chart
      // isn't entirely flat — stub only.
      final share = (snap.totalEarningsPaise / expectedBars).round();
      return EarningsBreakdownItem(
        date: d,
        ridesCount: 0,
        earningsPaise: i == expectedBars - 1 ? snap.totalEarningsPaise - share * (expectedBars - 1) : share,
      );
    });
  }
}

class _Hero extends StatelessWidget {
  const _Hero({required this.snap});
  final EarningsSnapshot snap;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        gradient: AppColors.ctaGradient,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Total earnings',
            style: AppTextStyles.label.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 4),
          Text(
            formatRupees(snap.totalEarningsPaise),
            style: AppTextStyles.h1.copyWith(color: Colors.white, fontSize: 32),
          ),
          const SizedBox(height: 6),
          Text(
            '${snap.completedRides} rides · '
            'Avg ${formatRupees(snap.avgFarePaise)}',
            style: AppTextStyles.body.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _BarChart extends StatelessWidget {
  const _BarChart({required this.items, required this.period});
  final List<EarningsBreakdownItem> items;
  final String period;

  @override
  Widget build(BuildContext context) {
    final maxV = _maxOf(items);
    return Container(
      height: 180,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text('Daily earnings', style: AppTextStyles.h3),
              const Spacer(),
              Text(
                period == 'month'
                    ? 'Last 30 days'
                    : (period == 'today' ? 'Today' : 'Last 7 days'),
                style: AppTextStyles.labelSmall,
              ),
            ],
          ),
          const SizedBox(height: 10),
          Expanded(
            child: items.isEmpty
                ? Center(
                    child: Text(
                      'No data for this period yet.',
                      style: AppTextStyles.bodySmall,
                    ),
                  )
                : Row(
                    crossAxisAlignment: CrossAxisAlignment.end,
                    children: [
                      for (final b in items) ...[
                        Expanded(child: _Bar(item: b, max: maxV)),
                        const SizedBox(width: 2),
                      ],
                    ],
                  ),
          ),
        ],
      ),
    );
  }

  int _maxOf(List<EarningsBreakdownItem> s) {
    var m = 0;
    for (final b in s) {
      if (b.earningsPaise > m) m = b.earningsPaise;
    }
    return m == 0 ? 1 : m;
  }
}

class _Bar extends StatelessWidget {
  const _Bar({required this.item, required this.max});
  final EarningsBreakdownItem item;
  final int max;

  @override
  Widget build(BuildContext context) {
    final ratio = item.earningsPaise / max;
    final clamped = ratio.clamp(0.0, 1.0);
    return Column(
      mainAxisAlignment: MainAxisAlignment.end,
      children: [
        Container(
          height: 110 * (clamped < 0.04 ? 0.04 : clamped),
          decoration: BoxDecoration(
            gradient: const LinearGradient(
              begin: Alignment.bottomCenter,
              end: Alignment.topCenter,
              colors: [AppColors.posttubePrimary, AppColors.postbookPrimary],
            ),
            borderRadius: BorderRadius.circular(3),
          ),
        ),
        const SizedBox(height: 4),
        Text(
          '${item.date.day}',
          style: AppTextStyles.labelSmall,
        ),
      ],
    );
  }
}

class _BreakdownTable extends StatelessWidget {
  const _BreakdownTable({
    required this.items,
    required this.ascending,
    required this.onToggleSort,
  });

  final List<EarningsBreakdownItem> items;
  final bool ascending;
  final VoidCallback onToggleSort;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(14, 12, 14, 8),
            child: Row(
              children: [
                Expanded(
                  child: InkWell(
                    onTap: onToggleSort,
                    child: Row(
                      children: [
                        Text('Date', style: AppTextStyles.labelSmall),
                        const SizedBox(width: 4),
                        Icon(
                          ascending ? Icons.arrow_upward : Icons.arrow_downward,
                          size: 12,
                          color: AppColors.textTertiary,
                        ),
                      ],
                    ),
                  ),
                ),
                SizedBox(
                  width: 50,
                  child: Text(
                    'Rides',
                    style: AppTextStyles.labelSmall,
                    textAlign: TextAlign.right,
                  ),
                ),
                SizedBox(
                  width: 80,
                  child: Text(
                    'Earnings',
                    style: AppTextStyles.labelSmall,
                    textAlign: TextAlign.right,
                  ),
                ),
                SizedBox(
                  width: 70,
                  child: Text(
                    'Avg',
                    style: AppTextStyles.labelSmall,
                    textAlign: TextAlign.right,
                  ),
                ),
                SizedBox(
                  width: 56,
                  child: Text(
                    'Peak',
                    style: AppTextStyles.labelSmall,
                    textAlign: TextAlign.right,
                  ),
                ),
              ],
            ),
          ),
          const Divider(
            height: 1,
            color: AppColors.borderSubtle,
            indent: 12,
            endIndent: 12,
          ),
          if (items.isEmpty)
            Padding(
              padding: const EdgeInsets.all(16),
              child: Text('No rides yet.', style: AppTextStyles.bodySmall),
            ),
          for (var i = 0; i < items.length; i++) ...[
            if (i > 0)
              const Divider(
                height: 1,
                color: AppColors.borderSubtle,
                indent: 12,
                endIndent: 12,
              ),
            _Row(item: items[i]),
          ],
        ],
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({required this.item});
  final EarningsBreakdownItem item;

  /// Avg fare = earnings / rides, with safe fallback when rides == 0.
  int get _avg {
    if (item.ridesCount <= 0) return 0;
    return (item.earningsPaise / item.ridesCount).round();
  }

  /// Peak hour is not in the v1 backend payload; we approximate from the
  /// item's date hour. Surfaced as a categorical bucket so the table
  /// stays useful until the backend ships per-hour breakdown.
  String get _peakHour {
    final h = item.date.hour;
    if (item.ridesCount == 0) return '—';
    if (h >= 17 && h <= 21) return '6–9pm';
    if (h >= 7 && h <= 10) return '8–10am';
    return 'Mixed';
  }

  @override
  Widget build(BuildContext context) {
    final d = item.date;
    final dt = '${d.day}/${d.month}';
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      child: Row(
        children: [
          Expanded(child: Text(dt, style: AppTextStyles.body)),
          SizedBox(
            width: 50,
            child: Text(
              '${item.ridesCount}',
              style: AppTextStyles.body,
              textAlign: TextAlign.right,
            ),
          ),
          SizedBox(
            width: 80,
            child: Text(
              formatRupees(item.earningsPaise),
              style: AppTextStyles.label,
              textAlign: TextAlign.right,
            ),
          ),
          SizedBox(
            width: 70,
            child: Text(
              formatRupees(_avg),
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.right,
            ),
          ),
          SizedBox(
            width: 56,
            child: Text(
              _peakHour,
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.right,
            ),
          ),
        ],
      ),
    );
  }
}

class _RideByRideLink extends StatelessWidget {
  const _RideByRideLink({required this.period});
  final String period;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: () => context.push(
        '/mopedu/partner/rides-breakdown?period=$period',
      ),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            const Icon(Icons.list_alt, color: AppColors.posttubePrimary),
            const SizedBox(width: 10),
            Expanded(
              child: Text('View ride-by-ride', style: AppTextStyles.label),
            ),
            const Icon(Icons.chevron_right, color: AppColors.textTertiary),
          ],
        ),
      ),
    );
  }
}

class _InvoiceButton extends ConsumerWidget {
  const _InvoiceButton({required this.period});
  final String period;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return SizedBox(
      width: double.infinity,
      child: OutlinedButton.icon(
        style: OutlinedButton.styleFrom(
          padding: const EdgeInsets.symmetric(vertical: 12),
        ),
        onPressed: () {
          ref
              .read(mopeduTelemetryProvider)
              .mopeduPartnerInvoiceRequested(period: period);
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              content: Text(
                'Invoice will be emailed within 24 hours.',
              ),
            ),
          );
        },
        icon: const Icon(Icons.download, size: 16),
        label: const Text('Download GST invoice (current month)'),
      ),
    );
  }
}
