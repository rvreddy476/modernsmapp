// Live-streaming v2: "Go Live" broadcaster form. Captures title +
// description + visibility + optional cover image + optional schedule,
// calls POST /v1/live/streams, then routes to the LiveKit publisher
// screen at /live/v2/:id/broadcast.

import 'dart:io';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/live_streams_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

class GoLiveScreen extends ConsumerStatefulWidget {
  const GoLiveScreen({super.key});

  @override
  ConsumerState<GoLiveScreen> createState() => _GoLiveScreenState();
}

class _GoLiveScreenState extends ConsumerState<GoLiveScreen> {
  final _formKey = GlobalKey<FormState>();
  final _titleController = TextEditingController();
  final _descController = TextEditingController();
  String _visibility = 'public';
  bool _submitting = false;
  String? _error;

  // Cover-image upload state. The 3-step media flow (init -> PUT presigned
  // -> confirm) is handled by ApiClient.uploadMedia, so we only need to
  // hold on to the local thumbnail + uploaded media_id here.
  XFile? _coverFile;
  String? _coverMediaId;
  bool _uploadingCover = false;

  // Scheduled-stream state. When the toggle is off (default) the stream
  // starts immediately on submit (current behaviour — scheduled_at stays
  // null and the broadcaster page kicks straight into publish mode).
  // When on, the date+time picker is shown and the API call carries
  // scheduled_at; the backend keeps the stream in `scheduled` status
  // and the broadcaster page renders the "scheduled at <time>" panel
  // instead of starting LiveKit.
  bool _scheduleEnabled = false;
  DateTime? _scheduledAt;

  @override
  void dispose() {
    _titleController.dispose();
    _descController.dispose();
    super.dispose();
  }

  Future<void> _pickCover() async {
    if (_submitting || _uploadingCover) return;
    final picker = ImagePicker();
    final picked = await picker.pickImage(
      source: ImageSource.gallery,
      imageQuality: 85,
    );
    if (picked == null) return;
    setState(() {
      _coverFile = picked;
      _coverMediaId = null;
      _uploadingCover = true;
      _error = null;
    });
    try {
      final api = ref.read(apiClientProvider);
      final mediaId = await api.uploadMedia(picked, type: 'image');
      if (!mounted) return;
      setState(() {
        _coverMediaId = mediaId;
        _uploadingCover = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _coverFile = null;
        _coverMediaId = null;
        _uploadingCover = false;
        _error = 'Couldn\'t upload the cover image. Try a different file.';
      });
    }
  }

  void _clearCover() {
    if (_submitting || _uploadingCover) return;
    setState(() {
      _coverFile = null;
      _coverMediaId = null;
    });
  }

  Future<void> _pickScheduledAt() async {
    if (_submitting) return;
    final now = DateTime.now();
    final initialDate = _scheduledAt ?? now.add(const Duration(hours: 1));
    final date = await showDatePicker(
      context: context,
      initialDate: initialDate.isBefore(now) ? now : initialDate,
      firstDate: now,
      lastDate: now.add(const Duration(days: 90)),
    );
    if (date == null || !mounted) return;
    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(initialDate),
    );
    if (time == null || !mounted) return;
    setState(() {
      _scheduledAt = DateTime(
        date.year,
        date.month,
        date.day,
        time.hour,
        time.minute,
      );
    });
  }

  Future<void> _submit() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    if (_uploadingCover) return;
    if (_scheduleEnabled && _scheduledAt == null) {
      setState(() => _error = 'Pick a date and time for the scheduled stream.');
      return;
    }
    if (_scheduleEnabled &&
        _scheduledAt != null &&
        _scheduledAt!.isBefore(DateTime.now())) {
      setState(() => _error = 'Pick a scheduled time in the future.');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final controller = ref.read(liveBroadcasterControllerProvider);
      final stream = await controller.create(
        title: _titleController.text.trim(),
        description: _descController.text.trim(),
        visibility: _visibility,
        coverMediaId: _coverMediaId,
        scheduledAt: _scheduleEnabled ? _scheduledAt : null,
      );
      if (!mounted) return;
      context.go('/live/v2/${stream.id}/broadcast');
    } catch (err) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = 'Couldn\'t start the stream. Try again.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Go Live', style: AppTextStyles.h2),
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(16),
          child: Form(
            key: _formKey,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  children: [
                    Container(
                      width: 36,
                      height: 36,
                      decoration: BoxDecoration(
                        color: AppColors.liveRed.withValues(alpha: 0.15),
                        borderRadius: BorderRadius.circular(10),
                      ),
                      child: const Icon(Icons.podcasts,
                          color: AppColors.liveRed, size: 18),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        'Set up your broadcast',
                        style: AppTextStyles.h3,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 18),
                Text('Title', style: AppTextStyles.labelSmall),
                const SizedBox(height: 6),
                TextFormField(
                  controller: _titleController,
                  enabled: !_submitting,
                  maxLength: 140,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  decoration: _inputDecoration(
                      hint: 'What\'s the stream about?'),
                  validator: (v) =>
                      (v == null || v.trim().isEmpty) ? 'Required' : null,
                ),
                const SizedBox(height: 8),
                Text('Description', style: AppTextStyles.labelSmall),
                const SizedBox(height: 6),
                TextFormField(
                  controller: _descController,
                  enabled: !_submitting,
                  maxLength: 500,
                  maxLines: 3,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  decoration: _inputDecoration(
                      hint: 'Add a short description (optional)'),
                ),
                const SizedBox(height: 16),
                Text('Cover image', style: AppTextStyles.labelSmall),
                const SizedBox(height: 6),
                _CoverPicker(
                  file: _coverFile,
                  uploading: _uploadingCover,
                  uploaded: _coverMediaId != null,
                  enabled: !_submitting,
                  onPick: _pickCover,
                  onClear: _clearCover,
                ),
                const SizedBox(height: 16),
                Text('Who can watch', style: AppTextStyles.labelSmall),
                const SizedBox(height: 6),
                _VisibilityChooser(
                  value: _visibility,
                  enabled: !_submitting,
                  onChanged: (v) => setState(() => _visibility = v),
                ),
                const SizedBox(height: 16),
                _SchedulePicker(
                  enabled: !_submitting,
                  scheduleEnabled: _scheduleEnabled,
                  scheduledAt: _scheduledAt,
                  onToggle: (v) => setState(() {
                    _scheduleEnabled = v;
                    if (!v) _scheduledAt = null;
                  }),
                  onPickTime: _pickScheduledAt,
                ),
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Container(
                    padding: const EdgeInsets.all(10),
                    decoration: BoxDecoration(
                      color: AppColors.statusError.withValues(alpha: 0.1),
                      borderRadius: BorderRadius.circular(8),
                      border: Border.all(
                          color: AppColors.statusError.withValues(alpha: 0.3)),
                    ),
                    child: Text(
                      _error!,
                      style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.statusError),
                    ),
                  ),
                ],
                const SizedBox(height: 22),
                FilledButton.icon(
                  onPressed: (_submitting || _uploadingCover) ? null : _submit,
                  icon: _submitting
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(
                              color: Colors.white, strokeWidth: 2),
                        )
                      : const Icon(Icons.podcasts),
                  label: Text(
                    _submitting ? 'Creating…' : 'Continue',
                    style: AppTextStyles.label.copyWith(color: Colors.white),
                  ),
                  style: FilledButton.styleFrom(
                    backgroundColor: AppColors.liveRed,
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  InputDecoration _inputDecoration({required String hint}) {
    return InputDecoration(
      hintText: hint,
      hintStyle:
          AppTextStyles.body.copyWith(color: AppColors.textDimmest),
      filled: true,
      fillColor: AppColors.bgCard,
      counterStyle: AppTextStyles.labelTiny,
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: BorderSide(color: AppColors.borderSubtle),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide:
            BorderSide(color: AppColors.liveRed.withValues(alpha: 0.5)),
      ),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: BorderSide(color: AppColors.borderSubtle),
      ),
    );
  }
}

class _CoverPicker extends StatelessWidget {
  final XFile? file;
  final bool uploading;
  final bool uploaded;
  final bool enabled;
  final VoidCallback onPick;
  final VoidCallback onClear;

  const _CoverPicker({
    required this.file,
    required this.uploading,
    required this.uploaded,
    required this.enabled,
    required this.onPick,
    required this.onClear,
  });

  @override
  Widget build(BuildContext context) {
    if (file == null) {
      return InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: enabled ? onPick : null,
        child: Container(
          padding: const EdgeInsets.symmetric(vertical: 22, horizontal: 12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(
              color: AppColors.borderSubtle,
              style: BorderStyle.solid,
            ),
          ),
          child: Column(
            children: [
              const Icon(Icons.add_photo_alternate_outlined,
                  color: AppColors.textTertiary, size: 28),
              const SizedBox(height: 6),
              Text(
                'Upload a cover image',
                style: AppTextStyles.bodySmall
                    .copyWith(color: AppColors.textTertiary),
              ),
            ],
          ),
        ),
      );
    }
    return Stack(
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(12),
          child: Image.file(
            File(file!.path),
            height: 160,
            width: double.infinity,
            fit: BoxFit.cover,
          ),
        ),
        if (uploading)
          Positioned.fill(
            child: ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: Container(
                color: Colors.black.withValues(alpha: 0.4),
                child: const Center(
                  child: SizedBox(
                    width: 22,
                    height: 22,
                    child: CircularProgressIndicator(
                        color: Colors.white, strokeWidth: 2),
                  ),
                ),
              ),
            ),
          ),
        Positioned(
          top: 6,
          right: 6,
          child: Material(
            color: Colors.black.withValues(alpha: 0.55),
            shape: const CircleBorder(),
            child: InkWell(
              customBorder: const CircleBorder(),
              onTap: enabled ? onClear : null,
              child: const Padding(
                padding: EdgeInsets.all(6),
                child: Icon(Icons.close, color: Colors.white, size: 16),
              ),
            ),
          ),
        ),
        if (!uploading && uploaded)
          Positioned(
            bottom: 6,
            left: 6,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
              decoration: BoxDecoration(
                color: Colors.black.withValues(alpha: 0.55),
                borderRadius: BorderRadius.circular(8),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.check_circle,
                      color: Colors.greenAccent, size: 14),
                  const SizedBox(width: 4),
                  Text(
                    'Uploaded',
                    style: AppTextStyles.labelTiny.copyWith(
                      color: Colors.white,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

class _SchedulePicker extends StatelessWidget {
  final bool enabled;
  final bool scheduleEnabled;
  final DateTime? scheduledAt;
  final ValueChanged<bool> onToggle;
  final VoidCallback onPickTime;

  const _SchedulePicker({
    required this.enabled,
    required this.scheduleEnabled,
    required this.scheduledAt,
    required this.onToggle,
    required this.onPickTime,
  });

  String _formatScheduled(DateTime t) {
    String two(int n) => n.toString().padLeft(2, '0');
    return '${t.year}-${two(t.month)}-${two(t.day)} ${two(t.hour)}:${two(t.minute)}';
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.schedule,
                  color: AppColors.textTertiary, size: 18),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Schedule for later',
                        style: AppTextStyles.bodyMedium
                            .copyWith(color: AppColors.textPrimary)),
                    const SizedBox(height: 2),
                    Text(
                      scheduleEnabled
                          ? 'Picks a future start time; the broadcast page will wait.'
                          : 'Off — the stream starts immediately on submit.',
                      style: AppTextStyles.labelSmall.copyWith(
                          color: AppColors.textTertiary),
                    ),
                  ],
                ),
              ),
              Switch(
                value: scheduleEnabled,
                activeThumbColor: AppColors.liveRed,
                onChanged: enabled ? onToggle : null,
              ),
            ],
          ),
          if (scheduleEnabled) ...[
            const SizedBox(height: 8),
            InkWell(
              borderRadius: BorderRadius.circular(8),
              onTap: enabled ? onPickTime : null,
              child: Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 10, vertical: 10),
                decoration: BoxDecoration(
                  color: AppColors.bgPrimary,
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Row(
                  children: [
                    const Icon(Icons.event,
                        color: AppColors.textTertiary, size: 16),
                    const SizedBox(width: 8),
                    Text(
                      scheduledAt != null
                          ? _formatScheduled(scheduledAt!)
                          : 'Pick date and time',
                      style: AppTextStyles.body
                          .copyWith(color: AppColors.textPrimary),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _VisibilityChooser extends StatelessWidget {
  final String value;
  final bool enabled;
  final ValueChanged<String> onChanged;

  const _VisibilityChooser({
    required this.value,
    required this.enabled,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final options = <_VisibilityOption>[
      const _VisibilityOption(
        value: 'public',
        label: 'Public',
        sub: 'Anyone on AtPost can watch',
        icon: Icons.public,
      ),
      const _VisibilityOption(
        value: 'followers',
        label: 'Followers only',
        sub: 'Only your followers can watch',
        icon: Icons.people_alt_outlined,
      ),
      const _VisibilityOption(
        value: 'paid',
        label: 'Paid',
        sub: 'Subscribers only (coming soon)',
        icon: Icons.attach_money,
      ),
    ];
    return Column(
      children: [
        for (final opt in options)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: InkWell(
              borderRadius: BorderRadius.circular(12),
              onTap: enabled ? () => onChanged(opt.value) : null,
              child: Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 10),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(
                    color: value == opt.value
                        ? AppColors.liveRed
                        : AppColors.borderSubtle,
                    width: value == opt.value ? 1.2 : 1,
                  ),
                ),
                child: Row(
                  children: [
                    Icon(opt.icon,
                        size: 18, color: AppColors.textTertiary),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(opt.label,
                              style: AppTextStyles.bodyMedium.copyWith(
                                  color: AppColors.textPrimary)),
                          const SizedBox(height: 2),
                          Text(opt.sub,
                              style: AppTextStyles.labelSmall.copyWith(
                                  color: AppColors.textTertiary)),
                        ],
                      ),
                    ),
                    Radio<String>(
                      value: opt.value,
                      groupValue: value,
                      activeColor: AppColors.liveRed,
                      onChanged: enabled
                          ? (v) {
                              if (v != null) onChanged(v);
                            }
                          : null,
                    ),
                  ],
                ),
              ),
            ),
          ),
      ],
    );
  }
}

class _VisibilityOption {
  final String value;
  final String label;
  final String sub;
  final IconData icon;
  const _VisibilityOption({
    required this.value,
    required this.label,
    required this.sub,
    required this.icon,
  });
}
