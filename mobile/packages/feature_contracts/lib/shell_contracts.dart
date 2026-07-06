import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// The app's bottom-nav tab indices, shared between the shell (which owns
/// the scaffold) and the feature surfaces embedded as tabs (reels, home)
/// so a tab can tell whether it is currently the foreground surface
/// without importing the shell. Mirrors the shell's own ShellTabIndex.
class ShellTab {
  ShellTab._();
  static const home = 0;
  static const friends = 1;
  static const reels = 2;
  static const explore = 3;
}

/// The currently selected bottom-nav tab index. The app binds this to the
/// shell's tab state; defaults to [ShellTab.home] so a tab surface renders
/// in isolation (tests, fullscreen routes).
final appShellTabProvider = Provider<int>((_) => ShellTab.home);

/// The app's shared RouteObserver, so a tab surface can gate expensive
/// work (e.g. pausing video autoplay) when another route is pushed on top
/// of it. Defaults to a standalone observer when no host is wired.
final appRouteObserverProvider = Provider<RouteObserver<ModalRoute<void>>>(
  (_) => RouteObserver<ModalRoute<void>>(),
);
