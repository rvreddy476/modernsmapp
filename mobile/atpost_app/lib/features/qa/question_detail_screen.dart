import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/repositories/qa_repository.dart';
import 'package:atpost_app/features/qa/widgets/qa_report_sheet.dart';
import 'package:atpost_app/features/qa/widgets/request_answer_sheet.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class QuestionDetailScreen extends ConsumerStatefulWidget {
  final String questionId;
  const QuestionDetailScreen({super.key, required this.questionId});

  @override
  ConsumerState<QuestionDetailScreen> createState() =>
      _QuestionDetailScreenState();
}

class _QuestionDetailScreenState
    extends ConsumerState<QuestionDetailScreen> {
  final TextEditingController _answerController = TextEditingController();
  bool _isSubmitting = false;
  bool _answerAnonymously = false;

  // Server-backed answer draft state.
  Timer? _draftDebounce;
  String? _draftId;
  bool _savingDraft = false;

  @override
  void initState() {
    super.initState();
    _answerController.addListener(_onAnswerChanged);
  }

  @override
  void dispose() {
    _draftDebounce?.cancel();
    _answerController.removeListener(_onAnswerChanged);
    _answerController.dispose();
    super.dispose();
  }

  void _onAnswerChanged() {
    _draftDebounce?.cancel();
    _draftDebounce = Timer(const Duration(milliseconds: 1500), _saveDraft);
  }

  Future<void> _saveDraft() async {
    final text = _answerController.text.trim();
    if (text.isEmpty) return;
    if (_savingDraft) return;
    if (mounted) setState(() => _savingDraft = true);
    try {
      final draft = await ref.read(qaRepositoryProvider).upsertAnswerDraft({
        if (_draftId != null) 'id': _draftId,
        'question_id': widget.questionId,
        'body': text,
        'is_anonymous': _answerAnonymously,
      });
      _draftId = draft.id;
    } catch (_) {
      // best-effort — drafts are not critical
    } finally {
      if (mounted) setState(() => _savingDraft = false);
    }
  }

  Future<void> _submitAnswer() async {
    final text = _answerController.text.trim();
    if (text.isEmpty || _isSubmitting) return;
    setState(() => _isSubmitting = true);
    try {
      await ref.read(qaRepositoryProvider).submitAnswer(
            widget.questionId,
            text,
            isAnonymous: _answerAnonymously,
          );
      _answerController.clear();
      if (_draftId != null) {
        try {
          await ref.read(qaRepositoryProvider).deleteAnswerDraft(_draftId!);
        } catch (_) {}
        _draftId = null;
      }
      ref.invalidate(questionDetailProvider(widget.questionId));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not post answer: $e')),
      );
    } finally {
      if (mounted) setState(() => _isSubmitting = false);
    }
  }

  Future<void> _toggleQuestionVote(Question q, String voteType) async {
    final repo = ref.read(qaRepositoryProvider);
    final wasUp = q.viewerVote == true;
    final wasDown = q.viewerVote == false;
    final tappedSame =
        (voteType == 'up' && wasUp) || (voteType == 'down' && wasDown);
    try {
      if (tappedSame) {
        await repo.removeQuestionVote(q.id);
      } else {
        await repo.voteQuestion(q.id, voteType);
      }
      ref.invalidate(questionDetailProvider(widget.questionId));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not register vote.')),
      );
    }
  }

  Future<void> _toggleAnswerVote(Answer a, String voteType) async {
    final repo = ref.read(qaRepositoryProvider);
    final wasUp = a.viewerVote == true;
    final wasDown = a.viewerVote == false;
    final tappedSame =
        (voteType == 'up' && wasUp) || (voteType == 'down' && wasDown);
    try {
      if (tappedSame) {
        await repo.removeAnswerVote(a.id);
      } else {
        await repo.voteAnswer(a.id, voteType);
      }
      ref.invalidate(questionDetailProvider(widget.questionId));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not register vote.')),
      );
    }
  }

  Future<void> _markAsBest(Question q, Answer a) async {
    try {
      await ref.read(qaRepositoryProvider).selectBestAnswer(q.id, a.id);
      ref.invalidate(questionDetailProvider(widget.questionId));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not mark best answer.')),
      );
    }
  }

  Future<void> _reportQuestion(Question q) async {
    final result = await showQaReportSheet(context);
    if (result == null) return;
    try {
      await ref
          .read(qaRepositoryProvider)
          .createQuestionReport(q.id, result.reason, result.details);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Report submitted.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not send report.')),
      );
    }
  }

  Future<void> _reportAnswer(Answer a) async {
    final result = await showQaReportSheet(context);
    if (result == null) return;
    try {
      await ref
          .read(qaRepositoryProvider)
          .createAnswerReport(a.id, result.reason, result.details);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Report submitted.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not send report.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final detailAsync = ref.watch(questionDetailProvider(widget.questionId));
    final currentUserAsync = ref.watch(currentUserProvider);
    final currentUserId = currentUserAsync.value?.id;

    return Scaffold(
      backgroundColor: Colors.black,
      body: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
            colors: [Color(0xFF0F111A), Color(0xFF090A11)],
          ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              _buildHeader(context, detailAsync.value?.question),
              Expanded(
                child: detailAsync.when(
                  loading: () => const Center(
                    child: CircularProgressIndicator(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                  error: (e, _) => Center(
                    child: Text(
                      'Error loading detail',
                      style: AppTextStyles.bodySmall,
                    ),
                  ),
                  data: (detail) => _buildContent(detail, currentUserId),
                ),
              ),
              _buildAnswerComposer(),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(BuildContext context, Question? question) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back,
            tooltip: 'Back',
            onPressed: () => context.pop(),
          ),
          const SizedBox(width: 12),
          Text('Question', style: AppTextStyles.h2),
          const Spacer(),
          if (question != null) ...[
            GlassIconButton(
              icon: Icons.person_add_alt_1_outlined,
              tooltip: 'Ask someone to answer',
              onPressed: () => RequestAnswerSheet.show(
                context,
                questionId: question.id,
              ),
            ),
            const SizedBox(width: 4),
            GlassIconButton(
              icon: Icons.flag_outlined,
              tooltip: 'Report',
              onPressed: () => _reportQuestion(question),
            ),
            const SizedBox(width: 4),
          ],
          const GlassIconButton(icon: Icons.share_outlined, tooltip: 'Share'),
        ],
      ),
    );
  }

  Widget _buildContent(QuestionDetail detail, String? currentUserId) {
    final isAuthor = currentUserId != null &&
        currentUserId.isNotEmpty &&
        currentUserId == detail.question.authorId;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _buildQuestionSection(detail.question),
        const Divider(height: 40, color: Colors.white10),
        Text('${detail.answers.length} Answers', style: AppTextStyles.h3),
        const SizedBox(height: 16),
        ...detail.answers.map(
          (answer) => _AnswerTile(
            answer: answer,
            canMarkBest: isAuthor && !answer.isAccepted,
            onVote: (vt) => _toggleAnswerVote(answer, vt),
            onMarkBest: () => _markAsBest(detail.question, answer),
            onReport: () => _reportAnswer(answer),
          ),
        ),
        const SizedBox(height: 100),
      ],
    );
  }

  Widget _buildQuestionSection(Question q) {
    final isAnon = q.isAnonymous || q.authorId == anonymousAuthorId;
    final displayName = isAnon ? 'Anonymous' : (q.authorName ?? 'Anonymous');
    final avatarUrl = isAnon ? null : q.authorAvatar;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (q.community != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: InkWell(
              onTap: () => context.push('/communities/${q.community!.id}'),
              child: Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 10,
                  vertical: 6,
                ),
                decoration: BoxDecoration(
                  color: AppColors.posttubePrimary.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(
                      Icons.groups_outlined,
                      size: 16,
                      color: AppColors.posttubePrimary,
                    ),
                    const SizedBox(width: 6),
                    Text(
                      q.community!.name,
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.posttubePrimary,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        Row(
          children: [
            CircleAvatar(
              radius: 14,
              backgroundColor: AppColors.bgTertiary,
              backgroundImage: avatarUrl != null && avatarUrl.isNotEmpty
                  ? NetworkImage(avatarUrl)
                  : null,
              child: avatarUrl == null || avatarUrl.isEmpty
                  ? Icon(
                      isAnon
                          ? Icons.visibility_off_outlined
                          : Icons.person,
                      size: 16,
                      color: Colors.white24,
                    )
                  : null,
            ),
            const SizedBox(width: 8),
            Text(
              displayName,
              style: AppTextStyles.labelSmall.copyWith(color: Colors.white70),
            ),
            const Spacer(),
            Text(
              _timeAgo(q.createdAt),
              style: AppTextStyles.labelSmall.copyWith(color: Colors.white24),
            ),
          ],
        ),
        const SizedBox(height: 16),
        Text(q.title, style: AppTextStyles.h2),
        if (q.body.trim().isNotEmpty) ...[
          const SizedBox(height: 12),
          Text(
            q.body,
            style: AppTextStyles.bodyMedium.copyWith(color: Colors.white70),
          ),
        ],
        if (q.topicObjects.isNotEmpty) ...[
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: q.topicObjects
                .map(
                  (topic) => Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 10,
                      vertical: 4,
                    ),
                    decoration: BoxDecoration(
                      color: AppColors.bgTertiary,
                      borderRadius: BorderRadius.circular(6),
                    ),
                    child: Text(
                      '#${topic.name}',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: Colors.white70,
                      ),
                    ),
                  ),
                )
                .toList(),
          ),
        ],
        const SizedBox(height: 16),
        _VoteRail(
          score: q.upvoteCount - q.downvoteCount,
          viewerVote: q.viewerVote,
          onUp: () => _toggleQuestionVote(q, 'up'),
          onDown: () => _toggleQuestionVote(q, 'down'),
        ),
      ],
    );
  }

  Widget _buildAnswerComposer() {
    return SafeArea(
      top: false,
      child: Container(
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
        decoration: const BoxDecoration(
          color: Color(0xFF11131C),
          border: Border(top: BorderSide(color: Colors.white12)),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Switch.adaptive(
                  value: _answerAnonymously,
                  onChanged: (v) => setState(() => _answerAnonymously = v),
                ),
                Text(
                  'Answer anonymously',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: Colors.white70,
                  ),
                ),
                const Spacer(),
                if (_savingDraft)
                  Text(
                    'Saving draft…',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: Colors.white38,
                    ),
                  ),
              ],
            ),
            const SizedBox(height: 6),
            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Expanded(
                  child: TextField(
                    controller: _answerController,
                    minLines: 1,
                    maxLines: 4,
                    style: AppTextStyles.bodyMedium,
                    decoration: InputDecoration(
                      hintText: 'Write an answer…',
                      hintStyle: AppTextStyles.bodyMedium.copyWith(
                        color: Colors.white38,
                      ),
                      filled: true,
                      fillColor: AppColors.bgTertiary,
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(10),
                        borderSide: BorderSide.none,
                      ),
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                ElevatedButton(
                  onPressed: _isSubmitting ? null : _submitAnswer,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                    foregroundColor: Colors.white,
                  ),
                  child: _isSubmitting
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : const Text('Post'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inDays > 0) return '${diff.inDays}d';
    if (diff.inHours > 0) return '${diff.inHours}h';
    if (diff.inMinutes > 0) return '${diff.inMinutes}m';
    return 'now';
  }
}

class _AnswerTile extends StatelessWidget {
  final Answer answer;
  final bool canMarkBest;
  final ValueChanged<String> onVote;
  final VoidCallback onMarkBest;
  final VoidCallback onReport;

  const _AnswerTile({
    required this.answer,
    required this.canMarkBest,
    required this.onVote,
    required this.onMarkBest,
    required this.onReport,
  });

  @override
  Widget build(BuildContext context) {
    final isAnon =
        answer.isAnonymous || answer.authorId == anonymousAuthorId;
    final displayName =
        isAnon ? 'Anonymous' : (answer.authorName ?? 'User');
    final avatarUrl = isAnon ? null : answer.authorAvatar;
    return Container(
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: answer.isAccepted
            ? AppColors.postbookPrimary.withValues(alpha: 0.06)
            : AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(12),
        border: answer.isAccepted
            ? Border.all(color: AppColors.postbookPrimary, width: 1)
            : null,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CircleAvatar(
                radius: 12,
                backgroundColor: AppColors.bgTertiary,
                backgroundImage: avatarUrl != null && avatarUrl.isNotEmpty
                    ? NetworkImage(avatarUrl)
                    : null,
                child: avatarUrl == null || avatarUrl.isEmpty
                    ? Icon(
                        isAnon
                            ? Icons.visibility_off_outlined
                            : Icons.person,
                        size: 14,
                        color: Colors.white24,
                      )
                    : null,
              ),
              const SizedBox(width: 8),
              Text(
                displayName,
                style: AppTextStyles.labelSmall
                    .copyWith(color: Colors.white70),
              ),
              const Spacer(),
              if (answer.isAccepted) ...[
                const Icon(
                  Icons.verified_rounded,
                  size: 16,
                  color: AppColors.postbookPrimary,
                ),
                const SizedBox(width: 4),
                Text(
                  'Best answer',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
                const SizedBox(width: 4),
              ],
              IconButton(
                icon: const Icon(Icons.flag_outlined, size: 18),
                color: Colors.white38,
                tooltip: 'Report',
                onPressed: onReport,
              ),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            answer.body,
            style: AppTextStyles.bodyMedium.copyWith(color: Colors.white),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              _VoteRail(
                small: true,
                score: answer.upvoteCount - answer.downvoteCount,
                viewerVote: answer.viewerVote,
                onUp: () => onVote('up'),
                onDown: () => onVote('down'),
              ),
              const Spacer(),
              if (canMarkBest)
                TextButton.icon(
                  icon: const Icon(Icons.check_circle_outline, size: 16),
                  label: const Text('Mark as best'),
                  onPressed: onMarkBest,
                  style: TextButton.styleFrom(
                    foregroundColor: AppColors.postbookPrimary,
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _VoteRail extends StatelessWidget {
  final int score;
  final bool? viewerVote; // true = up, false = down, null = none
  final VoidCallback onUp;
  final VoidCallback onDown;
  final bool small;

  const _VoteRail({
    required this.score,
    required this.viewerVote,
    required this.onUp,
    required this.onDown,
    this.small = false,
  });

  @override
  Widget build(BuildContext context) {
    final iconSize = small ? 18.0 : 22.0;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        IconButton(
          padding: EdgeInsets.zero,
          constraints: const BoxConstraints(),
          icon: Icon(
            Icons.arrow_upward_rounded,
            size: iconSize,
            color: viewerVote == true
                ? AppColors.postbookPrimary
                : Colors.white24,
          ),
          onPressed: onUp,
        ),
        const SizedBox(width: 8),
        Text(
          '$score',
          style: AppTextStyles.label.copyWith(
            fontWeight: FontWeight.bold,
            color: viewerVote != null
                ? AppColors.postbookPrimary
                : Colors.white,
          ),
        ),
        const SizedBox(width: 8),
        IconButton(
          padding: EdgeInsets.zero,
          constraints: const BoxConstraints(),
          icon: Icon(
            Icons.arrow_downward_rounded,
            size: iconSize,
            color: viewerVote == false ? Colors.blueAccent : Colors.white24,
          ),
          onPressed: onDown,
        ),
      ],
    );
  }
}
