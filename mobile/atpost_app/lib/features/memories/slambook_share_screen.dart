import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/memories/slambook_data.dart';
import 'package:atpost_app/features/memories/slambook_response_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SlambookShareScreen extends ConsumerWidget {
  const SlambookShareScreen({super.key, required this.shareToken});

  final String shareToken;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final detailAsync = ref.watch(slambookShareDetailProvider(shareToken));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: const Text('Shared SlamBook'),
      ),
      body: detailAsync.when(
        data: (detail) {
          final slambook = detail.slambook;
          return ListView(
            padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 32),
            children: [
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(slambook.title, style: AppTextStyles.h2),
                    if ((slambook.subtitle ?? '').trim().isNotEmpty) ...[
                      const SizedBox(height: 6),
                      Text(slambook.subtitle!, style: AppTextStyles.bodySmall),
                    ],
                    if ((slambook.description ?? '').trim().isNotEmpty) ...[
                      const SizedBox(height: 12),
                      Text(slambook.description!, style: AppTextStyles.bodySmall),
                    ],
                    const SizedBox(height: 12),
                    Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: [
                        _ChipText(text: slambookVisibilityLabel(slambook.visibility)),
                        _ChipText(text: slambookIdentityLabel(slambook.responseIdentityMode)),
                        _ChipText(text: '${detail.cards.length} prompts'),
                      ],
                    ),
                    const SizedBox(height: 14),
                    if (slambook.viewerCanRespond &&
                        slambook.status == 'active' &&
                        detail.viewerSession?.status != 'approved')
                      SizedBox(
                        width: double.infinity,
                        child: ElevatedButton.icon(
                          onPressed: () => Navigator.of(context).push(
                            MaterialPageRoute<void>(
                              builder: (_) => SlambookResponseScreen(
                                slambookId: slambook.id,
                                shareToken: shareToken,
                              ),
                            ),
                          ),
                          style: ElevatedButton.styleFrom(
                            backgroundColor: AppColors.postbookPrimary,
                            foregroundColor: Colors.white,
                          ),
                          icon: const Icon(Icons.reply_outlined),
                          label: Text(
                            detail.viewerSession == null ? 'Answer now' : 'Continue response',
                          ),
                        ),
                      ),
                    if (detail.viewerSession != null) ...[
                      const SizedBox(height: 12),
                      Container(
                        padding: const EdgeInsets.all(12),
                        decoration: BoxDecoration(
                          color: AppColors.bgSecondary,
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Text(
                          'Your response status: ${detail.viewerSession!.status}',
                          style: AppTextStyles.bodySmall,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
              const SizedBox(height: 18),
              Text('Prompt cards', style: AppTextStyles.h2),
              const SizedBox(height: 10),
              ...detail.cards.map(
                (card) => Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Container(
                    padding: const EdgeInsets.all(14),
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(card.title, style: AppTextStyles.h3),
                        const SizedBox(height: 6),
                        Text(card.prompt, style: AppTextStyles.bodySmall),
                      ],
                    ),
                  ),
                ),
              ),
            ],
          );
        },
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => const Center(
          child: Text('Could not load the shared SlamBook.'),
        ),
      ),
    );
  }
}

class _ChipText extends StatelessWidget {
  const _ChipText({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(text, style: AppTextStyles.labelSmall),
    );
  }
}
