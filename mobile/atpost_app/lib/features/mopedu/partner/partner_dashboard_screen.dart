// Partner dashboard (Sprint 2).
//
// Visible only when partner profile exists & status=approved. Shows the
// online toggle, today's earnings, plan card, key metrics, and recent
// rides. The online toggle starts/stops the location streamer and runs
// eligibility validation client-side (KYC + vehicle + subscription).
//
// Telemetry: only the toggle fires `mopedu.partner.online.toggled`. We
// do NOT log earnings; the dashboard provider returns paise but the UI
// renders via `formatRupees` and never re-emits the value.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/features/mopedu/partner/ride_request_modal.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class PartnerDashboardScreen extends ConsumerStatefulWidget {
  const PartnerDashboardScreen({super.key});

  @override
  ConsumerState<PartnerDashboardScreen> createState() =>
      _PartnerDashboardScreenState();
}

class _PartnerDashboardScreenState
    extends ConsumerState<PartnerDashboardScreen> with WidgetsBindingObserver {
  bool _modalOpen = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    final svc = ref.read(partnerLocationServiceProvider);
    svc.setForeground(state == AppLifecycleState.resumed);
  }

  @override
  Widget build(BuildContext context) {
    // Listen for incoming offers and pop the request modal once the first
    // pending offer arrives.
    ref.listen<AsyncValue<List<RideOffer>>>(incomingOffersProvider,
        (prev, next) {
      next.whenData((offers) {
        if (_modalOpen) return;
        final first = offers.firstWhere(
          (o) => o.status == 'pending',
          orElse: () => RideOffer(
            id: '',
            rideId: '',
            partnerId: '',
            score: 0,
            distanceKm: 0,
            expiresAt: DateTime.now(),
            status: 'expired',
            vehicleType: '',
            pickupAddress: '',
            dropAddress: '',
            fareEstimatePaise: 0,
            etaToPickupSeconds: 0,
          ),
        );
        if (first.id.isEmpty) return;
        _modalOpen = true;
        // Telemetry: vehicle type only.
        ref
            .read(mopeduTelemetryProvider)
            .mopeduPartnerOfferReceived(vehicleType: first.vehicleType);
        Navigator.of(context, rootNavigator: true).push(
          MaterialPageRoute(
            fullscreenDialog: true,
            builder: (_) => RideRequestModal(offer: first),
          ),
        ).whenComplete(() => _modalOpen = false);
      });
    });

    final asyncProfile = ref.watch(myPartnerProfileProvider);
    return asyncProfile.when(
      loading: () => const Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(child: CircularProgressIndicator()),
      ),
      error: (_, _) => const Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(child: Text('Could not load profile.')),
      ),
      data: (partner) => _DashboardScaffold(partner: partner),
    );
  }
}

class _DashboardScaffold extends ConsumerWidget {
  const _DashboardScaffold({required this.partner});

  final RiderPartner? partner;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (partner == null) {
      return Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('Partner profile not found.', style: AppTextStyles.h3),
                const SizedBox(height: 8),
                ElevatedButton(
                  onPressed: () => context.push('/mopedu/partner'),
                  child: const Text('Become a partner'),
                ),
              ],
            ),
          ),
        ),
      );
    }
    if (partner!.status != PartnerStatus.approved) {
      return Scaffold(
        backgroundColor: AppColors.bgPrimary,
        appBar: AppBar(
          backgroundColor: AppColors.bgPrimary,
          elevation: 0,
          title: Text('Partner mode', style: AppTextStyles.h2),
        ),
        body: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Icon(Icons.hourglass_top, color: AppColors.statusWarning),
              const SizedBox(height: 8),
              Text(
                'Status: ${partner!.status.label}',
                style: AppTextStyles.h2,
              ),
              const SizedBox(height: 4),
              Text(
                'You can drive once your profile is approved. Typical SLA '
                'is 24 hours from KYC submission.',
                style: AppTextStyles.body,
              ),
              const SizedBox(height: 20),
              ElevatedButton(
                onPressed: () => context.push('/mopedu/partner/onboarding'),
                child: const Text('Resume onboarding'),
              ),
            ],
          ),
        ),
      );
    }

    return _ApprovedDashboard(partner: partner!);
  }
}

class _ApprovedDashboard extends ConsumerWidget {
  const _ApprovedDashboard({required this.partner});

  final RiderPartner partner;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final dash = ref.watch(partnerDashboardProvider);
    final online = ref.watch(partnerOnlineStateProvider);
    final sub = ref.watch(mySubscriptionProvider);

    return Scaffold(
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
        title: Text('Partner mode', style: AppTextStyles.h2),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(partnerDashboardProvider);
          ref.invalidate(mySubscriptionProvider);
          await ref.read(partnerDashboardProvider.future);
        },
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
          children: [
            _OnlineToggleCard(state: online, partner: partner, sub: sub.value),
            const SizedBox(height: 12),
            dash.when(
              loading: () => const Padding(
                padding: EdgeInsets.symmetric(vertical: 24),
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (_, _) => Padding(
                padding: const EdgeInsets.all(16),
                child: Text(
                  'Could not load dashboard. Pull to refresh.',
                  style: AppTextStyles.bodySmall,
                ),
              ),
              data: (d) => Column(
                children: [
                  _EarningsHero(dash: d),
                  const SizedBox(height: 12),
                  const _ExpiringDocsBanners(),
                  _PlanCard(dash: d),
                  const SizedBox(height: 12),
                  _MetricsRow(dash: d),
                  const SizedBox(height: 12),
                  const _EarningsCta(),
                  const SizedBox(height: 8),
                  const _ReferralCta(),
                  const SizedBox(height: 16),
                  const _RecentRides(),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _OnlineToggleCard extends ConsumerWidget {
  const _OnlineToggleCard({
    required this.state,
    required this.partner,
    required this.sub,
  });

  final PartnerOnlineState state;
  final RiderPartner partner;
  final PartnerSubscription? sub;

  String? _eligibilityError() {
    if (partner.kycStatus != VerificationStatus.approved) {
      return 'KYC is not approved yet.';
    }
    if (sub == null) return 'No active subscription.';
    if (!sub!.status.canTakeRides) {
      return 'Subscription is ${sub!.status.label.toLowerCase()}. Please renew.';
    }
    return null;
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final issue = _eligibilityError();
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: state.isOnline
            ? AppColors.statusSuccess.withValues(alpha: 0.12)
            : AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(
          color: state.isOnline
              ? AppColors.statusSuccess
              : AppColors.borderSubtle,
          width: state.isOnline ? 1.5 : 1,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                state.isOnline ? Icons.online_prediction : Icons.power_settings_new,
                color: state.isOnline
                    ? AppColors.statusSuccess
                    : AppColors.textTertiary,
                size: 24,
              ),
              const SizedBox(width: 8),
              Text(
                state.isOnline ? 'You are online' : 'You are offline',
                style: AppTextStyles.h2,
              ),
              const Spacer(),
              Switch.adaptive(
                value: state.isOnline,
                onChanged: state.busy
                    ? null
                    : (_) async {
                        if (!state.isOnline && issue != null) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            SnackBar(content: Text(issue)),
                          );
                          return;
                        }
                        // Breadcrumb the requested target state.
                        // `state.isOnline` here is the current value, so
                        // the new target after toggle() is its negation.
                        MopeduBreadcrumbs.partnerOnlineToggled(
                          online: !state.isOnline,
                        );
                        await ref
                            .read(partnerOnlineStateProvider.notifier)
                            .toggle();
                      },
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            state.isOnline
                ? 'Listening for ride requests in your area.'
                : (issue ?? 'Tap the toggle to start receiving offers.'),
            style: AppTextStyles.bodySmall,
          ),
        ],
      ),
    );
  }
}

class _EarningsHero extends StatelessWidget {
  const _EarningsHero({required this.dash});
  final PartnerDashboard dash;

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
            "Today's earnings",
            style: AppTextStyles.label.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 4),
          Text(
            formatRupees(dash.todayEarningsPaise),
            style: AppTextStyles.h1.copyWith(color: Colors.white, fontSize: 32),
          ),
          const SizedBox(height: 8),
          Text(
            '${dash.completedRidesToday} rides completed today',
            style: AppTextStyles.body.copyWith(color: Colors.white),
          ),
        ],
      ),
    );
  }
}

class _PlanCard extends StatelessWidget {
  const _PlanCard({required this.dash});
  final PartnerDashboard dash;

  String get _renewalLine {
    if (dash.planExpiresAt == null) return 'Renews automatically';
    final d = dash.planExpiresAt!;
    final dt = '${d.day}/${d.month}/${d.year}';
    return 'Renews $dt';
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: () => context.push('/mopedu/partner/subscription'),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            const Icon(Icons.workspace_premium, color: AppColors.postbookPrimary),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(dash.planName, style: AppTextStyles.h3),
                  const SizedBox(height: 2),
                  Text(
                    '${dash.leadsUsed} of ${dash.leadAllotment} leads used · '
                    '$_renewalLine',
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, color: AppColors.textTertiary),
          ],
        ),
      ),
    );
  }
}

class _MetricsRow extends StatelessWidget {
  const _MetricsRow({required this.dash});
  final PartnerDashboard dash;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: _MetricTile(
            label: 'Rating',
            value: dash.rating.toStringAsFixed(1),
            icon: Icons.star,
            color: AppColors.statusWarning,
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: _MetricTile(
            label: 'Acceptance',
            value: '${dash.acceptanceRatePct.toStringAsFixed(0)}%',
            icon: Icons.thumb_up,
            color: AppColors.statusSuccess,
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: _MetricTile(
            label: 'Cancel rate',
            value: '${dash.cancellationRatePct.toStringAsFixed(0)}%',
            icon: Icons.cancel,
            color: AppColors.statusError,
          ),
        ),
      ],
    );
  }
}

class _MetricTile extends StatelessWidget {
  const _MetricTile({
    required this.label,
    required this.value,
    required this.icon,
    required this.color,
  });

  final String label;
  final String value;
  final IconData icon;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, color: color, size: 18),
          const SizedBox(height: 6),
          Text(value, style: AppTextStyles.h2),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _EarningsCta extends StatelessWidget {
  const _EarningsCta();

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: OutlinedButton.icon(
        onPressed: () => context.push('/mopedu/partner/earnings'),
        icon: const Icon(Icons.bar_chart, size: 16),
        label: const Text('View earnings'),
        style: OutlinedButton.styleFrom(
          padding: const EdgeInsets.symmetric(vertical: 12),
        ),
      ),
    );
  }
}

class _RecentRides extends ConsumerWidget {
  const _RecentRides();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncRides = ref.watch(myRidesProvider(const MyRidesQuery(limit: 5)));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text("Today's rides", style: AppTextStyles.h3),
        const SizedBox(height: 8),
        asyncRides.when(
          loading: () => const Padding(
            padding: EdgeInsets.all(16),
            child: Center(child: CircularProgressIndicator()),
          ),
          error: (_, _) => Text(
            'Could not load rides.',
            style: AppTextStyles.bodySmall,
          ),
          data: (page) {
            if (page.items.isEmpty) {
              return Padding(
                padding: const EdgeInsets.all(12),
                child: Text(
                  'No completed rides yet today.',
                  style: AppTextStyles.bodySmall,
                ),
              );
            }
            return Container(
              decoration: BoxDecoration(
                color: AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Column(
                children: [
                  for (var i = 0; i < page.items.length; i++) ...[
                    if (i > 0)
                      const Divider(
                        height: 1,
                        color: AppColors.borderSubtle,
                        indent: 12,
                        endIndent: 12,
                      ),
                    _RideRow(ride: page.items[i]),
                  ],
                ],
              ),
            );
          },
        ),
      ],
    );
  }
}

class _RideRow extends StatelessWidget {
  const _RideRow({required this.ride});
  final Ride ride;

  @override
  Widget build(BuildContext context) {
    final fare = ride.finalFarePaise ?? ride.fareEstimatePaise;
    return ListTile(
      leading: const Icon(
        Icons.directions_car,
        color: AppColors.posttubePrimary,
      ),
      title: Text(
        ride.drop.displayName,
        style: AppTextStyles.label,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        VehicleType.label(ride.vehicleType),
        style: AppTextStyles.bodySmall,
      ),
      trailing: Text(
        formatRupees(fare),
        style: AppTextStyles.h3,
      ),
      onTap: () => context.push('/mopedu/rides/${ride.id}'),
    );
  }
}

// ─── Sprint 4 — expiring document banners ──────────────────────────────
//
// Surfaces partner KYC + vehicle documents within 30 days of expiry. The
// banner has a single CTA back to the onboarding KYC step where the user
// can re-submit. Capped at 3 banners so the dashboard stays readable.
//
// Telemetry: emits `mopedu.partner.docs.expiring_banner_shown` with the
// rendered count. We do NOT log document numbers or types per banner —
// the banned-key guard would also strip `document_number` if a future
// caller forgets the rules.

class _ExpiringDocsBanners extends ConsumerWidget {
  const _ExpiringDocsBanners();

  static const int _maxBanners = 3;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncDocs = ref.watch(expiringDocsProvider);
    return asyncDocs.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (docs) {
        if (docs.isEmpty) return const SizedBox.shrink();
        final shown = docs.take(_maxBanners).toList();
        // Fire telemetry once per build via the provider's stable identity.
        // The autoDispose provider already debounces by re-fetch cadence;
        // emitting here is acceptable for MVP. PRIVACY: count only.
        WidgetsBinding.instance.addPostFrameCallback((_) {
          ref
              .read(mopeduTelemetryProvider)
              .mopeduPartnerDocsExpiringBannerShown(count: shown.length);
        });
        return Column(
          children: [
            for (final d in shown) ...[
              _ExpiringDocBanner(doc: d),
              const SizedBox(height: 8),
            ],
            const SizedBox(height: 4),
          ],
        );
      },
    );
  }
}

class _ExpiringDocBanner extends StatelessWidget {
  const _ExpiringDocBanner({required this.doc});
  final RiderDocument doc;

  String get _label {
    if (doc.ownerType == 'vehicle') {
      return VehicleDocumentType.label(doc.documentType);
    }
    return PartnerDocumentType.label(doc.documentType);
  }

  String _expiryLine() {
    final exp = doc.expiresAt;
    if (exp == null) return 'expires soon';
    final daysLeft = exp.difference(DateTime.now()).inDays;
    if (daysLeft < 0) return 'expired';
    if (daysLeft == 0) return 'expires today';
    if (daysLeft == 1) return 'expires tomorrow';
    return 'expires in $daysLeft days';
  }

  @override
  Widget build(BuildContext context) {
    final exp = doc.expiresAt;
    final dt = exp == null
        ? ''
        : ' on ${exp.day}/${exp.month}/${exp.year}';
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.statusWarning.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusWarning),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(
            Icons.assignment_late_rounded,
            color: AppColors.statusWarning,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Your $_label ${_expiryLine()}$dt.',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.statusWarning,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  'Update soon to avoid disruption.',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          TextButton(
            onPressed: () => context.push('/mopedu/partner/onboarding'),
            child: const Text('Update now'),
          ),
        ],
      ),
    );
  }
}

class _ReferralCta extends StatelessWidget {
  const _ReferralCta();

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: OutlinedButton.icon(
        onPressed: () => context.push('/mopedu/partner/referral'),
        icon: const Icon(Icons.card_giftcard, size: 16),
        label: const Text('Refer a driver'),
        style: OutlinedButton.styleFrom(
          padding: const EdgeInsets.symmetric(vertical: 12),
        ),
      ),
    );
  }
}
