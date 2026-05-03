import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/pulse/safety/panic_sheet.dart';
import 'package:atpost_app/features/pulse/safety/share_location_banner.dart';
import 'package:atpost_app/features/pulse/premium/paywalls/incognito_paywall.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — Safety Center.
///
/// Surfaces the eight safety sections per spec §6.10:
///   1. Trust badge (verification)
///   2. Vouching
///   3. Trusted contact
///   4. Block list & reports
///   5. Privacy & visibility (incognito gated, blur, photo visibility)
///   6. Quiet hours
///   7. Safe-meet check-in (premium)
///   8. Account safety (recent sessions, change phone, pause, delete)
class SafetyCenterScreen extends ConsumerWidget {
  const SafetyCenterScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    PulseBreadcrumbs.safetyCenterOpen();
    final contact = ref.watch(trustedContactProvider);
    final quiet = ref.watch(quietHoursProvider);

    void tap(String section, VoidCallback action) {
      PulseBreadcrumbs.safetySectionTap(section);
      action();
    }

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Safety Center', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
        ),
      ),
      floatingActionButton: const PanicFloatingButton(),
      body: SafeArea(
        child: ListView(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 96),
          children: [
            const ShareLocationBanner(),
            _SectionHeader(label: 'Identity & trust'),
            _ActionTile(
              icon: Icons.verified_user_outlined,
              title: 'Trust badge',
              subtitle:
                  'Verify your phone, selfie, or Aadhaar to unlock more of Pulse.',
              onTap: () => tap(
                'trust_badge',
                () => context.push('/pulse/verification'),
              ),
            ),
            _ActionTile(
              icon: Icons.handshake_outlined,
              title: 'Vouching',
              subtitle:
                  'Friends and community members co-sign your profile.',
              onTap: () => tap(
                'vouching',
                () => context.push('/pulse/safety/vouches'),
              ),
            ),
            _SectionHeader(label: 'Trusted contact'),
            _ActionTile(
              icon: Icons.contact_phone_outlined,
              title: contact == null
                  ? 'Add a trusted contact'
                  : 'Trusted contact: ${contact.name}',
              subtitle: contact == null
                  ? 'We share your live location with this person only when you choose.'
                  : contact.phone,
              onTap: () => tap(
                'trusted_contact',
                () => context.push('/pulse/safety/trusted-contact'),
              ),
            ),
            _SectionHeader(label: 'Block & report'),
            _ActionTile(
              icon: Icons.block,
              title: 'Block list',
              subtitle: 'See or unblock people you\'ve blocked.',
              onTap: () => tap(
                'block_list',
                () => context.push('/pulse/safety/blocks'),
              ),
            ),
            _ActionTile(
              icon: Icons.flag_outlined,
              title: 'My reports',
              subtitle: 'Reports you have filed and their status.',
              onTap: () => tap(
                'my_reports',
                () => context.push('/pulse/safety/reports'),
              ),
            ),
            _SectionHeader(label: 'Privacy & visibility'),
            _IncognitoTile(),
            _BlurModeTile(),
            _PhotoVisibilityTile(),
            _SectionHeader(label: 'Quiet hours'),
            _QuietHoursTile(quiet: quiet),
            _SectionHeader(label: 'Safe meet'),
            _ActionTile(
              icon: Icons.event_outlined,
              title: 'Safe-meet check-in',
              subtitle:
                  'Pulse Premium. Schedule a meet, get nudges, one-tap help.',
              onTap: () => tap('safe_meet', () {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                      content:
                          Text('Safe-meet check-in is a Pulse Premium feature.')),
                );
              }),
            ),
            _SectionHeader(label: 'Account safety'),
            _ActionTile(
              icon: Icons.devices_outlined,
              title: 'Recent sessions',
              subtitle: 'See where your account is signed in.',
              onTap: () => tap(
                'recent_sessions',
                () => context.push('/settings/security'),
              ),
            ),
            _ActionTile(
              icon: Icons.phone_android_outlined,
              title: 'Change phone number',
              subtitle: 'Update the number we use for OTP and trust signals.',
              onTap: () => tap(
                'change_phone',
                () => context.push('/settings/security'),
              ),
            ),
            _ActionTile(
              icon: Icons.pause_circle_outline,
              title: 'Pause Pulse',
              subtitle:
                  'Hide your profile and stop new matches without deleting.',
              onTap: () {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                    content: Text(
                        'Pause is wired in Sprint 5 — for now contact support.'),
                  ),
                );
              },
            ),
            _DangerTile(
              icon: Icons.delete_outline,
              title: 'Delete account',
              subtitle:
                  'Account and Pulse data are removed after a 30-day grace window.',
              onTap: () => _confirmDelete(context),
            ),
          ],
        ),
      ),
    );
  }

  void _confirmDelete(BuildContext context) {
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Delete your account?', style: AppTextStyles.h2),
        content: Text(
          'You have 30 days to change your mind. After that, your '
          'account, Pulse profile, vouches, and matches are removed '
          'permanently.',
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Keep my account'),
          ),
          FilledButton(
            onPressed: () {
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(
                  content: Text(
                      'Deletion request goes through Sprint 5 / settings flow.'),
                ),
              );
            },
            style: FilledButton.styleFrom(
                backgroundColor: AppColors.statusError),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.label});
  final String label;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 18, 4, 8),
      child: Text(label.toUpperCase(),
          style: AppTextStyles.labelTiny
              .copyWith(letterSpacing: 1.4)),
    );
  }
}

class _ActionTile extends StatelessWidget {
  const _ActionTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        leading: Icon(icon, color: AppColors.posttubePrimary),
        title: Text(title, style: AppTextStyles.h3),
        subtitle: Text(subtitle, style: AppTextStyles.bodySmall),
        trailing: const Icon(Icons.chevron_right,
            color: AppColors.textTertiary),
        onTap: onTap,
      ),
    );
  }
}

class _DangerTile extends StatelessWidget {
  const _DangerTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: AppColors.statusError.withAlpha(20),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusError.withAlpha(80)),
      ),
      child: ListTile(
        leading: Icon(icon, color: AppColors.statusError),
        title: Text(title,
            style: AppTextStyles.h3
                .copyWith(color: AppColors.statusError)),
        subtitle: Text(subtitle, style: AppTextStyles.bodySmall),
        trailing: const Icon(Icons.chevron_right,
            color: AppColors.textTertiary),
        onTap: onTap,
      ),
    );
  }
}

class _IncognitoTile extends ConsumerStatefulWidget {
  @override
  ConsumerState<_IncognitoTile> createState() => _IncognitoTileState();
}

class _IncognitoTileState extends ConsumerState<_IncognitoTile> {
  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: SwitchListTile(
        value: false,
        onChanged: (_) {
          // Sprint 5: route to the contextual paywall instead of a snackbar.
          IncognitoPaywall.show(context);
        },
        title: Semantics(
          header: true,
          child: Text('Incognito', style: AppTextStyles.h3),
        ),
        subtitle: Text(
            'Browse without your profile appearing on others\' Pulse. (Premium)',
            style: AppTextStyles.bodySmall),
      ),
    );
  }
}

class _BlurModeTile extends ConsumerStatefulWidget {
  @override
  ConsumerState<_BlurModeTile> createState() => _BlurModeTileState();
}

class _BlurModeTileState extends ConsumerState<_BlurModeTile> {
  bool _enabled = false;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: SwitchListTile(
        value: _enabled,
        onChanged: (v) => setState(() => _enabled = v),
        title: Text('Blur photos until match', style: AppTextStyles.h3),
        subtitle: Text(
          'Your primary photo appears blurred on Pulse cards until you both Spark.',
          style: AppTextStyles.bodySmall,
        ),
      ),
    );
  }
}

class _PhotoVisibilityTile extends ConsumerStatefulWidget {
  @override
  ConsumerState<_PhotoVisibilityTile> createState() =>
      _PhotoVisibilityTileState();
}

class _PhotoVisibilityTileState extends ConsumerState<_PhotoVisibilityTile> {
  String _value = 'public';

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        leading: const Icon(Icons.photo_library_outlined,
            color: AppColors.posttubePrimary),
        title: Text('Photo visibility', style: AppTextStyles.h3),
        subtitle: Text(
          _value == 'public'
              ? 'Public — anyone you match with can see all photos.'
              : 'Private — extra photos unlock after you Spark.',
          style: AppTextStyles.bodySmall,
        ),
        trailing: DropdownButton<String>(
          value: _value,
          underline: const SizedBox.shrink(),
          dropdownColor: AppColors.bgSecondary,
          items: const [
            DropdownMenuItem(value: 'public', child: Text('Public')),
            DropdownMenuItem(value: 'private', child: Text('Private')),
          ],
          onChanged: (v) => setState(() => _value = v ?? 'public'),
        ),
      ),
    );
  }
}

class _QuietHoursTile extends ConsumerWidget {
  const _QuietHoursTile({required this.quiet});

  final QuietHours quiet;

  Future<TimeOfDay?> _pick(BuildContext context, TimeOfDay initial) {
    return showTimePicker(context: context, initialTime: initial);
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.fromLTRB(14, 6, 14, 12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SwitchListTile(
            value: quiet.enabled,
            contentPadding: EdgeInsets.zero,
            onChanged: (v) => ref
                .read(quietHoursProvider.notifier)
                .save(quiet.copyWith(enabled: v)),
            title: Text('Quiet hours', style: AppTextStyles.h3),
            subtitle: Text(
              'No push notifications between these times.',
              style: AppTextStyles.bodySmall,
            ),
          ),
          Row(
            children: [
              Expanded(
                child: TextButton.icon(
                  onPressed: () async {
                    final t = await _pick(
                      context,
                      TimeOfDay(
                          hour: quiet.startHour, minute: quiet.startMinute),
                    );
                    if (t == null) return;
                    await ref.read(quietHoursProvider.notifier).save(
                          quiet.copyWith(
                              startHour: t.hour, startMinute: t.minute),
                        );
                  },
                  icon: const Icon(Icons.bedtime_outlined),
                  label: Text(
                    'Start ${quiet.startHour.toString().padLeft(2, '0')}:${quiet.startMinute.toString().padLeft(2, '0')}',
                  ),
                ),
              ),
              Expanded(
                child: TextButton.icon(
                  onPressed: () async {
                    final t = await _pick(
                      context,
                      TimeOfDay(
                          hour: quiet.endHour, minute: quiet.endMinute),
                    );
                    if (t == null) return;
                    await ref.read(quietHoursProvider.notifier).save(
                          quiet.copyWith(
                              endHour: t.hour, endMinute: t.minute),
                        );
                  },
                  icon: const Icon(Icons.wb_sunny_outlined),
                  label: Text(
                    'End ${quiet.endHour.toString().padLeft(2, '0')}:${quiet.endMinute.toString().padLeft(2, '0')}',
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
