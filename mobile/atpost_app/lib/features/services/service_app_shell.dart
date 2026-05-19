import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/data/service_providers.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/widgets/service_icon.dart';
import 'package:atpost_app/features/services/widgets/service_permission_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Wraps a mini-app (typically a coming-soon placeholder) with the
/// Postbook-branded shell header and a permission gate.
///
/// First-mount flow:
/// 1. Diff [app.permissions] against grants stored in [ServicePermissionsStore].
/// 2. If anything is pending → show the permission sheet.
/// 3. On Allow → render [child]. On Deny → pop back to /services.
///
/// Active modules don't use this shell — they're navigated to directly.
class ServiceAppShell extends ConsumerStatefulWidget {
  const ServiceAppShell({super.key, required this.app, required this.child});

  final ServiceApp app;
  final Widget child;

  @override
  ConsumerState<ServiceAppShell> createState() => _ServiceAppShellState();
}

class _ServiceAppShellState extends ConsumerState<ServiceAppShell> {
  bool _checking = true;
  bool _permitted = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _gate());
  }

  Future<void> _gate() async {
    final store = ref.read(servicePermissionsStoreProvider);
    final pending =
        await store.pendingFor(widget.app.id, widget.app.permissions);

    if (!mounted) return;
    if (pending.isEmpty) {
      setState(() {
        _checking = false;
        _permitted = true;
      });
      return;
    }

    final allowed = await showServicePermissionSheet(
      context: context,
      app: widget.app,
      pending: pending,
      store: store,
    );

    if (!mounted) return;
    if (allowed) {
      setState(() {
        _checking = false;
        _permitted = true;
      });
    } else {
      // User denied — leave the shell.
      if (context.canPop()) {
        context.pop();
      } else {
        context.go('/services');
      }
    }
  }

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
        titleSpacing: 0,
        title: Row(
          children: [
            Container(
              width: 30,
              height: 30,
              decoration: BoxDecoration(
                color: widget.app.accentColor.withValues(alpha: 0.16),
                borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
              ),
              alignment: Alignment.center,
              child: Icon(
                iconForServiceName(widget.app.iconName),
                color: widget.app.accentColor,
                size: 18,
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    widget.app.name,
                    style: AppTextStyles.h3,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (widget.app.isVerified)
                    Text(
                      'Verified by VChat',
                      style: AppTextStyles.labelTiny.copyWith(
                        color: AppColors.posttubePrimary,
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
        actions: [
          IconButton(
            onPressed: () {},
            icon: const Icon(Icons.more_horiz_rounded),
            color: AppColors.textSecondary,
          ),
        ],
      ),
      body: _checking
          ? const Center(child: CircularProgressIndicator())
          : (_permitted ? widget.child : const SizedBox.shrink()),
    );
  }
}
