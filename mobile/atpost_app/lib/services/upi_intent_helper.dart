// UPI Intent helper — Phase 2 Sprint 1 (consumer wallet).
//
// `url_launcher` is not in pubspec.yaml. Per task constraint we cannot add
// new packages this sprint, so we mirror the `razorpay_commerce_stub.dart`
// pattern — a placeholder bottom sheet that surfaces the UPI Intent URL,
// prompts the user to copy it (or pastes it via a manual share menu), and
// returns control to the caller.
//
// When a future sprint wires `url_launcher`, swap `_LaunchSheet` for the
// `launchUrl(url, mode: LaunchMode.externalApplication)` call. The public
// `launchUPIIntent` signature stays the same.
//
// PRIVACY: the URL contains a payee VPA + amount; we never log the URL.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// Launch the UPI Intent URL minted by `wallet-service`.
///
/// In production with `url_launcher` wired in, this opens the user's default
/// UPI app (GPay, PhonePe, BHIM, Paytm, etc.) which then handles the
/// `upi://pay?...` deep link.
///
/// In Sprint 1 we surface a confirmation sheet that lets the user copy the
/// URL and switch apps manually. The caller polls the top-up status either
/// way; the sheet exists only so dev/staging can finish the loop.
///
/// Returns `true` if the launch was attempted (or accepted by the user),
/// `false` if the user dismissed the sheet.
Future<bool> launchUPIIntent(
  BuildContext context,
  String upiIntentUrl,
) async {
  if (upiIntentUrl.isEmpty) {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text('Could not generate UPI Intent. Please retry.'),
      ),
    );
    return false;
  }
  final result = await showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgPrimary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
    ),
    builder: (ctx) => _LaunchSheet(url: upiIntentUrl),
  );
  return result ?? false;
}

class _LaunchSheet extends StatelessWidget {
  const _LaunchSheet({required this.url});

  final String url;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(
        AppSpacing.xxl,
        AppSpacing.l,
        AppSpacing.xxl,
        AppSpacing.xxl + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderSubtle,
                borderRadius: BorderRadius.circular(999),
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.l),
          Text(
            'Open in UPI app',
            style: AppTextStyles.h2,
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: AppSpacing.xs),
          Text(
            '(SDK launcher to wire in next sprint)',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: AppSpacing.xxl),
          Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.bgSecondary,
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'UPI Intent URL',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textTertiary,
                  ),
                ),
                const SizedBox(height: AppSpacing.s),
                Text(
                  url,
                  style: AppTextStyles.mono,
                  maxLines: 4,
                  overflow: TextOverflow.ellipsis,
                ),
              ],
            ),
          ),
          const SizedBox(height: AppSpacing.l),
          Text(
            'On a real device this opens GPay / PhonePe / BHIM / Paytm '
            'directly. For now copy the URL and paste it into your UPI app, '
            'then tap "I paid" — we will poll the status.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: AppSpacing.xxl),
          ElevatedButton.icon(
            onPressed: () async {
              await Clipboard.setData(ClipboardData(text: url));
              if (!context.mounted) return;
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(content: Text('UPI URL copied')),
              );
            },
            icon: const Icon(Icons.copy_outlined),
            label: const Text('Copy URL'),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.bgTertiary,
              foregroundColor: AppColors.textPrimary,
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
          ),
          const SizedBox(height: AppSpacing.m),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('I paid, poll status'),
          ),
          const SizedBox(height: AppSpacing.s),
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text(
              'Cancel',
              style: TextStyle(color: AppColors.textTertiary),
            ),
          ),
        ],
      ),
    );
  }
}
