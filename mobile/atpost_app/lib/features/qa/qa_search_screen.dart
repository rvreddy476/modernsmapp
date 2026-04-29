import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/qa/widgets/question_card.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class QaSearchScreen extends ConsumerStatefulWidget {
  final String? initialQuery;
  final String? communityId;
  final String? topicId;

  const QaSearchScreen({
    super.key,
    this.initialQuery,
    this.communityId,
    this.topicId,
  });

  @override
  ConsumerState<QaSearchScreen> createState() => _QaSearchScreenState();
}

class _QaSearchScreenState extends ConsumerState<QaSearchScreen> {
  late final TextEditingController _controller;
  Timer? _debounce;
  String _activeQuery = '';

  @override
  void initState() {
    super.initState();
    _controller = TextEditingController(text: widget.initialQuery ?? '');
    _activeQuery = widget.initialQuery ?? '';
    _controller.addListener(_onChanged);
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _controller.removeListener(_onChanged);
    _controller.dispose();
    super.dispose();
  }

  void _onChanged() {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 350), () {
      if (mounted) {
        setState(() => _activeQuery = _controller.text.trim());
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final params = QaSearchParams(
      query: _activeQuery,
      communityId: widget.communityId,
      topicId: widget.topicId,
    );
    final resultsAsync = ref.watch(qaSearchProvider(params));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.all(12),
              child: Row(
                children: [
                  GlassIconButton(
                    icon: Icons.arrow_back_ios_new,
                    tooltip: 'Back',
                    onPressed: () => context.pop(),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: _controller,
                      autofocus: widget.initialQuery == null ||
                          widget.initialQuery!.isEmpty,
                      style: AppTextStyles.body,
                      decoration: InputDecoration(
                        hintText: 'Search questions...',
                        prefixIcon:
                            const Icon(Icons.search, color: AppColors.textDim),
                        suffixIcon: _controller.text.isEmpty
                            ? null
                            : IconButton(
                                icon: const Icon(Icons.close,
                                    color: AppColors.textDim),
                                onPressed: () {
                                  _controller.clear();
                                  setState(() => _activeQuery = '');
                                },
                              ),
                        filled: true,
                        fillColor: AppColors.bgCard,
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(20),
                          borderSide: BorderSide.none,
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
            Expanded(
              child: _activeQuery.isEmpty
                  ? Center(
                      child: Text(
                        'Search questions across Q&A',
                        style: AppTextStyles.body
                            .copyWith(color: AppColors.textDim),
                      ),
                    )
                  : resultsAsync.when(
                      loading: () => const Center(
                        child: CircularProgressIndicator(
                            color: AppColors.postbookPrimary),
                      ),
                      error: (_, _) => Center(
                        child: Text(
                          'Search failed.',
                          style: AppTextStyles.body
                              .copyWith(color: AppColors.textDim),
                        ),
                      ),
                      data: (questions) {
                        if (questions.isEmpty) {
                          return Center(
                            child: Text(
                              'No matches.',
                              style: AppTextStyles.body
                                  .copyWith(color: AppColors.textDim),
                            ),
                          );
                        }
                        return ListView.builder(
                          padding: const EdgeInsets.all(16),
                          itemCount: questions.length,
                          itemBuilder: (context, index) {
                            final q = questions[index];
                            return QuestionCard(
                              question: q,
                              onTap: () =>
                                  context.push('/qa/question/${q.id}'),
                            );
                          },
                        );
                      },
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
