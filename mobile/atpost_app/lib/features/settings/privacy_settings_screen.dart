import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// Module 3 M3-P0-5 / SR-5 — launch is PUBLIC ACCOUNTS ONLY.
//
// This screen used to offer a "Default post audience" of Public / Followers /
// Friends. Two problems, both real:
//
//  1. The backend never enforced a non-public account. `account_visibility`
//     was stored and no service read it — graph-service's follow path never
//     consulted it — so an account set to "Followers" stayed followable by
//     anyone and its posts still reached the public feed and search.
//  2. This screen saved to `PUT /v1/users/me/privacy`, which does not exist
//     in any service. Every save failed.
//
// Offering the choice was therefore a promise on two counts. Until private
// accounts are genuinely built — pending follow requests, enforcement in the
// follow path, audience resolution in feed and search — the control is gone
// and the screen says so plainly.
enum _MessageAudience { everyone, followers, friends }

class PrivacySettingsScreen extends ConsumerStatefulWidget {
  const PrivacySettingsScreen({super.key});

  @override
  ConsumerState<PrivacySettingsScreen> createState() =>
      _PrivacySettingsScreenState();
}

class _PrivacySettingsScreenState extends ConsumerState<PrivacySettingsScreen> {
  _MessageAudience _messageAudience = _MessageAudience.everyone;
  bool _showFollowerCount = true;
  bool _saving = false;

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      // SR-5: saves go to `/v1/users/me/settings`, which exists. The previous
      // target, `/v1/users/me/privacy`, is not implemented by any service —
      // every save this screen made failed.
      //
      // `account_visibility` is not sent at all: it is public-only at launch,
      // and the backend refuses any other value rather than storing a setting
      // it cannot enforce.
      await ref
          .read(apiClientProvider)
          .put(
            '/v1/users/me/settings',
            data: {
              'allow_messages_from': _messageAudience.name,
              'show_follower_count': _showFollowerCount,
            },
          );
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Privacy settings saved')));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to save: $e')));
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _requestDataExport() async {
    // The auth endpoint returns only auth/session/device records, not a full
    // platform export. Until the cross-service coordinator exists, describe
    // the real verified manual process and create no false receipt.
    await showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgCard,
        title: Text('Request full data export', style: AppTextStyles.h3),
        content: Text(
          'Automated full-platform export is not available yet. Email '
          'privacy@cleestudio.com from your registered address to request a '
          'manual export. The privacy team will verify your identity, collect '
          'the data, and confirm secure delivery. Opening this message does '
          'not create an export request.',
          style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Close'),
          ),
        ],
      ),
    );
  }

  Future<void> _deleteAccount() async {
    final confirmController = TextEditingController();

    final confirmed = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          backgroundColor: AppColors.bgCard,
          // Module 3 LB-6: the copy describes what ACTUALLY happens.
          //
          // SR-7 changed this to "Deactivate" and promised the account would be
          // deactivated and the user signed out. LB-6 then disabled the
          // endpoint entirely — it mutates nothing — because the cross-service
          // erasure pipeline is incomplete and emitting the deletion event
          // could erase part of a user's data while leaving the rest.
          //
          // So the dialog can no longer promise deactivation either. It says
          // the feature is unavailable and offers the real path.
          title: Text(
            'Delete Account',
            style: AppTextStyles.h3.copyWith(color: Colors.red),
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Self-service account deletion is not available yet.\n\n'
                'You can request deletion of your account and erasure of your '
                'data by emailing privacy@cleestudio.com. We will process it '
                'manually and confirm when it is done.\n\n'
                'Continuing will send a request to check whether self-service '
                'deletion has become available. Nothing on your account changes '
                'unless it has.',
                style: AppTextStyles.body.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
              const SizedBox(height: 16),
              Text(
                'Type DELETE to continue',
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.textMuted,
                ),
              ),
              const SizedBox(height: 8),
              TextField(
                controller: confirmController,
                style: AppTextStyles.body.copyWith(
                  color: AppColors.textPrimary,
                ),
                onChanged: (_) => setDialogState(() {}),
                decoration: InputDecoration(
                  hintText: 'DELETE',
                  hintStyle: AppTextStyles.body.copyWith(
                    color: AppColors.textMuted,
                  ),
                  filled: true,
                  fillColor: AppColors.bgSecondary,
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 10,
                  ),
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
                style: AppTextStyles.label.copyWith(
                  color: AppColors.textSecondary,
                ),
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
                'Continue',
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

    if (confirmed != true) return;

    // Module 3 LB-6: do NOT log out unless the request actually succeeded.
    //
    // This caught every failure and signed the user out regardless, so a
    // request that never reached the server still ended the session — the user
    // believed their account was gone, and it was untouched. That is the
    // worst combination: no effect on the server, a total effect on the client.
    //
    // Self-service deletion is currently DISABLED server-side (it answers 503
    // and mutates nothing) because the cross-service erasure pipeline is
    // incomplete, so this path normally lands in the catch below.
    try {
      await ref.read(apiClientProvider).delete('/v1/auth/account');
    } catch (e) {
      if (!mounted) return;
      final message = e is DioException && e.response?.statusCode == 503
          ? 'Account deletion is not available yet. Nothing has been changed — '
                'you are still signed in. Email privacy@cleestudio.com to request '
                'deletion.'
          : 'Could not complete the request. Check your connection and try again.';
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(message), duration: const Duration(seconds: 6)),
      );
      return; // stay signed in — nothing happened on the server
    }

    // Only reached if the server genuinely accepted the request.
    ref.read(authServiceProvider).logout();
    if (mounted) context.go('/login');
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new,
            color: AppColors.textPrimary,
          ),
          onPressed: () => context.pop(),
        ),
        title: Text('Privacy', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 40),
        children: [
          // --- Content Visibility ---
          _SectionHeader('ACCOUNT VISIBILITY'),
          const SizedBox(height: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            // SR-5: the audience dropdown is gone. It offered Followers and
            // Friends, neither of which the platform enforced — the account
            // stayed followable by anyone and its posts still reached the
            // public feed and search. Saying so plainly is the honest version;
            // a disabled dropdown would still imply the feature is coming
            // back on soon.
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    const Icon(
                      Icons.public,
                      size: 20,
                      color: AppColors.textSecondary,
                    ),
                    const SizedBox(width: 12),
                    Text('Your account is public', style: AppTextStyles.body),
                  ],
                ),
                const SizedBox(height: 8),
                Text(
                  'Anyone can see your profile and posts, and anyone can follow you. '
                  'Private accounts are not available yet.',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
                const SizedBox(height: 8),
                Text(
                  'You can still block accounts, and blocking removes them from your '
                  'followers and hides your profile from them.',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
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
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textPrimary,
                  ),
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
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
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
              leading: const Icon(
                Icons.download_outlined,
                color: AppColors.textSecondary,
              ),
              title: Text(
                'Request full data export',
                style: AppTextStyles.body,
              ),
              subtitle: Text(
                'Manual request via privacy@cleestudio.com',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
              trailing: const Icon(
                Icons.chevron_right,
                color: AppColors.textMuted,
                size: 20,
              ),
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
              leading: const Icon(Icons.delete_outline, color: Colors.red),
              title: Text(
                'Delete Account',
                style: AppTextStyles.body.copyWith(color: Colors.red),
              ),
              // LB-6: the subtitle states the actual availability. Promising
              // "sign out everywhere and hide your profile" would be a fresh
              // false claim, because the endpoint now mutates nothing.
              subtitle: Text(
                'Not available yet — contact support to request deletion.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
              trailing: const Icon(
                Icons.chevron_right,
                color: Colors.red,
                size: 20,
              ),
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
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: Colors.white,
                      ),
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
