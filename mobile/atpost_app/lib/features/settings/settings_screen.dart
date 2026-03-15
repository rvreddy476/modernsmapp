import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final userAsync = ref.watch(currentUserProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: ListView(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 32),
          children: [
            Row(
              children: [
                IconButton(
                  onPressed: () => context.pop(),
                  icon: const Icon(
                    Icons.arrow_back_ios_new_rounded,
                    size: 18,
                    color: AppColors.textPrimary,
                  ),
                ),
                const SizedBox(width: 6),
                Text(
                  'Settings',
                  style: AppTextStyles.h1.copyWith(fontSize: 30),
                ),
              ],
            ),
            const SizedBox(height: 8),
            userAsync.when(
              loading: () => const _SettingsHeroPlaceholder(),
              error: (_, _) => const _SettingsHeroFallback(),
              data: (user) => _SettingsHero(
                name: user.displayName,
                username: user.username,
                isVerified: user.isVerified,
                onEdit: () => context.push('/settings/profile'),
              ),
            ),
            const SizedBox(height: 18),
            _SectionCard(
              title: 'Account',
              children: [
                _SettingTile(
                  icon: Icons.person_outline,
                  title: 'Edit Profile',
                  subtitle: 'Update bio, profile photo, and personal details',
                  onTap: () => context.push('/settings/profile'),
                ),
                _SettingTile(
                  icon: Icons.lock_outline,
                  title: 'Security',
                  subtitle: 'Password, sessions, and two-factor controls',
                  onTap: () => context.push('/settings/security'),
                ),
                _SettingTile(
                  icon: Icons.privacy_tip_outlined,
                  title: 'Privacy',
                  subtitle: 'Audience defaults, messaging and data controls',
                  onTap: () => context.push('/settings/privacy'),
                ),
                _SettingTile(
                  icon: Icons.notifications_outlined,
                  title: 'Notifications',
                  subtitle: 'Push and in-app notification preferences',
                  onTap: () => context.push('/settings/notifications'),
                ),
                _SettingTile(
                  icon: Icons.download_outlined,
                  title: 'Download My Data',
                  subtitle: 'Request an export of all your account data',
                  onTap: () async {
                    try {
                      await ref.read(userRepositoryProvider).requestDataExport();
                      if (!context.mounted) return;
                      showDialog(
                        context: context,
                        builder: (ctx) => AlertDialog(
                          backgroundColor: AppColors.bgCard,
                          title: Text(
                            'Data Export Requested',
                            style: AppTextStyles.h3,
                          ),
                          content: Text(
                            'Your data export has been requested. '
                            'You\'ll receive a download link via email.',
                            style: AppTextStyles.body,
                          ),
                          actions: [
                            TextButton(
                              onPressed: () => Navigator.of(ctx).pop(),
                              child: Text(
                                'OK',
                                style: AppTextStyles.label.copyWith(
                                  color: AppColors.postbookPrimary,
                                ),
                              ),
                            ),
                          ],
                        ),
                      );
                    } catch (_) {
                      if (!context.mounted) return;
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(
                          content: Text(
                            'Could not request data export. Please try again.',
                          ),
                        ),
                      );
                    }
                  },
                ),
              ],
            ),
            const SizedBox(height: 12),
            _SectionCard(
              title: 'Tools',
              children: [
                _SettingTile(
                  icon: Icons.receipt_long_outlined,
                  title: 'Orders',
                  subtitle: 'Track purchases and order history',
                  onTap: () => context.push('/orders'),
                ),
                _SettingTile(
                  icon: Icons.savings_outlined,
                  title: 'Monetization',
                  subtitle: 'Creator earnings, tiers, and payouts',
                  onTap: () => context.push('/monetization'),
                ),
                _SettingTile(
                  icon: Icons.group_outlined,
                  title: 'Friends & Requests',
                  subtitle: 'Manage connections and pending invites',
                  onTap: () => context.push('/friend-requests'),
                ),
              ],
            ),
            const SizedBox(height: 12),
            _SectionCard(
              title: 'Support',
              children: [
                _SettingTile(
                  icon: Icons.help_outline,
                  title: 'Help Center',
                  subtitle: 'Guides and contact support',
                  onTap: () {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('Opening help center...')),
                    );
                  },
                ),
                _SettingTile(
                  icon: Icons.info_outline,
                  title: 'About AtPost',
                  subtitle: 'Version and platform information',
                  onTap: () {
                    showAboutDialog(
                      context: context,
                      applicationName: 'AtPost',
                      applicationVersion: '1.0.0',
                      applicationLegalese:
                          '(c) 2026 AtPost. All rights reserved.',
                      children: const [
                        SizedBox(height: 8),
                        Text(
                          'AtPost is a social platform for creators and communities.',
                        ),
                      ],
                    );
                  },
                ),
              ],
            ),
            const SizedBox(height: 12),
            _DangerCard(
              onLogout: () async {
                final confirmed = await showDialog<bool>(
                  context: context,
                  builder: (ctx) => AlertDialog(
                    backgroundColor: AppColors.bgCard,
                    title: Text('Log Out', style: AppTextStyles.h3),
                    content: Text(
                      'Are you sure you want to log out of this account?',
                      style: AppTextStyles.body,
                    ),
                    actions: [
                      TextButton(
                        onPressed: () => Navigator.of(ctx).pop(false),
                        child: Text(
                          'Cancel',
                          style: AppTextStyles.label.copyWith(
                            color: AppColors.textSecondary,
                          ),
                        ),
                      ),
                      TextButton(
                        onPressed: () => Navigator.of(ctx).pop(true),
                        child: Text(
                          'Log Out',
                          style: AppTextStyles.label.copyWith(
                            color: Colors.red,
                          ),
                        ),
                      ),
                    ],
                  ),
                );

                if (confirmed == true) {
                  ref.read(authServiceProvider).logout();
                  if (context.mounted) {
                    context.go('/login');
                  }
                }
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _SettingsHero extends StatelessWidget {
  const _SettingsHero({
    required this.name,
    required this.username,
    required this.isVerified,
    required this.onEdit,
  });

  final String name;
  final String username;
  final bool isVerified;
  final VoidCallback onEdit;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
        gradient: const LinearGradient(
          colors: [Color(0x3325B2FF), Color(0x33FF6B35), Color(0x334ECDC4)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
      ),
      child: Row(
        children: [
          Container(
            width: 56,
            height: 56,
            decoration: const BoxDecoration(
              shape: BoxShape.circle,
              gradient: AppColors.postbookGradient,
            ),
            child: Center(
              child: Text(
                name.isEmpty ? 'U' : name[0].toUpperCase(),
                style: AppTextStyles.h2.copyWith(color: Colors.white),
              ),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Flexible(
                      child: Text(
                        name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: AppTextStyles.h2,
                      ),
                    ),
                    if (isVerified) ...[
                      const SizedBox(width: 6),
                      const Icon(
                        Icons.verified,
                        color: AppColors.posttubePrimary,
                        size: 16,
                      ),
                    ],
                  ],
                ),
                Text('@$username', style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          OutlinedButton(
            onPressed: onEdit,
            style: OutlinedButton.styleFrom(
              foregroundColor: AppColors.textPrimary,
              side: const BorderSide(color: AppColors.borderSubtle),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(10),
              ),
            ),
            child: const Text('Edit'),
          ),
        ],
      ),
    );
  }
}

class _SettingsHeroPlaceholder extends StatelessWidget {
  const _SettingsHeroPlaceholder();

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 92,
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
    );
  }
}

class _SettingsHeroFallback extends StatelessWidget {
  const _SettingsHeroFallback();

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Text(
        'Account details unavailable',
        style: AppTextStyles.bodySmall,
      ),
    );
  }
}

class _SectionCard extends StatelessWidget {
  const _SectionCard({required this.title, required this.children});

  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 4),
            child: Text(
              title,
              style: AppTextStyles.labelSmall.copyWith(
                color: AppColors.textMuted,
              ),
            ),
          ),
          ...children,
        ],
      ),
    );
  }
}

class _SettingTile extends StatelessWidget {
  const _SettingTile({
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
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      leading: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(10),
        ),
        child: Icon(icon, size: 18, color: AppColors.textSecondary),
      ),
      title: Text(title, style: AppTextStyles.label),
      subtitle: Text(subtitle, style: AppTextStyles.labelSmall),
      trailing: const Icon(
        Icons.chevron_right_rounded,
        color: AppColors.textMuted,
      ),
      onTap: onTap,
    );
  }
}

class _DangerCard extends StatelessWidget {
  const _DangerCard({required this.onLogout});

  final VoidCallback onLogout;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: const Color(0x1AFF0000),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: const Color(0x44FF4D4D)),
      ),
      child: Row(
        children: [
          const Icon(Icons.logout_rounded, color: Colors.red),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'Log out of your account',
              style: AppTextStyles.label.copyWith(color: Colors.red.shade200),
            ),
          ),
          TextButton(
            onPressed: onLogout,
            child: Text(
              'Log Out',
              style: AppTextStyles.label.copyWith(color: Colors.red.shade300),
            ),
          ),
        ],
      ),
    );
  }
}
