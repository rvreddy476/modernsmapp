import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/slambook.dart';
import 'package:atpost_app/data/repositories/memories_repository.dart';
import 'package:atpost_app/features/memories/slambook_data.dart';
import 'package:atpost_app/features/memories/slambook_detail_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SlambookComposeScreen extends ConsumerStatefulWidget {
  const SlambookComposeScreen({super.key});

  @override
  ConsumerState<SlambookComposeScreen> createState() => _SlambookComposeScreenState();
}

class _SlambookComposeScreenState extends ConsumerState<SlambookComposeScreen> {
  final _titleController = TextEditingController();
  final _subtitleController = TextEditingController();
  final _descriptionController = TextEditingController();
  final List<_CustomPromptDraft> _customPrompts = <_CustomPromptDraft>[];

  String? _selectedPackKey;
  String _visibility = 'invited_only';
  String _identityMode = 'named';
  bool _approvalRequired = false;
  bool _submitting = false;

  @override
  void dispose() {
    _titleController.dispose();
    _subtitleController.dispose();
    _descriptionController.dispose();
    for (final prompt in _customPrompts) {
      prompt.dispose();
    }
    super.dispose();
  }

  void _addCustomPrompt() {
    setState(() {
      _customPrompts.add(_CustomPromptDraft());
    });
  }

  void _removeCustomPrompt(_CustomPromptDraft prompt) {
    setState(() {
      _customPrompts.remove(prompt);
      prompt.dispose();
    });
  }

  Future<void> _create() async {
    final title = _titleController.text.trim();
    final customCards = _customPrompts
        .map((prompt) => prompt.toDraft())
        .whereType<SlambookCardDraft>()
        .toList();
    if (title.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Please enter a title.')));
      return;
    }
    if ((_selectedPackKey == null || _selectedPackKey!.isEmpty) &&
        customCards.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Pick a template pack or add at least one custom prompt.'),
        ),
      );
      return;
    }

    setState(() => _submitting = true);
    try {
      final slambook = await ref.read(memoriesRepositoryProvider).createSlambook(
            title: title,
            subtitle: _subtitleController.text.trim(),
            description: _descriptionController.text.trim(),
            visibility: _visibility,
            responseIdentityMode: _identityMode,
            approvalRequired: _approvalRequired,
            templatePackKey: _selectedPackKey ?? '',
            customCards: customCards,
          );
      ref.invalidate(mySlambooksProvider);
      if (!mounted) return;
      Navigator.of(context).pushReplacement(
        MaterialPageRoute<void>(
          builder: (_) => SlambookDetailScreen(slambookId: slambook.id),
        ),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not create the SlamBook.')),
      );
    } finally {
      if (mounted) {
        setState(() => _submitting = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final packsAsync = ref.watch(slambookTemplatePacksProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: const Text('New SlamBook'),
      ),
      body: ListView(
        padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 32),
        children: [
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Basics', style: AppTextStyles.h2),
                const SizedBox(height: 12),
                TextField(
                  controller: _titleController,
                  maxLength: 80,
                  decoration: const InputDecoration(
                    labelText: 'Title',
                    hintText: 'Aurora 11A, Birthday Notes, Night Shift...',
                  ),
                ),
                const SizedBox(height: 8),
                TextField(
                  controller: _subtitleController,
                  maxLength: 140,
                  decoration: const InputDecoration(
                    labelText: 'Subtitle',
                    hintText: 'A short line to set the tone',
                  ),
                ),
                const SizedBox(height: 8),
                TextField(
                  controller: _descriptionController,
                  minLines: 3,
                  maxLines: 4,
                  maxLength: 240,
                  decoration: const InputDecoration(
                    labelText: 'Description',
                    hintText: 'Tell people what kind of responses you want.',
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 18),
          Text('Template pack', style: AppTextStyles.h2),
          const SizedBox(height: 10),
          packsAsync.when(
            data: (packs) {
              if (packs.isEmpty) {
                return const _InlineStateCard(
                  icon: Icons.style_outlined,
                  message: 'No template packs available yet.',
                );
              }
              return Wrap(
                spacing: 10,
                runSpacing: 10,
                children: packs.map((pack) {
                  final selected = _selectedPackKey == pack.key;
                  final accent = slambookAccentColor(pack.key);
                  return Material(
                    color: Colors.transparent,
                    child: InkWell(
                      onTap: () {
                        setState(() {
                          _selectedPackKey = selected ? null : pack.key;
                        });
                      },
                      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                      child: Container(
                        width: 210,
                        padding: const EdgeInsets.all(14),
                        decoration: BoxDecoration(
                          color: AppColors.bgCard,
                          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                          border: Border.all(
                            color: selected ? accent : AppColors.borderSubtle,
                            width: selected ? 1.4 : 1,
                          ),
                        ),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(pack.title, style: AppTextStyles.h3),
                            const SizedBox(height: 4),
                            Text(
                              pack.description ?? 'Template pack',
                              maxLines: 3,
                              overflow: TextOverflow.ellipsis,
                              style: AppTextStyles.bodySmall,
                            ),
                            const SizedBox(height: 8),
                            Text(
                              '${pack.templates.length} prompts',
                              style: AppTextStyles.labelSmall.copyWith(color: accent),
                            ),
                          ],
                        ),
                      ),
                    ),
                  );
                }).toList(),
              );
            },
            loading: () => const Center(
              child: Padding(
                padding: EdgeInsets.symmetric(vertical: 20),
                child: CircularProgressIndicator(color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => const _InlineStateCard(
              icon: Icons.style_outlined,
              message: 'Could not load the template packs.',
            ),
          ),
          const SizedBox(height: 18),
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Response settings', style: AppTextStyles.h2),
                const SizedBox(height: 12),
                Text('Visibility', style: AppTextStyles.label),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  children: ['invited_only', 'public', 'private'].map((value) {
                    final selected = _visibility == value;
                    return ChoiceChip(
                      label: Text(value.replaceAll('_', ' ')),
                      selected: selected,
                      onSelected: (_) => setState(() => _visibility = value),
                    );
                  }).toList(),
                ),
                const SizedBox(height: 12),
                Text('Identity', style: AppTextStyles.label),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    'named',
                    'anonymous_allowed',
                    'fully_anonymous',
                  ].map((value) {
                    final selected = _identityMode == value;
                    return ChoiceChip(
                      label: Text(value.replaceAll('_', ' ')),
                      selected: selected,
                      onSelected: (_) => setState(() {
                        _identityMode = value;
                        if (value != 'named') {
                          _approvalRequired = true;
                        }
                      }),
                    );
                  }).toList(),
                ),
                const SizedBox(height: 12),
                SwitchListTile.adaptive(
                  value: _approvalRequired,
                  contentPadding: EdgeInsets.zero,
                  title: const Text('Require approval before posting'),
                  subtitle: const Text('Anonymous modes force this on the backend.'),
                  onChanged: (value) {
                    setState(() => _approvalRequired = value);
                  },
                ),
              ],
            ),
          ),
          const SizedBox(height: 18),
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(child: Text('Custom prompts', style: AppTextStyles.h2)),
                    TextButton.icon(
                      onPressed: _addCustomPrompt,
                      icon: const Icon(Icons.add),
                      label: const Text('Add'),
                    ),
                  ],
                ),
              if (_customPrompts.isEmpty)
                  const Text(
                    'Optional for v1. Use this when you want to add a text-only custom question.',
                  ),
                const SizedBox(height: 12),
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppColors.bgSecondary,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                  child: Text(
                    'Create the SlamBook first, then send share links or direct user-ID invites from the detail screen.',
                    style: AppTextStyles.bodySmall,
                  ),
                ),
                ..._customPrompts.map(
                  (prompt) => Padding(
                    padding: const EdgeInsets.only(top: 10),
                    child: _CustomPromptEditor(
                      draft: prompt,
                      onRemove: () => _removeCustomPrompt(prompt),
                    ),
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 18),
          SizedBox(
            width: double.infinity,
            child: ElevatedButton.icon(
              onPressed: _submitting ? null : _create,
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                foregroundColor: Colors.white,
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              icon: _submitting
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                    )
                  : const Icon(Icons.auto_stories_outlined),
              label: const Text('Create SlamBook'),
            ),
          ),
        ],
      ),
    );
  }
}

class _CustomPromptEditor extends StatelessWidget {
  const _CustomPromptEditor({
    required this.draft,
    required this.onRemove,
  });

  final _CustomPromptDraft draft;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Column(
        children: [
          TextField(
            controller: draft.titleController,
            decoration: const InputDecoration(
              labelText: 'Prompt title',
              hintText: 'Nickname, Best memory, Secret habit...',
            ),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: draft.promptController,
            decoration: const InputDecoration(
              labelText: 'Prompt text',
              hintText: 'What do you want people to answer?',
            ),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: DropdownButtonFormField<String>(
                  initialValue: draft.responseType,
                  items: const [
                    DropdownMenuItem(value: 'text', child: Text('Short text')),
                    DropdownMenuItem(value: 'long_text', child: Text('Long text')),
                  ],
                  onChanged: (value) {
                    if (value != null) {
                      draft.responseType = value;
                    }
                  },
                  decoration: const InputDecoration(labelText: 'Response type'),
                ),
              ),
              const SizedBox(width: 10),
              IconButton(
                onPressed: onRemove,
                icon: const Icon(Icons.delete_outline),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _CustomPromptDraft {
  final TextEditingController titleController = TextEditingController();
  final TextEditingController promptController = TextEditingController();
  String responseType = 'text';

  SlambookCardDraft? toDraft() {
    final prompt = promptController.text.trim();
    if (prompt.isEmpty) {
      return null;
    }
    final title = titleController.text.trim();
    return SlambookCardDraft(
      title: title,
      prompt: prompt,
      responseType: responseType,
      isRequired: false,
    );
  }

  void dispose() {
    titleController.dispose();
    promptController.dispose();
  }
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({
    required this.icon,
    required this.message,
  });

  final IconData icon;
  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(18),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textSecondary),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
        ],
      ),
    );
  }
}
