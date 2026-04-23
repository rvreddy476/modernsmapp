import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/providers/qa_provider.dart';
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

class _QuestionDetailScreenState extends ConsumerState<QuestionDetailScreen> {
  final TextEditingController _answerController = TextEditingController();
  final bool _isSubmitting = false;

  @override
  void dispose() {
    _answerController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final detailAsync = ref.watch(questionDetailProvider(widget.questionId));

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
              _buildHeader(context),
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
                  data: (detail) => _buildContent(detail),
                ),
              ),
              _buildAnswerComposer(),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(BuildContext context) {
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
          const GlassIconButton(icon: Icons.share_outlined, tooltip: 'Share'),
        ],
      ),
    );
  }

  Widget _buildContent(QuestionDetail detail) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _buildQuestionSection(detail.question),
        const Divider(height: 40, color: Colors.white10),
        Text('${detail.answers.length} Answers', style: AppTextStyles.h3),
        const SizedBox(height: 16),
        ...detail.answers.map((answer) => _AnswerTile(answer: answer)),
        const SizedBox(height: 100),
      ],
    );
  }

  Widget _buildQuestionSection(Question q) {
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
              backgroundImage:
                  q.authorAvatar != null && q.authorAvatar!.isNotEmpty
                  ? NetworkImage(q.authorAvatar!)
                  : null,
              child: q.authorAvatar == null || q.authorAvatar!.isEmpty
                  ? const Icon(Icons.person, size: 16, color: Colors.white24)
                  : null,
            ),
            const SizedBox(width: 8),
            Text(
              q.authorName ?? 'Anonymous',
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
        Text(q.title, style: AppTextStyles.h1.copyWith(fontSize: 22)),
        const SizedBox(height: 12),
        Text(
          q.body,
          style: AppTextStyles.body.copyWith(fontSize: 16, height: 1.5),
        ),
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
                      color: Colors.white.withOpacity(0.05),
                      borderRadius: BorderRadius.circular(6),
                      border: Border.all(color: Colors.white10),
                    ),
                    child: Text(
                      '#${topic.slug}',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: Colors.white54,
                      ),
                    ),
                  ),
                )
                .toList(),
          ),
        ],
        const SizedBox(height: 24),
        Row(
          children: [
            _VoteControls(score: q.voteScore, viewerVote: q.viewerVote),
            const SizedBox(width: 24),
            _IconStat(icon: Icons.visibility_outlined, value: q.viewCount),
            const SizedBox(width: 16),
            _IconStat(icon: Icons.forum_outlined, value: q.answerCount),
          ],
        ),
      ],
    );
  }

  Widget _buildAnswerComposer() {
    return Container(
      padding: EdgeInsets.only(
        left: 12,
        right: 12,
        top: 12,
        bottom: 12 + MediaQuery.of(context).viewInsets.bottom,
      ),
      decoration: BoxDecoration(
        color: const Color(0xFF1A1D2E),
        border: Border(top: BorderSide(color: Colors.white.withOpacity(0.05))),
      ),
      child: Row(
        children: [
          Expanded(
            child: TextField(
              controller: _answerController,
              maxLines: null,
              style: AppTextStyles.body,
              decoration: InputDecoration(
                hintText: 'Add an answer...',
                hintStyle: AppTextStyles.bodySmall.copyWith(
                  color: Colors.white24,
                ),
                border: InputBorder.none,
                contentPadding: const EdgeInsets.symmetric(horizontal: 12),
              ),
            ),
          ),
          IconButton(
            icon: _isSubmitting
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.send, color: AppColors.postbookPrimary),
            onPressed: _isSubmitting
                ? null
                : () {
                    if (_answerController.text.trim().isEmpty) return;
                    // Logic to submit answer
                  },
          ),
        ],
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

class _IconStat extends StatelessWidget {
  final IconData icon;
  final int value;
  const _IconStat({required this.icon, required this.value});

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 16, color: Colors.white38),
        const SizedBox(width: 6),
        Text(
          value.toString(),
          style: AppTextStyles.labelSmall.copyWith(color: Colors.white38),
        ),
      ],
    );
  }
}

class _AnswerTile extends StatelessWidget {
  final Answer answer;
  const _AnswerTile({required this.answer});

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CircleAvatar(
                radius: 12,
                backgroundImage: answer.authorAvatar != null
                    ? NetworkImage(answer.authorAvatar!)
                    : null,
              ),
              const SizedBox(width: 8),
              Text(
                answer.authorName ?? 'User',
                style: AppTextStyles.labelSmall.copyWith(color: Colors.white70),
              ),
              const Spacer(),
              if (answer.isAccepted)
                const Icon(Icons.check_circle, color: Colors.green, size: 16),
            ],
          ),
          const SizedBox(height: 10),
          Text(answer.body, style: AppTextStyles.body.copyWith(height: 1.4)),
          const SizedBox(height: 12),
          _VoteControls(
            score: answer.upvoteCount - answer.downvoteCount,
            viewerVote: answer.viewerVote,
            small: true,
          ),
        ],
      ),
    );
  }
}

class _VoteControls extends StatelessWidget {
  final int score;
  final bool? viewerVote;
  final bool small;
  const _VoteControls({
    required this.score,
    this.viewerVote,
    this.small = false,
  });

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(
          Icons.arrow_upward_rounded,
          size: small ? 16 : 20,
          color: viewerVote == true
              ? AppColors.postbookPrimary
              : Colors.white24,
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
        Icon(
          Icons.arrow_downward_rounded,
          size: small ? 16 : 20,
          color: viewerVote == false ? Colors.blueAccent : Colors.white24,
        ),
      ],
    );
  }
}
