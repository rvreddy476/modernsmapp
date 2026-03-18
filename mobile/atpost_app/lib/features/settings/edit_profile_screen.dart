import 'dart:io';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

class EditProfileScreen extends ConsumerStatefulWidget {
  const EditProfileScreen({super.key});

  @override
  ConsumerState<EditProfileScreen> createState() => _EditProfileScreenState();
}

class _EditProfileScreenState extends ConsumerState<EditProfileScreen> {
  final _formKey = GlobalKey<FormState>();

  final _displayNameController = TextEditingController();
  final _usernameController = TextEditingController();
  final _bioController = TextEditingController();
  final _pronounsController = TextEditingController();
  final _locationController = TextEditingController();
  final _professionController = TextEditingController();

  bool _initialized = false;
  bool _saving = false;
  File? _pickedImageFile;

  @override
  void dispose() {
    _displayNameController.dispose();
    _usernameController.dispose();
    _bioController.dispose();
    _pronounsController.dispose();
    _locationController.dispose();
    _professionController.dispose();
    super.dispose();
  }

  void _initControllers(dynamic user) {
    if (_initialized) return;
    _initialized = true;
    _displayNameController.text = user.displayName;
    _usernameController.text = user.username;
    _bioController.text = user.bio ?? '';
    _pronounsController.text = user.pronouns ?? '';
    _locationController.text = user.location ?? '';
    _professionController.text = user.profession ?? '';
  }

  bool _uploadingAvatar = false;
  String? _uploadedAvatarId;

  Future<void> _pickPhoto() async {
    final picker = ImagePicker();
    final picked = await picker.pickImage(source: ImageSource.gallery);
    if (picked != null) {
      setState(() {
        _pickedImageFile = File(picked.path);
        _uploadingAvatar = true;
      });
      try {
        final mediaId = await ref.read(apiClientProvider).uploadMedia(
          XFile(picked.path),
          type: 'avatar',
        );
        if (mounted) {
          setState(() {
            _uploadedAvatarId = mediaId;
            _uploadingAvatar = false;
          });
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Photo uploaded successfully')),
          );
        }
      } catch (e) {
        if (mounted) {
          setState(() => _uploadingAvatar = false);
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('Photo upload failed: $e')),
          );
        }
      }
    }
  }

  Future<void> _save() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    setState(() => _saving = true);
    try {
      final data = <String, dynamic>{
        'display_name': _displayNameController.text.trim(),
        'bio': _bioController.text.trim(),
        'pronouns': _pronounsController.text.trim(),
        'location': _locationController.text.trim(),
      };
      if (_uploadedAvatarId != null && _uploadedAvatarId!.isNotEmpty) {
        data['avatar_media_id'] = _uploadedAvatarId;
      }
      await ref.read(apiClientProvider).put('/v1/profiles/me', data: data);
      ref.invalidate(currentUserProvider);
      if (mounted) context.pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to save: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final userAsync = ref.watch(currentUserProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Edit Profile', style: AppTextStyles.h2),
        actions: [
          if (_saving)
            const Padding(
              padding: EdgeInsets.only(right: 16),
              child: Center(
                child: SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              ),
            )
          else
            TextButton(
              onPressed: _save,
              child: Text(
                'Save',
                style: AppTextStyles.label.copyWith(color: AppColors.postbookPrimary),
              ),
            ),
        ],
      ),
      body: userAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, _) => Center(
          child: Text('Could not load profile', style: AppTextStyles.bodySmall),
        ),
        data: (user) {
          _initControllers(user);
          return SingleChildScrollView(
            padding: AppSpacing.pagePadding.copyWith(top: 24, bottom: 40),
            child: Form(
              key: _formKey,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  // Avatar section
                  Center(
                    child: Column(
                      children: [
                        Stack(
                          children: [
                            CircleAvatar(
                              radius: 48,
                              backgroundColor: AppColors.bgCard,
                              backgroundImage: _pickedImageFile != null
                                  ? FileImage(_pickedImageFile!)
                                  : null,
                              child: _pickedImageFile == null
                                  ? Text(
                                      user.displayName.isNotEmpty
                                          ? user.displayName[0].toUpperCase()
                                          : 'U',
                                      style: AppTextStyles.h1.copyWith(color: AppColors.textPrimary),
                                    )
                                  : null,
                            ),
                            if (_uploadingAvatar)
                              const Positioned.fill(
                                child: CircleAvatar(
                                  radius: 48,
                                  backgroundColor: Colors.black38,
                                  child: SizedBox(
                                    width: 24,
                                    height: 24,
                                    child: CircularProgressIndicator(
                                      strokeWidth: 2,
                                      color: Colors.white,
                                    ),
                                  ),
                                ),
                              ),
                          ],
                        ),
                        const SizedBox(height: 8),
                        TextButton(
                          onPressed: _pickPhoto,
                          child: Text(
                            'Change photo',
                            style: AppTextStyles.label.copyWith(color: AppColors.postbookPrimary),
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(height: 24),
                  // Form fields
                  _FieldLabel('Display Name'),
                  const SizedBox(height: 6),
                  _ProfileTextField(
                    controller: _displayNameController,
                    hint: 'Your display name',
                    validator: (v) =>
                        (v == null || v.trim().isEmpty) ? 'Display name is required' : null,
                  ),
                  const SizedBox(height: 16),
                  _FieldLabel('Username'),
                  const SizedBox(height: 6),
                  _ProfileTextField(
                    controller: _usernameController,
                    hint: 'username',
                    prefixText: '@',
                    validator: (v) =>
                        (v == null || v.trim().isEmpty) ? 'Username is required' : null,
                  ),
                  const SizedBox(height: 16),
                  _FieldLabel('Bio'),
                  const SizedBox(height: 6),
                  _ProfileTextField(
                    controller: _bioController,
                    hint: 'Tell people about yourself',
                    maxLines: 3,
                  ),
                  const SizedBox(height: 16),
                  _FieldLabel('Pronouns'),
                  const SizedBox(height: 6),
                  _ProfileTextField(
                    controller: _pronounsController,
                    hint: 'e.g. she/her, he/him, they/them',
                  ),
                  const SizedBox(height: 16),
                  _FieldLabel('Location'),
                  const SizedBox(height: 6),
                  _ProfileTextField(
                    controller: _locationController,
                    hint: 'City, Country',
                  ),
                  const SizedBox(height: 16),
                  _FieldLabel('Profession'),
                  const SizedBox(height: 6),
                  _ProfileTextField(
                    controller: _professionController,
                    hint: 'e.g. Designer, Engineer',
                  ),
                  const SizedBox(height: 32),
                  SizedBox(
                    width: double.infinity,
                    child: ElevatedButton(
                      onPressed: _saving ? null : _save,
                      style: ElevatedButton.styleFrom(
                        backgroundColor: AppColors.postbookPrimary,
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(vertical: 14),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                        ),
                      ),
                      child: _saving
                          ? const SizedBox(
                              height: 20,
                              width: 20,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : Text('Save Changes', style: AppTextStyles.label),
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

class _FieldLabel extends StatelessWidget {
  const _FieldLabel(this.text);
  final String text;

  @override
  Widget build(BuildContext context) {
    return Text(
      text,
      style: AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
    );
  }
}

class _ProfileTextField extends StatelessWidget {
  const _ProfileTextField({
    required this.controller,
    required this.hint,
    this.prefixText,
    this.maxLines = 1,
    this.validator,
  });

  final TextEditingController controller;
  final String hint;
  final String? prefixText;
  final int maxLines;
  final String? Function(String?)? validator;

  @override
  Widget build(BuildContext context) {
    return TextFormField(
      controller: controller,
      maxLines: maxLines,
      validator: validator,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        hintText: hint,
        hintStyle: AppTextStyles.body.copyWith(color: AppColors.textMuted),
        prefixText: prefixText,
        prefixStyle: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
        filled: true,
        fillColor: AppColors.bgCard,
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: BorderSide(color: AppColors.borderSubtle),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: BorderSide(color: AppColors.borderSubtle),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: BorderSide(color: AppColors.postbookPrimary, width: 1.5),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: const BorderSide(color: Colors.red),
        ),
        focusedErrorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          borderSide: const BorderSide(color: Colors.red, width: 1.5),
        ),
      ),
    );
  }
}
