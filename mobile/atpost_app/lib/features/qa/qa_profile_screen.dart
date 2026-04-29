import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class QaProfileScreen extends ConsumerWidget {
  final String userId;
  const QaProfileScreen({super.key, required this.userId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final profileAsync = ref.watch(qaProfileProvider(userId));
    final questionsAsync = ref.watch(qaUserQuestionsProvider(userId));
    final answersAsync = ref.watch(qaUserAnswersProvider(userId));
    final badgesAsync = ref.watch(qaUserBadgesProvider(userId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: GlassIconButton(
          icon: Icons.arrow_back_ios_new,
          tooltip: 'Back',
          onPressed: () => context.pop(),
        ),
        title: Text('Q&A Profile', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          profileAsync.when(
            loading: () => const Center(
              child: Padding(
                padding: EdgeInsets.all(20),
                child: CircularProgressIndicator(
                    color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => Text(
              'Could not load profile.',
              style: AppTextStyles.body.copyWith(color: AppColors.textDim),
            ),
            data: (p) => _ProfileHeader(profile: p),
          ),
          const SizedBox(height: 16),
          _BadgesSection(badgesAsync: badgesAsync),
          const SizedBox(height: 16),
          _CollapsibleList<Question>(
            title: 'Questions asked',
            asyncValue: questionsAsync,
            itemBuilder: (q) => ListTile(
              dense: true,
              title: Text(
                q.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: AppTextStyles.body,
              ),
              subtitle: Text(
                '${q.answerCount} answers · ${q.voteScore} votes',
                style: AppTextStyles.labelSmall
                    .copyWith(color: AppColors.textSecondary),
              ),
              onTap: () => context.push('/qa/question/${q.id}'),
            ),
          ),
          const SizedBox(height: 8),
          _CollapsibleList<Answer>(
            title: 'Answers given',
            asyncValue: answersAsync,
            itemBuilder: (a) => ListTile(
              dense: true,
              title: Text(
                a.body,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: AppTextStyles.body,
              ),
              subtitle: Text(
                '${a.voteScore} votes${a.isAccepted ? ' · accepted' : ''}',
                style: AppTextStyles.labelSmall
                    .copyWith(color: AppColors.textSecondary),
              ),
              onTap: () => context.push('/qa/question/${a.questionId}'),
            ),
          ),
        ],
      ),
    );
  }
}

class _ProfileHeader extends StatelessWidget {
  final QaProfile profile;
  const _ProfileHeader({required this.profile});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CircleAvatar(
                radius: 28,
                backgroundColor: AppColors.bgTertiary,
                backgroundImage: profile.avatarUrl != null &&
                        profile.avatarUrl!.isNotEmpty
                    ? NetworkImage(profile.avatarUrl!)
                    : null,
                child: profile.avatarUrl == null || profile.avatarUrl!.isEmpty
                    ? const Icon(Icons.person, color: Colors.white24)
                    : null,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(profile.displayName, style: AppTextStyles.h3),
                    if (profile.bio.isNotEmpty)
                      Text(
                        profile.bio,
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.textSecondary,
                        ),
                      ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              _Stat(label: 'Reputation', value: profile.reputationScore),
              _Stat(label: 'Questions', value: profile.questionCount),
              _Stat(label: 'Answers', value: profile.answerCount),
              _Stat(label: 'Best', value: profile.bestAnswerCount),
            ],
          ),
          if (profile.expertiseAreas.isNotEmpty) ...[
            const SizedBox(height: 12),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: profile.expertiseAreas
                  .map((area) => Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 10, vertical: 4),
                        decoration: BoxDecoration(
                          color: AppColors.posttubePrimary
                              .withValues(alpha: 0.15),
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Text(
                          area,
                          style: AppTextStyles.labelSmall.copyWith(
                            color: AppColors.posttubePrimary,
                          ),
                        ),
                      ))
                  .toList(),
            ),
          ],
        ],
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  final String label;
  final int value;
  const _Stat({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '$value',
            style: AppTextStyles.label.copyWith(fontWeight: FontWeight.bold),
          ),
          const SizedBox(width: 4),
          Text(label,
              style: AppTextStyles.labelSmall
                  .copyWith(color: AppColors.textSecondary)),
        ],
      ),
    );
  }
}

class _BadgesSection extends StatelessWidget {
  final AsyncValue<List<ContributorBadge>> badgesAsync;
  const _BadgesSection({required this.badgesAsync});

  @override
  Widget build(BuildContext context) {
    return badgesAsync.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (badges) {
        if (badges.isEmpty) return const SizedBox.shrink();
        return Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Badges', style: AppTextStyles.h3),
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: badges
                    .map((b) => Chip(
                          backgroundColor: AppColors.bgTertiary,
                          label: Text(
                            b.title ?? b.badgeType,
                            style: AppTextStyles.labelSmall,
                          ),
                          avatar: const Icon(
                            Icons.emoji_events,
                            color: Colors.amber,
                            size: 16,
                          ),
                        ))
                    .toList(),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _CollapsibleList<T> extends StatefulWidget {
  final String title;
  final AsyncValue<List<T>> asyncValue;
  final Widget Function(T item) itemBuilder;

  const _CollapsibleList({
    required this.title,
    required this.asyncValue,
    required this.itemBuilder,
  });

  @override
  State<_CollapsibleList<T>> createState() => _CollapsibleListState<T>();
}

class _CollapsibleListState<T> extends State<_CollapsibleList<T>> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ExpansionTile(
        initiallyExpanded: _expanded,
        onExpansionChanged: (val) => setState(() => _expanded = val),
        title: Text(widget.title, style: AppTextStyles.h3),
        children: [
          widget.asyncValue.when(
            loading: () => const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: CircularProgressIndicator(
                    color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => Padding(
              padding: const EdgeInsets.all(12),
              child: Text(
                'Could not load.',
                style:
                    AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
              ),
            ),
            data: (items) {
              if (items.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.all(12),
                  child: Text(
                    'Nothing here yet.',
                    style: AppTextStyles.bodySmall
                        .copyWith(color: AppColors.textDim),
                  ),
                );
              }
              return Column(children: items.map(widget.itemBuilder).toList());
            },
          ),
        ],
      ),
    );
  }
}
