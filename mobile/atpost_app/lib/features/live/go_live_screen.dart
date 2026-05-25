// Live-streaming v2: "Go Live" broadcaster form. Captures title +
// description + visibility, calls POST /v1/live/streams, then routes to
// the LiveKit publisher screen at /live/v2/:id/broadcast.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/live_streams_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

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

  @override
  void dispose() {
    _titleController.dispose();
    _descController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
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
                Text('Who can watch', style: AppTextStyles.labelSmall),
                const SizedBox(height: 6),
                _VisibilityChooser(
                  value: _visibility,
                  enabled: !_submitting,
                  onChanged: (v) => setState(() => _visibility = v),
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
                  onPressed: _submitting ? null : _submit,
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
