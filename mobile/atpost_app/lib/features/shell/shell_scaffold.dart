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
import 'package:atpost_app/features/shell/create_options_sheet.dart';
import 'package:atpost_app/features/shell/home_tab.dart';
import 'package:atpost_app/features/shell/inbox_tab.dart';
import 'package:atpost_app/features/shell/me_tab.dart';
import 'package:atpost_app/features/shell/search_tab.dart';
import 'package:atpost_app/features/shell/shell_providers.dart';
import 'package:atpost_app/services/shell_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Stable index map for the four real tabs. `create` (index 2 in the visual
/// row) is intentionally NOT a tab — it's a FAB that opens a sheet.
class ShellTabIndex {
  ShellTabIndex._();

  static const home = 0;
  static const search = 1;
  static const inbox = 2;
  static const me = 3;

  static const count = 4;
}

class ShellScaffold extends ConsumerStatefulWidget {
  const ShellScaffold({super.key, this.initialTab = ShellTabIndex.home});

  final int initialTab;

  @override
  ConsumerState<ShellScaffold> createState() => _ShellScaffoldState();
}

class _ShellScaffoldState extends ConsumerState<ShellScaffold> {
  @override
  void initState() {
    super.initState();
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
  Widget build(BuildContext context) {
    final current = ref.watch(shellTabProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      extendBody: true,
      body: IndexedStack(
        index: _safeIndex(current),
        children: const [
          HomeTab(),
          SearchTab(),
          InboxTab(),
          MeTab(),
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
              icon: Icons.search,
              label: 'Search',
              index: ShellTabIndex.search,
              currentIndex: currentIndex,
              telemetryKey: ShellTab.search,
            ),
            // Visual gap for the FAB notch.
            const SizedBox(width: 56),
            _NavItem(
              icon: Icons.notifications,
              label: 'Inbox',
              index: ShellTabIndex.inbox,
              currentIndex: currentIndex,
              telemetryKey: ShellTab.inbox,
            ),
            _NavItem(
              icon: Icons.person,
              label: 'Me',
              index: ShellTabIndex.me,
              currentIndex: currentIndex,
              telemetryKey: ShellTab.me,
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
