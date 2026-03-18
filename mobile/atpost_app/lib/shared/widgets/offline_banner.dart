import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/connectivity_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// A thin amber banner that appears at the top of the screen when offline.
///
/// Wrap your Scaffold body with this widget to get automatic offline indication.
class OfflineBanner extends ConsumerWidget {
  const OfflineBanner({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isOffline = ref.watch(isOfflineProvider);

    return Column(
      children: [
        if (isOffline)
          Container(
            width: double.infinity,
            padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 16),
            color: AppColors.statusWarning.withValues(alpha: 0.9),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(Icons.wifi_off, size: 14, color: Colors.white),
                const SizedBox(width: 6),
                Text(
                  'You are offline',
                  style: AppTextStyles.labelSmall.copyWith(color: Colors.white),
                ),
              ],
            ),
          )
              .animate()
              .slideY(begin: -1, end: 0, duration: 300.ms)
              .fadeIn(duration: 300.ms),
        Expanded(child: child),
      ],
    );
  }
}
