import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/communities_repository.dart';
import 'package:atpost_app/providers/communities_provider.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CreateCommunityScreen extends ConsumerStatefulWidget {
  const CreateCommunityScreen({super.key});

  @override
  ConsumerState<CreateCommunityScreen> createState() =>
      _CreateCommunityScreenState();
}

class _CreateCommunityScreenState extends ConsumerState<CreateCommunityScreen> {
  final _formKey = GlobalKey<FormState>();
  final _nameCtrl = TextEditingController();
  final _handleCtrl = TextEditingController();
  final _descCtrl = TextEditingController();
  final _topicCtrl = TextEditingController();
  String _communityType = 'public';
  bool _creating = false;
  final Set<String> _topicTags = <String>{};

  static const _communityTypes = [
    'public',
    'private',
    'invite',
    'education',
    'local',
    'professional',
    'fan',
    'brand',
  ];

  @override
  void dispose() {
    _nameCtrl.dispose();
    _handleCtrl.dispose();
    _descCtrl.dispose();
    _topicCtrl.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate() || _creating) return;
    setState(() => _creating = true);
    try {
      final repo = ref.read(communitiesRepositoryProvider);
      await repo.createCommunity(
        name: _nameCtrl.text.trim(),
        handle: _handleCtrl.text.trim(),
        communityType: _communityType,
        description: _descCtrl.text.trim(),
        topicTags: _topicTags.toList(),
      );
      ref.invalidate(communitiesProvider);
      if (mounted) context.pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to create community: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _creating = false);
    }
  }

  void _addTopicTag(String rawValue) {
    final normalized = rawValue
        .trim()
        .replaceFirst(RegExp(r'^#+'), '')
        .toLowerCase();
    if (normalized.isEmpty) return;
    setState(() => _topicTags.add(normalized));
    _topicCtrl.clear();
  }

  void _removeTopicTag(String tag) {
    setState(() => _topicTags.remove(tag));
  }

  @override
  Widget build(BuildContext context) {
    final topicsAsync = ref.watch(qaTopicsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Create Community', style: AppTextStyles.h2),
      ),
      body: Form(
        key: _formKey,
        child: ListView(
          padding: AppSpacing.pagePadding.copyWith(top: 20, bottom: 100),
          children: [
            // Name
            Text('Community Name', style: AppTextStyles.label),
            const SizedBox(height: 6),
            TextFormField(
              controller: _nameCtrl,
              style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
              decoration: _inputDecoration('e.g. Flutter Developers'),
              validator: (v) =>
                  (v == null || v.trim().isEmpty) ? 'Name is required' : null,
            ),

            const SizedBox(height: 18),

            // Handle
            Text('Handle', style: AppTextStyles.label),
            const SizedBox(height: 6),
            TextFormField(
              controller: _handleCtrl,
              style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
              decoration: _inputDecoration('@flutter-devs'),
              validator: (v) =>
                  (v == null || v.trim().isEmpty) ? 'Handle is required' : null,
            ),

            const SizedBox(height: 18),

            // Type dropdown
            Text('Community Type', style: AppTextStyles.label),
            const SizedBox(height: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: DropdownButtonHideUnderline(
                child: DropdownButton<String>(
                  value: _communityType,
                  isExpanded: true,
                  dropdownColor: AppColors.bgSecondary,
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  items: _communityTypes
                      .map(
                        (t) => DropdownMenuItem(
                          value: t,
                          child: Text(t[0].toUpperCase() + t.substring(1)),
                        ),
                      )
                      .toList(),
                  onChanged: (v) {
                    if (v != null) setState(() => _communityType = v);
                  },
                ),
              ),
            ),

            const SizedBox(height: 18),

            // Description
            Text('Description', style: AppTextStyles.label),
            const SizedBox(height: 6),
            TextFormField(
              controller: _descCtrl,
              style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
              maxLines: 4,
              decoration: _inputDecoration('What is this community about?'),
            ),

            const SizedBox(height: 18),

            Text('Topic Tags', style: AppTextStyles.label),
            const SizedBox(height: 6),
            TextFormField(
              controller: _topicCtrl,
              style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
              onFieldSubmitted: _addTopicTag,
              decoration: _inputDecoration('Add #flutter, #ai, #design')
                  .copyWith(
                    suffixIcon: IconButton(
                      onPressed: () => _addTopicTag(_topicCtrl.text),
                      icon: const Icon(
                        Icons.add_rounded,
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  ),
            ),
            const SizedBox(height: 10),
            Text(
              'Communities are scoped under topics. Add a few tags so they can be discovered from topic pages.',
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textSecondary,
              ),
            ),
            if (_topicTags.isNotEmpty) ...[
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: _topicTags
                    .map(
                      (tag) => InputChip(
                        label: Text('#$tag'),
                        onDeleted: () => _removeTopicTag(tag),
                        backgroundColor: AppColors.postbookPrimary.withValues(
                          alpha: 0.14,
                        ),
                        side: const BorderSide(color: AppColors.borderSubtle),
                        labelStyle: AppTextStyles.label.copyWith(
                          color: AppColors.postbookPrimary,
                        ),
                        deleteIconColor: AppColors.postbookPrimary,
                      ),
                    )
                    .toList(),
              ),
            ],
            const SizedBox(height: 14),
            topicsAsync.when(
              loading: () => const SizedBox.shrink(),
              error: (_, _) => const SizedBox.shrink(),
              data: (topics) {
                final suggestions = topics
                    .where((topic) => !_topicTags.contains(topic.slug))
                    .take(8)
                    .toList();
                if (suggestions.isEmpty) {
                  return const SizedBox.shrink();
                }
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Suggested topics',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                    const SizedBox(height: 8),
                    Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: suggestions
                          .map(
                            (topic) => ActionChip(
                              onPressed: () => _addTopicTag(topic.slug),
                              label: Text('#${topic.slug}'),
                              backgroundColor: AppColors.bgCard,
                              side: const BorderSide(
                                color: AppColors.borderSubtle,
                              ),
                              labelStyle: AppTextStyles.labelSmall.copyWith(
                                color: AppColors.textSecondary,
                              ),
                            ),
                          )
                          .toList(),
                    ),
                  ],
                );
              },
            ),

            const SizedBox(height: 28),

            // Submit
            SizedBox(
              width: double.infinity,
              child: Container(
                decoration: BoxDecoration(
                  gradient: AppColors.postbookGradient,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                ),
                child: ElevatedButton(
                  onPressed: _creating ? null : _submit,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: Colors.transparent,
                    shadowColor: Colors.transparent,
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusMedium,
                      ),
                    ),
                  ),
                  child: _creating
                      ? const SizedBox(
                          width: 20,
                          height: 20,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : Text(
                          'Create Community',
                          style: AppTextStyles.label.copyWith(
                            color: Colors.white,
                          ),
                        ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  InputDecoration _inputDecoration(String hint) {
    return InputDecoration(
      hintText: hint,
      hintStyle: AppTextStyles.body.copyWith(color: AppColors.textDim),
      filled: true,
      fillColor: AppColors.bgCard,
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        borderSide: const BorderSide(color: AppColors.postbookPrimary),
      ),
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
    );
  }
}
