import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/repositories/communities_repository.dart';
import 'package:atpost_app/data/repositories/community_posts_repository.dart';
import 'package:atpost_app/features/discover/question_detail_screen.dart';
import 'package:atpost_app/features/discover/qa_question_tile.dart';
import 'package:atpost_app/providers/communities_provider.dart';
import 'package:atpost_app/providers/community_posts_provider.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/shared/widgets/community_post_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CommunityDetailScreen extends ConsumerStatefulWidget {
  final String communityId;
  const CommunityDetailScreen({super.key, required this.communityId});

  @override
  ConsumerState<CommunityDetailScreen> createState() =>
      _CommunityDetailScreenState();
}

class _CommunityDetailScreenState extends ConsumerState<CommunityDetailScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabCtrl;
  bool _joined = false;
  bool _toggleLoading = false;
  String? _selectedSpaceId;
  int _activeTabIndex = 0;

  @override
  void initState() {
    super.initState();
    _tabCtrl = TabController(length: 3, vsync: this);
    _tabCtrl.addListener(_handleTabChange);
  }

  void _handleTabChange() {
    if (!mounted || _activeTabIndex == _tabCtrl.index) {
      return;
    }
    setState(() => _activeTabIndex = _tabCtrl.index);
  }

  @override
  void dispose() {
    _tabCtrl.removeListener(_handleTabChange);
    _tabCtrl.dispose();
    super.dispose();
  }

  Future<void> _toggleJoin() async {
    if (_toggleLoading) return;
    final wasJoined = _joined;
    setState(() {
      _joined = !_joined;
      _toggleLoading = true;
    });
    try {
      final repo = ref.read(communitiesRepositoryProvider);
      if (wasJoined) {
        await repo.leave(widget.communityId);
      } else {
        await repo.join(widget.communityId);
      }
      ref.invalidate(communityDetailProvider(widget.communityId));
      ref.invalidate(communitiesProvider);
    } catch (_) {
      if (mounted) setState(() => _joined = wasJoined);
    } finally {
      if (mounted) setState(() => _toggleLoading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final communityAsync = ref.watch(
      communityDetailProvider(widget.communityId),
    );
    final spacesAsync = ref.watch(communitySpacesProvider(widget.communityId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: communityAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(
                Icons.error_outline,
                color: AppColors.textDim,
                size: 40,
              ),
              const SizedBox(height: 12),
              Text('Failed to load community', style: AppTextStyles.body),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () =>
                    ref.invalidate(communityDetailProvider(widget.communityId)),
                child: Text(
                  'Retry',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
            ],
          ),
        ),
        data: (community) {
          final isJoinedFromServer =
              community.viewerRole != null &&
              community.viewerRole != 'outsider';
          if (!_toggleLoading && _joined != isJoinedFromServer) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (mounted && !_toggleLoading && _joined != isJoinedFromServer) {
                setState(() => _joined = isJoinedFromServer);
              }
            });
          }

          return NestedScrollView(
            headerSliverBuilder: (context, _) => [
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
                          AppColors.postbookPrimary,
                          AppColors.accentPurple,
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
                                      community.name.isNotEmpty
                                          ? community.name[0].toUpperCase()
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
                                              community.name,
                                              style: const TextStyle(
                                                color: Colors.white,
                                                fontWeight: FontWeight.w700,
                                                fontSize: 18,
                                              ),
                                            ),
                                          ),
                                          if (community.isVerified) ...[
                                            const SizedBox(width: 4),
                                            const Icon(
                                              Icons.verified,
                                              color: Colors.white,
                                              size: 18,
                                            ),
                                          ],
                                        ],
                                      ),
                                      Text(
                                        '@${community.handle}',
                                        style: TextStyle(
                                          color: Colors.white.withValues(
                                            alpha: 0.8,
                                          ),
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
                              '${community.memberCount} members · ${community.spaceCount} spaces · ${community.communityType}',
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

              // Action buttons row: Joined/Leave + Notify + Echo
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 8),
                  child: Row(
                    children: [
                      Expanded(
                        flex: 3,
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
                            : _joined
                            ? OutlinedButton.icon(
                                onPressed: _toggleJoin,
                                style: OutlinedButton.styleFrom(
                                  foregroundColor: AppColors.textSecondary,
                                  side: const BorderSide(
                                    color: AppColors.borderSubtle,
                                  ),
                                  padding: const EdgeInsets.symmetric(
                                    vertical: 12,
                                  ),
                                  shape: RoundedRectangleBorder(
                                    borderRadius: BorderRadius.circular(
                                      AppSpacing.radiusMedium,
                                    ),
                                  ),
                                ),
                                icon: const Icon(
                                  Icons.check_circle_outline,
                                  size: 18,
                                ),
                                label: Text(
                                  'Joined',
                                  style: AppTextStyles.label,
                                ),
                              )
                            : Container(
                                decoration: BoxDecoration(
                                  gradient: AppColors.postbookGradient,
                                  borderRadius: BorderRadius.circular(
                                    AppSpacing.radiusMedium,
                                  ),
                                ),
                                child: OutlinedButton(
                                  onPressed: _toggleJoin,
                                  style: OutlinedButton.styleFrom(
                                    foregroundColor: Colors.white,
                                    side: BorderSide.none,
                                    padding: const EdgeInsets.symmetric(
                                      vertical: 12,
                                    ),
                                    shape: RoundedRectangleBorder(
                                      borderRadius: BorderRadius.circular(
                                        AppSpacing.radiusMedium,
                                      ),
                                    ),
                                  ),
                                  child: Text(
                                    'Join Community',
                                    style: AppTextStyles.label,
                                  ),
                                ),
                              ),
                      ),
                      const SizedBox(width: 8),
                      _ActionIconButton(
                        icon: Icons.notifications_outlined,
                        onTap: () {
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(
                              content: Text(
                                'Notification preferences coming soon.',
                              ),
                            ),
                          );
                        },
                      ),
                      const SizedBox(width: 8),
                      _ActionIconButton(
                        icon: Icons.repeat_rounded,
                        onTap: () {
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(
                              content: Text('Echo community coming soon.'),
                            ),
                          );
                        },
                      ),
                    ],
                  ),
                ),
              ),

              // Description
              if (community.description.isNotEmpty)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 4),
                    child: Text(
                      community.description,
                      style: AppTextStyles.body.copyWith(
                        color: AppColors.textSecondary,
                      ),
                    ),
                  ),
                ),

              if (community.topicTags.isNotEmpty)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 6),
                    child: Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: community.topicTags
                          .map((tag) => _CommunityTopicChip(label: '#$tag'))
                          .toList(),
                    ),
                  ),
                ),

              if (_activeTabIndex == 0)
                SliverToBoxAdapter(
                  child: _SpaceTabs(
                    communityId: widget.communityId,
                    selectedSpaceId: _selectedSpaceId,
                    onSpaceSelected: (spaceId) {
                      setState(() => _selectedSpaceId = spaceId);
                    },
                  ),
                ),

              // Tabs
              SliverPersistentHeader(
                pinned: true,
                delegate: _TabBarDelegate(
                  TabBar(
                    controller: _tabCtrl,
                    labelColor: AppColors.postbookPrimary,
                    unselectedLabelColor: AppColors.textDim,
                    indicatorColor: AppColors.postbookPrimary,
                    labelStyle: AppTextStyles.label,
                    tabs: const [
                      Tab(text: 'Spaces'),
                      Tab(text: 'Questions'),
                      Tab(text: 'Members'),
                    ],
                  ),
                ),
              ),
            ],
            body: TabBarView(
              controller: _tabCtrl,
              children: [
                // Spaces tab — shows posts from selected space, or space list
                _buildSpacesTab(spacesAsync),
                _CommunityQuestionsView(
                  communityId: widget.communityId,
                  topicTags: community.topicTags,
                ),
                _buildMembersTab(community),
              ],
            ),
          );
        },
      ),
    );
  }

  Widget _buildSpacesTab(AsyncValue<List<CommunitySpace>> spacesAsync) {
    if (_selectedSpaceId != null) {
      return _SpacePostsView(
        communityId: widget.communityId,
        spaceId: _selectedSpaceId!,
      );
    }

    return spacesAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Text('Failed to load spaces', style: AppTextStyles.body),
      ),
      data: (spaces) {
        if (spaces.isEmpty) {
          return Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(
                  Icons.space_dashboard_outlined,
                  color: AppColors.textDim,
                  size: 40,
                ),
                const SizedBox(height: 8),
                Text(
                  'No spaces yet',
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ],
            ),
          );
        }

        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: spaces.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, index) =>
              _SpaceTile(space: spaces[index], communityId: widget.communityId),
        );
      },
    );
  }

  Widget _buildMembersTab(Community community) {
    return Center(
      child: Padding(
        padding: AppSpacing.pagePadding,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(
              Icons.people_outline,
              color: AppColors.textDim,
              size: 40,
            ),
            const SizedBox(height: 8),
            Text(
              '${community.memberCount} members',
              style: AppTextStyles.body.copyWith(
                color: AppColors.textSecondary,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              'Community Q&A now has its own tab, while member and moderation tools can stay here.',
              textAlign: TextAlign.center,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textMuted,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ActionIconButton extends StatelessWidget {
  final IconData icon;
  final VoidCallback onTap;

  const _ActionIconButton({required this.icon, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: IconButton(
        onPressed: onTap,
        icon: Icon(icon, color: AppColors.textSecondary, size: 20),
        constraints: const BoxConstraints(minWidth: 44, minHeight: 44),
      ),
    );
  }
}

class _CommunityTopicChip extends StatelessWidget {
  final String label;

  const _CommunityTopicChip({required this.label});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.posttubePrimary.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Text(
        label,
        style: AppTextStyles.label.copyWith(color: AppColors.posttubePrimary),
      ),
    );
  }
}

class _SpaceTabs extends ConsumerWidget {
  final String communityId;
  final String? selectedSpaceId;
  final ValueChanged<String?> onSpaceSelected;

  const _SpaceTabs({
    required this.communityId,
    required this.selectedSpaceId,
    required this.onSpaceSelected,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final spacesAsync = ref.watch(communitySpacesProvider(communityId));

    return spacesAsync.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (spaces) {
        if (spaces.isEmpty) return const SizedBox.shrink();
        return Padding(
          padding: const EdgeInsets.only(top: 12, bottom: 4),
          child: SizedBox(
            height: 40,
            child: ListView.builder(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 18),
              itemCount: spaces.length + 1,
              itemBuilder: (context, index) {
                if (index == 0) {
                  final isSelected = selectedSpaceId == null;
                  return Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: _SpaceChip(
                      label: 'All Spaces',
                      isSelected: isSelected,
                      onTap: () => onSpaceSelected(null),
                    ),
                  );
                }
                final space = spaces[index - 1];
                final isSelected = selectedSpaceId == space.id;
                return Padding(
                  padding: const EdgeInsets.only(right: 8),
                  child: _SpaceChip(
                    label: space.name,
                    isSelected: isSelected,
                    onTap: () => onSpaceSelected(space.id),
                  ),
                );
              },
            ),
          ),
        );
      },
    );
  }
}

class _SpaceChip extends StatelessWidget {
  final String label;
  final bool isSelected;
  final VoidCallback onTap;

  const _SpaceChip({
    required this.label,
    required this.isSelected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: isSelected
              ? AppColors.accentPurple.withValues(alpha: 0.2)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: isSelected ? AppColors.accentPurple : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: isSelected
                ? AppColors.accentPurple
                : AppColors.textSecondary,
          ),
        ),
      ),
    );
  }
}

class _CommunityQuestionsView extends ConsumerStatefulWidget {
  final String communityId;
  final List<String> topicTags;

  const _CommunityQuestionsView({
    required this.communityId,
    required this.topicTags,
  });

  @override
  ConsumerState<_CommunityQuestionsView> createState() =>
      _CommunityQuestionsViewState();
}

class _CommunityQuestionsViewState
    extends ConsumerState<_CommunityQuestionsView> {
  String _sort = 'recent';
  String? _selectedTopicSlug;

  @override
  Widget build(BuildContext context) {
    final params = CommunityQuestionsParams(
      communityId: widget.communityId,
      topicSlug: _selectedTopicSlug,
      sort: _sort,
    );
    final questionsAsync = ref.watch(communityQuestionsProvider(params));

    return questionsAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Padding(
          padding: AppSpacing.pagePadding,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(
                Icons.error_outline,
                color: AppColors.textDim,
                size: 40,
              ),
              const SizedBox(height: 12),
              Text(
                'Could not load community questions',
                style: AppTextStyles.body,
              ),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () =>
                    ref.invalidate(communityQuestionsProvider(params)),
                child: Text(
                  'Retry',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
      data: (result) {
        final filters = _buildTopicFilters(
          result.availableTopics,
          widget.topicTags,
        );

        return RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: () async {
            ref.invalidate(communityQuestionsProvider(params));
          },
          child: ListView(
            padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
            children: [
              _CommunityQaSummaryCard(settings: result.settings),
              if (filters.length > 1) ...[
                const SizedBox(height: 16),
                Text('Topic Scope', style: AppTextStyles.label),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: filters
                      .map(
                        (filter) => _QuestionFilterChip(
                          label: filter.label,
                          selected: filter.slug == _selectedTopicSlug,
                          onTap: () {
                            setState(() => _selectedTopicSlug = filter.slug);
                          },
                        ),
                      )
                      .toList(),
                ),
              ],
              const SizedBox(height: 18),
              Row(
                children: [
                  Text('Questions', style: AppTextStyles.h2),
                  const Spacer(),
                  _QuestionSortChip(
                    label: 'Recent',
                    selected: _sort == 'recent',
                    onTap: () => setState(() => _sort = 'recent'),
                  ),
                  const SizedBox(width: 8),
                  _QuestionSortChip(
                    label: 'Top',
                    selected: _sort == 'votes',
                    onTap: () => setState(() => _sort = 'votes'),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              if (!result.settings.qaEnabled)
                const _QuestionEmptyCard(
                  icon: Icons.lock_outline,
                  title: 'Q&A disabled',
                  message: 'This community has Q&A turned off right now.',
                )
              else if (result.questions.isEmpty)
                _QuestionEmptyCard(
                  icon: Icons.forum_outlined,
                  title: 'No questions yet',
                  message: _selectedTopicSlug == null
                      ? 'This community does not have any questions yet.'
                      : 'No questions are available for #$_selectedTopicSlug in this community.',
                )
              else
                Column(
                  children: result.questions
                      .map(
                        (question) => Padding(
                          padding: const EdgeInsets.only(bottom: 12),
                          child: QaQuestionTile(
                            question: question,
                            onTap: () {
                              Navigator.of(context).push(
                                MaterialPageRoute<void>(
                                  builder: (_) => QuestionDetailScreen(
                                    questionId: question.id,
                                  ),
                                ),
                              );
                            },
                          ),
                        ),
                      )
                      .toList(),
                ),
            ],
          ),
        );
      },
    );
  }

  List<_TopicFilterOption> _buildTopicFilters(
    List<QaTopicOption> availableTopics,
    List<String> fallbackTopicTags,
  ) {
    final filters = <_TopicFilterOption>[
      const _TopicFilterOption(label: 'All topics'),
    ];
    final seen = <String>{};

    for (final topic in availableTopics) {
      final slug = topic.slug.trim();
      if (slug.isEmpty || !seen.add(slug)) {
        continue;
      }
      filters.add(_TopicFilterOption(label: '#$slug', slug: slug));
    }

    for (final tag in fallbackTopicTags) {
      final slug = tag.trim().replaceFirst(RegExp(r'^#+'), '');
      if (slug.isEmpty || !seen.add(slug)) {
        continue;
      }
      filters.add(_TopicFilterOption(label: '#$slug', slug: slug));
    }

    return filters;
  }
}

class _TopicFilterOption {
  final String label;
  final String? slug;

  const _TopicFilterOption({required this.label, this.slug});
}

class _CommunityQaSummaryCard extends StatelessWidget {
  final CommunityQaSettings settings;

  const _CommunityQaSummaryCard({required this.settings});

  @override
  Widget build(BuildContext context) {
    final description = settings.welcomeMessage.trim().isNotEmpty
        ? settings.welcomeMessage.trim()
        : 'Questions here stay scoped to this community while still benefiting from topic discovery.';

    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Community Q&A', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          Text(
            description,
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              _CommunityMetricPill(
                label: '${settings.totalQuestions} questions',
              ),
              _CommunityMetricPill(label: '${settings.totalAnswers} answers'),
              _CommunityMetricPill(
                label: '${settings.uniqueContributors} contributors',
              ),
              _CommunityMetricPill(label: 'Ask: ${settings.askPermission}'),
              _CommunityMetricPill(
                label: 'Answer: ${settings.answerPermission}',
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _CommunityMetricPill extends StatelessWidget {
  final String label;

  const _CommunityMetricPill({required this.label});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(
          color: AppColors.textSecondary,
        ),
      ),
    );
  }
}

class _QuestionFilterChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;

  const _QuestionFilterChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Ink(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.posttubePrimary.withValues(alpha: 0.14)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: selected
                ? AppColors.posttubePrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: selected
                ? AppColors.posttubePrimary
                : AppColors.textSecondary,
          ),
        ),
      ),
    );
  }
}

class _QuestionSortChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;

  const _QuestionSortChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Ink(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.16)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.textSecondary,
          ),
        ),
      ),
    );
  }
}

class _QuestionEmptyCard extends StatelessWidget {
  final IconData icon;
  final String title;
  final String message;

  const _QuestionEmptyCard({
    required this.icon,
    required this.title,
    required this.message,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(18),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        children: [
          Icon(icon, color: AppColors.textDim, size: 34),
          const SizedBox(height: 10),
          Text(title, style: AppTextStyles.h3),
          const SizedBox(height: 6),
          Text(
            message,
            textAlign: TextAlign.center,
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
        ],
      ),
    );
  }
}

class _SpacePostsView extends ConsumerWidget {
  final String communityId;
  final String spaceId;

  const _SpacePostsView({required this.communityId, required this.spaceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final postsAsync = ref.watch(
      communityPostsProvider(
        CommunityPostsParams(communityId: communityId, spaceId: spaceId),
      ),
    );

    return postsAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, color: AppColors.textDim, size: 40),
            const SizedBox(height: 12),
            Text('Failed to load posts', style: AppTextStyles.body),
            const SizedBox(height: 8),
            TextButton(
              onPressed: () => ref.invalidate(
                communityPostsProvider(
                  CommunityPostsParams(
                    communityId: communityId,
                    spaceId: spaceId,
                  ),
                ),
              ),
              child: Text(
                'Retry',
                style: AppTextStyles.label.copyWith(
                  color: AppColors.postbookPrimary,
                ),
              ),
            ),
          ],
        ),
      ),
      data: (posts) {
        if (posts.isEmpty) {
          return Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(
                  Icons.forum_outlined,
                  color: AppColors.textDim,
                  size: 40,
                ),
                const SizedBox(height: 8),
                Text(
                  'No posts in this space yet.',
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
                const SizedBox(height: 4),
                TextButton(
                  onPressed: () =>
                      context.push('/communities/$communityId/spaces/$spaceId'),
                  child: Text(
                    'Open Space',
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                ),
              ],
            ),
          );
        }

        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: posts.length,
          separatorBuilder: (_, _) => const SizedBox(height: 10),
          itemBuilder: (context, index) {
            final post = posts[index];
            return CommunityPostCard(
              post: post,
              onTap: () =>
                  context.push('/communities/$communityId/spaces/$spaceId'),
              onSpark: () {
                ref
                    .read(communityPostsRepositoryProvider)
                    .sparkPost(communityId, spaceId, post.id);
              },
            );
          },
        );
      },
    );
  }
}

class _SpaceTile extends StatelessWidget {
  final CommunitySpace space;
  final String communityId;
  const _SpaceTile({required this.space, required this.communityId});

  IconData _iconForType(String type) {
    switch (type) {
      case 'group':
        return Icons.group;
      case 'channel':
        return Icons.campaign;
      case 'discussion':
        return Icons.forum;
      case 'events':
        return Icons.event;
      case 'resources':
        return Icons.folder_open;
      default:
        return Icons.space_dashboard;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        leading: Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Icon(
            _iconForType(space.spaceType),
            color: AppColors.postbookPrimary,
            size: 20,
          ),
        ),
        title: Row(
          children: [
            Flexible(child: Text(space.name, style: AppTextStyles.h3)),
            if (space.isQuarantined) ...[
              const SizedBox(width: 4),
              const Icon(Icons.warning_amber, color: Colors.amber, size: 14),
            ],
          ],
        ),
        subtitle: Text(
          space.description.isNotEmpty ? space.description : space.spaceType,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: AppTextStyles.labelSmall.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
        trailing: Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Text(
            space.spaceType,
            style: AppTextStyles.labelSmall.copyWith(
              color: AppColors.textDim,
              fontSize: 10,
            ),
          ),
        ),
        onTap: () {
          if (space.linkedGroupId != null) {
            context.push('/groups/${space.linkedGroupId}');
          } else if (space.linkedChannelId != null) {
            context.push('/channels/${space.linkedChannelId}');
          } else {
            context.push('/communities/$communityId/spaces/${space.id}');
          }
        },
      ),
    );
  }
}

class _TabBarDelegate extends SliverPersistentHeaderDelegate {
  final TabBar tabBar;
  _TabBarDelegate(this.tabBar);

  @override
  Widget build(
    BuildContext context,
    double shrinkOffset,
    bool overlapsContent,
  ) {
    return Container(color: AppColors.bgPrimary, child: tabBar);
  }

  @override
  double get maxExtent => tabBar.preferredSize.height;

  @override
  double get minExtent => tabBar.preferredSize.height;

  @override
  bool shouldRebuild(covariant _TabBarDelegate oldDelegate) => false;
}
