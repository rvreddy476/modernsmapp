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
  final _nameController = TextEditingController();
  final _descController = TextEditingController();

  String _privacy = 'public';
  bool _isMature = false;
  bool _submitting = false;

  @override
  void dispose() {
    _nameController.dispose();
    _descController.dispose();
    super.dispose();
  }

  Future<void> _create() async {
    if (_submitting) return;

    final name = _nameController.text.trim();
    final description = _descController.text.trim();

    if (name.length < 3) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Name must be at least 3 characters.')),
      );
      return;
    }
    if (description.length < 10) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
            content: Text('Please add a longer description (10+ chars).')),
      );
      return;
    }

    setState(() => _submitting = true);
    try {
      final group = await ref.read(groupsRepositoryProvider).createGroup(
            name: name,
            description: description,
            privacy: _privacy,
            isMature: _isMature,
          );
      ref.invalidate(myGroupsProvider);
      ref.invalidate(discoverGroupsProvider);
      if (!mounted) return;
      context.go('/groups/${group.id}');
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not create space. Please retry.')),
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
        elevation: 0,
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_rounded,
              color: AppColors.textPrimary),
        ),
        title: Text('Create Space', style: AppTextStyles.h2),
      ),
      body: SafeArea(
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Main form
            Expanded(
              child: SingleChildScrollView(
                padding: AppSpacing.pagePadding.copyWith(bottom: 24),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Hero banner
                    Container(
                      width: double.infinity,
                      padding: const EdgeInsets.all(16),
                      decoration: BoxDecoration(
                        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                        gradient: const LinearGradient(
                          begin: Alignment.topLeft,
                          end: Alignment.bottomRight,
                          colors: [
                            Color(0x33FF6B35),
                            Color(0x334ECDC4),
                          ],
                        ),
                        border: Border.all(color: AppColors.borderMedium),
                      ),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text('Build a Space', style: AppTextStyles.h1),
                          const SizedBox(height: 6),
                          Text(
                            'Create your own space to share posts, host conversations, and grow your audience.',
                            style: AppTextStyles.bodySmall,
                          ),
                        ],
                      ),
                    ),

                    const SizedBox(height: 20),

                    // Name
                    Text('Space Name', style: AppTextStyles.label),
                    const SizedBox(height: 6),
                    TextField(
                      controller: _nameController,
                      maxLength: 60,
                      textInputAction: TextInputAction.next,
                      onChanged: (_) => setState(() {}),
                      decoration: InputDecoration(
                        hintText: 'e.g. VChat Creators Hub',
                        hintStyle: AppTextStyles.bodySmall,
                      ),
                    ),

                    const SizedBox(height: 10),

                    // Description
                    Text('Description', style: AppTextStyles.label),
                    const SizedBox(height: 6),
                    TextField(
                      controller: _descController,
                      maxLength: 240,
                      minLines: 3,
                      maxLines: 5,
                      onChanged: (_) => setState(() {}),
                      decoration: InputDecoration(
                        hintText: 'What is this space about?',
                        hintStyle: AppTextStyles.bodySmall,
                      ),
                    ),

                    const SizedBox(height: 14),

                    // Privacy — Reddit-style
                    Text('Privacy', style: AppTextStyles.label),
                    const SizedBox(height: 10),
                    ...[
                      _PrivacyOption(
                        title: 'Public',
                        subtitle: 'Anyone can find and join',
                        icon: Icons.public,
                        value: 'public',
                        selected: _privacy,
                        onTap: (v) => setState(() => _privacy = v),
                      ),
                      const SizedBox(height: 8),
                      _PrivacyOption(
                        title: 'Restricted',
                        subtitle: 'Visible to all; joining requires approval',
                        icon: Icons.shield_outlined,
                        value: 'restricted',
                        selected: _privacy,
                        onTap: (v) => setState(() => _privacy = v),
                      ),
                      const SizedBox(height: 8),
                      _PrivacyOption(
                        title: 'Private',
                        subtitle: 'Invite only, hidden from discovery',
                        icon: Icons.lock_outline,
                        value: 'private',
                        selected: _privacy,
                        onTap: (v) => setState(() => _privacy = v),
                      ),
                    ],

                    const SizedBox(height: 16),

                    // 18+ Mature toggle
                    Container(
                      padding: const EdgeInsets.all(14),
                      decoration: BoxDecoration(
                        color: AppColors.bgCard,
                        borderRadius:
                            BorderRadius.circular(AppSpacing.radiusLarge),
                        border: Border.all(
                          color: _isMature
                              ? AppColors.statusError.withValues(alpha: 0.4)
                              : AppColors.borderSubtle,
                        ),
                      ),
                      child: Row(
                        children: [
                          Container(
                            padding: const EdgeInsets.all(6),
                            decoration: BoxDecoration(
                              color: _isMature
                                  ? AppColors.statusError.withValues(alpha: 0.15)
                                  : AppColors.bgCard,
                              borderRadius: BorderRadius.circular(8),
                            ),
                            child: Icon(
                              Icons.warning_amber_outlined,
                              color: _isMature
                                  ? AppColors.statusError
                                  : AppColors.textMuted,
                              size: 18,
                            ),
                          ),
                          const SizedBox(width: 12),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text('Mature content (18+)',
                                    style: AppTextStyles.label),
                                Text(
                                  'Mark this space as containing adult content',
                                  style: AppTextStyles.labelSmall
                                      .copyWith(color: AppColors.textMuted),
                                ),
                              ],
                            ),
                          ),
                          Switch(
                            value: _isMature,
                            onChanged: (v) => setState(() => _isMature = v),
                            activeColor: AppColors.statusError,
                            trackColor: WidgetStateProperty.resolveWith(
                              (states) => states.contains(WidgetState.selected)
                                  ? AppColors.statusError.withValues(alpha: 0.3)
                                  : AppColors.borderMedium,
                            ),
                          ),
                        ],
                      ),
                    ),

                    const SizedBox(height: 20),

                    // Create button
                    SizedBox(
                      width: double.infinity,
                      child: ElevatedButton.icon(
                        onPressed: _submitting ? null : _create,
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.textPrimary,
                          foregroundColor: AppColors.bgPrimary,
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
                                  color: AppColors.bgPrimary,
                                ),
                              )
                            : const Icon(Icons.group_add_rounded),
                        label: const Text('Create Space'),
                      ),
                    ),
                  ],
                ),
              ),
            ),

            // Live preview panel (≥600dp)
            if (MediaQuery.of(context).size.width >= 600)
              SizedBox(
                width: 240,
                child: _LivePreview(
                  name: _nameController.text,
                  description: _descController.text,
                  privacy: _privacy,
                  isMature: _isMature,
                ),
              ),
          ],
        ),
      ),
    );
  }
}

// ─── Privacy option card ────────────────────────────────────────────────────

class _PrivacyOption extends StatelessWidget {
  final String title;
  final String subtitle;
  final IconData icon;
  final String value;
  final String selected;
  final ValueChanged<String> onTap;

  const _PrivacyOption({
    required this.title,
    required this.subtitle,
    required this.icon,
    required this.value,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isSelected = value == selected;
    return GestureDetector(
      onTap: () => onTap(value),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: isSelected
              ? AppColors.textPrimary.withValues(alpha: 0.06)
              : AppColors.bgCard,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(
            color: isSelected ? AppColors.textPrimary : AppColors.borderSubtle,
            width: isSelected ? 1.5 : 1,
          ),
        ),
        child: Row(
          children: [
            Icon(
              icon,
              size: 18,
              color:
                  isSelected ? AppColors.textPrimary : AppColors.textMuted,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: AppTextStyles.label),
                  Text(subtitle,
                      style: AppTextStyles.labelSmall
                          .copyWith(color: AppColors.textMuted)),
                ],
              ),
            ),
            Icon(
              isSelected
                  ? Icons.radio_button_checked
                  : Icons.radio_button_unchecked,
              color: isSelected ? AppColors.textPrimary : AppColors.textMuted,
              size: 18,
            ),
          ],
        ),
      ),
    );
  }
}

// ─── Live preview ───────────────────────────────────────────────────────────

class _LivePreview extends StatelessWidget {
  final String name;
  final String description;
  final String privacy;
  final bool isMature;

  const _LivePreview({
    required this.name,
    required this.description,
    required this.privacy,
    required this.isMature,
  });

  @override
  Widget build(BuildContext context) {
    final displayName = name.isEmpty ? 'Space Name' : name;
    final displayDesc = description.isEmpty ? 'Description will appear here…' : description;

    return Container(
      margin: const EdgeInsets.fromLTRB(0, 16, 16, 24),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Preview',
            style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
          ),
          const SizedBox(height: 10),
          // Cover placeholder
          Container(
            height: 60,
            decoration: BoxDecoration(
              gradient: const LinearGradient(
                colors: [Color(0xFFFF6B35), Color(0xFF7B68EE)],
              ),
              borderRadius: BorderRadius.circular(10),
            ),
          ),
          const SizedBox(height: 10),
          // Avatar + name
          Row(
            children: [
              Container(
                width: 36,
                height: 36,
                decoration: BoxDecoration(
                  gradient: const LinearGradient(
                    colors: [Color(0xFFFF6B35), Color(0xFF7B68EE)],
                  ),
                  borderRadius: BorderRadius.circular(10),
                ),
                child: Center(
                  child: Text(
                    displayName.isNotEmpty
                        ? displayName[0].toUpperCase()
                        : 'S',
                    style: AppTextStyles.label
                        .copyWith(color: Colors.white),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  displayName,
                  style: AppTextStyles.label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 4,
            runSpacing: 4,
            children: [
              _PreviewBadge(
                label: privacy[0].toUpperCase() + privacy.substring(1),
                color: privacy == 'public'
                    ? AppColors.statusSuccess
                    : privacy == 'restricted'
                        ? AppColors.statusWarning
                        : AppColors.statusError,
              ),
              if (isMature)
                _PreviewBadge(
                  label: '18+',
                  color: AppColors.statusError,
                ),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            displayDesc,
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
            style: AppTextStyles.labelSmall
                .copyWith(color: AppColors.textMuted),
          ),
        ],
      ),
    );
  }
}

class _PreviewBadge extends StatelessWidget {
  final String label;
  final Color color;
  const _PreviewBadge({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: color.withValues(alpha: 0.3)),
      ),
      child: Text(
        label,
        style: AppTextStyles.labelSmall.copyWith(
          color: color,
          fontSize: 9,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}
