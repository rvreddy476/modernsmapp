import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/repositories/qa_repository.dart';
import 'package:atpost_app/providers/communities_provider.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class AskQuestionScreen extends ConsumerStatefulWidget {
  final String? draftId;
  final String? initialTitle;
  final String? initialBody;
  final String? initialCommunityId;
  final List<String>? initialTopics;
  final bool initialIsAnonymous;

  const AskQuestionScreen({
    super.key,
    this.draftId,
    this.initialTitle,
    this.initialBody,
    this.initialCommunityId,
    this.initialTopics,
    this.initialIsAnonymous = false,
  });

  @override
  ConsumerState<AskQuestionScreen> createState() => _AskQuestionScreenState();
}

class _AskQuestionScreenState extends ConsumerState<AskQuestionScreen> {
  final _formKey = GlobalKey<FormState>();
  late final TextEditingController _titleController;
  late final TextEditingController _bodyController;
  late final TextEditingController _topicsController;
  bool _submitting = false;
  bool _isAnonymous = false;
  String? _communityId;

  // Server-backed draft state.
  String? _draftId;
  Timer? _draftDebounce;
  bool _savingDraft = false;
  DateTime? _lastSaved;

  @override
  void initState() {
    super.initState();
    _titleController = TextEditingController(text: widget.initialTitle ?? '');
    _bodyController = TextEditingController(text: widget.initialBody ?? '');
    _topicsController = TextEditingController(
      text: (widget.initialTopics ?? const []).join(', '),
    );
    _isAnonymous = widget.initialIsAnonymous;
    _communityId = widget.initialCommunityId;
    _draftId = widget.draftId;

    _titleController.addListener(_scheduleDraft);
    _bodyController.addListener(_scheduleDraft);
    _topicsController.addListener(_scheduleDraft);
  }

  @override
  void dispose() {
    _draftDebounce?.cancel();
    _titleController.dispose();
    _bodyController.dispose();
    _topicsController.dispose();
    super.dispose();
  }

  void _scheduleDraft() {
    _draftDebounce?.cancel();
    _draftDebounce = Timer(const Duration(milliseconds: 1500), _saveDraft);
  }

  Future<void> _saveDraft() async {
    if (_savingDraft) return;
    final title = _titleController.text.trim();
    final body = _bodyController.text.trim();
    if (title.isEmpty && body.isEmpty) return;
    _savingDraft = true;
    if (mounted) setState(() {});
    try {
      final draft = await ref.read(qaRepositoryProvider).upsertQuestionDraft({
        if (_draftId != null) 'id': _draftId,
        if (_communityId != null) 'community_id': _communityId,
        'title': title,
        'body': body,
        'tags': _parseTopics(_topicsController.text),
        'is_anonymous': _isAnonymous,
      });
      _draftId = draft.id;
      _lastSaved = DateTime.now();
    } catch (_) {
      // Drafts are best-effort.
    } finally {
      _savingDraft = false;
      if (mounted) setState(() {});
    }
  }

  Future<void> _submit() async {
    if (_submitting || !(_formKey.currentState?.validate() ?? false)) return;

    setState(() => _submitting = true);
    try {
      final question = await ref.read(qaRepositoryProvider).createQuestion(
            title: _titleController.text.trim(),
            body: _bodyController.text.trim(),
            topics: _parseTopics(_topicsController.text),
            communityId: _communityId,
            isAnonymous: _isAnonymous,
          );

      if (_draftId != null) {
        try {
          await ref
              .read(qaRepositoryProvider)
              .deleteQuestionDraft(_draftId!);
        } catch (_) {}
      }

      ref.invalidate(qaFeedProvider);
      if (!mounted) return;
      context.go('/qa/question/${question.id}');
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not post your question.')),
      );
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  List<String> _parseTopics(String value) {
    return value
        .split(',')
        .map((topic) => topic.replaceFirst('#', '').trim().toLowerCase())
        .where((topic) => topic.isNotEmpty)
        .toSet()
        .take(5)
        .toList();
  }

  @override
  Widget build(BuildContext context) {
    final myCommunitiesAsync = ref.watch(myCommunitiesProvider);

    final bool anonymityEnabled;
    if (_communityId == null) {
      anonymityEnabled = true;
    } else {
      final qaSettingsAsync =
          ref.watch(qaCommunitySettingsProvider(_communityId!));
      anonymityEnabled = qaSettingsAsync.maybeWhen(
        data: (settings) => settings.anonymityEnabled,
        orElse: () => false,
      );
    }

    if (_communityId != null && !anonymityEnabled && _isAnonymous) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) setState(() => _isAnonymous = false);
      });
    }

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
              Padding(
                padding: const EdgeInsets.all(16),
                child: Row(
                  children: [
                    GlassIconButton(
                      icon: Icons.arrow_back_ios_new,
                      tooltip: 'Back',
                      onPressed: () => context.pop(),
                    ),
                    const SizedBox(width: 12),
                    Text('Ask Question', style: AppTextStyles.h1),
                    const Spacer(),
                    TextButton.icon(
                      onPressed: () => context.push('/qa/drafts'),
                      icon: const Icon(Icons.drafts_outlined,
                          size: 18, color: AppColors.textSecondary),
                      label: Text(
                        'Drafts',
                        style: AppTextStyles.label.copyWith(
                          color: AppColors.textSecondary,
                        ),
                      ),
                    ),
                    TextButton(
                      onPressed: _submitting ? null : _submit,
                      child: _submitting
                          ? const SizedBox(
                              width: 18,
                              height: 18,
                              child: CircularProgressIndicator(
                                  strokeWidth: 2),
                            )
                          : Text(
                              'Post',
                              style: AppTextStyles.label.copyWith(
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                    ),
                  ],
                ),
              ),
              Expanded(
                child: Form(
                  key: _formKey,
                  child: ListView(
                    padding: const EdgeInsets.fromLTRB(16, 6, 16, 32),
                    children: [
                      myCommunitiesAsync.when(
                        loading: () => const SizedBox.shrink(),
                        error: (_, _) => const SizedBox.shrink(),
                        data: (communities) =>
                            _buildCommunityDropdown(communities),
                      ),
                      const SizedBox(height: 14),
                      _QuestionField(
                        controller: _titleController,
                        label: 'Title',
                        hint: 'What do you want to know?',
                        maxLength: 180,
                        validator: (value) {
                          final text = value?.trim() ?? '';
                          if (text.length < 12) {
                            return 'Use at least 12 characters.';
                          }
                          return null;
                        },
                      ),
                      const SizedBox(height: 14),
                      _QuestionField(
                        controller: _bodyController,
                        label: 'Details',
                        hint:
                            'Add context, what you tried, and what answer would help.',
                        minLines: 7,
                        maxLines: 12,
                        maxLength: 4000,
                        validator: (value) {
                          final text = value?.trim() ?? '';
                          if (text.length < 24) {
                            return 'Add a little more detail.';
                          }
                          return null;
                        },
                      ),
                      const SizedBox(height: 14),
                      _QuestionField(
                        controller: _topicsController,
                        label: 'Topics',
                        hint: 'flutter, startups, design',
                        maxLength: 120,
                      ),
                      const SizedBox(height: 6),
                      Text(
                        'Use up to five comma-separated topics.',
                        style: AppTextStyles.labelSmall.copyWith(
                          color: AppColors.textMuted,
                        ),
                      ),
                      const SizedBox(height: 12),
                      SwitchListTile.adaptive(
                        contentPadding: EdgeInsets.zero,
                        value: _isAnonymous,
                        onChanged: anonymityEnabled
                            ? (val) => setState(() => _isAnonymous = val)
                            : null,
                        title: Text(
                          'Post anonymously',
                          style: AppTextStyles.label,
                        ),
                        subtitle: Text(
                          anonymityEnabled
                              ? 'Your name and avatar will be hidden.'
                              : 'This community does not allow anonymous posts.',
                          style: AppTextStyles.labelSmall.copyWith(
                            color: AppColors.textMuted,
                          ),
                        ),
                      ),
                      const SizedBox(height: 16),
                      ElevatedButton.icon(
                        onPressed: _submitting ? null : _submit,
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.postbookPrimary,
                          foregroundColor: Colors.white,
                          padding: const EdgeInsets.symmetric(vertical: 14),
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(16),
                          ),
                        ),
                        icon: const Icon(Icons.send_rounded, size: 18),
                        label: const Text('Post question'),
                      ),
                      const SizedBox(height: 12),
                      _DraftFooter(
                        saving: _savingDraft,
                        lastSaved: _lastSaved,
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildCommunityDropdown(List<Community> communities) {
    final items = <DropdownMenuItem<String?>>[
      const DropdownMenuItem<String?>(
        value: null,
        child: Text('Public Q&A (no community)'),
      ),
      ...communities.map(
        (c) => DropdownMenuItem<String?>(
          value: c.id,
          child: Text(c.name),
        ),
      ),
    ];

    return DropdownButtonFormField<String?>(
      initialValue: _communityId,
      decoration: InputDecoration(
        labelText: 'Community',
        labelStyle: AppTextStyles.label.copyWith(
          color: AppColors.textSecondary,
        ),
        filled: true,
        fillColor: Colors.white.withValues(alpha: 0.04),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: AppColors.borderSubtle),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: AppColors.postbookPrimary),
        ),
      ),
      dropdownColor: AppColors.bgCard,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      items: items,
      onChanged: (val) => setState(() => _communityId = val),
    );
  }
}

class _DraftFooter extends StatelessWidget {
  final bool saving;
  final DateTime? lastSaved;
  const _DraftFooter({required this.saving, this.lastSaved});

  @override
  Widget build(BuildContext context) {
    String text;
    if (saving) {
      text = 'Saving draft...';
    } else if (lastSaved != null) {
      text = 'Draft saved';
    } else {
      text = 'Drafts auto-save as you type.';
    }
    return Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Icon(
          saving
              ? Icons.cloud_upload_outlined
              : Icons.cloud_done_outlined,
          size: 14,
          color: AppColors.textMuted,
        ),
        const SizedBox(width: 6),
        Text(
          text,
          style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
        ),
      ],
    );
  }
}

class _QuestionField extends StatelessWidget {
  const _QuestionField({
    required this.controller,
    required this.label,
    required this.hint,
    this.minLines = 1,
    this.maxLines = 1,
    this.maxLength,
    this.validator,
  });

  final TextEditingController controller;
  final String label;
  final String hint;
  final int minLines;
  final int maxLines;
  final int? maxLength;
  final FormFieldValidator<String>? validator;

  @override
  Widget build(BuildContext context) {
    return TextFormField(
      controller: controller,
      minLines: minLines,
      maxLines: maxLines,
      maxLength: maxLength,
      validator: validator,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        labelText: label,
        hintText: hint,
        filled: true,
        fillColor: Colors.white.withValues(alpha: 0.04),
        labelStyle: AppTextStyles.label.copyWith(
          color: AppColors.textSecondary,
        ),
        hintStyle: AppTextStyles.bodySmall.copyWith(color: AppColors.textDim),
        errorMaxLines: 2,
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: AppColors.borderSubtle),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: AppColors.postbookPrimary),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: AppColors.statusError),
        ),
        focusedErrorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: AppColors.statusError),
        ),
      ),
    );
  }
}
