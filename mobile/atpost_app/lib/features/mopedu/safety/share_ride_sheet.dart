// Mopedu — share-ride sheet.
//
// Sprint 3. Generates a one-time share token via the rider service,
// then surfaces the URL with copy / WhatsApp / SMS launch buttons.
// `url_launcher` is NOT in pubspec yet — WhatsApp/SMS buttons fall
// back to a "Copy and paste into your app" snackbar so the customer
// still has a path forward.
//
// PRIVACY: telemetry is a single counter event. The token, ride id,
// and share URL never enter telemetry.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ShareRideSheet extends ConsumerStatefulWidget {
  const ShareRideSheet({super.key, required this.rideId});

  final String rideId;

  @override
  ConsumerState<ShareRideSheet> createState() => _ShareRideSheetState();
}

class _ShareRideSheetState extends ConsumerState<ShareRideSheet> {
  ShareTokenResult? _result;
  bool _loading = true;
  Object? _error;

  @override
  void initState() {
    super.initState();
    _generate();
  }

  Future<void> _generate() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final repo = ref.read(mopeduRepositoryProvider);
      final r = await repo.createShareToken(widget.rideId);
      // PRIVACY: counter only. NEVER log token/ride_id.
      ref.read(mopeduTelemetryProvider).mopeduSafetyShareRideCreated();
      if (!mounted) return;
      setState(() {
        _result = r;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e;
      });
    }
  }

  Future<void> _copy() async {
    final url = _result?.shareUrl;
    if (url == null) return;
    await Clipboard.setData(ClipboardData(text: url));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Share link copied to clipboard.')),
    );
  }

  void _shareViaWhatsApp() {
    // url_launcher not in pubspec yet. Surface a clear fallback so the
    // customer still has a viable path: copy-and-paste.
    _fallbackShare('WhatsApp');
  }

  void _shareViaSms() {
    _fallbackShare('SMS');
  }

  void _fallbackShare(String channel) {
    final url = _result?.shareUrl;
    if (url == null) return;
    Clipboard.setData(ClipboardData(text: url));
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          'Link copied. Paste it into $channel to share with someone you trust.',
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final padding = MediaQuery.of(context).viewInsets.bottom;
    return Padding(
      padding: EdgeInsets.fromLTRB(20, 20, 20, 20 + padding),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Center(
            child: Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderMedium,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 14),
          Row(
            children: [
              const Icon(
                Icons.share_location,
                color: AppColors.posttubePrimary,
              ),
              const SizedBox(width: 8),
              Text('Share live ride', style: AppTextStyles.h2),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            'Send this one-time link to someone you trust. They can see '
            'your live location, driver, and ETA.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 16),
          if (_loading)
            const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: CircularProgressIndicator(),
              ),
            )
          else if (_error != null)
            _ErrorBlock(error: _error!, onRetry: _generate)
          else if (_result != null)
            _SharePayload(
              result: _result!,
              onCopy: _copy,
              onWhatsApp: _shareViaWhatsApp,
              onSms: _shareViaSms,
            ),
          const SizedBox(height: 12),
          SizedBox(
            height: 44,
            child: OutlinedButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('Close'),
            ),
          ),
        ],
      ),
    );
  }
}

class _SharePayload extends StatelessWidget {
  const _SharePayload({
    required this.result,
    required this.onCopy,
    required this.onWhatsApp,
    required this.onSms,
  });

  final ShareTokenResult result;
  final VoidCallback onCopy;
  final VoidCallback onWhatsApp;
  final VoidCallback onSms;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  result.shareUrl,
                  style: AppTextStyles.mono,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              IconButton(
                tooltip: 'Copy',
                onPressed: onCopy,
                icon: const Icon(Icons.copy, size: 18),
              ),
            ],
          ),
        ),
        const SizedBox(height: 8),
        Text(
          'This link is valid until your ride ends.',
          style: AppTextStyles.labelSmall,
        ),
        const SizedBox(height: 14),
        Row(
          children: [
            Expanded(
              child: ElevatedButton.icon(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.statusSuccess,
                  foregroundColor: Colors.white,
                ),
                onPressed: onWhatsApp,
                icon: const Icon(Icons.chat),
                label: const Text('WhatsApp'),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: ElevatedButton.icon(
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.posttubePrimary,
                  foregroundColor: Colors.white,
                ),
                onPressed: onSms,
                icon: const Icon(Icons.sms),
                label: const Text('SMS'),
              ),
            ),
          ],
        ),
      ],
    );
  }
}

class _ErrorBlock extends StatelessWidget {
  const _ErrorBlock({required this.error, required this.onRetry});

  final Object error;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.statusError.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.statusError.withValues(alpha: 0.3)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Could not generate share link.',
            style: AppTextStyles.label.copyWith(color: AppColors.statusError),
          ),
          const SizedBox(height: 4),
          Text(error.toString(), style: AppTextStyles.bodySmall),
          const SizedBox(height: 10),
          OutlinedButton(onPressed: onRetry, child: const Text('Retry')),
        ],
      ),
    );
  }
}
