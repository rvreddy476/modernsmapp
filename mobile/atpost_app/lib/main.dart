import 'package:atpost_app/app/atpost_app.dart';
import 'package:atpost_app/app/mopedu_host_bindings.dart';
import 'package:atpost_app/app/pulse_host_bindings.dart';
import 'package:atpost_app/app/wallet_host_bindings.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_core/cache/cache_manager.dart';
import 'package:atpost_network/auth_session.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Root DI bindings: package-level contracts get their app
/// implementations here (and only here). Tests that want the real
/// wiring can reuse this list in their own ProviderScope.
List<Override> appOverrides() => [
  authSessionProvider.overrideWith((ref) => ref.watch(authServiceProvider)),
  ...pulseHostBindings(),
  ...walletHostBindings(),
  ...mopeduHostBindings(),
];

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Non-blocking initialization or at least with a timeout to prevent app hang
  try {
    await CacheManager.init().timeout(const Duration(seconds: 10));
  } catch (e) {
    debugPrint('Initialization error: $e');
  }

  runApp(ProviderScope(overrides: appOverrides(), child: const AtpostApp()));
}
