import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/broadcast_channels_repository.dart';
import 'package:atpost_app/providers/broadcast_channels_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ChannelDetailScreen extends ConsumerStatefulWidget {
  final String channelId;
  const ChannelDetailScreen({super.key, required this.channelId});

  @override
  ConsumerState<ChannelDetailScreen> createState() =>
      _ChannelDetailScreenState();
}

class _ChannelDetailScreenState extends ConsumerState<ChannelDetailScreen> {
  bool _subscribed = false;
  bool _toggleLoading = false;

  Future<void> _toggleSubscription() async {
    if (_toggleLoading) return;
    final wasSubscribed = _subscribed;
    setState(() {
      _subscribed = !_subscribed;
      _toggleLoading = true;
    });
    try {
      final repo = ref.read(broadcastChannelsRepositoryProvider);
      if (wasSubscribed) {
        await repo.unsubscribe(widget.channelId);
      } else {
        await repo.subscribe(widget.channelId);
      }
      ref.invalidate(broadcastChannelDetailProvider(widget.channelId));
    } catch (_) {
      if (mounted) setState(() => _subscribed = wasSubscribed);
    } finally {
      if (mounted) setState(() => _toggleLoading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final channelAsync = ref.watch(broadcastChannelDetailProvider(widget.channelId));
    final updatesAsync = ref.watch(channelUpdatesProvider(widget.channelId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: channelAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.error_outline, color: AppColors.textDim, size: 40),
              const SizedBox(height: 12),
              Text('Failed to load channel', style: AppTextStyles.body),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () => ref.invalidate(
                    broadcastChannelDetailProvider(widget.channelId)),
                child: Text('Retry',
                    style: AppTextStyles.label
                        .copyWith(color: AppColors.postbookPrimary)),
              ),
            ],
          ),
        ),
        data: (channel) {
          // Sync subscription state
          if (!_toggleLoading) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (mounted && !_toggleLoading) {
                setState(() => _subscribed = channel.viewerRole != null);
              }
            });
          }

          return CustomScrollView(
            slivers: [
              // Header
              SliverAppBar(
                expandedHeight: 200,
                pinned: true,
                backgroundColor: AppColors.bgPrimary,
                leading: IconButton(
                  icon: const Icon(Icons.arrow_back, color: Colors.white),
                  onPressed: () => context.pop(),
                ),
                flexibleSpace: FlexibleSpaceBar(
                  background: Container(
                    decoration: const BoxDecoration(
                      gradient: LinearGradient(
                        begin: Alignment.topLeft,
                        end: Alignment.bottomRight,
                        colors: [
                          AppColors.accentPurple,
                          AppColors.postbookPrimary,
                        ],
                      ),
                    ),
                    child: SafeArea(
                      child: Padding(
                        padding: const EdgeInsets.fromLTRB(20, 60, 20, 20),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisAlignment: MainAxisAlignment.end,
                          children: [
                            Row(
                              children: [
                                Container(
                                  width: 56,
                                  height: 56,
                                  decoration: BoxDecoration(
                                    color: Colors.white.withValues(alpha: 0.2),
                                    borderRadius: BorderRadius.circular(16),
                                  ),
                                  child: Center(
                                    child: Text(
                                      channel.name.isNotEmpty
                                          ? channel.name[0].toUpperCase()
                                          : 'C',
                                      style: const TextStyle(
                                        color: Colors.white,
                                        fontWeight: FontWeight.w900,
                                        fontSize: 24,
                                      ),
                                    ),
                                  ),
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Row(
                                        children: [
                                          Flexible(
                                            child: Text(
                                              channel.name,
                                              style: const TextStyle(
                                                color: Colors.white,
                                                fontWeight: FontWeight.w700,
                                                fontSize: 18,
                                              ),
                                            ),
                                          ),
                                          if (channel.isVerified) ...[
                                            const SizedBox(width: 4),
                                            const Icon(Icons.verified,
                                                color: Colors.white, size: 18),
                                          ],
                                        ],
                                      ),
                                      Text(
                                        '@${channel.handle}',
                                        style: TextStyle(
                                          color: Colors.white
                                              .withValues(alpha: 0.8),
                                          fontSize: 13,
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                              ],
                            ),
                            const SizedBox(height: 8),
                            Text(
                              '${channel.subscriberCount} subscribers · ${channel.updateCount} updates',
                              style: TextStyle(
                                color: Colors.white.withValues(alpha: 0.8),
                                fontSize: 12,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
              ),

              // Subscribe button
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding
                      .copyWith(top: 16, bottom: 8),
                  child: SizedBox(
                    width: double.infinity,
                    child: _toggleLoading
                        ? const Center(
                            child: Padding(
                              padding: EdgeInsets.all(12),
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                          )
                        : _subscribed
                            ? OutlinedButton(
                                onPressed: _toggleSubscription,
                                style: OutlinedButton.styleFrom(
                                  foregroundColor: AppColors.textSecondary,
                                  side: const BorderSide(
                                      color: AppColors.borderSubtle),
                                  padding:
                                      const EdgeInsets.symmetric(vertical: 12),
                                  shape: RoundedRectangleBorder(
                                    borderRadius: BorderRadius.circular(
                                        AppSpacing.radiusMedium),
                                  ),
                                ),
                                child: Text('Subscribed',
                                    style: AppTextStyles.label),
                              )
                            : Container(
                                decoration: BoxDecoration(
                                  gradient: AppColors.postbookGradient,
                                  borderRadius: BorderRadius.circular(
                                      AppSpacing.radiusMedium),
                                ),
                                child: OutlinedButton(
                                  onPressed: _toggleSubscription,
                                  style: OutlinedButton.styleFrom(
                                    foregroundColor: Colors.white,
                                    side: BorderSide.none,
                                    padding: const EdgeInsets.symmetric(
                                        vertical: 12),
                                    shape: RoundedRectangleBorder(
                                      borderRadius: BorderRadius.circular(
                                          AppSpacing.radiusMedium),
                                    ),
                                  ),
                                  child: Text('Subscribe',
                                      style: AppTextStyles.label),
                                ),
                              ),
                  ),
                ),
              ),

              // Description
              if (channel.description.isNotEmpty)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: AppSpacing.pagePadding
                        .copyWith(top: 8, bottom: 16),
                    child: Text(
                      channel.description,
                      style: AppTextStyles.body
                          .copyWith(color: AppColors.textSecondary),
                    ),
                  ),
                ),

              // Divider
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding,
                  child: Text('Updates', style: AppTextStyles.h3),
                ),
              ),

              // Updates list
              updatesAsync.when(
                loading: () => const SliverToBoxAdapter(
                  child: Padding(
                    padding: EdgeInsets.all(40),
                    child: Center(
                      child: CircularProgressIndicator(
                          color: AppColors.postbookPrimary),
                    ),
                  ),
                ),
                error: (_, _) => SliverToBoxAdapter(
                  child: Center(
                    child: Padding(
                      padding: const EdgeInsets.all(40),
                      child: Text('Failed to load updates',
                          style: AppTextStyles.body),
                    ),
                  ),
                ),
                data: (updates) {
                  if (updates.isEmpty) {
                    return SliverToBoxAdapter(
                      child: Padding(
                        padding: const EdgeInsets.all(40),
                        child: Center(
                          child: Column(
                            children: [
                              const Icon(Icons.article_outlined,
                                  color: AppColors.textDim, size: 40),
                              const SizedBox(height: 8),
                              Text('No updates yet',
                                  style: AppTextStyles.body.copyWith(
                                      color: AppColors.textSecondary)),
                            ],
                          ),
                        ),
                      ),
                    );
                  }
                  return SliverList(
                    delegate: SliverChildBuilderDelegate(
                      (context, index) {
                        final update = updates[index];
                        return Padding(
                          padding: AppSpacing.pagePadding
                              .copyWith(top: 8, bottom: 8),
                          child: Card(
                            color: AppColors.bgCard,
                            shape: RoundedRectangleBorder(
                              borderRadius: BorderRadius.circular(
                                  AppSpacing.radiusLarge),
                              side: const BorderSide(
                                  color: AppColors.borderSubtle),
                            ),
                            child: Padding(
                              padding: const EdgeInsets.all(14),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  if (update.title != null) ...[
                                    Text(update.title!,
                                        style: AppTextStyles.h3),
                                    const SizedBox(height: 6),
                                  ],
                                  Text(
                                    update.body,
                                    style: AppTextStyles.body.copyWith(
                                        color: AppColors.textSecondary),
                                  ),
                                  const SizedBox(height: 10),
                                  Row(
                                    children: [
                                      Icon(Icons.visibility_outlined,
                                          size: 14,
                                          color: AppColors.textDim),
                                      const SizedBox(width: 4),
                                      Text('${update.viewCount}',
                                          style: AppTextStyles.labelSmall
                                              .copyWith(
                                                  color: AppColors.textDim)),
                                      const SizedBox(width: 12),
                                      Icon(Icons.favorite_outline,
                                          size: 14,
                                          color: AppColors.textDim),
                                      const SizedBox(width: 4),
                                      Text('${update.reactionCount}',
                                          style: AppTextStyles.labelSmall
                                              .copyWith(
                                                  color: AppColors.textDim)),
                                      const SizedBox(width: 12),
                                      Icon(Icons.comment_outlined,
                                          size: 14,
                                          color: AppColors.textDim),
                                      const SizedBox(width: 4),
                                      Text('${update.commentCount}',
                                          style: AppTextStyles.labelSmall
                                              .copyWith(
                                                  color: AppColors.textDim)),
                                    ],
                                  ),
                                ],
                              ),
                            ),
                          ),
                        );
                      },
                      childCount: updates.length,
                    ),
                  );
                },
              ),
            ],
          );
        },
      ),
    );
  }
}
