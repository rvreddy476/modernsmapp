import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ExploreScreen extends ConsumerStatefulWidget {
  const ExploreScreen({super.key});

  @override
  ConsumerState<ExploreScreen> createState() => _ExploreScreenState();
}

class _ExploreScreenState extends ConsumerState<ExploreScreen> {
  final TextEditingController _searchController = TextEditingController();
  List<Map<String, dynamic>> _autocompleteResults = [];

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _onSearchChanged(String value) async {
    if (value.isEmpty) {
      setState(() => _autocompleteResults = []);
      return;
    }
    if (value.isNotEmpty) {
      final results = await ref
          .read(userRepositoryProvider)
          .searchAutocomplete(value);
      if (mounted) {
        setState(() => _autocompleteResults = results);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: SingleChildScrollView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Explore', style: AppTextStyles.h1),
            const SizedBox(height: 16),

            // Search bar
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Row(
                children: [
                  const Icon(Icons.search, color: AppColors.textDim, size: 20),
                  const SizedBox(width: 10),
                  Expanded(
                    child: TextField(
                      controller: _searchController,
                      onChanged: _onSearchChanged,
                      style: AppTextStyles.body,
                      decoration: InputDecoration(
                        hintText: 'Search people, posts, tags...',
                        hintStyle: AppTextStyles.body.copyWith(
                          color: AppColors.textDim,
                        ),
                        border: InputBorder.none,
                        isDense: true,
                        contentPadding: const EdgeInsets.symmetric(vertical: 10),
                      ),
                      cursorColor: AppColors.postbookPrimary,
                    ),
                  ),
                  if (_searchController.text.isNotEmpty)
                    GestureDetector(
                      onTap: () {
                        _searchController.clear();
                        setState(() => _autocompleteResults = []);
                      },
                      child: const Icon(
                        Icons.close,
                        color: AppColors.textDim,
                        size: 18,
                      ),
                    ),
                ],
              ),
            ),

            // Autocomplete results
            if (_autocompleteResults.isNotEmpty) ...[
              const SizedBox(height: 8),
              Container(
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Column(
                  children: _autocompleteResults.asMap().entries.map((entry) {
                    final index = entry.key;
                    final item = entry.value;
                    final username = item['username'] as String? ?? '';
                    final displayName = item['display_name'] as String? ?? username;
                    final initials = displayName.isNotEmpty
                        ? displayName[0].toUpperCase()
                        : '?';
                    final isLast = index == _autocompleteResults.length - 1;

                    return Column(
                      children: [
                        InkWell(
                          borderRadius: BorderRadius.vertical(
                            top: index == 0
                                ? Radius.circular(AppSpacing.radiusXL)
                                : Radius.zero,
                            bottom: isLast
                                ? Radius.circular(AppSpacing.radiusXL)
                                : Radius.zero,
                          ),
                          onTap: () {
                            final userId = item['user_id'] as String?;
                            if (userId != null && userId.isNotEmpty) {
                              context.push('/profile/$userId');
                            } else {
                              ScaffoldMessenger.of(context).showSnackBar(
                                SnackBar(
                                  content: Text('@$username'),
                                  backgroundColor: AppColors.bgCard,
                                  behavior: SnackBarBehavior.floating,
                                ),
                              );
                            }
                          },
                          child: Padding(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 16,
                              vertical: 10,
                            ),
                            child: Row(
                              children: [
                                CircleAvatar(
                                  radius: 18,
                                  backgroundColor:
                                      AppColors.postbookPrimary.withValues(alpha: 0.25),
                                  child: Text(
                                    initials,
                                    style: AppTextStyles.label.copyWith(
                                      color: AppColors.postbookPrimary,
                                    ),
                                  ),
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment: CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        displayName,
                                        style: AppTextStyles.h3,
                                        maxLines: 1,
                                        overflow: TextOverflow.ellipsis,
                                      ),
                                      Text(
                                        '@$username',
                                        style: AppTextStyles.bodySmall.copyWith(
                                          color: AppColors.textDim,
                                        ),
                                        maxLines: 1,
                                        overflow: TextOverflow.ellipsis,
                                      ),
                                    ],
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                        if (!isLast)
                          Divider(
                            height: 1,
                            thickness: 1,
                            color: AppColors.borderSubtle,
                            indent: 16,
                            endIndent: 16,
                          ),
                      ],
                    );
                  }).toList(),
                ),
              ),
            ],

            const SizedBox(height: 24),

            // Feature tiles
            Text('Features', style: AppTextStyles.h2),
            const SizedBox(height: 12),
            GridView.count(
              crossAxisCount: 2,
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              mainAxisSpacing: 12,
              crossAxisSpacing: 12,
              childAspectRatio: 1.4,
              children: [
                _FeatureTile(
                  icon: Icons.storefront,
                  label: 'Shop',
                  subtitle: 'Buy & sell',
                  color: AppColors.postbookPrimary,
                  onTap: () => context.push('/shop'),
                ),
                _FeatureTile(
                  icon: Icons.photo_album,
                  label: 'Memories',
                  subtitle: 'On this day',
                  color: AppColors.accentPurple,
                  onTap: () => context.push('/memories'),
                ),
                _FeatureTile(
                  icon: Icons.live_tv,
                  label: 'Live',
                  subtitle: 'Watch & go live',
                  color: AppColors.liveRed,
                  onTap: () => context.push('/live'),
                ),
                _FeatureTile(
                  icon: Icons.video_library,
                  label: 'PostTube',
                  subtitle: 'Long videos',
                  color: AppColors.posttubePrimary,
                  onTap: () => context.push('/posttube'),
                ),
              ],
            ),
            const SizedBox(height: 24),

            // Trending section placeholder
            Text('Trending', style: AppTextStyles.h2),
            const SizedBox(height: 12),
            Container(
              height: 160,
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: const Center(
                child: Icon(Icons.trending_up, color: AppColors.textDim, size: 40),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _FeatureTile extends StatelessWidget {
  final IconData icon;
  final String label;
  final String subtitle;
  final Color color;
  final VoidCallback onTap;

  const _FeatureTile({
    required this.icon,
    required this.label,
    required this.subtitle,
    required this.color,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
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
              width: 36,
              height: 36,
              decoration: BoxDecoration(
                color: color.withValues(alpha: 0.2),
                borderRadius: BorderRadius.circular(10),
              ),
              child: Icon(icon, color: color, size: 20),
            ),
            const Spacer(),
            Text(label, style: AppTextStyles.h3),
            Text(subtitle, style: AppTextStyles.labelSmall.copyWith(color: AppColors.textDim)),
          ],
        ),
      ),
    );
  }
}
