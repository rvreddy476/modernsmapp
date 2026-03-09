import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/groups_repository.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CreateGroupScreen extends ConsumerStatefulWidget {
  const CreateGroupScreen({super.key});

  @override
  ConsumerState<CreateGroupScreen> createState() => _CreateGroupScreenState();
}

class _CreateGroupScreenState extends ConsumerState<CreateGroupScreen> {
  final TextEditingController _nameController = TextEditingController();
  final TextEditingController _descriptionController = TextEditingController();

  String _privacy = 'public';
  bool _submitting = false;

  @override
  void dispose() {
    _nameController.dispose();
    _descriptionController.dispose();
    super.dispose();
  }

  Future<void> _createGroup() async {
    if (_submitting) return;

    final name = _nameController.text.trim();
    final description = _descriptionController.text.trim();

    if (name.length < 3) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Group name must be at least 3 characters.'),
        ),
      );
      return;
    }
    if (description.length < 10) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Please add a longer group description.')),
      );
      return;
    }

    setState(() => _submitting = true);
    try {
      final group = await ref
          .read(groupsRepositoryProvider)
          .createGroup(name: name, description: description, privacy: _privacy);

      ref.invalidate(myGroupsProvider);
      ref.invalidate(discoverGroupsProvider);

      if (!mounted) return;
      context.go('/groups/${group.id}');
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not create group. Please retry.')),
      );
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(
            Icons.arrow_back_rounded,
            color: AppColors.textPrimary,
          ),
        ),
        title: Text('Create Group', style: AppTextStyles.h2),
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: AppSpacing.pagePadding.copyWith(bottom: 24),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  gradient: const LinearGradient(
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                    colors: [Color(0x33FF6B35), Color(0x334ECDC4)],
                  ),
                  border: Border.all(color: AppColors.borderMedium),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Build a Community', style: AppTextStyles.h1),
                    const SizedBox(height: 6),
                    Text(
                      'Create your own space to share posts, host conversations, and grow your audience.',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 18),
              Text('Group Name', style: AppTextStyles.label),
              const SizedBox(height: 6),
              TextField(
                controller: _nameController,
                maxLength: 60,
                textInputAction: TextInputAction.next,
                decoration: InputDecoration(
                  hintText: 'Example: Postbook Creators Hub',
                  hintStyle: AppTextStyles.bodySmall,
                ),
              ),
              const SizedBox(height: 10),
              Text('Description', style: AppTextStyles.label),
              const SizedBox(height: 6),
              TextField(
                controller: _descriptionController,
                maxLength: 240,
                minLines: 3,
                maxLines: 5,
                decoration: InputDecoration(
                  hintText: 'What is this group about?',
                  hintStyle: AppTextStyles.bodySmall,
                ),
              ),
              const SizedBox(height: 10),
              Text('Privacy', style: AppTextStyles.label),
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  _PrivacyOptionCard(
                    title: 'Public',
                    subtitle: 'Anyone can discover and join',
                    selected: _privacy == 'public',
                    onTap: () => setState(() => _privacy = 'public'),
                  ),
                  _PrivacyOptionCard(
                    title: 'Private',
                    subtitle: 'Visible, but requires approval',
                    selected: _privacy == 'private',
                    onTap: () => setState(() => _privacy = 'private'),
                  ),
                  _PrivacyOptionCard(
                    title: 'Secret',
                    subtitle: 'Invite only',
                    selected: _privacy == 'secret',
                    onTap: () => setState(() => _privacy = 'secret'),
                  ),
                ],
              ),
              const SizedBox(height: 18),
              SizedBox(
                width: double.infinity,
                child: ElevatedButton.icon(
                  onPressed: _submitting ? null : _createGroup,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                    foregroundColor: Colors.white,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                    padding: const EdgeInsets.symmetric(vertical: 14),
                  ),
                  icon: _submitting
                      ? const SizedBox(
                          width: 14,
                          height: 14,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : const Icon(Icons.group_add_rounded),
                  label: const Text('Create Group'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _PrivacyOptionCard extends StatelessWidget {
  const _PrivacyOptionCard({
    required this.title,
    required this.subtitle,
    required this.selected,
    required this.onTap,
  });

  final String title;
  final String subtitle;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 220,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(12),
          child: Ink(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: selected
                  ? AppColors.postbookPrimary.withValues(alpha: 0.16)
                  : AppColors.bgCard,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(
                color: selected
                    ? AppColors.postbookPrimary
                    : AppColors.borderSubtle,
              ),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text(title, style: AppTextStyles.label),
                    const Spacer(),
                    Icon(
                      selected
                          ? Icons.radio_button_checked
                          : Icons.radio_button_unchecked,
                      color: selected
                          ? AppColors.postbookPrimary
                          : AppColors.textMuted,
                      size: 18,
                    ),
                  ],
                ),
                const SizedBox(height: 4),
                Text(subtitle, style: AppTextStyles.labelSmall),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
