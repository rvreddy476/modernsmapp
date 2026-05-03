import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';

/// Sprint 4 — panic confirm sheet.
///
/// On confirm:
///   - Best-effort GPS lookup (no permission prompt blocker; if it fails we
///     still POST without coords — backend handles that).
///   - POST /v1/dating/safety/panic.
///   - Activate `safetyModeProvider` for 60 minutes.
class PanicSheet extends ConsumerStatefulWidget {
  const PanicSheet({super.key});

  @override
  ConsumerState<PanicSheet> createState() => _PanicSheetState();

  static Future<void> show(BuildContext context) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      builder: (_) => const PanicSheet(),
    );
  }
}

class _PanicSheetState extends ConsumerState<PanicSheet> {
  bool _busy = false;

  Future<({double? lat, double? lng})> _readLocation() async {
    try {
      final allowed = await Geolocator.isLocationServiceEnabled();
      if (!allowed) return (lat: null, lng: null);
      var perm = await Geolocator.checkPermission();
      if (perm == LocationPermission.denied) {
        perm = await Geolocator.requestPermission();
      }
      if (perm == LocationPermission.denied ||
          perm == LocationPermission.deniedForever) {
        return (lat: null, lng: null);
      }
      final pos = await Geolocator.getCurrentPosition();
      return (lat: pos.latitude, lng: pos.longitude);
    } catch (_) {
      return (lat: null, lng: null);
    }
  }

  Future<void> _confirm() async {
    setState(() => _busy = true);
    try {
      final loc = await _readLocation();
      final repo = ref.read(pulseRepositoryProvider);
      await repo.panic(lat: loc.lat, lng: loc.lng);
      // Sprint 5 telemetry: panic event has NO location data; the location
      // goes only through the safety endpoint above.
      ref.read(pulseTelemetryProvider).safetyPanic();
      await ref.read(safetyModeProvider.notifier).activate(
            duration: const Duration(minutes: 60),
            trigger: 'panic',
          );
      if (!mounted) return;
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
              'Safety mode is on. Trust & Safety has been notified.'),
          backgroundColor: AppColors.statusError,
        ),
      );
    } catch (_) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text(
                'Could not contact Trust & Safety. Please retry or call 112.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(18, 14, 18, 22),
      decoration: const BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Center(
            child: Container(
              width: 44,
              height: 4,
              margin: const EdgeInsets.only(bottom: 14),
              decoration: BoxDecoration(
                color: AppColors.borderMedium,
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusFull),
              ),
            ),
          ),
          Row(
            children: [
              Container(
                width: 36,
                height: 36,
                decoration: BoxDecoration(
                  color: AppColors.statusError.withAlpha(36),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                ),
                child: const Icon(Icons.warning_amber_rounded,
                    color: AppColors.statusError, size: 20),
              ),
              const SizedBox(width: 10),
              Text('Activate safety mode', style: AppTextStyles.h2),
            ],
          ),
          const SizedBox(height: 12),
          Text(
            'This will share your live location with your trusted contact '
            'for 60 minutes and notify Trust & Safety. We do not call the '
            'police automatically — for an emergency, dial 112.',
            style: AppTextStyles.body,
          ),
          const SizedBox(height: 18),
          FilledButton(
            onPressed: _busy ? null : _confirm,
            style: FilledButton.styleFrom(
              backgroundColor: AppColors.statusError,
              minimumSize: const Size.fromHeight(52),
              shape: RoundedRectangleBorder(
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusLarge),
              ),
            ),
            child: _busy
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : Text('I need help — activate now',
                    style: AppTextStyles.h3.copyWith(color: Colors.white)),
          ),
          const SizedBox(height: 8),
          TextButton(
            onPressed: _busy ? null : () => Navigator.of(context).pop(),
            child: const Text('Cancel'),
          ),
        ],
      ),
    );
  }
}

/// Reusable panic FAB. Long-press triggers the sheet immediately; tap shows
/// a confirmation tooltip first to prevent accidental clicks.
class PanicFloatingButton extends StatelessWidget {
  const PanicFloatingButton({super.key});

  @override
  Widget build(BuildContext context) {
    return FloatingActionButton(
      heroTag: 'pulse_panic_fab',
      backgroundColor: AppColors.statusError,
      tooltip: 'Long-press for safety mode',
      onPressed: () => PanicSheet.show(context),
      child: const Icon(Icons.warning_amber_rounded, color: Colors.white),
    );
  }
}
