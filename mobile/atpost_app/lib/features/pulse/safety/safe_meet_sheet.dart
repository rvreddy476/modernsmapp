import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 4 — schedule a safe-meet.
///
/// Premium gate: backend returns HTTP 402 for free plans. We surface that as
/// a paywall snackbar; v1 doesn't deep-link to a checkout flow yet.
class SafeMeetSheet extends ConsumerStatefulWidget {
  const SafeMeetSheet({
    super.key,
    required this.withUserId,
    this.withUserName,
  });

  final String withUserId;
  final String? withUserName;

  static Future<void> show(
    BuildContext context, {
    required String withUserId,
    String? withUserName,
  }) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      builder: (_) => SafeMeetSheet(
        withUserId: withUserId,
        withUserName: withUserName,
      ),
    );
  }

  @override
  ConsumerState<SafeMeetSheet> createState() => _SafeMeetSheetState();
}

class _SafeMeetSheetState extends ConsumerState<SafeMeetSheet> {
  final _venueController = TextEditingController();
  DateTime? _when;
  bool _busy = false;

  @override
  void dispose() {
    _venueController.dispose();
    super.dispose();
  }

  Future<void> _pickDate() async {
    final now = DateTime.now();
    final date = await showDatePicker(
      context: context,
      firstDate: now,
      lastDate: now.add(const Duration(days: 60)),
      initialDate: now.add(const Duration(days: 1)),
    );
    if (date == null || !mounted) return;
    final time = await showTimePicker(
      context: context,
      initialTime: const TimeOfDay(hour: 18, minute: 0),
    );
    if (time == null) return;
    setState(() {
      _when = DateTime(
          date.year, date.month, date.day, time.hour, time.minute);
    });
  }

  Future<void> _save() async {
    if (_when == null || _venueController.text.trim().isEmpty) return;
    setState(() => _busy = true);
    try {
      final repo = ref.read(pulseRepositoryProvider);
      // Lat/lng are required by the spec but we don't have a place picker
      // yet — Sprint 5 follow-up. Send 0/0 and let the backend treat as
      // "venue-name only".
      await repo.scheduleSafeMeet(
        withUserId: widget.withUserId,
        when: _when!,
        lat: 0,
        lng: 0,
        venueName: _venueController.text.trim(),
      );
      if (!mounted) return;
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Safe meet scheduled.')),
      );
    } on DioException catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      if (e.response?.statusCode == 402) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text(
                'Safe-meet check-in is a Pulse Premium feature.'),
          ),
        );
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not schedule safe meet.')),
        );
      }
    } catch (_) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not schedule safe meet.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final viewInsets = MediaQuery.of(context).viewInsets;
    return Padding(
      padding: EdgeInsets.only(bottom: viewInsets.bottom),
      child: Container(
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
            Text(
              'Schedule a safe meet'
              '${widget.withUserName != null ? ' with ${widget.withUserName}' : ''}',
              style: AppTextStyles.h2,
            ),
            const SizedBox(height: 6),
            Text(
              'We will nudge you to check in before, during, and after. '
              'If you tap "I need help", Trust & Safety is notified.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 14),
            ListTile(
              contentPadding: EdgeInsets.zero,
              leading: const Icon(Icons.event,
                  color: AppColors.posttubePrimary),
              title: Text(
                _when == null
                    ? 'Pick a date and time'
                    : 'When: ${_when!.toLocal()}',
                style: AppTextStyles.body,
              ),
              onTap: _pickDate,
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _venueController,
              style: AppTextStyles.body
                  .copyWith(color: AppColors.textPrimary),
              decoration: const InputDecoration(
                  hintText: 'Venue (e.g. Blue Tokai, Indiranagar)'),
            ),
            const SizedBox(height: 14),
            FilledButton(
              onPressed: _busy ||
                      _when == null ||
                      _venueController.text.trim().isEmpty
                  ? null
                  : _save,
              style: FilledButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                minimumSize: const Size.fromHeight(48),
              ),
              child: _busy
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: Colors.white),
                    )
                  : Text('Schedule',
                      style:
                          AppTextStyles.h3.copyWith(color: Colors.white)),
            ),
          ],
        ),
      ),
    );
  }
}
