import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/communities_repository.dart';
import 'package:atpost_app/providers/communities_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CreateCommunityScreen extends ConsumerStatefulWidget {
  const CreateCommunityScreen({super.key});

  @override
  ConsumerState<CreateCommunityScreen> createState() =>
      _CreateCommunityScreenState();
}

class _CreateCommunityScreenState
    extends ConsumerState<CreateCommunityScreen> {
  final _formKey = GlobalKey<FormState>();
  final _nameCtrl = TextEditingController();
  final _handleCtrl = TextEditingController();
  final _descCtrl = TextEditingController();
  String _communityType = 'public';
  bool _creating = false;

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
      );
      ref.invalidate(myCommunitiesProvider);
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

  @override
  Widget build(BuildContext context) {
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
              style:
                  AppTextStyles.body.copyWith(color: AppColors.textPrimary),
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
              style:
                  AppTextStyles.body.copyWith(color: AppColors.textPrimary),
              decoration: _inputDecoration('@flutter-devs'),
              validator: (v) => (v == null || v.trim().isEmpty)
                  ? 'Handle is required'
                  : null,
            ),

            const SizedBox(height: 18),

            // Type dropdown
            Text('Community Type', style: AppTextStyles.label),
            const SizedBox(height: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusMedium),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: DropdownButtonHideUnderline(
                child: DropdownButton<String>(
                  value: _communityType,
                  isExpanded: true,
                  dropdownColor: AppColors.bgSecondary,
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textPrimary),
                  items: _communityTypes
                      .map((t) => DropdownMenuItem(
                            value: t,
                            child:
                                Text(t[0].toUpperCase() + t.substring(1)),
                          ))
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
              style:
                  AppTextStyles.body.copyWith(color: AppColors.textPrimary),
              maxLines: 4,
              decoration:
                  _inputDecoration('What is this community about?'),
            ),

            const SizedBox(height: 28),

            // Submit
            SizedBox(
              width: double.infinity,
              child: Container(
                decoration: BoxDecoration(
                  gradient: AppColors.postbookGradient,
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusMedium),
                ),
                child: ElevatedButton(
                  onPressed: _creating ? null : _submit,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: Colors.transparent,
                    shadowColor: Colors.transparent,
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(
                          AppSpacing.radiusMedium),
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
                      : Text('Create Community',
                          style: AppTextStyles.label
                              .copyWith(color: Colors.white)),
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
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
    );
  }
}
