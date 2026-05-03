import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 4 — share-live-location banner.
///
/// Shows a countdown ("Sharing location · 47:21 left") plus a big red
/// "Stop sharing" button whenever `safetyModeProvider.active` is true.
/// Returns `SizedBox.shrink()` when off so it composes cheaply into any
/// scaffold body.
class ShareLocationBanner extends ConsumerStatefulWidget {
  const ShareLocationBanner({super.key});

  @override
  ConsumerState<ShareLocationBanner> createState() =>
      _ShareLocationBannerState();
}

class _ShareLocationBannerState extends ConsumerState<ShareLocationBanner> {
  Timer? _ticker;

  @override
  void initState() {
    super.initState();
    _ticker = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _ticker?.cancel();
    super.dispose();
  }

  String _format(Duration d) {
    final m = d.inMinutes.remainder(60).toString().padLeft(2, '0');
    final s = d.inSeconds.remainder(60).toString().padLeft(2, '0');
    final h = d.inHours;
    if (h > 0) return '$h:$m:$s';
    return '$m:$s';
  }

  @override
  Widget build(BuildContext context) {
    final mode = ref.watch(safetyModeProvider);
    if (!mode.active) return const SizedBox.shrink();
    final remaining = mode.remaining ?? Duration.zero;
    final label = mode.trigger == 'panic'
        ? 'Safety mode active'
        : 'Sharing live location';
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
      decoration: BoxDecoration(
        color: AppColors.statusError.withAlpha(36),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusError.withAlpha(140)),
      ),
      child: Row(
        children: [
          const Icon(Icons.location_on_outlined,
              color: AppColors.statusError, size: 20),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(label,
                    style: AppTextStyles.h3
                        .copyWith(color: AppColors.statusError)),
                const SizedBox(height: 2),
                Text('${_format(remaining)} left',
                    style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          FilledButton(
            onPressed: () async {
              await ref.read(safetyModeProvider.notifier).stop();
            },
            style: FilledButton.styleFrom(
              backgroundColor: AppColors.statusError,
              shape: RoundedRectangleBorder(
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusFull),
              ),
            ),
            child: const Text('Stop sharing'),
          ),
        ],
      ),
    );
  }
}

/// Helper that calls share-location on the backend, then activates the
/// local banner. Used by the safety center toggle and the chat safety sheet.
Future<bool> startShareLocation(
  WidgetRef ref, {
  required int durationMinutes,
  required String contactId,
}) async {
  try {
    final repo = ref.read(pulseRepositoryProvider);
    await repo.shareLocation(
      durationMinutes: durationMinutes,
      contactId: contactId,
    );
    await ref.read(safetyModeProvider.notifier).activate(
          duration: Duration(minutes: durationMinutes),
          trigger: 'share_location',
        );
    return true;
  } catch (_) {
    return false;
  }
}
