import 'dart:async';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/features/auth/auth_routes.dart';
import 'package:atpost_app/features/billpay/billpay_routes.dart';
import 'package:atpost_app/features/channels/community_routes.dart';
import 'package:atpost_app/features/commerce/commerce_routes.dart';
import 'package:atpost_app/features/content/content_routes.dart';
import 'package:atpost_app/features/monetization/monetization_routes.dart';
import 'package:atpost_app/features/mopedu/mopedu_routes.dart';
import 'package:atpost_app/features/pulse/pulse_routes.dart';
import 'package:atpost_app/features/qa/qa_routes.dart';
import 'package:atpost_app/features/settings/settings_routes.dart';
import 'package:atpost_app/features/shell/shell_routes.dart';
import 'package:atpost_app/features/shell/shell_scaffold.dart';
import 'package:atpost_app/features/social/social_routes.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Auth routes that don't require login.
const _publicPaths = {
  '/login',
  '/register',
  '/forgot-password',
  '/verify-otp',
  '/auth/step-up',
};

const _publicPathPrefixes = <String>['/mopedu/share/', '/live/v2/'];

bool _isPublicPath(String path) {
  if (_publicPaths.contains(path)) return true;
  for (final prefix in _publicPathPrefixes) {
    if (path.startsWith(prefix)) {
      if (prefix == '/live/v2/') {
        if (path == '/live/v2/new' || path.endsWith('/broadcast')) {
          continue;
        }
      }
      return true;
    }
  }
  return false;
}

class _AuthRouterRefresh extends ChangeNotifier {
  _AuthRouterRefresh(Stream<AuthState> stream) {
    _subscription = stream.listen((_) => notifyListeners());
  }

  late final StreamSubscription<AuthState> _subscription;
  final Set<Timer> _redirectDeadlines = <Timer>{};

  Future<void> awaitWithDeadline(Future<void> future, Duration timeout) {
    final completer = Completer<void>();
    late final Timer timer;
    void cleanup() {
      _redirectDeadlines.remove(timer);
      timer.cancel();
    }

    timer = Timer(timeout, () {
      if (!completer.isCompleted) {
        cleanup();
        completer.completeError(TimeoutException('redirect deadline', timeout));
      }
    });
    _redirectDeadlines.add(timer);

    future.then((_) {
      if (!completer.isCompleted) {
        cleanup();
        completer.complete();
      }
    }).catchError((Object e) {
      if (!completer.isCompleted) {
        cleanup();
        completer.completeError(e);
      }
    });

    return completer.future;
  }

  @override
  void dispose() {
    for (final t in _redirectDeadlines) {
      t.cancel();
    }
    _redirectDeadlines.clear();
    _subscription.cancel();
    super.dispose();
  }
}

class _CallRouteObserver extends ConsumerWidget {
  const _CallRouteObserver({required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    ref.listen(callProvider, (previous, next) {
      if (next != null && previous == null && next.state != CallState.idle) {
        GoRouter.of(context).push('/call');
      }
    });
    return child;
  }
}

final routeObserverProvider = Provider<RouteObserver<ModalRoute<void>>>(
  (_) => RouteObserver<ModalRoute<void>>(),
);

final appRouterProvider = Provider<GoRouter>((ref) {
  final authService = ref.watch(authServiceProvider);
  final refresh = _AuthRouterRefresh(authService.stateStream);
  ref.onDispose(refresh.dispose);
  final routeObserver = ref.watch(routeObserverProvider);

  return GoRouter(
    initialLocation: '/splash',
    refreshListenable: refresh,
    observers: [routeObserver],
    redirect: (context, state) async {
      final path = state.uri.path;

      try {
        await refresh.awaitWithDeadline(
          authService.sessionReady,
          const Duration(seconds: 3),
        );
      } catch (_) {
        AppLogger.warn('Router: Session restoration timed out', tag: 'Router');
      }

      final isAuthenticated = authService.isAuthenticated;
      final isPublicRoute = _isPublicPath(path);

      if (path == '/splash') return isAuthenticated ? '/' : '/login';
      if (!isAuthenticated && !isPublicRoute) return '/login';
      if (isAuthenticated && _publicPaths.contains(path)) return '/';
      return null;
    },
    routes: [
      ShellRoute(
        builder: (context, state, child) => _CallRouteObserver(child: child),
        routes: [
          ...ShellRoutes.routes,
          ...AuthRoutes.routes,
          ...SocialRoutes.routes,
          ...ContentRoutes.routes,
          ...CommerceRoutes.routes,
          ...PulseRoutes.routes,
          ...MopeduRoutes.routes,
          ...BillPayRoutes.routes,
          ...QARoutes.routes,
          ...CommunityRoutes.routes,
          ...MonetizationRoutes.routes,
          ...SettingsRoutes.routes,
        ],
      ),
    ],
  );
});
