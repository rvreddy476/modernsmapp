import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Module 1 P0-4 — viewer control over long video in the social feed.
///
/// This affects the social home feed only. PostTube, your subscriptions,
/// long-video search, and direct links are never filtered by it — the
/// setting shapes what arrives uninvited, not what you go looking for.
class ContentPreferencesScreen extends ConsumerStatefulWidget {
  const ContentPreferencesScreen({super.key});

  @override
  ConsumerState<ContentPreferencesScreen> createState() =>
      _ContentPreferencesScreenState();
}

class _ContentPreferencesScreenState
    extends ConsumerState<ContentPreferencesScreen> {
  static const _options = <String, ({String label, String subtitle})>{
    'hidden': (
      label: 'Hidden',
      subtitle: 'No long videos in your feed at all',
    ),
    'reduced': (
      label: 'Reduced',
      subtitle: 'Occasional long videos (about 1 in 10 posts)',
    ),
    'balanced': (
      label: 'Balanced',
      subtitle: 'A normal mix (about 1 in 4 posts)',
    ),
    'preferred': (
      label: 'Preferred',
      subtitle: 'More long videos (up to half your feed)',
    ),
  };

  String _value = 'balanced';
  bool _loading = true;
  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final res = await ref.read(apiClientProvider).get('/v1/feed/preference');
      final data = res.data;
      final body = (data is Map && data['data'] is Map)
          ? Map<String, dynamic>.from(data['data'] as Map)
          : <String, dynamic>{};
      final freq = body['long_video_frequency'] as String?;
      if (!mounted) return;
      setState(() {
        if (freq != null && _options.containsKey(freq)) _value = freq;
        _loading = false;
      });
    } catch (_) {
      // Preference read failed — show the default rather than an error
      // wall; saving still works and re-syncs the server.
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _save(String next) async {
    final previous = _value;
    setState(() {
      _value = next;
      _saving = true;
      _error = null;
    });
    try {
      await ref.read(apiClientProvider).post(
            '/v1/feed/preference',
            data: {'long_video_frequency': next},
          );
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _value = previous;
        _error = 'Could not save your preference. Please try again.';
      });
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        title: const Text('Content preferences'),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : ListView(
              padding: const EdgeInsets.all(16),
              children: [
                Text('Long videos in your feed', style: AppTextStyles.h3),
                const SizedBox(height: 6),
                Text(
                  'Controls how often long videos appear in your social feed. '
                  'PostTube and your subscriptions are not affected.',
                  style:
                      AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
                ),
                const SizedBox(height: 16),
                ..._options.entries.map((entry) {
                  return RadioListTile<String>(
                    contentPadding: EdgeInsets.zero,
                    value: entry.key,
                    groupValue: _value,
                    onChanged: _saving ? null : (v) => v == null ? null : _save(v),
                    title: Text(entry.value.label,
                        style: AppTextStyles.bodyMedium),
                    subtitle: Text(
                      entry.value.subtitle,
                      style: AppTextStyles.bodySmall
                          .copyWith(color: AppColors.textDim),
                    ),
                  );
                }),
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Text(
                    _error!,
                    style: AppTextStyles.bodySmall
                        .copyWith(color: AppColors.statusError),
                  ),
                ],
              ],
            ),
    );
  }
}
