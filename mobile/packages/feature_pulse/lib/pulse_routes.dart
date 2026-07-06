import 'package:feature_pulse/match_inbox_screen.dart';
import 'package:feature_pulse/onboarding/echoes_consent_screen.dart';
import 'package:feature_pulse/onboarding/intent_picker_screen.dart';
import 'package:feature_pulse/onboarding/tune_setup_screen.dart';
import 'package:feature_pulse/premium/data_export_screen.dart';
import 'package:feature_pulse/premium/premium_screen.dart';
import 'package:feature_pulse/pulse_chat_screen.dart';
import 'package:feature_pulse/pulse_discover_screen.dart';
import 'package:feature_pulse/pulse_gate.dart';
import 'package:feature_pulse/pulse_landing_screen.dart';
import 'package:feature_pulse/pulse_matches_screen.dart';
import 'package:feature_pulse/pulse_onboarding_screen.dart';
import 'package:feature_pulse/pulse_profile_screen.dart';
import 'package:feature_pulse/safety/block_list_screen.dart';
import 'package:feature_pulse/safety/reports_screen.dart';
import 'package:feature_pulse/safety/safety_center_screen.dart';
import 'package:feature_pulse/safety/trusted_contact_picker.dart';
import 'package:feature_pulse/safety/vouch_inbox_screen.dart';
import 'package:feature_pulse/safety/vouch_management_screen.dart';
import 'package:feature_pulse/verification/aadhaar_verification_screen.dart';
import 'package:feature_pulse/verification/selfie_verification_screen.dart';
import 'package:feature_pulse/verification/verification_landing_screen.dart';
import 'package:go_router/go_router.dart';

/// Pulse's route table. The app's router spreads this into its shell
/// route children — the feature owns its paths, the app owns the shell.
///
/// Sprint 2: /pulse is the orbital + list hero surface; the legacy
/// landing screen lives at /pulse/landing for any deep links.
/// Sprint 6: every user-facing surface is wrapped in [PulseGate], which
/// gates on the master `pulse_enabled_master` flag + the v1 city
/// allow-list.
List<RouteBase> pulseRoutes() => [
  GoRoute(
    path: '/pulse',
    builder: (context, state) => const PulseGate(child: PulseDiscoverScreen()),
  ),
  GoRoute(
    path: '/pulse/landing',
    builder: (context, state) => const PulseGate(child: PulseLandingScreen()),
  ),
  GoRoute(
    path: '/pulse/onboarding',
    builder: (context, state) =>
        const PulseGate(child: PulseOnboardingScreen()),
  ),
  GoRoute(
    path: '/pulse/discover',
    builder: (context, state) => const PulseGate(child: PulseDiscoverScreen()),
  ),
  GoRoute(
    path: '/pulse/matches',
    builder: (context, state) => MatchInboxScreen(
      initialTab: state.uri.queryParameters['tab'],
    ),
  ),
  // Sprint 3: deep-link target for `dating.spark.matched` push — opens
  // the inbox positioned on the right tab. The S1 matches surface stays
  // accessible at /pulse/matches/legacy if anyone deep-linked to it.
  GoRoute(
    path: '/pulse/matches/legacy',
    builder: (context, state) => const PulseMatchesScreen(),
  ),
  GoRoute(
    path: '/pulse/matches/:matchId',
    builder: (context, state) => MatchInboxScreen(
      initialTab: state.uri.queryParameters['tab'],
    ),
  ),
  GoRoute(
    path: '/pulse/profile',
    builder: (context, state) => const PulseProfileScreen(),
  ),
  GoRoute(
    path: '/pulse/chat/:conversationId',
    builder: (context, state) => PulseChatScreen(
      conversationId: state.pathParameters['conversationId']!,
    ),
  ),
  // Sprint 1: Pulse onboarding additions (intent -> tune -> echoes consent).
  GoRoute(
    path: '/pulse/onboarding/intent',
    builder: (context, state) => const IntentPickerScreen(),
  ),
  GoRoute(
    path: '/pulse/onboarding/tune',
    builder: (context, state) => const TuneSetupScreen(),
  ),
  GoRoute(
    path: '/pulse/onboarding/echoes',
    builder: (context, state) => const EchoesConsentScreen(),
  ),
  // Sprint 4: verification ladder.
  GoRoute(
    path: '/pulse/verification',
    builder: (_, _) => const VerificationLandingScreen(),
  ),
  GoRoute(
    path: '/pulse/verification/aadhaar',
    builder: (_, _) => const AadhaarVerificationScreen(),
  ),
  GoRoute(
    path: '/pulse/verification/aadhaar/callback',
    builder: (context, state) => AadhaarVerificationScreen(
      incomingCode: state.uri.queryParameters['code'],
      incomingState: state.uri.queryParameters['state'],
    ),
  ),
  GoRoute(
    path: '/pulse/verification/selfie',
    builder: (_, _) => const SelfieVerificationScreen(),
  ),
  // Sprint 4: safety center + sub-screens.
  GoRoute(
    path: '/pulse/safety',
    builder: (_, _) => const SafetyCenterScreen(),
  ),
  GoRoute(
    path: '/pulse/safety/vouches',
    builder: (_, _) => const VouchManagementScreen(),
  ),
  GoRoute(
    path: '/pulse/safety/vouches/inbox',
    builder: (_, _) => const VouchInboxScreen(),
  ),
  GoRoute(
    path: '/pulse/safety/trusted-contact',
    builder: (_, _) => const TrustedContactPicker(),
  ),
  GoRoute(
    path: '/pulse/safety/blocks',
    builder: (_, _) => const BlockListScreen(),
  ),
  GoRoute(
    path: '/pulse/safety/reports',
    builder: (_, _) => const MyReportsScreen(),
  ),
  // Sprint 5: Premium tier + DPDP data export.
  GoRoute(
    path: '/pulse/premium',
    builder: (_, _) => const PremiumScreen(),
  ),
  GoRoute(
    path: '/pulse/data-export',
    builder: (_, _) => const DataExportScreen(),
  ),
  // Legacy /postmatch/* redirects (30-day deprecation window from
  // Sprint 1 ship). Remove after confirming no inbound deep links.
  GoRoute(
    path: '/postmatch',
    redirect: (_, _) => '/pulse',
  ),
  GoRoute(
    path: '/postmatch/onboarding',
    redirect: (_, _) => '/pulse/onboarding',
  ),
  GoRoute(
    path: '/postmatch/discover',
    redirect: (_, _) => '/pulse/discover',
  ),
  GoRoute(
    path: '/postmatch/matches',
    redirect: (_, _) => '/pulse/matches',
  ),
  GoRoute(
    path: '/postmatch/profile',
    redirect: (_, _) => '/pulse/profile',
  ),
  GoRoute(
    path: '/postmatch/chat/:conversationId',
    redirect: (_, state) =>
        '/pulse/chat/${state.pathParameters['conversationId']}',
  ),
];
