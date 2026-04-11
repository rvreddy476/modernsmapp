import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/mini_apps/mini_app_permissions.dart';
import 'package:flutter/material.dart';

Future<List<String>?> showMiniAppPermissionPrompt({
  required BuildContext context,
  required String appName,
  required List<String> requestedPermissions,
  List<String> initiallyGrantedPermissions = const [],
}) {
  final normalizedRequested = normalizeMiniAppPermissions(requestedPermissions);
  if (normalizedRequested.isEmpty) {
    return Future.value(const <String>[]);
  }

  final initialSelection = normalizedRequested
      .where(initiallyGrantedPermissions.toSet().contains)
      .toSet();

  return showModalBottomSheet<List<String>>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
    ),
    builder: (context) {
      return _MiniAppPermissionPromptSheet(
        appName: appName,
        requestedPermissions: normalizedRequested,
        initialSelection: initialSelection,
      );
    },
  );
}

class _MiniAppPermissionPromptSheet extends StatefulWidget {
  final String appName;
  final List<String> requestedPermissions;
  final Set<String> initialSelection;

  const _MiniAppPermissionPromptSheet({
    required this.appName,
    required this.requestedPermissions,
    required this.initialSelection,
  });

  @override
  State<_MiniAppPermissionPromptSheet> createState() =>
      _MiniAppPermissionPromptSheetState();
}

class _MiniAppPermissionPromptSheetState
    extends State<_MiniAppPermissionPromptSheet> {
  late final Set<String> _selectedPermissions;

  @override
  void initState() {
    super.initState();
    _selectedPermissions = {...widget.initialSelection};
    if (_selectedPermissions.isEmpty) {
      _selectedPermissions.addAll(widget.requestedPermissions);
    }
  }

  @override
  Widget build(BuildContext context) {
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;

    return SafeArea(
      top: false,
      child: Padding(
        padding: EdgeInsets.fromLTRB(24, 24, 24, 24 + bottomInset),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Review Permissions', style: AppTextStyles.h3),
            const SizedBox(height: 8),
            Text(
              '${widget.appName} is requesting access to the following platform features.',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textTertiary,
              ),
            ),
            const SizedBox(height: 20),
            ...widget.requestedPermissions.map((permission) {
              final definition = miniAppPermissionFor(permission);
              return Container(
                margin: const EdgeInsets.only(bottom: 12),
                decoration: BoxDecoration(
                  color: AppColors.bgPrimary,
                  borderRadius: BorderRadius.circular(16),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: SwitchListTile.adaptive(
                  value: _selectedPermissions.contains(permission),
                  onChanged: (enabled) {
                    setState(() {
                      if (enabled) {
                        _selectedPermissions.add(permission);
                      } else {
                        _selectedPermissions.remove(permission);
                      }
                    });
                  },
                  activeThumbColor: AppColors.postbookPrimary,
                  activeTrackColor: AppColors.postbookPrimary.withValues(
                    alpha: 0.35,
                  ),
                  title: Text(definition.title, style: AppTextStyles.label),
                  subtitle: Text(
                    definition.description,
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textTertiary,
                    ),
                  ),
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 4,
                  ),
                ),
              );
            }),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: OutlinedButton(
                    onPressed: () => Navigator.of(context).pop(),
                    style: OutlinedButton.styleFrom(
                      foregroundColor: AppColors.textPrimary,
                      side: const BorderSide(color: AppColors.borderSubtle),
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                    child: const Text('Cancel'),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: ElevatedButton(
                    onPressed: () {
                      Navigator.of(
                        context,
                      ).pop(_selectedPermissions.toList(growable: false));
                    },
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppColors.postbookPrimary,
                      foregroundColor: Colors.white,
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                    child: const Text('Continue'),
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
