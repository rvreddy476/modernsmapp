import 'package:feature_live/broadcast_screen.dart';
import 'package:feature_live/go_live_screen.dart';
import 'package:feature_live/live_broadcaster_screen.dart';
import 'package:feature_live/live_list_screen.dart';
import 'package:feature_live/live_screen.dart';
import 'package:feature_live/live_viewer_screen.dart';
import 'package:go_router/go_router.dart';

/// Live-streaming route table — legacy v1 (/live, /live/broadcast/:id)
/// plus v2 (LiveKit, /live/v2/*). Spread into the app router's shell. The
/// router's public-path logic for anonymous v2 viewers stays app-side.
List<RouteBase> liveRoutes() => [
  GoRoute(
    path: '/live',
    builder: (context, state) => const LiveScreen(),
  ),
  GoRoute(
    path: '/live/broadcast/:streamId',
    builder: (context, state) => BroadcastScreen(
      streamId: state.pathParameters['streamId']!,
      title: state.uri.queryParameters['title'] ?? 'Live Stream',
    ),
  ),
  // Live streaming v2 (LiveKit / live-service-v2).
  GoRoute(
    path: '/live/v2',
    builder: (_, _) => const LiveListScreen(),
  ),
  GoRoute(
    path: '/live/v2/new',
    builder: (_, _) => const GoLiveScreen(),
  ),
  GoRoute(
    path: '/live/v2/:streamId',
    builder: (context, state) => LiveViewerScreen(
      streamId: state.pathParameters['streamId']!,
    ),
  ),
  GoRoute(
    path: '/live/v2/:streamId/broadcast',
    builder: (context, state) => LiveBroadcasterScreen(
      streamId: state.pathParameters['streamId']!,
    ),
  ),
];
