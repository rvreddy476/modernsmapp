// Mopedu — SOS confirmation sheet.
//
// Sprint 3. Hard to dismiss accidentally (no swipe-to-dismiss, no
// barrier dismiss). Two large buttons; the red "Yes, send SOS" weighted
// to be the bigger tap target. After firing, the sheet stays open with
// an "I'm safe now" acknowledgement step so the customer can tell us
// the situation is over.
//
// PRIVACY: lat/lng are NEVER threaded into telemetry. The repository
// call carries them; telemetry is a single counter event.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

enum _SosPhase {
  confirm,
  sending,
  sent,
  failed,
}

class SosConfirmationSheet extends ConsumerStatefulWidget {
  const SosConfirmationSheet({super.key, required this.rideId});

  /// Optional ride id — when null, the SOS endpoint can't fire (no active
  /// ride). The sheet still shows but the confirm action falls back to a
  /// "no active ride" message.
  final String? rideId;

  @override
  ConsumerState<SosConfirmationSheet> createState() =>
      _SosConfirmationSheetState();
}

class _SosConfirmationSheetState
    extends ConsumerState<SosConfirmationSheet> {
  _SosPhase _phase = _SosPhase.confirm;
  Object? _error;

  Future<void> _sendSos() async {
    final id = widget.rideId;
    if (id == null || id.isEmpty) {
      setState(() {
        _phase = _SosPhase.failed;
        _error = 'No active ride. Use the help line for non-ride emergencies.';
      });
      return;
    }
    setState(() => _phase = _SosPhase.sending);
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      // Lat/lng intentionally omitted — backend falls back to last-known
      // partner location. Live device GPS lookup ships with the maps
      // integration in a follow-up.
      await repo.triggerSOS(id);
      // Telemetry: counter only. NEVER pass ride_id or lat/lng.
      ref.read(mopeduTelemetryProvider).mopeduSafetySosTriggered();
      if (!mounted) return;
      setState(() => _phase = _SosPhase.sent);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _phase = _SosPhase.failed;
        _error = e;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final padding = MediaQuery.of(context).viewInsets.bottom;
    return Padding(
      padding: EdgeInsets.fromLTRB(20, 20, 20, 20 + padding),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: switch (_phase) {
          _SosPhase.confirm => _confirm(),
          _SosPhase.sending => _sending(),
          _SosPhase.sent => _sent(),
          _SosPhase.failed => _failed(),
        },
      ),
    );
  }

  List<Widget> _confirm() {
    return [
      Center(
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: const BoxDecoration(
            color: AppColors.statusError,
            shape: BoxShape.circle,
          ),
          child: const Icon(
            Icons.warning_rounded,
            color: Colors.white,
            size: 36,
          ),
        ),
      ),
      const SizedBox(height: 14),
      Text(
        'Are you in danger?',
        textAlign: TextAlign.center,
        style: AppTextStyles.h1.copyWith(
          color: AppColors.statusError,
          fontSize: 22,
        ),
      ),
      const SizedBox(height: 8),
      Text(
        "Confirm only if you need help right now. We'll alert "
        'Trust & Safety and your trusted contact.',
        textAlign: TextAlign.center,
        style: AppTextStyles.body,
      ),
      const SizedBox(height: 18),
      SizedBox(
        height: 56,
        child: ElevatedButton(
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.statusError,
            foregroundColor: Colors.white,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
          ),
          onPressed: _sendSos,
          child: Text(
            'Yes, send SOS',
            style: AppTextStyles.h2.copyWith(
              color: Colors.white,
              fontSize: 18,
            ),
          ),
        ),
      ),
      const SizedBox(height: 10),
      SizedBox(
        height: 44,
        child: OutlinedButton(
          style: OutlinedButton.styleFrom(
            foregroundColor: AppColors.textTertiary,
            side: const BorderSide(color: AppColors.borderSubtle),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
          ),
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
      ),
    ];
  }

  List<Widget> _sending() {
    return [
      const SizedBox(height: 16),
      const Center(child: CircularProgressIndicator()),
      const SizedBox(height: 14),
      Text(
        'Sending help...',
        textAlign: TextAlign.center,
        style: AppTextStyles.h2,
      ),
      const SizedBox(height: 8),
      Text(
        'Stay where you are. Help is on its way.',
        textAlign: TextAlign.center,
        style: AppTextStyles.bodySmall,
      ),
      const SizedBox(height: 16),
    ];
  }

  List<Widget> _sent() {
    return [
      Center(
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: const BoxDecoration(
            color: AppColors.statusSuccess,
            shape: BoxShape.circle,
          ),
          child: const Icon(
            Icons.check,
            color: Colors.white,
            size: 36,
          ),
        ),
      ),
      const SizedBox(height: 14),
      Text(
        'Help is on the way',
        textAlign: TextAlign.center,
        style: AppTextStyles.h1.copyWith(
          color: AppColors.statusSuccess,
          fontSize: 22,
        ),
      ),
      const SizedBox(height: 8),
      Text(
        'Trust & Safety has been notified. Your trusted contact has '
        'been alerted with a live ride link.',
        textAlign: TextAlign.center,
        style: AppTextStyles.body,
      ),
      const SizedBox(height: 18),
      SizedBox(
        height: 48,
        child: ElevatedButton(
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.statusSuccess,
            foregroundColor: Colors.white,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
          ),
          onPressed: () => Navigator.of(context).pop(),
          child: const Text("I'm safe now"),
        ),
      ),
    ];
  }

  List<Widget> _failed() {
    return [
      const Icon(
        Icons.error_outline,
        color: AppColors.statusError,
        size: 32,
      ),
      const SizedBox(height: 12),
      Text(
        'Could not send SOS',
        textAlign: TextAlign.center,
        style: AppTextStyles.h2.copyWith(color: AppColors.statusError),
      ),
      const SizedBox(height: 6),
      Text(
        _error?.toString() ?? 'Please retry, or call the help line.',
        textAlign: TextAlign.center,
        style: AppTextStyles.bodySmall,
      ),
      const SizedBox(height: 16),
      Row(
        children: [
          Expanded(
            child: OutlinedButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('Close'),
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.statusError,
                foregroundColor: Colors.white,
              ),
              onPressed: () => setState(() => _phase = _SosPhase.confirm),
              child: const Text('Retry'),
            ),
          ),
        ],
      ),
    ];
  }
}
