// AtPost super-app shell.
//
// Five-tab bottom navigation: Home / Search / Create / Inbox / Me.
//
// `Create` is a center FAB — tapping it does NOT change the active tab; it
// opens `CreateOptionsSheet` (a modal bottom sheet with six composer
// shortcuts). The pattern is `BottomAppBar` + `FloatingActionButton.
// centerDocked`.
//
// Each real tab (Home, Search, Inbox, Me) is hosted inside an
// `IndexedStack` so scroll positions and any inner state survive tab
// switches. The legacy `homeFeedProvider` etc. continue to power the Home
// tab — the unification is purely shell-level.
//
// Existing routes (`/pulse`, `/commerce`, `/wallet`, /reels`, etc.) are
// untouched: every standalone module surface stays reachable both from
// inside the shell (Home quick-actions, Me-tab launcher grid) and from
// external deep links.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/features/home/home_feed_screen.dart';
import 'package:atpost_app/features/reels/reels_screen.dart';
import 'package:atpost_app/features/services/services_screen.dart';
import 'package:atpost_app/features/shell/create_options_sheet.dart';
import 'package:atpost_app/features/shell/notification_toast_queue.dart';
import 'package:atpost_app/features/shell/shell_providers.dart';
import 'package:atpost_app/features/wallet/wallet_home_screen.dart';
import 'package:atpost_app/providers/notification_provider.dart';
import 'package:atpost_app/services/shell_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Stable index map for the four real tabs. `create` (visually centered)
/// is intentionally NOT a tab — it's a FAB that opens a sheet.
///
/// Layout (left → right): Home · Wallet · [+] · Reels · Explore
///   * Home    — feed (For You / Following / #HashTag)
///   * Wallet  — wallet hero + transactions + send/top-up
///   * Reels   — vertical short-form video feed
///   * Explore — mini-app launcher (services_screen.dart). Hosts every
///               module that doesn't get its own bottom-tab slot
///               (Pulse, Mopedu, Billpay, Commerce, Figo, etc.).
class ShellTabIndex {
  ShellTabIndex._();

  static const home = 0;
  static const wallet = 1;
  static const reels = 2;
  static const explore = 3;

  static const count = 4;
}

class ShellScaffold extends ConsumerStatefulWidget {
  const ShellScaffold({super.key, this.initialTab = ShellTabIndex.home});

  final int initialTab;

  @override
  ConsumerState<ShellScaffold> createState() => _ShellScaffoldState();
}

class _ShellScaffoldState extends ConsumerState<ShellScaffold> {
  late final NotificationToastQueue _toastQueue;

  @override
  void initState() {
    super.initState();
    _toastQueue = NotificationToastQueue(onView: _renderToast);
    // Hop the active tab to whatever the deep-link asked for. We do this in
    // a post-frame callback so we don't mutate provider state during the
    // first build.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final desired = _safeIndex(widget.initialTab);
      if (ref.read(shellTabProvider) != desired) {
        ref.read(shellTabProvider.notifier).state = desired;
      }
    });
  }

  @override
  void dispose() {
    _toastQueue.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final current = ref.watch(shellTabProvider);

    // Live notifications. Every NotificationEvent off the WS multiplex
    // surfaces here exactly once: we invalidate the bell + inbox so
    // the badge updates without a refetch, then show a tap-to-open
    // toast. Sitting at the shell level means the toast is reachable
    // from every tab, but a fullscreen route on top (e.g. /reels) can
    // still cover it — that's intentional, the snackbar host follows
    // the topmost Scaffold.
    ref.listen<AsyncValue<NotificationEvent>>(liveNotificationsProvider, (
      _,
      next,
    ) {
      final evt = next.valueOrNull;
      if (evt == null || !mounted) return;
      ref.invalidate(unreadNotificationCountProvider);
      ref.invalidate(notificationsProvider);
      // Hand to the queue: it debounces bursts and collapses
      // matching collapse_keys into a single toast view, then calls
      // _renderToast with the merged result.
      _toastQueue.add(evt);
    });

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      extendBody: true,
      body: IndexedStack(
        index: _safeIndex(current),
        children: const [
          // Home — original 3-strip feed (For You / Following /
          // #HashTag). Twitter/IG shape; top-bar icons cover the
          // shortcuts (search, shopping, posttube, notifications,
          // profile-avatar).
          HomeFeedScreen(),
          // Wallet — balance hero + transactions + top-up + send.
          WalletHomeScreen(),
          // Reels — vertical PageView feed.
          ReelsScreen(),
          // Explore — mini-app launcher: Pulse, Mopedu, Billpay,
          // Commerce, Figo, plus the legacy services menu.
          ServicesScreen(),
        ],
      ),
      floatingActionButton: _CreateFab(
        onTap: () {
          ref.read(shellTelemetryProvider).shellTabSelected(ShellTab.create);
          showCreateOptionsSheet(context);
        },
      ),
      floatingActionButtonLocation: FloatingActionButtonLocation.centerDocked,
      bottomNavigationBar: _BottomBar(currentIndex: _safeIndex(current)),
    );
  }

  int _safeIndex(int v) {
    if (v < 0 || v >= ShellTabIndex.count) return ShellTabIndex.home;
    return v;
  }

  /// Returns the deep link only if it's a safe in-app path
  /// ("/foo/bar"). Rejects external URLs, scheme-prefixed inputs,
  /// query-string-only fragments, and malformed strings — go_router
  /// crashes on anything but a route path, and a server that emits a
  /// `https://evil.example/...` deep link must NOT be allowed to
  /// punt the user out of the app.
  String? _validateDeepLink(String? raw) {
    if (raw == null) return null;
    final trimmed = raw.trim();
    if (trimmed.isEmpty) return null;
    if (!trimmed.startsWith('/')) return null;
    if (trimmed.startsWith('//')) return null; // protocol-relative
    // Path can include query + fragment but the prefix must look like
    // a route — letters, digits, `_`, `-`, `/`, `:` for params, and
    // common URL chars after.
    final pathOnly = trimmed.split('?').first.split('#').first;
    if (!RegExp(r'^[A-Za-z0-9_\-/:%.]+$').hasMatch(pathOnly)) return null;
    return trimmed;
  }

  /// Render a (potentially merged) toast view from the queue. The
  /// queue handles the bursty merge logic; this method only knows how
  /// to paint pixels. Dismissing the current snackbar before showing
  /// the new one keeps the visible stack at one — the queue already
  /// folded the prior bursts into the view we're about to render.
  void _renderToast(NotificationToastView view) {
    final messenger = ScaffoldMessenger.maybeOf(context);
    if (messenger == null) return;
    final hasBody = view.body.isNotEmpty && view.body != view.title;
    final deepLink = view.deepLink;
    messenger.removeCurrentSnackBar();
    messenger.showSnackBar(
      SnackBar(
        behavior: SnackBarBehavior.floating,
        backgroundColor: AppColors.bgSecondary,
        duration: const Duration(seconds: 4),
        content: Row(
          children: [
            Container(
              width: 32,
              height: 32,
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary.withValues(alpha: 0.18),
                borderRadius: BorderRadius.circular(8),
              ),
              child: const Icon(
                Icons.notifications_rounded,
                size: 18,
                color: AppColors.postbookPrimary,
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    view.title.isNotEmpty ? view.title : 'New notification',
                    style: AppTextStyles.label.copyWith(
                      color: Colors.white,
                      fontWeight: FontWeight.w700,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (hasBody)
                    Text(
                      view.body,
                      style: AppTextStyles.labelSmall.copyWith(
                        color: Colors.white70,
                      ),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                ],
              ),
            ),
          ],
        ),
        action: _validateDeepLink(deepLink) != null
            ? SnackBarAction(
                label: 'Open',
                textColor: AppColors.postbookPrimary,
                onPressed: () {
                  final path = _validateDeepLink(deepLink);
                  if (!mounted || path == null) return;
                  context.push(path);
                },
              )
            : null,
      ),
    );
  }
}

class _CreateFab extends StatelessWidget {
  const _CreateFab({required this.onTap});

  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 58,
      height: 58,
      child: FloatingActionButton(
        onPressed: onTap,
        backgroundColor: AppColors.postbookPrimary,
        elevation: 6,
        shape: const CircleBorder(),
        child: Container(
          width: 58,
          height: 58,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            gradient: AppColors.ctaGradient,
            boxShadow: const [
              BoxShadow(
                color: Color(0x66FF6B35),
                blurRadius: 14,
                offset: Offset(0, 4),
              ),
            ],
          ),
          child: const Icon(Icons.add, color: Colors.white, size: 28),
        ),
      ),
    );
  }
}

class _BottomBar extends ConsumerWidget {
  const _BottomBar({required this.currentIndex});

  final int currentIndex;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return BottomAppBar(
      color: AppColors.bgSecondary,
      elevation: 0,
      shape: const CircularNotchedRectangle(),
      notchMargin: 6,
      padding: EdgeInsets.zero,
      child: SizedBox(
        height: 60,
        child: Row(
          children: [
            _NavItem(
              icon: Icons.home_filled,
              label: 'Home',
              index: ShellTabIndex.home,
              currentIndex: currentIndex,
              telemetryKey: ShellTab.home,
            ),
            _NavItem(
              icon: Icons.account_balance_wallet_rounded,
              label: 'Wallet',
              index: ShellTabIndex.wallet,
              currentIndex: currentIndex,
              telemetryKey: ShellTab.wallet,
            ),
            // Visual gap for the FAB notch.
            const SizedBox(width: 56),
            _NavItem(
              icon: Icons.movie_creation_rounded,
              label: 'Reels',
              index: ShellTabIndex.reels,
              currentIndex: currentIndex,
              telemetryKey: ShellTab.reels,
            ),
            _NavItem(
              // Apps grid icon — the user-facing mini-app center.
              icon: Icons.apps_rounded,
              label: 'Explore',
              index: ShellTabIndex.explore,
              currentIndex: currentIndex,
              telemetryKey: ShellTab.explore,
            ),
          ],
        ),
      ),
    );
  }
}

class _NavItem extends ConsumerWidget {
  const _NavItem({
    required this.icon,
    required this.label,
    required this.index,
    required this.currentIndex,
    required this.telemetryKey,
  });

  final IconData icon;
  final String label;
  final int index;
  final int currentIndex;
  final String telemetryKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final active = currentIndex == index;
    final color = active ? AppColors.postbookPrimary : AppColors.textDimmest;
    return Expanded(
      child: InkWell(
        onTap: () {
          if (currentIndex == index) return;
          ref.read(shellTabProvider.notifier).state = index;
          ref.read(shellTelemetryProvider).shellTabSelected(telemetryKey);
        },
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, color: color, size: 24),
            const SizedBox(height: 2),
            Text(
              label,
              style: AppTextStyles.labelTiny.copyWith(
                color: color,
                fontWeight: active ? FontWeight.w800 : FontWeight.w600,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
