import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — placeholder for "my reports".
///
/// Sprint 4 backend ships only the write endpoint
/// (`POST /v1/dating/safety/report`). The list endpoint is a Sprint 5
/// follow-up; this screen explains that while still living at its final
/// route so we don't have to renumber anything later.
class MyReportsScreen extends ConsumerWidget {
  const MyReportsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('My reports', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
        ),
      ),
      body: SafeArea(
        child: Padding(
          padding: AppSpacing.pagePadding.copyWith(top: 18),
          child: Container(
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
                Text('Reports stay confidential',
                    style: AppTextStyles.h3),
                const SizedBox(height: 6),
                Text(
                  'Trust & Safety reviews every report. The list view '
                  'here is on the Sprint 5 roadmap; for now you will '
                  'receive an in-app notification when a report is acted '
                  'on.',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
