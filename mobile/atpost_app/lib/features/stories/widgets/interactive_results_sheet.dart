import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/story.dart';
import 'package:atpost_app/providers/stories_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Bottom sheet shown when the creator taps "View results" on their own story.
/// Renders a per-type summary fetched from the backend.
class InteractiveResultsSheet extends ConsumerWidget {
  const InteractiveResultsSheet({
    super.key,
    required this.storyId,
    required this.interactive,
  });

  final String storyId;
  final StoryInteractive interactive;

  static Future<void> show(
    BuildContext context, {
    required String storyId,
    required StoryInteractive interactive,
  }) {
    return showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => InteractiveResultsSheet(
        storyId: storyId,
        interactive: interactive,
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final key = InteractiveResultsKey(storyId, interactive.id);
    final asyncResults = ref.watch(interactiveResultsProvider(key));

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Container(
              width: 40,
              height: 4,
              margin: const EdgeInsets.only(bottom: 12),
              decoration: BoxDecoration(
                color: AppColors.borderMedium,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            Center(
              child: Text(
                _titleFor(interactive.type),
                style: AppTextStyles.h2,
              ),
            ),
            const SizedBox(height: 4),
            Center(
              child: Text(
                interactive.question,
                style: AppTextStyles.body,
                textAlign: TextAlign.center,
              ),
            ),
            const SizedBox(height: 16),
            asyncResults.when(
              loading: () => const Padding(
                padding: EdgeInsets.symmetric(vertical: 32),
                child: Center(
                  child: CircularProgressIndicator(
                    color: AppColors.postbookPrimary,
                  ),
                ),
              ),
              error: (_, _) => Padding(
                padding: const EdgeInsets.symmetric(vertical: 32),
                child: Center(
                  child: Text(
                    'Could not load results.',
                    style: AppTextStyles.body,
                  ),
                ),
              ),
              data: (results) {
                if (results == null) {
                  return Padding(
                    padding: const EdgeInsets.symmetric(vertical: 32),
                    child: Center(
                      child: Text(
                        'No responses yet.',
                        style: AppTextStyles.body,
                      ),
                    ),
                  );
                }
                return _ResultsBody(
                  interactive: interactive,
                  results: results,
                );
              },
            ),
          ],
        ),
      ),
    );
  }

  String _titleFor(String type) {
    switch (type) {
      case 'poll':
        return 'Poll results';
      case 'quiz':
        return 'Quiz results';
      case 'countdown':
        return 'Reminders set';
      case 'question':
        return 'Replies';
      case 'slider':
        return 'Slider responses';
      default:
        return 'Results';
    }
  }
}

class _ResultsBody extends StatelessWidget {
  const _ResultsBody({required this.interactive, required this.results});

  final StoryInteractive interactive;
  final StoryInteractiveResults results;

  @override
  Widget build(BuildContext context) {
    switch (interactive.type) {
      case 'poll':
      case 'quiz':
        return _OptionVotesList(interactive: interactive, results: results);
      case 'countdown':
        return _CountdownSummary(results: results);
      case 'question':
        return _RepliesList(results: results);
      case 'slider':
        return _SliderSummary(results: results);
      default:
        return const SizedBox.shrink();
    }
  }
}

class _OptionVotesList extends StatelessWidget {
  const _OptionVotesList({required this.interactive, required this.results});

  final StoryInteractive interactive;
  final StoryInteractiveResults results;

  @override
  Widget build(BuildContext context) {
    final total = results.totalResponses == 0 ? 1 : results.totalResponses;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < interactive.options.length; i++) ...[
          _OptionVoteRow(
            label: interactive.options[i].text,
            votes: results.votes[interactive.options[i].id] ?? 0,
            total: total,
            isCorrect: interactive.type == 'quiz' &&
                interactive.correctIdx == i,
          ),
          const SizedBox(height: 8),
        ],
        const SizedBox(height: 8),
        Text('${results.totalResponses} responses',
            style: AppTextStyles.labelSmall),
      ],
    );
  }
}

class _OptionVoteRow extends StatelessWidget {
  const _OptionVoteRow({
    required this.label,
    required this.votes,
    required this.total,
    required this.isCorrect,
  });

  final String label;
  final int votes;
  final int total;
  final bool isCorrect;

  @override
  Widget build(BuildContext context) {
    final pct = total == 0 ? 0.0 : votes / total;
    return Stack(
      children: [
        Container(
          height: 40,
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(8),
          ),
        ),
        FractionallySizedBox(
          widthFactor: pct.clamp(0, 1).toDouble(),
          child: Container(
            height: 40,
            decoration: BoxDecoration(
              color: isCorrect
                  ? AppColors.statusSuccess.withAlpha(120)
                  : AppColors.postbookPrimary.withAlpha(120),
              borderRadius: BorderRadius.circular(8),
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          child: Row(
            children: [
              if (isCorrect)
                const Padding(
                  padding: EdgeInsets.only(right: 6),
                  child: Icon(Icons.check_circle,
                      size: 16, color: AppColors.statusSuccess),
                ),
              Expanded(
                child: Text(
                  label,
                  style: AppTextStyles.label,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text('$votes (${(pct * 100).round()}%)',
                  style: AppTextStyles.labelSmall),
            ],
          ),
        ),
      ],
    );
  }
}

class _CountdownSummary extends StatelessWidget {
  const _CountdownSummary({required this.results});

  final StoryInteractiveResults results;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 24),
      child: Center(
        child: Column(
          children: [
            Text('${results.remindersSet}', style: AppTextStyles.h1),
            const SizedBox(height: 4),
            Text('reminders set', style: AppTextStyles.body),
          ],
        ),
      ),
    );
  }
}

class _RepliesList extends StatelessWidget {
  const _RepliesList({required this.results});

  final StoryInteractiveResults results;

  @override
  Widget build(BuildContext context) {
    if (results.replies.isEmpty) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 32),
        child: Center(
            child: Text('No replies yet.', style: AppTextStyles.body)),
      );
    }
    return ConstrainedBox(
      constraints: const BoxConstraints(maxHeight: 360),
      child: ListView.separated(
        shrinkWrap: true,
        itemCount: results.replies.length,
        separatorBuilder: (_, _) =>
            const Divider(color: AppColors.borderSubtle),
        itemBuilder: (context, i) {
          final r = results.replies[i];
          return Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(r.displayName.isEmpty ? 'Anonymous' : r.displayName,
                    style: AppTextStyles.label),
                const SizedBox(height: 2),
                Text(r.text, style: AppTextStyles.body),
              ],
            ),
          );
        },
      ),
    );
  }
}

class _SliderSummary extends StatelessWidget {
  const _SliderSummary({required this.results});

  final StoryInteractiveResults results;

  @override
  Widget build(BuildContext context) {
    final avg = results.sliderAverage ?? 0;
    final histogram = results.sliderHistogram;
    final maxH =
        histogram.fold<int>(0, (acc, v) => v > acc ? v : acc);

    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Center(
          child: Column(
            children: [
              Text(avg.toStringAsFixed(1), style: AppTextStyles.h1),
              Text('average', style: AppTextStyles.labelSmall),
            ],
          ),
        ),
        const SizedBox(height: 16),
        if (histogram.isNotEmpty)
          SizedBox(
            height: 80,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                for (final h in histogram)
                  Expanded(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 1),
                      child: Container(
                        height: maxH == 0 ? 1 : (h / maxH * 80),
                        decoration: BoxDecoration(
                          color: AppColors.postbookPrimary.withAlpha(180),
                          borderRadius: BorderRadius.circular(2),
                        ),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        const SizedBox(height: 8),
        Text('${results.totalResponses} responses',
            style: AppTextStyles.labelSmall),
      ],
    );
  }
}
