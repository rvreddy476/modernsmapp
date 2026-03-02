import 'package:atpost_app/app/router.dart';
import 'package:atpost_app/core/theme/app_theme.dart';
import 'package:atpost_app/features/call/call_overlay.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class AtpostApp extends ConsumerWidget {
  const AtpostApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final router = ref.watch(appRouterProvider);
    final callInfo = ref.watch(callProvider);

    return MaterialApp.router(
      title: 'atpost',
      theme: AppTheme.darkTheme,
      debugShowCheckedModeBanner: false,
      routerConfig: router,
      builder: (context, child) {
        return Stack(
          children: [
            child!,
            if (callInfo != null) const CallOverlay(),
          ],
        );
      },
    );
  }
}

