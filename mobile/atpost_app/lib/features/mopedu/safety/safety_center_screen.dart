// Mopedu — customer Safety Center.
//
// Sprint 3. Reachable from:
//   * Booking-in-progress screen (replaces the placeholder Safety button)
//   * Mopedu home (small shield icon top-right)
//   * Direct route /mopedu/safety
//
// Visual language reuses the Pulse Safety Center pattern (section
// headers + action tiles + glass-card SOS hero) so customers feel a
// consistent "safety surface" across mini-apps.
//
// PRIVACY: this screen never logs phone, lat/lng, ride_id, or token.
// The SOS event payload is a single counter; the trusted-contact display
// shows the masked phone only.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/features/mopedu/safety/share_ride_sheet.dart';
import 'package:atpost_app/features/mopedu/safety/sos_confirmation_sheet.dart';
import 'package:atpost_app/features/mopedu/safety/trusted_contact_picker_sheet.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:atpost_app/services/mopedu_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SafetyCenterScreen extends ConsumerStatefulWidget {
  const SafetyCenterScreen({super.key});

  @override
  ConsumerState<SafetyCenterScreen> createState() =>
      _SafetyCenterScreenState();
}

class _SafetyCenterScreenState extends ConsumerState<SafetyCenterScreen> {
  bool _firedOpened = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_firedOpened) return;
      _firedOpened = true;
      ref.read(mopeduTelemetryProvider).mopeduSafetyOpened();
      MopeduBreadcrumbs.safetyCenterOpen();
    });
  }

  Future<void> _refresh() async {
    ref.invalidate(trustedContactProvider);
    ref.invalidate(myComplaintsProvider);
  }

  @override
  Widget build(BuildContext context) {
    final contact = ref.watch(trustedContactProvider);
    final complaints = ref.watch(myComplaintsProvider);
    final activeRideId = ref.watch(currentRideProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Safety Center', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/mopedu'),
        ),
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
          children: [
            const _SectionHeader('Trusted contact'),
            _TrustedContactCard(asyncContact: contact),
            const SizedBox(height: 16),
            const _SectionHeader('Share live ride'),
            _ShareRideCard(activeRideId: activeRideId),
            const SizedBox(height: 16),
            const _SectionHeader('Emergency'),
            _SosHero(activeRideId: activeRideId),
            const SizedBox(height: 16),
            const _SectionHeader('Recent complaints'),
            _ComplaintsList(asyncComplaints: complaints),
            const SizedBox(height: 16),
            const _SectionHeader('More help'),
            const _HelpLineTile(),
            const SizedBox(height: 16),
            const _PrivacyNote(),
          ],
        ),
      ),
    );
  }
}

// ─── Building blocks ──────────────────────────────────────────────────

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.label);
  final String label;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(left: 4, top: 8, bottom: 8),
      child: Text(label, style: AppTextStyles.h3),
    );
  }
}

BoxDecoration _cardDecoration() {
  return BoxDecoration(
    color: AppColors.bgSecondary,
    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
    border: Border.all(color: AppColors.borderSubtle),
  );
}

// ─── Trusted contact card ─────────────────────────────────────────────

class _TrustedContactCard extends ConsumerWidget {
  const _TrustedContactCard({required this.asyncContact});

  final AsyncValue<TrustedContact?> asyncContact;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return asyncContact.when(
      loading: () => Container(
        padding: const EdgeInsets.all(20),
        decoration: _cardDecoration(),
        child: const Center(child: CircularProgressIndicator()),
      ),
      error: (e, _) => Container(
        padding: const EdgeInsets.all(14),
        decoration: _cardDecoration(),
        child: Text(
          'Could not load trusted contact.',
          style: AppTextStyles.bodySmall,
        ),
      ),
      data: (c) {
        if (c == null) return const _TrustedContactEmpty();
        return _TrustedContactPresent(contact: c);
      },
    );
  }
}

class _TrustedContactEmpty extends StatelessWidget {
  const _TrustedContactEmpty();

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: () => _openPicker(context),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: _cardDecoration(),
        child: Row(
          children: [
            const Icon(
              Icons.contact_phone_outlined,
              color: AppColors.posttubePrimary,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Add a trusted contact', style: AppTextStyles.label),
                  const SizedBox(height: 4),
                  Text(
                    'Share your live location during SOS.',
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            const Icon(
              Icons.chevron_right,
              color: AppColors.textTertiary,
            ),
          ],
        ),
      ),
    );
  }
}

class _TrustedContactPresent extends ConsumerWidget {
  const _TrustedContactPresent({required this.contact});

  final TrustedContact contact;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: _cardDecoration(),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const CircleAvatar(
                radius: 18,
                backgroundColor: AppColors.bgTertiary,
                child: Icon(
                  Icons.person,
                  size: 18,
                  color: AppColors.textTertiary,
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(contact.name, style: AppTextStyles.label),
                    const SizedBox(height: 2),
                    Text(
                      contact.maskedPhone,
                      style: AppTextStyles.bodySmall,
                    ),
                    if (contact.relationship != null &&
                        contact.relationship!.isNotEmpty)
                      Text(
                        contact.relationship!,
                        style: AppTextStyles.labelSmall,
                      ),
                  ],
                ),
              ),
              IconButton(
                tooltip: 'Edit',
                icon: const Icon(
                  Icons.edit_outlined,
                  color: AppColors.textTertiary,
                  size: 18,
                ),
                onPressed: () => _openPicker(context, existing: contact),
              ),
            ],
          ),
          const Divider(color: AppColors.borderSubtle, height: 16),
          SwitchListTile.adaptive(
            contentPadding: EdgeInsets.zero,
            value: contact.shareLocationOnSos,
            onChanged: (v) async {
              final repo = ref.read(mopeduRepositoryProvider);
              try {
                await repo.setTrustedContact(
                  contact.copyWith(shareLocationOnSos: v),
                );
                ref.invalidate(trustedContactProvider);
              } catch (_) {
                if (!context.mounted) return;
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Could not update.')),
                );
              }
            },
            title: Text(
              'Share location on SOS',
              style: AppTextStyles.label,
            ),
            subtitle: Text(
              'Send a live ride link when you press SOS.',
              style: AppTextStyles.bodySmall,
            ),
            activeColor: AppColors.posttubePrimary,
          ),
        ],
      ),
    );
  }
}

Future<void> _openPicker(
  BuildContext context, {
  TrustedContact? existing,
}) async {
  await showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (_) => TrustedContactPickerSheet(existing: existing),
  );
}

// ─── Share live ride card ─────────────────────────────────────────────

class _ShareRideCard extends StatelessWidget {
  const _ShareRideCard({required this.activeRideId});

  final String? activeRideId;

  @override
  Widget build(BuildContext context) {
    final hasActive = activeRideId != null && activeRideId!.isNotEmpty;
    return Opacity(
      opacity: hasActive ? 1.0 : 0.5,
      child: InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        onTap: hasActive
            ? () => _openShareSheet(context, activeRideId!)
            : null,
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: _cardDecoration(),
          child: Row(
            children: [
              const Icon(Icons.share_location, color: AppColors.posttubePrimary),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      hasActive
                          ? 'Share live ride'
                          : 'No active ride',
                      style: AppTextStyles.label,
                    ),
                    const SizedBox(height: 4),
                    Text(
                      hasActive
                          ? 'Send a one-time link with your live location, '
                              'driver, and ETA. Valid until your ride ends.'
                          : 'You can share a ride link only while a ride '
                              'is in progress.',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              if (hasActive)
                const Icon(
                  Icons.chevron_right,
                  color: AppColors.textTertiary,
                ),
            ],
          ),
        ),
      ),
    );
  }
}

Future<void> _openShareSheet(BuildContext context, String rideId) async {
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgSecondary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (_) => ShareRideSheet(rideId: rideId),
  );
}

// ─── SOS hero ─────────────────────────────────────────────────────────

class _SosHero extends ConsumerStatefulWidget {
  const _SosHero({required this.activeRideId});
  final String? activeRideId;

  @override
  ConsumerState<_SosHero> createState() => _SosHeroState();
}

class _SosHeroState extends ConsumerState<_SosHero>
    with SingleTickerProviderStateMixin {
  late final AnimationController _pulse;

  @override
  void initState() {
    super.initState();
    _pulse = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1500),
    )..repeat(reverse: true);
  }

  @override
  void dispose() {
    _pulse.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onLongPress: _onLongPress,
      child: Container(
        padding: const EdgeInsets.all(20),
        decoration: BoxDecoration(
          gradient: const LinearGradient(
            colors: [AppColors.statusError, AppColors.postgramPrimary],
          ),
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
        child: Column(
          children: [
            FadeTransition(
              opacity: _pulse,
              child: const Icon(
                Icons.warning_amber_rounded,
                color: Colors.white,
                size: 38,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              'Long-press to send SOS',
              style: AppTextStyles.h2.copyWith(color: Colors.white),
            ),
            const SizedBox(height: 4),
            Text(
              'Trust & Safety will be alerted immediately.',
              style: AppTextStyles.bodySmall.copyWith(color: Colors.white70),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _onLongPress() async {
    final id = widget.activeRideId;
    MopeduBreadcrumbs.sosTriggered(rideId: id);
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      isDismissible: false,
      enableDrag: false,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => SosConfirmationSheet(rideId: id),
    );
  }
}

// ─── Recent complaints ────────────────────────────────────────────────

class _ComplaintsList extends ConsumerWidget {
  const _ComplaintsList({required this.asyncComplaints});

  final AsyncValue<List<Complaint>> asyncComplaints;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return asyncComplaints.when(
      loading: () => Container(
        padding: const EdgeInsets.all(20),
        decoration: _cardDecoration(),
        child: const Center(child: CircularProgressIndicator()),
      ),
      error: (e, _) => Container(
        padding: const EdgeInsets.all(14),
        decoration: _cardDecoration(),
        child: Text(
          'Could not load complaints.',
          style: AppTextStyles.bodySmall,
        ),
      ),
      data: (list) {
        if (list.isEmpty) {
          return Container(
            padding: const EdgeInsets.all(14),
            decoration: _cardDecoration(),
            child: Text(
              'No complaints yet.',
              style: AppTextStyles.bodySmall,
            ),
          );
        }
        final top = list.take(3).toList();
        return Column(
          children: [
            for (final c in top) _ComplaintRow(complaint: c),
            if (list.length > 3)
              TextButton(
                onPressed: () => context.push('/mopedu/complaints'),
                child: const Text('View all'),
              ),
          ],
        );
      },
    );
  }
}

class _ComplaintRow extends StatelessWidget {
  const _ComplaintRow({required this.complaint});
  final Complaint complaint;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.all(12),
      decoration: _cardDecoration(),
      child: Row(
        children: [
          const Icon(Icons.flag_outlined, color: AppColors.statusWarning),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(complaint.category.label, style: AppTextStyles.label),
                if (complaint.description != null &&
                    complaint.description!.isNotEmpty)
                  Text(
                    complaint.description!,
                    style: AppTextStyles.bodySmall,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
              ],
            ),
          ),
          _StatusPill(status: complaint.status),
        ],
      ),
    );
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});
  final String status;

  @override
  Widget build(BuildContext context) {
    final color = ComplaintStatus.isTerminal(status)
        ? AppColors.statusSuccess
        : AppColors.statusWarning;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Text(
        ComplaintStatus.label(status),
        style: AppTextStyles.labelSmall.copyWith(color: color),
      ),
    );
  }
}

// ─── Help line + privacy ──────────────────────────────────────────────

class _HelpLineTile extends StatelessWidget {
  const _HelpLineTile();

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      onTap: () {
        // url_launcher is not in pubspec yet — surface dial instructions.
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text(
              'Dial 1800-XXX-XXXX for the Mopedu help line.',
            ),
          ),
        );
      },
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: _cardDecoration(),
        child: Row(
          children: [
            const Icon(Icons.support_agent, color: AppColors.posttubePrimary),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Mopedu help line', style: AppTextStyles.label),
                  const SizedBox(height: 2),
                  Text(
                    '1800-XXX-XXXX · 24/7 support',
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
            const Icon(Icons.phone, color: AppColors.textTertiary),
          ],
        ),
      ),
    );
  }
}

class _PrivacyNote extends StatelessWidget {
  const _PrivacyNote();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary.withValues(alpha: 0.6),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(
            Icons.privacy_tip_outlined,
            color: AppColors.textTertiary,
            size: 18,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'Your location is shared only when you press SOS or '
              'generate a share-ride link. We don’t track you '
              'outside an active ride.',
              style: AppTextStyles.bodySmall,
            ),
          ),
        ],
      ),
    );
  }
}
