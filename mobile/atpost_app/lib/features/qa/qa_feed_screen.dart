import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/qa/widgets/question_card.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class QAFeedScreen extends ConsumerStatefulWidget {
  const QAFeedScreen({super.key});

  @override
  ConsumerState<QAFeedScreen> createState() => _QAFeedScreenState();
}

class _QAFeedScreenState extends ConsumerState<QAFeedScreen> {
  final List<String> _sortOptions = ['trending', 'new', 'top'];
  String _currentSort = 'trending';

  @override
  Widget build(BuildContext context) {
    final qaState = ref.watch(qaFeedProvider);

    return Scaffold(
      backgroundColor: Colors.black,
      body: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [Color(0xFF0F111A), Color(0xFF090A11)],
          ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              _buildHeader(context),
              _buildSortBar(),
              Expanded(
                child: RefreshIndicator(
                  onRefresh: () => ref
                      .read(qaFeedProvider.notifier)
                      .refresh(sort: _currentSort),
                  color: AppColors.postbookPrimary,
                  child: qaState.when(
                    loading: () => const Center(
                      child: CircularProgressIndicator(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                    error: (e, _) => _buildErrorState(),
                    data: (state) => _buildQuestionList(state),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: () => context.push('/qa/ask'),
        backgroundColor: AppColors.postbookPrimary,
        child: const Icon(Icons.add_comment_outlined, color: Colors.white),
      ),
    );
  }

  Widget _buildHeader(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back_ios_new,
            tooltip: 'Back',
            onPressed: () => context.pop(),
          ),
          const SizedBox(width: 12),
          Text('Questions', style: AppTextStyles.h1),
          const Spacer(),
          GlassIconButton(
            icon: Icons.search,
            tooltip: 'Search',
            onPressed: () {},
          ),
        ],
      ),
    );
  }

  Widget _buildSortBar() {
    return Container(
      height: 40,
      margin: const EdgeInsets.only(bottom: 12),
      child: ListView.builder(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        itemCount: _sortOptions.length,
        itemBuilder: (context, index) {
          final option = _sortOptions[index];
          final isSelected = _currentSort == option;
          return Padding(
            padding: const EdgeInsets.only(right: 8),
            child: ChoiceChip(
              label: Text(option.toUpperCase()),
              selected: isSelected,
              onSelected: (val) {
                if (val) {
                  setState(() => _currentSort = option);
                  ref.read(qaFeedProvider.notifier).refresh(sort: option);
                }
              },
              selectedColor: AppColors.postbookPrimary.withOpacity(0.2),
              backgroundColor: Colors.white.withOpacity(0.03),
              labelStyle: TextStyle(
                color: isSelected ? AppColors.postbookPrimary : Colors.white38,
                fontWeight: isSelected ? FontWeight.bold : FontWeight.normal,
                fontSize: 12,
              ),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(20),
              ),
              side: BorderSide(
                color: isSelected ? AppColors.postbookPrimary : Colors.white10,
              ),
              showCheckmark: false,
            ),
          );
        },
      ),
    );
  }

  Widget _buildQuestionList(QAFeedState state) {
    if (state.questions.isEmpty) {
      return Center(
        child: Text(
          'No questions found',
          style: AppTextStyles.bodySmall.copyWith(color: Colors.white24),
        ),
      );
    }

    return ListView.builder(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      itemCount: state.questions.length + (state.isLoadingMore ? 1 : 0),
      itemBuilder: (context, index) {
        if (index == state.questions.length) {
          return const Padding(
            padding: EdgeInsets.symmetric(vertical: 20),
            child: Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
                strokeWidth: 2,
              ),
            ),
          );
        }

        final question = state.questions[index];
        // Pre-fetch check
        if (index == state.questions.length - 3) {
          ref.read(qaFeedProvider.notifier).loadMore();
        }

        return QuestionCard(
          question: question,
          onTap: () => context.push('/qa/question/${question.id}'),
        );
      },
    );
  }

  Widget _buildErrorState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, color: Colors.redAccent, size: 40),
          const SizedBox(height: 16),
          Text('Failed to load questions', style: AppTextStyles.body),
          TextButton(
            onPressed: () =>
                ref.read(qaFeedProvider.notifier).refresh(sort: _currentSort),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}
