import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — block list view.
///
/// The dating-service exposes a write endpoint (`POST /v1/dating/safety/block`)
/// but Sprint 4 backend has not yet shipped a paginated read endpoint for
/// the dating-side block list. Until that lands, this screen reads through
/// the existing graph-service block list (handled by the user repository in
/// other parts of the app). For S4 we render a placeholder + a deep link
/// to the privacy settings screen which already manages graph blocks.
class BlockListScreen extends ConsumerWidget {
  const BlockListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Block list', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
        ),
      ),
      body: SafeArea(
        child: Padding(
          padding: AppSpacing.pagePadding.copyWith(top: 18),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusLarge),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Pulse blocks',
                        style: AppTextStyles.h3),
                    const SizedBox(height: 6),
                    Text(
                      'Blocking on Pulse cuts both directions of the dating '
                      'graph immediately. The unblock control is part of '
                      'the privacy settings screen.',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 12),
              FilledButton.tonal(
                onPressed: () => context.push('/settings/privacy'),
                child: const Text('Open privacy settings'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
