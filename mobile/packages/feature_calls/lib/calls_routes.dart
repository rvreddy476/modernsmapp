import 'package:feature_calls/call_screen.dart';
import 'package:go_router/go_router.dart';

/// Call UI route. Pushed by the app's call-state listener whenever an
/// incoming/outgoing call leaves the idle state.
List<RouteBase> callsRoutes() => [
  GoRoute(
    path: '/call',
    builder: (context, state) => const CallScreen(),
  ),
];
