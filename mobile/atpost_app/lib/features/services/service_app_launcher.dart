import 'package:atpost_app/features/services/data/service_providers.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/widgets/service_permission_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Single entry point for opening any [ServiceApp]. Centralizes the
/// permission-gate-then-navigate flow so callers (Explore drawer, Services
/// screen, /services/:slug route) don't drift.
///
/// Behavior:
/// - **Coming-soon / disabled** or **no route** → push `/services/<slug>`
///   so the shell + coming-soon placeholder renders.
/// - **Active / beta with route** → check pending permissions, show the
///   permission sheet if needed, then push the app's native route. No
///   shell wrapping (existing modules have their own Scaffolds).
Future<void> openServiceApp(
  BuildContext context,
  WidgetRef ref,
  ServiceApp app,
) async {
  if (!app.status.isOpenable || app.route == null) {
    if (context.mounted) context.push('/services/${app.slug}');
    return;
  }

  final store = ref.read(servicePermissionsStoreProvider);
  final pending = await store.pendingFor(app.id, app.permissions);

  if (!context.mounted) return;

  if (pending.isEmpty) {
    context.push(app.route!);
    return;
  }

  final allowed = await showServicePermissionSheet(
    context: context,
    app: app,
    pending: pending,
    store: store,
  );

  if (!context.mounted || !allowed) return;
  context.push(app.route!);
}
