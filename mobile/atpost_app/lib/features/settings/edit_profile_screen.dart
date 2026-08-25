import 'dart:io';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/core/utils/validators.dart';
import 'package:atpost_app/core/widgets/app_toast.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/shared/widgets/v_input_field.dart';
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
  final _firstNameController = TextEditingController();
  final _lastNameController = TextEditingController();
  final _usernameController = TextEditingController();
  final _bioController = TextEditingController();
  final _pronounsController = TextEditingController();
  final _locationController = TextEditingController();
  final _professionController = TextEditingController();

  String? _firstNameError;
  String? _lastNameError;
  String? _usernameError;

  bool _initialized = false;
  bool _saving = false;

  File? _pickedAvatarFile;
  File? _pickedCoverFile;

  bool _uploadingAvatar = false;
  bool _uploadingCover = false;
  String? _uploadedAvatarId;
  String? _uploadedCoverId;

  @override
  void dispose() {
    _firstNameController.dispose();
    _lastNameController.dispose();
    _usernameController.dispose();
    _bioController.dispose();
    _pronounsController.dispose();
    _locationController.dispose();
    _professionController.dispose();
    super.dispose();
  }

  void _initControllers(User user) {
    if (_initialized) return;
    _initialized = true;
    _firstNameController.text = user.firstName;
    _lastNameController.text = user.lastName;
    _usernameController.text = user.username;
    _bioController.text = user.bio ?? '';
    _pronounsController.text = user.pronouns ?? '';
    _locationController.text = user.location ?? '';
    _professionController.text = user.profession ?? '';
  }

  Future<void> _pickImage(bool isAvatar) async {
    final picker = ImagePicker();
    final picked = await picker.pickImage(source: ImageSource.gallery);
    if (picked == null) return;

    setState(() {
      if (isAvatar) {
        _pickedAvatarFile = File(picked.path);
        _uploadingAvatar = true;
      } else {
        _pickedCoverFile = File(picked.path);
        _uploadingCover = true;
      }
    });

    try {
      final mediaId = await ref.read(apiClientProvider).uploadMedia(
            XFile(picked.path),
            type: isAvatar ? 'avatar' : 'cover',
          );
      if (mounted) {
        setState(() {
          if (isAvatar) {
            _uploadedAvatarId = mediaId;
            _uploadingAvatar = false;
          } else {
            _uploadedCoverId = mediaId;
            _uploadingCover = false;
          }
        });
        AppToast.success(context, '${isAvatar ? "Profile" : "Cover"} photo uploaded!');
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          if (isAvatar) _uploadingAvatar = false;
          else _uploadingCover = false;
        });
        AppToast.error(context, 'Upload failed. Please try again.');
      }
    }
  }

  bool _validate() {
    setState(() {
      _firstNameError = Validators.required(_firstNameController.text, 'First Name');
      _lastNameError = Validators.required(_lastNameController.text, 'Last Name');
      _usernameError = Validators.required(_usernameController.text, 'Username');
    });
    return _firstNameError == null && _lastNameError == null && _usernameError == null;
  }

  Future<void> _save() async {
    if (!_validate()) return;
    setState(() => _saving = true);
    try {
      final data = <String, dynamic>{
        'first_name': _firstNameController.text.trim(),
        'last_name': _lastNameController.text.trim(),
        'username': _usernameController.text.trim(),
        'bio': _bioController.text.trim(),
        'pronouns': _pronounsController.text.trim(),
        'location': _locationController.text.trim(),
        'profession': _professionController.text.trim(),
      };
      if (_uploadedAvatarId != null) data['avatar_media_id'] = _uploadedAvatarId;
      if (_uploadedCoverId != null) data['cover_media_id'] = _uploadedCoverId;

      await ref.read(apiClientProvider).put('/v1/profiles/me', data: data);
      ref.invalidate(currentUserProvider);

      if (mounted) {
        AppToast.success(context, 'Profile updated successfully!');
        context.pop();
      }
    } catch (e) {
      if (mounted) AppToast.error(context, 'Failed to save profile changes');
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
        backgroundColor: Colors.transparent,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.close_rounded, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Edit Profile', style: AppTextStyles.h2),
        centerTitle: true,
        actions: [
          if (_saving)
            const Center(child: Padding(padding: EdgeInsets.all(16.0), child: SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))))
          else
            TextButton(
              onPressed: _save,
              child: Text('Done', style: AppTextStyles.label.copyWith(color: AppColors.postbookPrimary, fontWeight: FontWeight.bold)),
            ),
        ],
      ),
      body: userAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, __) => const Center(child: Text('Profile unavailable')),
        data: (user) {
          _initControllers(user);
          return SingleChildScrollView(
            child: Column(
              children: [
                _buildPhotoSection(user),
                Padding(
                  padding: const EdgeInsets.all(20.0),
                  child: Column(
                    children: [
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Expanded(
                            child: VInputField(
                              label: 'First Name',
                              controller: _firstNameController,
                              errorText: _firstNameError,
                              isMandatory: true,
                            ),
                          ),
                          const SizedBox(width: 12),
                          Expanded(
                            child: VInputField(
                              label: 'Last Name',
                              controller: _lastNameController,
                              errorText: _lastNameError,
                              isMandatory: true,
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 20),
                      VInputField(
                        label: 'Username',
                        controller: _usernameController,
                        errorText: _usernameError,
                        isMandatory: true,
                      ),
                      const SizedBox(height: 20),
                      VInputField(
                        label: 'Bio',
                        controller: _bioController,
                        hint: 'A little about you...',
                        textInputAction: TextInputAction.newline,
                      ),
                      const SizedBox(height: 20),
                      VInputField(
                        label: 'Location',
                        controller: _locationController,
                        hint: 'e.g. London, UK',
                      ),
                      const SizedBox(height: 20),
                      VInputField(
                        label: 'Profession',
                        controller: _professionController,
                        hint: 'e.g. Designer',
                      ),
                    ],
                  ),
                ),
              ],
            ),
          );
        },
      ),
    );
  }

  Widget _buildPhotoSection(User user) {
    return SizedBox(
      height: 240,
      child: Stack(
        children: [
          // Cover Photo
          GestureDetector(
            onTap: () => _pickImage(false),
            child: Container(
              height: 160,
              width: double.infinity,
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                image: _pickedCoverFile != null
                    ? DecorationImage(image: FileImage(_pickedCoverFile!), fit: BoxFit.cover)
                    : (user.hasCover ? DecorationImage(image: NetworkImage(user.coverUrl!), fit: BoxFit.cover) : null),
              ),
              child: _uploadingCover
                ? const Center(child: CircularProgressIndicator())
                : Container(
                    color: Colors.black26,
                    child: const Icon(Icons.camera_alt_rounded, color: Colors.white, size: 30),
                  ),
            ),
          ),

          // Avatar
          Positioned(
            bottom: 10,
            left: 20,
            child: GestureDetector(
              onTap: () => _pickImage(true),
              child: Container(
                padding: const EdgeInsets.all(4),
                decoration: const BoxDecoration(color: AppColors.bgPrimary, shape: BoxShape.circle),
                child: Stack(
                  children: [
                    CircleAvatar(
                      radius: 50,
                      backgroundColor: AppColors.bgTertiary,
                      backgroundImage: _pickedAvatarFile != null
                          ? FileImage(_pickedAvatarFile!)
                          : (user.hasAvatar ? NetworkImage(user.avatarUrl) : null) as ImageProvider?,
                      child: _pickedAvatarFile == null && !user.hasAvatar
                          ? const Icon(Icons.person, size: 40, color: Colors.white24)
                          : null,
                    ),
                    if (_uploadingAvatar)
                      const Positioned.fill(child: Center(child: CircularProgressIndicator())),
                    const Positioned(
                      bottom: 0,
                      right: 0,
                      child: CircleAvatar(
                        radius: 16,
                        backgroundColor: AppColors.postbookPrimary,
                        child: Icon(Icons.camera_alt_rounded, size: 16, color: Colors.white),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
