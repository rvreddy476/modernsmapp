import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

enum _PostAudience { public, followers, friends }

enum _MessageAudience { everyone, followers, friends }

class PrivacySettingsScreen extends ConsumerStatefulWidget {
  const PrivacySettingsScreen({super.key});

  @override
  ConsumerState<PrivacySettingsScreen> createState() => _PrivacySettingsScreenState();
}

class _PrivacySettingsScreenState extends ConsumerState<PrivacySettingsScreen> {
  _PostAudience _postAudience = _PostAudience.public;
  _MessageAudience _messageAudience = _MessageAudience.everyone;
  bool _showFollowerCount = true;
  bool _saving = false;

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      await ref.read(apiClientProvider).put('/v1/users/me/privacy', data: {
        'default_post_audience': _postAudience.name,
        'who_can_message': _messageAudience.name,
        'show_follower_count': _showFollowerCount,
      });
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Privacy settings saved')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to save: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _requestDataExport() async {
    try {
      await ref.read(apiClientProvider).get('/v1/auth/data-export');
    } catch (_) {
      // Best-effort; snackbar shown regardless
    }
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Data export requested')),
      );
    }
  }

  Future<void> _deleteAccount() async {
    final confirmController = TextEditingController();

    final confirmed = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          backgroundColor: AppColors.bgCard,
          title: Text(
            'Delete Account',
            style: AppTextStyles.h3.copyWith(color: Colors.red),
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'This action is permanent and cannot be undone. All your data will be deleted.',
                style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
              ),
              const SizedBox(height: 16),
              Text(
                'Type DELETE to confirm',
                style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
              ),
              const SizedBox(height: 8),
              TextField(
                controller: confirmController,
                style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
                onChanged: (_) => setDialogState(() {}),
                decoration: InputDecoration(
                  hintText: 'DELETE',
                  hintStyle: AppTextStyles.body.copyWith(color: AppColors.textMuted),
                  filled: true,
                  fillColor: AppColors.bgSecondary,
                  contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    borderSide: const BorderSide(color: Colors.red),
                  ),
                  enabledBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    borderSide: const BorderSide(color: Colors.red),
                  ),
                  focusedBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                    borderSide: const BorderSide(color: Colors.red, width: 1.5),
                  ),
                ),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () {
                confirmController.dispose();
                Navigator.of(ctx).pop(false);
              },
              child: Text(
                'Cancel',
                style: AppTextStyles.label.copyWith(color: AppColors.textSecondary),
              ),
            ),
            TextButton(
              onPressed: confirmController.text == 'DELETE'
                  ? () {
                      confirmController.dispose();
                      Navigator.of(ctx).pop(true);
                    }
                  : null,
              child: Text(
                'Delete Account',
                style: AppTextStyles.label.copyWith(
                  color: confirmController.text == 'DELETE'
                      ? Colors.red
                      : AppColors.textMuted,
                ),
              ),
            ),
          ],
        ),
      ),
    );

    if (confirmed == true) {
      try {
        await ref.read(apiClientProvider).delete('/v1/auth/account');
      } catch (_) {
        // Proceed with logout regardless
      }
      ref.read(authServiceProvider).logout();
      if (mounted) context.go('/login');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Privacy', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 40),
        children: [
          // --- Content Visibility ---
          _SectionHeader('CONTENT VISIBILITY'),
          const SizedBox(height: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Default post audience', style: AppTextStyles.body),
                DropdownButton<_PostAudience>(
                  value: _postAudience,
                  dropdownColor: AppColors.bgCard,
                  underline: const SizedBox.shrink(),
                  style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
                  items: const [
                    DropdownMenuItem(
                      value: _PostAudience.public,
                      child: Text('Public'),
                    ),
                    DropdownMenuItem(
                      value: _PostAudience.followers,
                      child: Text('Followers'),
                    ),
                    DropdownMenuItem(
                      value: _PostAudience.friends,
                      child: Text('Friends'),
                    ),
                  ],
                  onChanged: (val) {
                    if (val != null) setState(() => _postAudience = val);
                  },
                ),
              ],
            ),
          ),
          const SizedBox(height: 24),
          // --- Messaging ---
          _SectionHeader('MESSAGING'),
          const SizedBox(height: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Who can message you', style: AppTextStyles.body),
                DropdownButton<_MessageAudience>(
                  value: _messageAudience,
                  dropdownColor: AppColors.bgCard,
                  underline: const SizedBox.shrink(),
                  style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
                  items: const [
                    DropdownMenuItem(
                      value: _MessageAudience.everyone,
                      child: Text('Everyone'),
                    ),
                    DropdownMenuItem(
                      value: _MessageAudience.followers,
                      child: Text('Followers'),
                    ),
                    DropdownMenuItem(
                      value: _MessageAudience.friends,
                      child: Text('Friends'),
                    ),
                  ],
                  onChanged: (val) {
                    if (val != null) setState(() => _messageAudience = val);
                  },
                ),
              ],
            ),
          ),
          const SizedBox(height: 24),
          // --- Profile ---
          _SectionHeader('PROFILE'),
          const SizedBox(height: 8),
          Container(
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: SwitchListTile(
              contentPadding: const EdgeInsets.symmetric(horizontal: 16),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              ),
              title: Text('Show follower count', style: AppTextStyles.body),
              subtitle: Text(
                'Display your follower count on your profile',
                style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
              ),
              value: _showFollowerCount,
              activeThumbColor: AppColors.postbookPrimary,
              onChanged: (val) => setState(() => _showFollowerCount = val),
            ),
          ),
          const SizedBox(height: 24),
          // --- Data ---
          _SectionHeader('DATA'),
          const SizedBox(height: 8),
          Container(
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: ListTile(
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              ),
              leading: const Icon(Icons.download_outlined, color: AppColors.textSecondary),
              title: Text('Download my data', style: AppTextStyles.body),
              trailing: const Icon(Icons.chevron_right, color: AppColors.textMuted, size: 20),
              onTap: _requestDataExport,
            ),
          ),
          const SizedBox(height: 24),
          // --- Danger Zone ---
          _SectionHeader('DANGER ZONE'),
          const SizedBox(height: 8),
          Container(
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: Colors.red.withAlpha(77)),
            ),
            child: ListTile(
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              ),
              leading: const Icon(Icons.delete_forever_outlined, color: Colors.red),
              title: Text(
                'Delete Account',
                style: AppTextStyles.body.copyWith(color: Colors.red),
              ),
              subtitle: Text(
                'Permanently delete your account and all data',
                style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
              ),
              trailing: const Icon(Icons.chevron_right, color: Colors.red, size: 20),
              onTap: _deleteAccount,
            ),
          ),
          const SizedBox(height: 32),
          // --- Save button ---
          SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              onPressed: _saving ? null : _save,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                foregroundColor: Colors.white,
                padding: const EdgeInsets.symmetric(vertical: 14),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                ),
              ),
              child: _saving
                  ? const SizedBox(
                      height: 20,
                      width: 20,
                      child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                    )
                  : Text('Save Settings', style: AppTextStyles.label),
            ),
          ),
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
    return Text(
      title,
      style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
    );
  }
}
