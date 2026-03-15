import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class NotificationSettingsScreen extends ConsumerStatefulWidget {
  const NotificationSettingsScreen({super.key});

  @override
  ConsumerState<NotificationSettingsScreen> createState() =>
      _NotificationSettingsScreenState();
}

class _NotificationSettingsScreenState
    extends ConsumerState<NotificationSettingsScreen> {
  Map<String, bool> _prefs = {
    'likes_enabled': true,
    'comments_enabled': true,
    'follows_enabled': true,
    'mentions_enabled': true,
    'messages_enabled': true,
    'push_enabled': true,
  };
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _loadPrefs();
  }

  Future<void> _loadPrefs() async {
    try {
      final api = ref.read(apiClientProvider);
      final response = await api.get('/v1/users/me/notification-preferences');
      final data = response.data['data'] as Map<String, dynamic>?;
      if (data != null && mounted) {
        setState(() {
          _prefs = {
            'likes_enabled': data['likes_enabled'] as bool? ?? true,
            'comments_enabled': data['comments_enabled'] as bool? ?? true,
            'follows_enabled': data['follows_enabled'] as bool? ?? true,
            'mentions_enabled': data['mentions_enabled'] as bool? ?? true,
            'messages_enabled': data['messages_enabled'] as bool? ?? true,
            'push_enabled': data['push_enabled'] as bool? ?? true,
          };
        });
      }
    } catch (_) {
      // Use defaults on error — graceful degradation
    }
    if (mounted) setState(() => _loading = false);
  }

  Future<void> _updatePref(String key, bool value) async {
    setState(() => _prefs[key] = value);
    try {
      await ref
          .read(apiClientProvider)
          .put('/v1/users/me/notification-preferences', data: Map<String, dynamic>.from(_prefs));
    } catch (_) {
      // Revert on failure
      if (mounted) setState(() => _prefs[key] = !value);
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
        title: Text('Notifications', style: AppTextStyles.h2),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : ListView(
              padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 40),
              children: [
                _SectionHeader('NOTIFICATION TYPES'),
                const SizedBox(height: 8),
                Container(
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Column(
                    children: [
                      _PrefTile(
                        title: 'Likes',
                        subtitle: 'When someone likes your post',
                        prefKey: 'likes_enabled',
                        prefs: _prefs,
                        onChanged: _updatePref,
                        isFirst: true,
                      ),
                      _Divider(),
                      _PrefTile(
                        title: 'Comments',
                        subtitle: 'When someone comments on your post',
                        prefKey: 'comments_enabled',
                        prefs: _prefs,
                        onChanged: _updatePref,
                      ),
                      _Divider(),
                      _PrefTile(
                        title: 'New Followers',
                        subtitle: 'When someone follows you',
                        prefKey: 'follows_enabled',
                        prefs: _prefs,
                        onChanged: _updatePref,
                      ),
                      _Divider(),
                      _PrefTile(
                        title: 'Mentions',
                        subtitle: 'When someone mentions you in a post',
                        prefKey: 'mentions_enabled',
                        prefs: _prefs,
                        onChanged: _updatePref,
                      ),
                      _Divider(),
                      _PrefTile(
                        title: 'Messages',
                        subtitle: 'When you receive a new message',
                        prefKey: 'messages_enabled',
                        prefs: _prefs,
                        onChanged: _updatePref,
                        isLast: true,
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 24),
                _SectionHeader('PUSH NOTIFICATIONS'),
                const SizedBox(height: 8),
                Container(
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: _PrefTile(
                    title: 'Push Notifications',
                    subtitle: 'Receive notifications on this device',
                    prefKey: 'push_enabled',
                    prefs: _prefs,
                    onChanged: _updatePref,
                    isFirst: true,
                    isLast: true,
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

class _Divider extends StatelessWidget {
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

class _PrefTile extends StatelessWidget {
  const _PrefTile({
    required this.title,
    required this.subtitle,
    required this.prefKey,
    required this.prefs,
    required this.onChanged,
    this.isFirst = false,
    this.isLast = false,
  });

  final String title;
  final String subtitle;
  final String prefKey;
  final Map<String, bool> prefs;
  final Future<void> Function(String, bool) onChanged;
  final bool isFirst;
  final bool isLast;

  @override
  Widget build(BuildContext context) {
    final radius = AppSpacing.radiusXL;
    return SwitchListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.only(
          topLeft: isFirst ? Radius.circular(radius) : Radius.zero,
          topRight: isFirst ? Radius.circular(radius) : Radius.zero,
          bottomLeft: isLast ? Radius.circular(radius) : Radius.zero,
          bottomRight: isLast ? Radius.circular(radius) : Radius.zero,
        ),
      ),
      title: Text(title, style: AppTextStyles.body),
      subtitle: Text(
        subtitle,
        style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
      ),
      value: prefs[prefKey] ?? true,
      activeThumbColor: AppColors.postbookPrimary,
      onChanged: (val) => onChanged(prefKey, val),
    );
  }
}
