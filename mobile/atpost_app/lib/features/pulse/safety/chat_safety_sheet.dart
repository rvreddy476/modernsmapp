import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/pulse/safety/panic_sheet.dart';
import 'package:atpost_app/features/pulse/safety/report_block_sheet.dart';
import 'package:atpost_app/features/pulse/safety/safe_meet_sheet.dart';
import 'package:atpost_app/features/pulse/safety/share_location_banner.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — safety sheet that opens from the Pulse chat header.
///
/// Surfaces: Report, Block, Share live location, Schedule safe meet, Panic.
class ChatSafetySheet extends ConsumerWidget {
  const ChatSafetySheet({
    super.key,
    required this.otherUserId,
    this.otherUserName,
  });

  final String otherUserId;
  final String? otherUserName;

  static Future<void> show(
    BuildContext context, {
    required String otherUserId,
    String? otherUserName,
  }) {
    return showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      builder: (_) => ChatSafetySheet(
        otherUserId: otherUserId,
        otherUserName: otherUserName,
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(8, 14, 8, 18),
        child: Column(
          mainAxisSize: MainAxisSize.min,
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
            ListTile(
              leading:
                  const Icon(Icons.flag_outlined, color: AppColors.statusError),
              title: Text('Report ${otherUserName ?? 'this profile'}',
                  style: AppTextStyles.body),
              onTap: () {
                Navigator.of(context).pop();
                ReportSheet.show(
                  context,
                  targetUserId: otherUserId,
                  targetName: otherUserName,
                );
              },
            ),
            ListTile(
              leading: const Icon(Icons.block,
                  color: AppColors.statusError),
              title: Text('Block ${otherUserName ?? 'this profile'}',
                  style: AppTextStyles.body),
              onTap: () async {
                Navigator.of(context).pop();
                await showBlockDialog(
                  context,
                  ref,
                  targetUserId: otherUserId,
                  targetName: otherUserName,
                );
              },
            ),
            ListTile(
              leading: const Icon(Icons.location_on_outlined,
                  color: AppColors.posttubePrimary),
              title: Text('Share live location', style: AppTextStyles.body),
              subtitle: Text(
                'Sends your location to your trusted contact for 60 minutes.',
                style: AppTextStyles.labelSmall,
              ),
              onTap: () async {
                Navigator.of(context).pop();
                final contact = ref.read(trustedContactProvider);
                if (contact == null) {
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(
                        content: Text(
                            'Add a trusted contact in Safety Center first.')),
                  );
                  return;
                }
                final ok = await startShareLocation(
                  ref,
                  durationMinutes: 60,
                  contactId: contact.id,
                );
                if (!context.mounted) return;
                ScaffoldMessenger.of(context).showSnackBar(
                  SnackBar(
                    content: Text(ok
                        ? 'Sharing location with ${contact.name} for 60 min.'
                        : 'Could not start sharing.'),
                  ),
                );
              },
            ),
            ListTile(
              leading: const Icon(Icons.event_outlined,
                  color: AppColors.posttubePrimary),
              title: Text('Schedule safe meet', style: AppTextStyles.body),
              subtitle: Text('Pulse Premium feature',
                  style: AppTextStyles.labelSmall),
              onTap: () {
                Navigator.of(context).pop();
                SafeMeetSheet.show(
                  context,
                  withUserId: otherUserId,
                  withUserName: otherUserName,
                );
              },
            ),
            ListTile(
              leading: const Icon(Icons.warning_amber_rounded,
                  color: AppColors.statusError),
              title: Text('Panic — I need help',
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.statusError)),
              onTap: () {
                Navigator.of(context).pop();
                PanicSheet.show(context);
              },
            ),
            const SizedBox(height: 8),
            TextButton(
              onPressed: () {
                Navigator.of(context).pop();
                context.push('/pulse/safety');
              },
              child: const Text('Open Safety Center'),
            ),
          ],
        ),
      ),
    );
  }
}
