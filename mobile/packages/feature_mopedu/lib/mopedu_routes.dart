import 'package:feature_mopedu/mopedu/booking_in_progress_screen.dart';
import 'package:feature_mopedu/mopedu/mopedu_gate.dart';
import 'package:feature_mopedu/mopedu/mopedu_home_screen.dart';
import 'package:feature_mopedu/mopedu/partner/partner_dashboard_screen.dart';
import 'package:feature_mopedu/mopedu/partner/partner_earnings_screen.dart';
import 'package:feature_mopedu/mopedu/partner/partner_landing_screen.dart';
import 'package:feature_mopedu/mopedu/partner/partner_onboarding_screen.dart';
import 'package:feature_mopedu/mopedu/partner/partner_referral_screen.dart';
import 'package:feature_mopedu/mopedu/partner/partner_rides_breakdown_screen.dart';
import 'package:feature_mopedu/mopedu/partner/partner_subscription_screen.dart';
import 'package:feature_mopedu/mopedu/partner/ride_navigation_screen.dart';
import 'package:feature_mopedu/mopedu/ride_history_screen.dart';
import 'package:feature_mopedu/mopedu/ride_summary_screen.dart';
import 'package:feature_mopedu/mopedu/safety/my_complaints_screen.dart';
import 'package:feature_mopedu/mopedu/safety/safety_center_screen.dart'
    as mopedu_safety;
import 'package:feature_mopedu/mopedu/safety/shared_ride_viewer_screen.dart';
import 'package:feature_mopedu/mopedu/saved_places_screen.dart';
import 'package:go_router/go_router.dart';

/// Mopedu ride-hailing route table (rider + partner). Most surfaces are
/// wrapped in [MopeduGate] — the v1 city allow-list waitlist. The app
/// router spreads this into its shell.
List<RouteBase> mopeduRoutes() => [
  GoRoute(
    path: '/mopedu',
    builder: (_, _) => const MopeduGate(child: MopeduHomeScreen()),
  ),
  GoRoute(
    path: '/mopedu/booking/:id',
    builder: (context, state) => MopeduGate(
      child: BookingInProgressScreen(
        rideId: state.pathParameters['id']!,
      ),
    ),
  ),
  GoRoute(
    path: '/mopedu/rides',
    builder: (_, _) => const MopeduGate(child: RideHistoryScreen()),
  ),
  GoRoute(
    path: '/mopedu/rides/:id',
    builder: (context, state) => MopeduGate(
      child: RideSummaryScreen(rideId: state.pathParameters['id']!),
    ),
  ),
  GoRoute(
    path: '/mopedu/saved-places',
    builder: (_, _) => const MopeduGate(child: SavedPlacesScreen()),
  ),
  // Sprint 3 — Mopedu customer safety surfaces.
  GoRoute(
    path: '/mopedu/safety',
    builder: (_, _) => const MopeduGate(
      child: mopedu_safety.SafetyCenterScreen(),
    ),
  ),
  GoRoute(
    path: '/mopedu/complaints',
    builder: (_, _) => const MopeduGate(child: MyComplaintsScreen()),
  ),
  // Public, no-auth shared-ride viewer reached via deep link. NOT wrapped
  // in MopeduGate — share-link recipients may not be in a launch city,
  // and withholding the ride view would defeat the safety share flow.
  GoRoute(
    path: '/mopedu/share/:token',
    builder: (context, state) => SharedRideViewerScreen(
      token: state.pathParameters['token']!,
    ),
  ),
  // Sprint 2 — Mopedu partner side. Partner routes are gated too because
  // we are not recruiting partners outside the v1 allow-list.
  GoRoute(
    path: '/mopedu/partner',
    builder: (_, _) => const MopeduGate(child: PartnerLandingScreen()),
  ),
  GoRoute(
    path: '/mopedu/partner/onboarding',
    builder: (_, _) => const MopeduGate(child: PartnerOnboardingScreen()),
  ),
  GoRoute(
    path: '/mopedu/partner/dashboard',
    builder: (_, _) => const MopeduGate(child: PartnerDashboardScreen()),
  ),
  GoRoute(
    path: '/mopedu/partner/earnings',
    builder: (_, _) => const MopeduGate(child: PartnerEarningsScreen()),
  ),
  GoRoute(
    path: '/mopedu/partner/subscription',
    builder: (_, _) => const MopeduGate(child: PartnerSubscriptionScreen()),
  ),
  // Sprint 4 — partner polish.
  GoRoute(
    path: '/mopedu/partner/rides-breakdown',
    builder: (context, state) => MopeduGate(
      child: PartnerRidesBreakdownScreen(
        period: state.uri.queryParameters['period'] ?? 'week',
      ),
    ),
  ),
  GoRoute(
    path: '/mopedu/partner/referral',
    builder: (_, _) => const MopeduGate(child: PartnerReferralScreen()),
  ),
  GoRoute(
    path: '/mopedu/partner/rides/:id',
    builder: (context, state) => MopeduGate(
      child: RideNavigationScreen(
        rideId: state.pathParameters['id']!,
      ),
    ),
  ),
];
