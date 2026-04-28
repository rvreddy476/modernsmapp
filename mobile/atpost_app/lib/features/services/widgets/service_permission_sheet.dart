import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/data/service_permissions_store.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/widgets/service_icon.dart';
import 'package:flutter/material.dart';

/// Shows a modal bottom sheet asking the user to grant the [pending]
/// permissions for [app]. Resolves to `true` if the user tapped Allow,
/// `false` if Deny. Persists the result via [store].
Future<bool> showServicePermissionSheet({
  required BuildContext context,
  required ServiceApp app,
  required List<ServicePermission> pending,
  required ServicePermissionsStore store,
}) async {
  final result = await showModalBottomSheet<bool>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (ctx) => _ServicePermissionSheet(
      app: app,
      pending: pending,
      store: store,
    ),
  );
  return result ?? false;
}

class _ServicePermissionSheet extends StatelessWidget {
  const _ServicePermissionSheet({
    required this.app,
    required this.pending,
    required this.store,
  });

  final ServiceApp app;
  final List<ServicePermission> pending;
  final ServicePermissionsStore store;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      padding: EdgeInsets.only(
        left: 20,
        right: 20,
        top: 20,
        bottom: MediaQuery.of(context).viewInsets.bottom + 24,
      ),
      child: SafeArea(
        top: false,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Center(
              child: Container(
                width: 36,
                height: 4,
                decoration: BoxDecoration(
                  color: AppColors.borderMedium,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: 18),
            Row(
              children: [
                Container(
                  width: 48,
                  height: 48,
                  decoration: BoxDecoration(
                    color: app.accentColor.withValues(alpha: 0.16),
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusMedium),
                  ),
                  alignment: Alignment.center,
                  child: Icon(
                    iconForServiceName(app.iconName),
                    color: app.accentColor,
                    size: 24,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        app.name,
                        style: AppTextStyles.h2,
                      ),
                      Text(
                        'Wants access to:',
                        style: AppTextStyles.bodySmall,
                      ),
                    ],
                  ),
                ),
              ],
            ),
            const SizedBox(height: 18),
            ...pending.map((p) => Padding(
                  padding: const EdgeInsets.symmetric(vertical: 6),
                  child: Row(
                    children: [
                      Icon(p.icon,
                          size: 18, color: AppColors.textTertiary),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Text(p.label, style: AppTextStyles.body),
                      ),
                    ],
                  ),
                )),
            const SizedBox(height: 16),
            Text(
              'Permissions are controlled by Postbook, not by the mini app. '
              'You can revoke access at any time from settings.',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textMuted,
                height: 1.45,
              ),
            ),
            const SizedBox(height: 22),
            Row(
              children: [
                Expanded(
                  child: OutlinedButton(
                    onPressed: () async {
                      await store.deny(app.id, pending);
                      if (context.mounted) Navigator.of(context).pop(false);
                    },
                    style: OutlinedButton.styleFrom(
                      side: const BorderSide(
                        color: AppColors.borderMedium,
                      ),
                      padding: const EdgeInsets.symmetric(vertical: 14),
                      shape: RoundedRectangleBorder(
                        borderRadius:
                            BorderRadius.circular(AppSpacing.radiusMedium),
                      ),
                    ),
                    child: Text(
                      'Deny',
                      style: AppTextStyles.label
                          .copyWith(color: AppColors.textSecondary),
                    ),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: ElevatedButton(
                    onPressed: () async {
                      await store.grant(app.id, pending);
                      if (context.mounted) Navigator.of(context).pop(true);
                    },
                    style: ElevatedButton.styleFrom(
                      backgroundColor: app.accentColor,
                      foregroundColor: Colors.white,
                      padding: const EdgeInsets.symmetric(vertical: 14),
                      shape: RoundedRectangleBorder(
                        borderRadius:
                            BorderRadius.circular(AppSpacing.radiusMedium),
                      ),
                      elevation: 0,
                    ),
                    child: Text('Allow', style: AppTextStyles.label
                        .copyWith(color: Colors.white)),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
