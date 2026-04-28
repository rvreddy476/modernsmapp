import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/coming_soon_screen.dart';
import 'package:atpost_app/features/services/data/service_registry.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/service_app_shell.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Routed widget for `/services/:slug`. Resolves the slug to a [ServiceApp]
/// and dispatches based on status:
///
///  - unknown slug → 404 placeholder
///  - active/beta  → redirect to the app's real route (no shell stacking)
///  - coming_soon  → render the shell + coming-soon placeholder
///  - disabled     → 404 placeholder (treat as not-found)
class ServiceSlugRouter extends ConsumerStatefulWidget {
  const ServiceSlugRouter({super.key, required this.slug});

  final String slug;

  @override
  ConsumerState<ServiceSlugRouter> createState() => _ServiceSlugRouterState();
}

class _ServiceSlugRouterState extends ConsumerState<ServiceSlugRouter> {
  ServiceApp? _app;
  bool _redirecting = false;

  @override
  void initState() {
    super.initState();
    _app = ServiceRegistry.bySlug(widget.slug);
    final app = _app;
    if (app != null && app.status.isOpenable && app.route != null) {
      _redirecting = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) context.go(app.route!);
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final app = _app;
    if (app == null || app.status == ServiceStatus.disabled) {
      return const _ServiceNotFoundScreen();
    }
    if (_redirecting) {
      return const Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(child: CircularProgressIndicator()),
      );
    }
    // coming_soon
    return ServiceAppShell(
      app: app,
      child: ComingSoonScreen(app: app),
    );
  }
}

class _ServiceNotFoundScreen extends StatelessWidget {
  const _ServiceNotFoundScreen();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded),
          color: AppColors.textPrimary,
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/services'),
        ),
      ),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(
                Icons.error_outline_rounded,
                size: 56,
                color: AppColors.textMuted,
              ),
              const SizedBox(height: 16),
              Text("That mini app doesn't exist.",
                  style: AppTextStyles.h2,
                  textAlign: TextAlign.center),
              const SizedBox(height: 8),
              Text(
                "It may have been renamed or removed.",
                style: AppTextStyles.body,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 24),
              FilledButton(
                onPressed: () => context.go('/services'),
                child: const Text('Browse Services'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
