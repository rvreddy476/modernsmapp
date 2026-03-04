import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class DiscoverScreen extends ConsumerStatefulWidget {
  const DiscoverScreen({super.key});

  @override
  ConsumerState<DiscoverScreen> createState() => _DiscoverScreenState();
}

class _DiscoverScreenState extends ConsumerState<DiscoverScreen> {
  List<String> _hashtags = [];
  List<Map<String, dynamic>> _suggestions = [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  Future<void> _loadData() async {
    try {
      final api = ref.read(apiClientProvider);
      final results = await Future.wait([
        api.get('/v1/search/trending'),
        api.get('/v1/graph/suggestions'),
      ]);
      final trendingRes = results[0];
      final suggestionsRes = results[1];
      if (mounted) {
        setState(() {
          _hashtags = ((trendingRes.data['data'] as List<dynamic>?) ?? [])
              .map((e) => (e as Map)['tag']?.toString() ?? e.toString())
              .toList();
          _suggestions =
              ((suggestionsRes.data['data'] as List<dynamic>?) ?? [])
                  .map((e) => Map<String, dynamic>.from(e as Map))
                  .toList();
          _loading = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        title: Text('Discover', style: AppTextStyles.h3),
        leading: const BackButton(color: AppColors.textSecondary),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _loadData,
              child: SingleChildScrollView(
                physics: const AlwaysScrollableScrollPhysics(),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const SizedBox(height: 8),

                    // Trending Hashtags section
                    Padding(
                      padding: AppSpacing.pagePadding.copyWith(bottom: 8),
                      child: Text('Trending Hashtags', style: AppTextStyles.h3),
                    ),
                    if (_hashtags.isEmpty)
                      Padding(
                        padding: AppSpacing.pagePadding.copyWith(bottom: 16),
                        child: Text(
                          'No trending hashtags right now',
                          style: AppTextStyles.bodySmall,
                        ),
                      )
                    else
                      SizedBox(
                        height: 44,
                        child: ListView.separated(
                          scrollDirection: Axis.horizontal,
                          padding: AppSpacing.pagePadding,
                          itemCount: _hashtags.length,
                          separatorBuilder: (_, _) => const SizedBox(width: 8),
                          itemBuilder: (context, index) {
                            final tag = _hashtags[index];
                            return ActionChip(
                              label: Text(
                                '#$tag',
                                style: AppTextStyles.label.copyWith(
                                  color: AppColors.postbookPrimary,
                                ),
                              ),
                              backgroundColor: AppColors.bgCard,
                              side: const BorderSide(color: AppColors.borderSubtle),
                              onPressed: () {
                                context.push(
                                  '/search/results?q=%23${Uri.encodeComponent(tag)}',
                                );
                              },
                            );
                          },
                        ),
                      ),

                    const SizedBox(height: 24),

                    // Suggested Accounts section
                    Padding(
                      padding: AppSpacing.pagePadding.copyWith(bottom: 12),
                      child: Text('Suggested Accounts', style: AppTextStyles.h3),
                    ),
                    if (_suggestions.isEmpty)
                      Padding(
                        padding: AppSpacing.pagePadding.copyWith(bottom: 16),
                        child: Text(
                          'No suggestions available',
                          style: AppTextStyles.bodySmall,
                        ),
                      )
                    else
                      SizedBox(
                        height: 120,
                        child: ListView.separated(
                          scrollDirection: Axis.horizontal,
                          padding: AppSpacing.pagePadding,
                          itemCount: _suggestions.length,
                          separatorBuilder: (_, _) => const SizedBox(width: 12),
                          itemBuilder: (context, index) {
                            final user = _suggestions[index];
                            final name =
                                user['display_name']?.toString() ??
                                user['username']?.toString() ??
                                'User';
                            final initials =
                                name.isNotEmpty ? name[0].toUpperCase() : 'U';
                            return SizedBox(
                              width: 80,
                              child: Column(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  CircleAvatar(
                                    radius: 28,
                                    backgroundColor: AppColors.bgTertiary,
                                    child: Text(
                                      initials,
                                      style: AppTextStyles.h3,
                                    ),
                                  ),
                                  const SizedBox(height: 6),
                                  Text(
                                    name,
                                    style: AppTextStyles.labelSmall,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    textAlign: TextAlign.center,
                                  ),
                                  const SizedBox(height: 4),
                                  GestureDetector(
                                    onTap: () {
                                      // Follow action placeholder
                                      ScaffoldMessenger.of(context)
                                          .showSnackBar(
                                        SnackBar(
                                          content: Text('Following $name'),
                                          duration: const Duration(seconds: 1),
                                        ),
                                      );
                                    },
                                    child: Container(
                                      padding: const EdgeInsets.symmetric(
                                        horizontal: 10,
                                        vertical: 3,
                                      ),
                                      decoration: BoxDecoration(
                                        gradient: AppColors.postbookGradient,
                                        borderRadius: BorderRadius.circular(
                                          AppSpacing.radiusFull,
                                        ),
                                      ),
                                      child: Text(
                                        'Follow',
                                        style: AppTextStyles.labelSmall
                                            .copyWith(color: Colors.white),
                                      ),
                                    ),
                                  ),
                                ],
                              ),
                            );
                          },
                        ),
                      ),

                    const SizedBox(height: 24),

                    // Trending Posts section
                    Padding(
                      padding: AppSpacing.pagePadding.copyWith(bottom: 12),
                      child: Text('Trending Posts', style: AppTextStyles.h3),
                    ),
                    _TrendingPostsPlaceholder(),

                    const SizedBox(height: 40),
                  ],
                ),
              ),
            ),
    );
  }
}

class _TrendingPostsPlaceholder extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    // Placeholder trending post cards — real implementation would use a provider
    return ListView.separated(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      padding: AppSpacing.pagePadding,
      itemCount: 5,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, index) {
        return Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            children: [
              CircleAvatar(
                radius: 18,
                backgroundColor: AppColors.bgTertiary,
                child: Text(
                  String.fromCharCode(65 + index),
                  style: AppTextStyles.label,
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Trending post #${index + 1}',
                      style: AppTextStyles.label,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      'Tap to view this trending content...',
                      style: AppTextStyles.bodySmall,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ),
              ),
              const Icon(
                Icons.trending_up,
                color: AppColors.postbookPrimary,
                size: 18,
              ),
            ],
          ),
        );
      },
    );
  }
}
