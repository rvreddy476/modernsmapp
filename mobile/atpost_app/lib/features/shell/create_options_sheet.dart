// Create options sheet — AtPost super-app shell.
//
// Opened by the center FAB on the bottom-nav. Six large tappable cards,
// each color-coded to its module's brand colour. Selecting a card emits a
// `shell.create.option.picked` telemetry event and routes to the right
// composer.
//
// `Live` doesn't have a composer route yet. We surface a snackbar fallback
// rather than a broken push, and still emit the telemetry event so we know
// the demand exists.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/shell_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Open the create-options modal bottom sheet. Returns once the sheet is
/// dismissed (whether by tap-out, back, or selection).
Future<void> showCreateOptionsSheet(BuildContext context) {
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    barrierColor: Colors.black.withValues(alpha: 0.55),
    builder: (_) => const CreateOptionsSheet(),
  );
}

class CreateOptionsSheet extends ConsumerWidget {
  const CreateOptionsSheet({super.key});

  static const _options = <_CreateOption>[
    _CreateOption(
      key: ShellCreateOption.post,
      label: 'Post',
      caption: 'Share a thought or photo',
      icon: Icons.edit_note,
      color: AppColors.postbookPrimary,
      route: '/create',
    ),
    _CreateOption(
      key: ShellCreateOption.reel,
      label: 'Reel',
      caption: 'Short-form video',
      icon: Icons.movie_filter,
      color: AppColors.postgramPrimary,
      route: '/reels/editor',
    ),
    _CreateOption(
      key: ShellCreateOption.story,
      label: 'Story',
      caption: 'Lasts 24 hours',
      icon: Icons.photo_camera,
      color: AppColors.accentPurple,
      route: '/stories/create',
    ),
    _CreateOption(
      key: ShellCreateOption.question,
      label: 'Question',
      caption: 'Ask the community',
      icon: Icons.help_outline,
      color: AppColors.posttubePrimary,
      route: '/qa/ask',
    ),
    _CreateOption(
      key: ShellCreateOption.live,
      label: 'Live',
      caption: 'Go live (coming soon)',
      icon: Icons.live_tv,
      color: AppColors.liveRed,
      route: null, // No composer yet — we snackbar instead.
    ),
    _CreateOption(
      key: ShellCreateOption.listing,
      label: 'Listing',
      caption: 'Sell something',
      icon: Icons.local_offer,
      color: AppColors.statusWarning,
      route: '/seller/listings/new',
    ),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return SafeArea(
      top: false,
      child: Container(
        margin: const EdgeInsets.all(12),
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Create', style: AppTextStyles.h2),
                IconButton(
                  icon: const Icon(
                    Icons.close,
                    color: AppColors.textTertiary,
                  ),
                  onPressed: () => Navigator.of(context).maybePop(),
                ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              'What would you like to share?',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 16),
            GridView.builder(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              gridDelegate:
                  const SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: 2,
                crossAxisSpacing: 12,
                mainAxisSpacing: 12,
                childAspectRatio: 1.6,
              ),
              itemCount: _options.length,
              itemBuilder: (context, index) {
                final opt = _options[index];
                return _CreateCard(
                  option: opt,
                  onTap: () => _onPick(context, ref, opt),
                );
              },
            ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }

  void _onPick(BuildContext context, WidgetRef ref, _CreateOption opt) {
    ref.read(shellTelemetryProvider).shellCreateOptionPicked(opt.key);
    final route = opt.route;
    final navigator = Navigator.of(context);
    final messenger = ScaffoldMessenger.of(context);
    final router = GoRouter.of(context);
    navigator.pop();
    if (route == null) {
      messenger.showSnackBar(
        SnackBar(
          content: Text('${opt.label} coming soon'),
          duration: const Duration(seconds: 2),
        ),
      );
      return;
    }
    router.push(route);
  }
}

class _CreateOption {
  const _CreateOption({
    required this.key,
    required this.label,
    required this.caption,
    required this.icon,
    required this.color,
    required this.route,
  });

  final String key;
  final String label;
  final String caption;
  final IconData icon;
  final Color color;
  final String? route;
}

class _CreateCard extends StatelessWidget {
  const _CreateCard({required this.option, required this.onTap});

  final _CreateOption option;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
              colors: [
                option.color.withValues(alpha: 0.22),
                option.color.withValues(alpha: 0.08),
              ],
            ),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: option.color.withValues(alpha: 0.35)),
          ),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: option.color.withValues(alpha: 0.25),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Icon(option.icon, color: option.color, size: 22),
              ),
              const SizedBox(height: 8),
              Text(
                option.label,
                style: AppTextStyles.h3.copyWith(
                  color: AppColors.textPrimary,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                option.caption,
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.textTertiary,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ],
          ),
        ),
      ),
    );
  }
}
