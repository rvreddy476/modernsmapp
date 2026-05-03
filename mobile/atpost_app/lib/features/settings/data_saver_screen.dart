// Data saver settings — recon §F.2 ("Indian-launch table-stakes").
//
// Renders the toggle UX described in the brief:
//   * Parent toggle "Save data" (off by default).
//   * Sub-toggle "Auto-enable on slow connection" (default on when
//     parent is on; rendered disabled when parent is off so the user
//     can't twiddle a no-op).
//   * Three info rows describing what data-saver does, surfacing the
//     opt-in nature of the feature.
//
// Persistence is handled inside `DataSaverNotifier`; this screen just
// renders state and emits intent.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/data_saver_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class DataSaverScreen extends ConsumerWidget {
  const DataSaverScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(dataSaverProvider);
    final notifier = ref.read(dataSaverProvider.notifier);
    final effective = ref.watch(effectiveDataSaverProvider);

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
                Text('Data saver', style: AppTextStyles.h1.copyWith(fontSize: 30)),
              ],
            ),
            const SizedBox(height: 8),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4),
              child: Text(
                'Reduce mobile-data use across reels, Posttube, and the feed. '
                'Off by default. Turn on whenever you are roaming or on a '
                'capped data plan.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
            ),
            const SizedBox(height: 18),
            _SectionCard(
              title: 'Mode',
              children: [
                _ToggleTile(
                  icon: Icons.data_saver_on_outlined,
                  title: 'Save data',
                  subtitle:
                      'Suppresses video autoplay and lowers image quality',
                  value: state.enabled,
                  onChanged: (v) => notifier.setEnabled(v, source: 'manual'),
                ),
                _ToggleTile(
                  icon: Icons.signal_cellular_alt,
                  title: 'Auto-enable on slow connection',
                  subtitle: state.enabled
                      ? 'Turn on automatically when you are on 2G or edge'
                      : 'Turn on Save data first to enable',
                  value: state.autoOnSlowConnection,
                  enabled: state.enabled,
                  onChanged: (v) => notifier.setAutoOnSlowConnection(v),
                ),
              ],
            ),
            const SizedBox(height: 12),
            _SectionCard(
              title: 'When data saver is on',
              children: const [
                _InfoRow(
                  icon: Icons.play_disabled_outlined,
                  label: "Videos won't autoplay",
                ),
                _InfoRow(
                  icon: Icons.high_quality_outlined,
                  label: 'Reels capped at 240p',
                ),
                _InfoRow(
                  icon: Icons.image_outlined,
                  label: 'Images compressed (0.5x)',
                ),
              ],
            ),
            const SizedBox(height: 12),
            _StatusBanner(active: effective),
          ],
        ),
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

class _ToggleTile extends StatelessWidget {
  const _ToggleTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.value,
    required this.onChanged,
    this.enabled = true,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final bool value;
  final ValueChanged<bool> onChanged;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return Opacity(
      opacity: enabled ? 1.0 : 0.55,
      child: ListTile(
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
        trailing: Switch.adaptive(
          value: value,
          onChanged: enabled ? onChanged : null,
        ),
      ),
    );
  }
}

class _InfoRow extends StatelessWidget {
  const _InfoRow({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 8),
      child: Row(
        children: [
          Icon(icon, size: 18, color: AppColors.textMuted),
          const SizedBox(width: 12),
          Expanded(
            child: Text(label, style: AppTextStyles.label),
          ),
        ],
      ),
    );
  }
}

class _StatusBanner extends StatelessWidget {
  const _StatusBanner({required this.active});

  final bool active;

  @override
  Widget build(BuildContext context) {
    final color = active ? AppColors.posttubePrimary : AppColors.borderSubtle;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: color),
      ),
      child: Row(
        children: [
          Icon(
            active ? Icons.check_circle : Icons.radio_button_unchecked,
            color: color,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              active
                  ? 'Data saver is active right now.'
                  : 'Data saver is off.',
              style: AppTextStyles.label,
            ),
          ),
        ],
      ),
    );
  }
}
