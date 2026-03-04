import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Settings', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          _SectionHeader('ACCOUNT'),
          _SettingsTile(
            title: 'Edit Profile',
            icon: Icons.person_outline,
            onTap: () => context.push('/settings/profile'),
          ),
          _TileDivider(),
          _SettingsTile(
            title: 'Security',
            icon: Icons.lock_outline,
            onTap: () => context.push('/settings/security'),
          ),
          _SectionHeader('PRIVACY'),
          _SettingsTile(
            title: 'Privacy Settings',
            icon: Icons.shield_outlined,
            onTap: () => context.push('/settings/privacy'),
          ),
          _SectionHeader('NOTIFICATIONS'),
          _SettingsTile(
            title: 'Notification Preferences',
            icon: Icons.notifications_outlined,
            onTap: () => context.push('/settings/notifications'),
          ),
          _SectionHeader('SUPPORT'),
          _SettingsTile(
            title: 'Help & Support',
            icon: Icons.help_outline,
            onTap: () {
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(content: Text('Opening help center...')),
              );
            },
          ),
          _TileDivider(),
          _SettingsTile(
            title: 'About AtPost',
            icon: Icons.info_outline,
            onTap: () {
              showAboutDialog(
                context: context,
                applicationName: 'AtPost',
                applicationVersion: '1.0.0',
                applicationLegalese: '© 2026 AtPost. All rights reserved.',
                children: [
                  const SizedBox(height: 8),
                  const Text(
                    'AtPost is the next-generation social platform for creators and communities.',
                  ),
                ],
              );
            },
          ),
          _SectionHeader('ACCOUNT ACTIONS'),
          _SettingsTile(
            title: 'Log Out',
            icon: Icons.logout,
            titleColor: Colors.red,
            iconColor: Colors.red,
            onTap: () async {
              final confirmed = await showDialog<bool>(
                context: context,
                builder: (ctx) => AlertDialog(
                  backgroundColor: AppColors.bgCard,
                  title: Text('Log Out', style: AppTextStyles.h3),
                  content: Text(
                    'Are you sure you want to log out?',
                    style: AppTextStyles.body,
                  ),
                  actions: [
                    TextButton(
                      onPressed: () => Navigator.of(ctx).pop(false),
                      child: Text(
                        'Cancel',
                        style: AppTextStyles.label.copyWith(color: AppColors.textSecondary),
                      ),
                    ),
                    TextButton(
                      onPressed: () => Navigator.of(ctx).pop(true),
                      child: Text(
                        'Log Out',
                        style: AppTextStyles.label.copyWith(color: Colors.red),
                      ),
                    ),
                  ],
                ),
              );
              if (confirmed == true) {
                ref.read(authServiceProvider).logout();
                if (context.mounted) context.go('/login');
              }
            },
          ),
          const SizedBox(height: 40),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title);

  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
      child: Text(
        title,
        style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
      ),
    );
  }
}

class _SettingsTile extends StatelessWidget {
  const _SettingsTile({
    required this.title,
    required this.icon,
    required this.onTap,
    this.titleColor,
    this.iconColor,
  });

  final String title;
  final IconData icon;
  final VoidCallback onTap;
  final Color? titleColor;
  final Color? iconColor;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      tileColor: AppColors.bgCard,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      contentPadding: const EdgeInsets.symmetric(horizontal: 16),
      leading: Icon(icon, color: iconColor ?? AppColors.textSecondary, size: 22),
      title: Text(
        title,
        style: AppTextStyles.body.copyWith(color: titleColor ?? AppColors.textPrimary),
      ),
      trailing: Icon(Icons.chevron_right, color: AppColors.textMuted, size: 20),
      onTap: onTap,
    );
  }
}

class _TileDivider extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Divider(
      height: 1,
      thickness: 1,
      color: AppColors.borderSubtle,
      indent: 16,
      endIndent: 16,
    );
  }
}
