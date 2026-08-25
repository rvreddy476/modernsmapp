import 'package:atpost_app/features/mopedu/booking_in_progress_screen.dart';
import 'package:atpost_app/features/mopedu/mopedu_gate.dart';
import 'package:atpost_app/features/mopedu/mopedu_home_screen.dart';
import 'package:atpost_app/features/mopedu/safety/my_complaints_screen.dart';
import 'package:atpost_app/features/mopedu/safety/safety_center_screen.dart'
    as mopedu_safety;
import 'package:atpost_app/features/mopedu/safety/shared_ride_viewer_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_dashboard_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_earnings_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_landing_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_onboarding_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_referral_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_rides_breakdown_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_subscription_screen.dart';
import 'package:atpost_app/features/mopedu/partner/ride_navigation_screen.dart';
import 'package:atpost_app/features/mopedu/ride_history_screen.dart';
import 'package:atpost_app/features/mopedu/ride_summary_screen.dart';
import 'package:atpost_app/features/mopedu/saved_places_screen.dart';
import 'package:go_router/go_router.dart';

class MopeduRoutes {
  static List<RouteBase> get routes => [
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
        GoRoute(
          path: '/mopedu/safety',
          builder: (_, _) =>
              const MopeduGate(child: mopedu_safety.SafetyCenterScreen()),
        ),
        GoRoute(
          path: '/mopedu/complaints',
          builder: (_, _) => const MopeduGate(child: MyComplaintsScreen()),
        ),
        GoRoute(
          path: '/mopedu/share/:token',
          builder: (context, state) =>
              SharedRideViewerScreen(token: state.pathParameters['token']!),
        ),
        GoRoute(
          path: '/mopedu/partner',
          builder: (_, _) => const MopeduGate(child: PartnerLandingScreen()),
        ),
        GoRoute(
          path: '/mopedu/partner/onboarding',
          builder: (_, _) =>
              const MopeduGate(child: PartnerOnboardingScreen()),
        ),
        GoRoute(
          path: '/mopedu/partner/dashboard',
          builder: (_, _) =>
              const MopeduGate(child: PartnerDashboardScreen()),
        ),
        GoRoute(
          path: '/mopedu/partner/earnings',
          builder: (_, _) => const MopeduGate(child: PartnerEarningsScreen()),
        ),
        GoRoute(
          path: '/mopedu/partner/subscription',
          builder: (_, _) =>
              const MopeduGate(child: PartnerSubscriptionScreen()),
        ),
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
            child: RideNavigationScreen(rideId: state.pathParameters['id']!),
          ),
        ),
      ];
}
