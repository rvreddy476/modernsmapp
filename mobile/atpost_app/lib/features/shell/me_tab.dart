// Me tab — AtPost super-app shell.
//
// The "everything else" tab. Five vertical sections:
//
//   1. Profile header     avatar, name, @handle, View profile + Edit
//   2. Wallet card        balance + Add Money / Send buttons
//   3. App launcher grid  4-col grid of all 8 modules + a 9th "More" tile
//   4. Quick links        saved, watch later, liked, orders, pulse profile
//   5. Settings + sign out
//
// The launcher grid is the "8-app" door — every tap routes to the existing
// standalone module surface. Telemetry tags every tap with the module key.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/models/wallet.dart';
import 'package:atpost_app/features/mopedu/mopedu_gate.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/providers/wallet_providers.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/shell_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class MeTab extends ConsumerWidget {
  const MeTab({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.only(bottom: 100),
          children: const [
            _ProfileHeader(),
            SizedBox(height: 12),
            _WalletCard(),
            SizedBox(height: 12),
            _PartnerModeSection(),
            SizedBox(height: 16),
            _LauncherGrid(),
            SizedBox(height: 16),
            _QuickLinks(),
            SizedBox(height: 16),
            _SettingsSection(),
          ],
        ),
      ),
    );
  }
}

// ─── Profile header ────────────────────────────────────────────────────

class _ProfileHeader extends ConsumerWidget {
  const _ProfileHeader();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncMe = ref.watch(currentUserProvider);
    return asyncMe.when(
      data: (me) => _profileRow(context, me),
      loading: () => const Padding(
        padding: EdgeInsets.all(16),
        child: Center(child: CircularProgressIndicator()),
      ),
      error: (_, _) => Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          'Could not load your profile.',
          style: AppTextStyles.bodySmall,
        ),
      ),
    );
  }

  Widget _profileRow(BuildContext context, User me) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
      child: Row(
        children: [
          CircleAvatar(
            radius: 32,
            backgroundColor: AppColors.bgTertiary,
            backgroundImage: me.hasAvatar ? NetworkImage(me.avatarUrl) : null,
            child: !me.hasAvatar
                ? Text(
                    (me.displayName.isNotEmpty
                            ? me.displayName[0]
                            : '?')
                        .toUpperCase(),
                    style: AppTextStyles.h2,
                  )
                : null,
          ),
          const SizedBox(width: 14),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(me.displayName, style: AppTextStyles.h2),
                const SizedBox(height: 2),
                Text(
                  '@${me.username}',
                  style: AppTextStyles.bodySmall,
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    _SmallButton(
                      label: 'View profile',
                      icon: Icons.visibility_outlined,
                      onTap: () => context.push('/profile/${me.id}'),
                    ),
                    const SizedBox(width: 8),
                    _SmallButton(
                      label: 'Edit',
                      icon: Icons.edit,
                      onTap: () => context.push('/settings/profile'),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _SmallButton extends StatelessWidget {
  const _SmallButton({
    required this.label,
    required this.icon,
    required this.onTap,
  });

  final String label;
  final IconData icon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(99),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(99),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 14, color: AppColors.textTertiary),
              const SizedBox(width: 6),
              Text(label, style: AppTextStyles.labelSmall),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Wallet card ───────────────────────────────────────────────────────

class _WalletCard extends ConsumerWidget {
  const _WalletCard();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncBal = ref.watch(walletBalanceProvider);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: GestureDetector(
        onTap: () => context.push('/wallet'),
        child: Container(
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            gradient: AppColors.ctaGradient,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  const Icon(
                    Icons.account_balance_wallet,
                    color: Colors.white,
                    size: 18,
                  ),
                  const SizedBox(width: 6),
                  Text(
                    'VChat Wallet',
                    style: AppTextStyles.label.copyWith(color: Colors.white),
                  ),
                  const Spacer(),
                  const Icon(
                    Icons.chevron_right,
                    color: Colors.white,
                    size: 20,
                  ),
                ],
              ),
              const SizedBox(height: 8),
              Text(
                _balanceText(asyncBal),
                style: AppTextStyles.h1.copyWith(color: Colors.white),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: _WalletAction(
                      label: 'Add Money',
                      icon: Icons.add,
                      onTap: () => context.push('/wallet/top-up'),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: _WalletAction(
                      label: 'Send',
                      icon: Icons.send,
                      onTap: () => context.push('/wallet/send'),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  String _balanceText(AsyncValue<WalletBalance> v) {
    return v.maybeWhen(
      data: (b) => formatRupees(b.availablePaise),
      orElse: () => '—',
    );
  }
}

class _WalletAction extends StatelessWidget {
  const _WalletAction({
    required this.label,
    required this.icon,
    required this.onTap,
  });

  final String label;
  final IconData icon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(99),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 12),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: 0.18),
            borderRadius: BorderRadius.circular(99),
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(icon, color: Colors.white, size: 16),
              const SizedBox(width: 6),
              Text(
                label,
                style: AppTextStyles.label.copyWith(color: Colors.white),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── App launcher grid ─────────────────────────────────────────────────

class _LauncherGrid extends ConsumerWidget {
  const _LauncherGrid();

  static const _tiles = <_LauncherTile>[
    _LauncherTile(
      key: ShellModule.feed,
      label: 'Feed',
      icon: Icons.home,
      route: '/',
      color: AppColors.postbookPrimary,
    ),
    _LauncherTile(
      key: ShellModule.posttube,
      label: 'Posttube',
      icon: Icons.play_circle_filled,
      route: '/posttube',
      color: AppColors.posttubePrimary,
    ),
    _LauncherTile(
      key: ShellModule.reels,
      label: 'Reels',
      icon: Icons.movie_filter,
      route: '/reels',
      color: AppColors.postgramPrimary,
    ),
    _LauncherTile(
      key: ShellModule.pulse,
      label: 'Pulse',
      icon: Icons.favorite_border,
      route: '/pulse',
      color: AppColors.postgramPrimary,
    ),
    _LauncherTile(
      key: ShellModule.qa,
      label: 'Q&A',
      icon: Icons.help_outline,
      route: '/qa',
      color: AppColors.posttubePrimary,
    ),
    _LauncherTile(
      key: ShellModule.shop,
      label: 'Shop',
      icon: Icons.shopping_bag,
      route: '/commerce',
      color: AppColors.statusWarning,
    ),
    _LauncherTile(
      key: ShellModule.wallet,
      label: 'Wallet',
      icon: Icons.account_balance_wallet,
      route: '/wallet',
      color: AppColors.postbookPrimary,
    ),
    _LauncherTile(
      key: ShellModule.billpay,
      label: 'Bill-pay',
      icon: Icons.receipt_long,
      route: '/billpay',
      color: AppColors.statusSuccess,
    ),
    _LauncherTile(
      key: ShellModule.mopedu,
      label: 'Mopedu',
      icon: Icons.directions_car,
      route: '/mopedu',
      color: AppColors.posttubePrimary,
    ),
    _LauncherTile(
      key: ShellModule.more,
      label: 'More',
      icon: Icons.apps,
      route: null,
      color: AppColors.accentPurple,
    ),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Sprint 5: hide the Mopedu launcher tile entirely when the master
    // `mopedu_enabled_master` flag is OFF or while the flag is loading
    // (we treat unknown as "hide" — same fail-closed posture as
    // `MopeduGate`). The tile re-appears the next time `Me` rebuilds
    // after the flag flips on.
    final mopeduFlag = ref.watch(mopeduEnabledProvider);
    final showMopedu = mopeduFlag.maybeWhen(
      data: (enabled) => enabled,
      orElse: () => false,
    );
    final visibleTiles = _tiles
        .where((t) => showMopedu || t.key != ShellModule.mopedu)
        .toList(growable: false);

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 4, 4, 8),
              child: Text('Apps', style: AppTextStyles.h3),
            ),
            GridView.builder(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              gridDelegate:
                  const SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: 4,
                crossAxisSpacing: 8,
                mainAxisSpacing: 8,
                childAspectRatio: 0.9,
              ),
              itemCount: visibleTiles.length,
              itemBuilder: (context, i) {
                final t = visibleTiles[i];
                return _TileButton(
                  tile: t,
                  onTap: () => _onTileTap(context, ref, t),
                );
              },
            ),
          ],
        ),
      ),
    );
  }

  void _onTileTap(BuildContext context, WidgetRef ref, _LauncherTile t) {
    ref.read(shellTelemetryProvider).shellLauncherTileTapped(t.key);
    if (t.key == ShellModule.more) {
      _showMoreSheet(context);
      return;
    }
    final route = t.route;
    if (route != null) context.push(route);
  }

  void _showMoreSheet(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (sheetContext) {
        return SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('More mini-apps', style: AppTextStyles.h2),
                const SizedBox(height: 4),
                Text(
                  'These are coming after launch.',
                  style: AppTextStyles.bodySmall,
                ),
                const SizedBox(height: 16),
                _MoreTile(
                  label: 'Food',
                  icon: Icons.restaurant,
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('Food coming soon')),
                    );
                  },
                ),
                _MoreTile(
                  label: 'Mini-apps',
                  icon: Icons.apps,
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    context.push('/apps');
                  },
                ),
                _MoreTile(
                  label: 'Review videos',
                  icon: Icons.rate_review_outlined,
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    context.push('/reviewer/dashboard');
                  },
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

class _LauncherTile {
  const _LauncherTile({
    required this.key,
    required this.label,
    required this.icon,
    required this.color,
    required this.route,
  });

  final String key;
  final String label;
  final IconData icon;
  final Color color;
  final String? route;
}

class _TileButton extends StatelessWidget {
  const _TileButton({required this.tile, required this.onTap});

  final _LauncherTile tile;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        onTap: onTap,
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Container(
              width: 48,
              height: 48,
              decoration: BoxDecoration(
                color: tile.color.withValues(alpha: 0.18),
                borderRadius: BorderRadius.circular(14),
              ),
              child: Icon(tile.icon, color: tile.color, size: 22),
            ),
            const SizedBox(height: 6),
            Text(
              tile.label,
              style: AppTextStyles.labelSmall,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }
}

class _MoreTile extends StatelessWidget {
  const _MoreTile({
    required this.label,
    required this.icon,
    required this.onTap,
  });

  final String label;
  final IconData icon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: CircleAvatar(
        backgroundColor: AppColors.bgTertiary,
        child: Icon(icon, color: AppColors.textPrimary),
      ),
      title: Text(label, style: AppTextStyles.label),
      trailing: const Icon(
        Icons.chevron_right,
        color: AppColors.textTertiary,
      ),
      onTap: onTap,
    );
  }
}

// ─── Quick links ──────────────────────────────────────────────────────

class _QuickLinks extends StatelessWidget {
  const _QuickLinks();

  static const _links = <_LinkRow>[
    _LinkRow(label: 'Saved items', icon: Icons.bookmark, route: '/bookmarks'),
    _LinkRow(
      label: 'Watch later',
      icon: Icons.watch_later,
      route: '/posttube',
    ),
    _LinkRow(label: 'Liked posts', icon: Icons.favorite, route: '/profile/me'),
    _LinkRow(
      label: 'Order history',
      icon: Icons.shopping_bag,
      route: '/commerce/orders',
    ),
    _LinkRow(
      label: 'Pulse profile',
      icon: Icons.favorite_border,
      route: '/pulse/profile',
    ),
  ];

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          children: [
            for (var i = 0; i < _links.length; i++) ...[
              if (i > 0)
                const Divider(
                  height: 1,
                  color: AppColors.borderSubtle,
                  indent: 16,
                  endIndent: 16,
                ),
              _LinkTile(link: _links[i]),
            ],
          ],
        ),
      ),
    );
  }
}

class _LinkRow {
  const _LinkRow({
    required this.label,
    required this.icon,
    required this.route,
  });

  final String label;
  final IconData icon;
  final String route;
}

class _LinkTile extends StatelessWidget {
  const _LinkTile({required this.link});

  final _LinkRow link;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: Icon(link.icon, color: AppColors.textTertiary),
      title: Text(link.label, style: AppTextStyles.label),
      trailing: const Icon(
        Icons.chevron_right,
        color: AppColors.textTertiary,
      ),
      onTap: () => context.push(link.route),
    );
  }
}

// ─── Partner mode (Sprint 2 — Mopedu) ──────────────────────────────────

class _PartnerModeSection extends ConsumerWidget {
  const _PartnerModeSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Sprint 5: same gate as the launcher tile. We never advertise
    // "Become a Mopedu Partner" outside an active launch city.
    final mopeduFlag = ref.watch(mopeduEnabledProvider);
    final showMopedu = mopeduFlag.maybeWhen(
      data: (enabled) => enabled,
      orElse: () => false,
    );
    if (!showMopedu) return const SizedBox.shrink();

    final asyncPartner = ref.watch(myPartnerProfileProvider);
    return asyncPartner.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (partner) => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: _content(context, partner),
      ),
    );
  }

  Widget _content(BuildContext context, RiderPartner? partner) {
    if (partner == null) {
      // Not yet a partner — recruitment CTA.
      return InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        onTap: () => context.push('/mopedu/partner'),
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.posttubePrimary, width: 1),
          ),
          child: Row(
            children: [
              const Icon(
                Icons.local_taxi,
                color: AppColors.posttubePrimary,
                size: 22,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Become a Mopedu Partner',
                      style: AppTextStyles.h3,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      'Drive on your own terms. No commission.',
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
    if (partner.status == PartnerStatus.approved) {
      return InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        onTap: () => context.push('/mopedu/partner/dashboard'),
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              const Icon(
                Icons.toggle_on,
                color: AppColors.statusSuccess,
                size: 22,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  "I'm a Mopedu partner — switch to partner view",
                  style: AppTextStyles.label,
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
    // Pending verification banner.
    final stepsDone = _stepsCompleted(partner);
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.statusWarning.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.statusWarning),
      ),
      child: Row(
        children: [
          const Icon(Icons.hourglass_top, color: AppColors.statusWarning),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Waiting for verification',
                  style: AppTextStyles.h3,
                ),
                Text(
                  'Step $stepsDone of 5 done',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
          TextButton(
            onPressed: () => context.push('/mopedu/partner/onboarding'),
            child: Text('View', style: AppTextStyles.label),
          ),
        ],
      ),
    );
  }

  int _stepsCompleted(RiderPartner p) {
    var n = 1; // profile created
    if (p.kycStatus != VerificationStatus.draft) n++;
    if (p.kycStatus == VerificationStatus.approved) n++;
    if (p.bankStatus == VerificationStatus.approved) n++;
    if (p.status == PartnerStatus.approved) n++;
    return n;
  }
}

// ─── Settings + sign out ───────────────────────────────────────────────

class _SettingsSection extends ConsumerWidget {
  const _SettingsSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          children: [
            ListTile(
              leading: const Icon(
                Icons.settings,
                color: AppColors.textTertiary,
              ),
              title: Text('Settings', style: AppTextStyles.label),
              trailing: const Icon(
                Icons.chevron_right,
                color: AppColors.textTertiary,
              ),
              onTap: () => context.push('/settings'),
            ),
            const Divider(
              height: 1,
              color: AppColors.borderSubtle,
              indent: 16,
              endIndent: 16,
            ),
            ListTile(
              leading: const Icon(
                Icons.logout,
                color: AppColors.statusError,
              ),
              title: Text(
                'Sign out',
                style: AppTextStyles.label.copyWith(
                  color: AppColors.statusError,
                ),
              ),
              onTap: () {
                ref.read(authServiceProvider).logout();
                if (context.mounted) context.go('/login');
              },
            ),
          ],
        ),
      ),
    );
  }
}
