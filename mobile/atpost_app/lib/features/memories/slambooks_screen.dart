import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/slambook.dart';
import 'package:atpost_app/features/memories/slambook_compose_screen.dart';
import 'package:atpost_app/features/memories/slambook_data.dart';
import 'package:atpost_app/features/memories/slambook_detail_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SlambooksScreen extends ConsumerWidget {
  const SlambooksScreen({super.key});

  Future<void> _refresh(WidgetRef ref) async {
    ref.invalidate(mySlambooksProvider);
    ref.invalidate(slambookTemplatePacksProvider);
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final slambooksAsync = ref.watch(mySlambooksProvider);
    final packsAsync = ref.watch(slambookTemplatePacksProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () => _refresh(ref),
          child: CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 24),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      _HeroCard(
                        onCreate: () => Navigator.of(context).push(
                          MaterialPageRoute<void>(
                            builder: (_) => const SlambookComposeScreen(),
                          ),
                        ),
                      ),
                      const SizedBox(height: 20),
                      Text('Template packs', style: AppTextStyles.h2),
                      const SizedBox(height: 10),
                      packsAsync.when(
                        data: (packs) {
                          if (packs.isEmpty) {
                            return const _InlineStateCard(
                              icon: Icons.style_outlined,
                              message: 'No template packs are available yet.',
                            );
                          }
                          return SizedBox(
                            height: 160,
                            child: ListView.separated(
                              scrollDirection: Axis.horizontal,
                              itemCount: packs.length,
                              separatorBuilder: (_, _) => const SizedBox(width: 10),
                              itemBuilder: (context, index) {
                                final pack = packs[index];
                                return _TemplatePackCard(pack: pack);
                              },
                            ),
                          );
                        },
                        loading: () => const SizedBox(
                          height: 120,
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        ),
                        error: (_, _) => const _InlineStateCard(
                          icon: Icons.style_outlined,
                          message: 'Could not load SlamBook template packs.',
                        ),
                      ),
                      const SizedBox(height: 24),
                      Row(
                        children: [
                          Text('Your SlamBooks', style: AppTextStyles.h2),
                          const Spacer(),
                          ElevatedButton.icon(
                            onPressed: () => Navigator.of(context).push(
                              MaterialPageRoute<void>(
                                builder: (_) => const SlambookComposeScreen(),
                              ),
                            ),
                            style: ElevatedButton.styleFrom(
                              backgroundColor: AppColors.postbookPrimary,
                              foregroundColor: Colors.white,
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(10),
                              ),
                            ),
                            icon: const Icon(Icons.add, size: 16),
                            label: const Text('New'),
                          ),
                        ],
                      ),
                      const SizedBox(height: 10),
                      slambooksAsync.when(
                        data: (slambooks) {
                          if (slambooks.isEmpty) {
                            return const _InlineStateCard(
                              icon: Icons.auto_stories_outlined,
                              message: 'Create your first SlamBook to start collecting responses.',
                            );
                          }
                          return Column(
                            children: slambooks
                                .map(
                                  (slambook) => Padding(
                                    padding: const EdgeInsets.only(bottom: 12),
                                    child: _SlambookCard(
                                      slambook: slambook,
                                      onTap: () => Navigator.of(context).push(
                                        MaterialPageRoute<void>(
                                          builder: (_) => SlambookDetailScreen(
                                            slambookId: slambook.id,
                                          ),
                                        ),
                                      ),
                                    ),
                                  ),
                                )
                                .toList(),
                          );
                        },
                        loading: () => const Padding(
                          padding: EdgeInsets.symmetric(vertical: 32),
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        ),
                        error: (_, _) => const _InlineStateCard(
                          icon: Icons.auto_stories_outlined,
                          message: 'Could not load your SlamBooks.',
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _HeroCard extends StatelessWidget {
  const _HeroCard({required this.onCreate});

  final VoidCallback onCreate;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        gradient: const LinearGradient(
          colors: [Color(0x33FF6B35), Color(0x337B68EE), Color(0x334ECDC4)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text('SlamBooks', style: AppTextStyles.h1.copyWith(fontSize: 30)),
              ),
              const Icon(Icons.auto_stories_outlined, color: AppColors.textPrimary),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            'Profile-first prompt books with approvals, share links, and an opinion board.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 14),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: const [
              _MiniChip(icon: Icons.edit_note_outlined, text: 'Create'),
              _MiniChip(icon: Icons.rate_review_outlined, text: 'Moderate'),
              _MiniChip(icon: Icons.push_pin_outlined, text: 'Opinion board'),
            ],
          ),
          const SizedBox(height: 14),
          SizedBox(
            width: double.infinity,
            child: ElevatedButton.icon(
              onPressed: onCreate,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                foregroundColor: Colors.white,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              icon: const Icon(Icons.add_circle_outline),
              label: const Text('New SlamBook'),
            ),
          ),
        ],
      ),
    );
  }
}

class _TemplatePackCard extends StatelessWidget {
  const _TemplatePackCard({required this.pack});

  final SlambookTemplatePack pack;

  @override
  Widget build(BuildContext context) {
    final accent = slambookAccentColor(pack.key);
    return Container(
      width: 220,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: accent.withValues(alpha: 0.14),
              borderRadius: BorderRadius.circular(12),
            ),
            child: Icon(Icons.layers_outlined, color: accent),
          ),
          const SizedBox(height: 12),
          Text(pack.title, style: AppTextStyles.h3),
          const SizedBox(height: 4),
          Text(
            pack.description ?? 'Template pack',
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
            style: AppTextStyles.bodySmall,
          ),
          const Spacer(),
          Text(
            '${pack.templates.length} prompts',
            style: AppTextStyles.labelSmall.copyWith(color: accent),
          ),
        ],
      ),
    );
  }
}

class _SlambookCard extends StatelessWidget {
  const _SlambookCard({
    required this.slambook,
    required this.onTap,
  });

  final Slambook slambook;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final accent = slambookAccentColor(slambook.themeKey);
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                  gradient: LinearGradient(
                    colors: [
                      accent.withValues(alpha: 0.24),
                      accent.withValues(alpha: 0.10),
                    ],
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                  ),
                ),
                child: Row(
                  children: [
                    Container(
                      width: 42,
                      height: 42,
                      decoration: BoxDecoration(
                        color: accent.withValues(alpha: 0.18),
                        borderRadius: BorderRadius.circular(14),
                      ),
                      child: Icon(Icons.menu_book_rounded, color: accent),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(slambook.title, style: AppTextStyles.h2),
                          if ((slambook.subtitle ?? '').trim().isNotEmpty) ...[
                            const SizedBox(height: 2),
                            Text(
                              slambook.subtitle!,
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: AppTextStyles.bodySmall,
                            ),
                          ],
                        ],
                      ),
                    ),
                    const Icon(Icons.chevron_right_rounded, color: AppColors.textMuted),
                  ],
                ),
              ),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  _MiniChip(
                    icon: Icons.people_outline,
                    text: slambookVisibilityLabel(slambook.visibility),
                  ),
                  _MiniChip(
                    icon: Icons.auto_awesome_outlined,
                    text: '${slambook.approvedCount} approved',
                  ),
                  _MiniChip(
                    icon: Icons.reply_outlined,
                    text: '${slambook.responseCount} responses',
                  ),
                  _MiniChip(
                    icon: Icons.lock_outline,
                    text: slambookIdentityLabel(slambook.responseIdentityMode),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Text(
                    'Updated ${slambookRelativeDate(slambook.updatedAt)}',
                    style: AppTextStyles.labelSmall,
                  ),
                  const Spacer(),
                  Text(
                    slambook.allowShareLink ? 'share link on' : 'link off',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: slambook.allowShareLink
                          ? AppColors.onlineGreen
                          : AppColors.textMuted,
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({
    required this.icon,
    required this.message,
  });

  final IconData icon;
  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(18),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textSecondary),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
        ],
      ),
    );
  }
}

class _MiniChip extends StatelessWidget {
  const _MiniChip({required this.icon, required this.text});

  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 14, color: AppColors.textSecondary),
          const SizedBox(width: 5),
          Text(text, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}
