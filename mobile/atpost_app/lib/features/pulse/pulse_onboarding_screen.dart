import 'dart:async';
import 'dart:io';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:atpost_app/services/pulse_face_verification_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mime/mime.dart';

enum _OnboardingStep { rules, personal, media, location }

enum _LocationStatus { idle, requesting, granted, denied }

class _PendingPhoto {
  final XFile file;
  final bool uploading;
  final bool done;

  const _PendingPhoto({
    required this.file,
    this.uploading = false,
    this.done = false,
  });

  _PendingPhoto copyWith({bool? uploading, bool? done}) {
    return _PendingPhoto(
      file: file,
      uploading: uploading ?? this.uploading,
      done: done ?? this.done,
    );
  }
}

// formerly PostMatchOnboardingScreen
class PulseOnboardingScreen extends ConsumerStatefulWidget {
  const PulseOnboardingScreen({super.key});

  @override
  ConsumerState<PulseOnboardingScreen> createState() =>
      _PulseOnboardingScreenState();
}

class _PulseOnboardingScreenState
    extends ConsumerState<PulseOnboardingScreen> {
  static const _steps = [
    _OnboardingStep.rules,
    _OnboardingStep.personal,
    _OnboardingStep.media,
    _OnboardingStep.location,
  ];

  final _firstNameController = TextEditingController();
  final _dobDayController = TextEditingController();
  final _dobMonthController = TextEditingController();
  final _dobYearController = TextEditingController();
  final _dobMonthFocus = FocusNode();
  final _dobYearFocus = FocusNode();
  late final PulseFaceVerificationService _faceVerificationService;

  _OnboardingStep _step = _OnboardingStep.rules;
  _LocationStatus _locationStatus = _LocationStatus.idle;

  final List<_PendingPhoto> _photos = [];
  _PendingPhoto? _selfie;
  PulseFaceVerificationResult? _faceVerificationResult;

  bool _bootstrapping = true;
  bool _loading = false;
  bool _verifyingFace = false;
  bool _mediaNeedsVerification = false;
  String _error = '';

  String _gender = '';
  String _lookingFor = '';
  String _intent = '';
  double? _latitude;
  double? _longitude;

  @override
  void initState() {
    super.initState();
    _faceVerificationService = PulseFaceVerificationService();
    _bootstrap();
  }

  @override
  void dispose() {
    _firstNameController.dispose();
    _dobDayController.dispose();
    _dobMonthController.dispose();
    _dobYearController.dispose();
    _dobMonthFocus.dispose();
    _dobYearFocus.dispose();
    unawaited(_faceVerificationService.dispose());
    super.dispose();
  }

  Future<void> _bootstrap() async {
    User? currentUser;
    try {
      currentUser = await ref.read(currentUserProvider.future);
    } catch (_) {
      currentUser = null;
    }
    final auth = ref.read(pulseAuthServiceProvider);
    await auth.sessionReady;

    final displayName = currentUser?.displayName.trim() ?? '';
    if (displayName.isNotEmpty) {
      _firstNameController.text = displayName.split(RegExp(r'\s+')).first;
    }

    if (!auth.hasSession && currentUser != null) {
      final ok = await auth.ssoFromPostbook(postbookUserId: currentUser.id);
      if (!ok) {
        if (!mounted) return;
        setState(() {
          _error =
              'Could not connect Pulse to your AtPost account. Please try again.';
          _bootstrapping = false;
        });
        return;
      }
    }

    if (!mounted) return;
    if (auth.isReady) {
      context.go('/pulse/discover');
      return;
    }
    setState(() => _bootstrapping = false);
  }

  int get _stepIndex => _steps.indexOf(_step);

  Future<void> _pickPhotos() async {
    if (_loading || _verifyingFace) return;
    final picked = await ImagePicker().pickMultiImage(imageQuality: 92);
    if (!mounted || picked.isEmpty) return;
    final allowed = 6 - _photos.length;
    if (allowed <= 0) return;
    setState(() {
      _photos.addAll(
        picked.take(allowed).map((file) => _PendingPhoto(file: file)),
      );
      _error = '';
      _markFaceVerificationDirty();
    });
  }

  Future<void> _captureSelfie() async {
    if (_loading || _verifyingFace) return;
    final picked = await ImagePicker().pickImage(
      source: ImageSource.camera,
      preferredCameraDevice: CameraDevice.front,
      imageQuality: 92,
    );
    if (!mounted || picked == null) return;
    setState(() {
      _selfie = _PendingPhoto(file: picked);
      _error = '';
      _markFaceVerificationDirty();
    });
    await _runFaceVerification();
  }

  void _markFaceVerificationDirty() {
    _faceVerificationResult = null;
    _mediaNeedsVerification = _selfie != null;
  }

  void _removePhotoAt(int index) {
    if (_loading || _verifyingFace) return;
    setState(() {
      _photos.removeAt(index);
      _error = '';
      _markFaceVerificationDirty();
    });
  }

  void _removeSelfie() {
    if (_loading || _verifyingFace) return;
    setState(() {
      _selfie = null;
      _error = '';
      _faceVerificationResult = null;
      _mediaNeedsVerification = false;
    });
  }

  Future<bool> _runFaceVerification({bool showErrorBanner = false}) async {
    final selfie = _selfie;
    if (selfie == null) return false;

    setState(() {
      _verifyingFace = true;
      if (showErrorBanner) _error = '';
    });

    try {
      final result = await _faceVerificationService.verify(
        photos: _photos.map((photo) => photo.file).toList(growable: false),
        selfie: selfie.file,
      );
      if (!mounted) return false;
      setState(() {
        _faceVerificationResult = result;
        _mediaNeedsVerification = false;
        _verifyingFace = false;
        if (showErrorBanner && !result.isMatch) {
          _error = _faceVerificationErrorMessage(result);
        }
      });
      return result.isMatch;
    } catch (_) {
      if (!mounted) return false;
      setState(() {
        _verifyingFace = false;
        _mediaNeedsVerification = true;
        if (showErrorBanner) {
          _error = 'Face verification failed. Please try again.';
        }
      });
      return false;
    }
  }

  Future<bool> _ensureVerifiedMedia() async {
    final selfie = _selfie;
    if (selfie == null) {
      setState(() => _error = 'A live selfie is required for verification.');
      return false;
    }

    if (_mediaNeedsVerification || _faceVerificationResult == null) {
      return _runFaceVerification(showErrorBanner: true);
    }

    if (!_faceVerificationResult!.isMatch) {
      setState(
        () => _error = _faceVerificationErrorMessage(_faceVerificationResult!),
      );
      return false;
    }

    return true;
  }

  String? _validatePersonal() {
    final name = _firstNameController.text.trim();
    final day = int.tryParse(_dobDayController.text.trim());
    final month = int.tryParse(_dobMonthController.text.trim());
    final year = int.tryParse(_dobYearController.text.trim());

    if (name.isEmpty || name.length > 22) {
      return 'Name must be 1-22 characters.';
    }
    if (day == null ||
        month == null ||
        year == null ||
        day < 1 ||
        day > 31 ||
        month < 1 ||
        month > 12 ||
        year < 1920) {
      return 'Enter a valid date of birth.';
    }
    if (DateTime.now().year - year < 18) return 'You must be at least 18.';
    if (_gender.isEmpty) return 'Select your gender.';
    if (_intent.isEmpty) return 'Select what you are looking for.';
    return null;
  }

  void _setUploading(int index, bool uploading) {
    setState(() {
      if (index < _photos.length) {
        _photos[index] = _photos[index].copyWith(uploading: uploading);
      } else if (_selfie != null) {
        _selfie = _selfie!.copyWith(uploading: uploading);
      }
    });
  }

  void _setDone(int index) {
    setState(() {
      if (index < _photos.length) {
        _photos[index] = _photos[index].copyWith(uploading: false, done: true);
      } else if (_selfie != null) {
        _selfie = _selfie!.copyWith(uploading: false, done: true);
      }
    });
  }

  void _resetUploading() {
    setState(() {
      for (var i = 0; i < _photos.length; i++) {
        _photos[i] = _photos[i].copyWith(uploading: false);
      }
      if (_selfie != null) {
        _selfie = _selfie!.copyWith(uploading: false);
      }
    });
  }

  Future<bool> _uploadMedia() async {
    if (_photos.isEmpty) {
      setState(() => _error = 'Upload at least 1 profile photo.');
      return false;
    }
    if (_selfie == null) {
      setState(() => _error = 'A live selfie is required for verification.');
      return false;
    }
    final verified = await _ensureVerifiedMedia();
    if (!verified) return false;

    final repo = ref.read(pulseRepositoryProvider);
    final allMedia = [..._photos, _selfie!];

    for (var index = 0; index < allMedia.length; index++) {
      final item = allMedia[index];
      if (item.done) continue;
      try {
        _setUploading(index, true);
        final file = File(item.file.path);
        final upload = await repo.initPhotoUpload(
          contentType: lookupMimeType(item.file.path) ?? 'image/jpeg',
          fileName: item.file.name,
          fileSize: await file.length(),
        );
        await repo.uploadFileToPresignedUrl(
          uploadUrl: upload.uploadUrl,
          file: file,
          contentType: lookupMimeType(item.file.path) ?? 'image/jpeg',
        );
        await repo.completePhotoUpload(
          mediaId: upload.mediaId,
          mediaKey: upload.mediaKey,
          isPrimary: index == 0,
        );
        _setDone(index);
      } catch (_) {
        _resetUploading();
        setState(() => _error = 'Upload failed. Please try again.');
        return false;
      }
    }

    await repo.setOnboardingStatus('photos_uploaded');
    return true;
  }

  Future<void> _requestLocation() async {
    setState(() => _locationStatus = _LocationStatus.requesting);
    try {
      final enabled = await Geolocator.isLocationServiceEnabled();
      if (!enabled) {
        setState(() => _locationStatus = _LocationStatus.denied);
        return;
      }

      var permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        permission = await Geolocator.requestPermission();
      }
      if (permission == LocationPermission.denied ||
          permission == LocationPermission.deniedForever) {
        setState(() => _locationStatus = _LocationStatus.denied);
        return;
      }

      final position = await Geolocator.getCurrentPosition(
        desiredAccuracy: LocationAccuracy.medium,
        timeLimit: const Duration(seconds: 10),
      );
      if (!mounted) return;
      setState(() {
        _latitude = position.latitude;
        _longitude = position.longitude;
        _locationStatus = _LocationStatus.granted;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _locationStatus = _LocationStatus.denied);
    }
  }

  Future<bool> _saveProfile() async {
    final repo = ref.read(pulseRepositoryProvider);
    try {
      await repo.updateProfile({
        'first_name': _firstNameController.text.trim(),
        'date_of_birth':
            '${_dobYearController.text.padLeft(4, '0')}-${_dobMonthController.text.padLeft(2, '0')}-${_dobDayController.text.padLeft(2, '0')}',
        'gender': _gender,
        'looking_for': _lookingFor.isEmpty ? 'everyone' : _lookingFor,
        'relationship_intent': _intent.isEmpty ? 'figuring_out' : _intent,
        if (_latitude != null) 'latitude': _latitude,
        if (_longitude != null) 'longitude': _longitude,
      });
      await repo.setOnboardingStatus('ready');
      return true;
    } catch (_) {
      setState(() => _error = 'Failed to save your profile.');
      return false;
    }
  }

  bool get _isFaceVerified =>
      !_mediaNeedsVerification && (_faceVerificationResult?.isMatch ?? false);

  String _faceVerificationErrorMessage(PulseFaceVerificationResult result) {
    switch (result.status) {
      case PulseFaceVerificationStatus.matched:
        return '';
      case PulseFaceVerificationStatus.noFace:
        return 'No face was detected in your selfie. Retake it with your face centered and well lit.';
      case PulseFaceVerificationStatus.multiFace:
        return 'Multiple faces were detected in your selfie. Make sure only you are visible.';
      case PulseFaceVerificationStatus.mismatch:
        return 'Your selfie does not match the profile photos you selected. Retake it or change your photos.';
      case PulseFaceVerificationStatus.noUsablePhotos:
        return 'Add at least 1 clear solo profile photo so we can verify your selfie.';
    }
  }

  String _faceVerificationSummary() {
    if (_verifyingFace) {
      return 'Checking your selfie against the clear solo faces found in your profile photos...';
    }
    if (_selfie == null) {
      return 'Required. Your selfie must match at least 1 clear solo profile photo.';
    }
    if (_mediaNeedsVerification) {
      return 'Your media changed. We will re-check this selfie before upload.';
    }

    final result = _faceVerificationResult;
    if (result == null) {
      return 'Required. Your selfie must match at least 1 clear solo profile photo.';
    }

    switch (result.status) {
      case PulseFaceVerificationStatus.matched:
        final comparablePhotos = result.comparablePhotoCount;
        final skippedPhotos = result.skippedPhotoCount;
        final summary =
            'Verified against $comparablePhotos clear ${comparablePhotos == 1 ? 'photo' : 'photos'}.';
        if (skippedPhotos == 0) return summary;
        return '$summary $skippedPhotos other ${skippedPhotos == 1 ? 'photo was' : 'photos were'} not usable for matching.';
      case PulseFaceVerificationStatus.noFace:
      case PulseFaceVerificationStatus.multiFace:
      case PulseFaceVerificationStatus.mismatch:
      case PulseFaceVerificationStatus.noUsablePhotos:
        return _faceVerificationErrorMessage(result);
    }
  }

  String _faceVerificationStatusLabel() {
    if (_verifyingFace) return 'Checking';
    if (_isFaceVerified) return 'Verified';
    if (_selfie == null) return 'Required';
    if (_mediaNeedsVerification) return 'Needs review';

    return switch (_faceVerificationResult?.status) {
      PulseFaceVerificationStatus.noUsablePhotos => 'Photo issue',
      PulseFaceVerificationStatus.matched => 'Verified',
      _ => 'Retake needed',
    };
  }

  Color _faceVerificationAccentColor() {
    if (_verifyingFace || _mediaNeedsVerification) {
      return AppColors.postbookPrimary;
    }
    if (_isFaceVerified) return AppColors.statusSuccess;
    return AppColors.statusError;
  }

  IconData _faceVerificationIcon() {
    if (_verifyingFace) return Icons.face_retouching_natural;
    if (_isFaceVerified) return Icons.verified_rounded;
    if (_mediaNeedsVerification) return Icons.refresh_rounded;

    return switch (_faceVerificationResult?.status) {
      PulseFaceVerificationStatus.noUsablePhotos =>
        Icons.photo_library_outlined,
      PulseFaceVerificationStatus.noFace => Icons.face_outlined,
      PulseFaceVerificationStatus.multiFace => Icons.groups_2_outlined,
      PulseFaceVerificationStatus.mismatch => Icons.warning_amber_rounded,
      PulseFaceVerificationStatus.matched => Icons.verified_rounded,
      _ => Icons.camera_alt_outlined,
    };
  }

  Future<void> _handleContinue() async {
    setState(() => _error = '');
    switch (_step) {
      case _OnboardingStep.rules:
        setState(() => _step = _OnboardingStep.personal);
        return;
      case _OnboardingStep.personal:
        final validation = _validatePersonal();
        if (validation != null) {
          setState(() => _error = validation);
          return;
        }
        setState(() => _step = _OnboardingStep.media);
        return;
      case _OnboardingStep.media:
        setState(() => _loading = true);
        final uploaded = await _uploadMedia();
        if (!mounted) return;
        setState(() => _loading = false);
        if (uploaded) setState(() => _step = _OnboardingStep.location);
        return;
      case _OnboardingStep.location:
        setState(() => _loading = true);
        final saved = await _saveProfile();
        if (!mounted) return;
        setState(() => _loading = false);
        // S1 onboarding flow: identity/photos/location -> Tune -> Echoes ->
        // discover. The Tune and Echoes screens forward into the discover
        // feed once they finish saving.
        if (saved) context.go('/pulse/onboarding/tune');
        return;
    }
  }

  void _goBack() {
    setState(() {
      _error = '';
      switch (_step) {
        case _OnboardingStep.rules:
          context.pop();
          break;
        case _OnboardingStep.personal:
          _step = _OnboardingStep.rules;
          break;
        case _OnboardingStep.media:
          _step = _OnboardingStep.personal;
          break;
        case _OnboardingStep.location:
          _step = _OnboardingStep.media;
          break;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    if (_bootstrapping) {
      return Scaffold(
        backgroundColor: AppColors.bgPrimary,
        body: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const CircularProgressIndicator(color: AppColors.postbookPrimary),
              const SizedBox(height: 14),
              Text(
                'Connecting Pulse to your AtPost account...',
                style: AppTextStyles.bodySmall,
              ),
            ],
          ),
        ),
      );
    }

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: AppSpacing.pagePadding.copyWith(top: 14, bottom: 14),
              child: Column(
                children: [
                  Row(
                    children: [
                      IconButton(
                        onPressed: _goBack,
                        icon: const Icon(
                          Icons.arrow_back_ios_new,
                          color: AppColors.textPrimary,
                          size: 18,
                        ),
                      ),
                      Container(
                        width: 36,
                        height: 36,
                        decoration: BoxDecoration(
                          gradient: AppColors.ctaGradient,
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: const Icon(
                          Icons.favorite,
                          color: Colors.white,
                          size: 18,
                        ),
                      ),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Text('Pulse', style: AppTextStyles.h2),
                      ),
                      Text(
                        'Step ${_stepIndex + 1} of ${_steps.length}',
                        style: AppTextStyles.labelSmall,
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  ClipRRect(
                    borderRadius: BorderRadius.circular(999),
                    child: LinearProgressIndicator(
                      minHeight: 6,
                      value: _stepIndex / (_steps.length - 1),
                      backgroundColor: AppColors.bgTertiary,
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                ],
              ),
            ),
            Expanded(
              child: SingleChildScrollView(
                padding: AppSpacing.pagePadding.copyWith(bottom: 30),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (_error.isNotEmpty) ...[
                      _ErrorBanner(message: _error),
                      const SizedBox(height: 12),
                    ],
                    switch (_step) {
                      _OnboardingStep.rules => _buildRules(),
                      _OnboardingStep.personal => _buildPersonal(),
                      _OnboardingStep.media => _buildMedia(),
                      _OnboardingStep.location => _buildLocation(),
                    },
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildRules() {
    const rules = [
      ('Be authentic', 'Use real photos and accurate information.'),
      ('Protect your privacy', 'Do not share sensitive details too early.'),
      ('Be respectful', 'Harassment and abuse are not tolerated.'),
      ('Report concerns', 'Block and report anything unsafe or suspicious.'),
    ];

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Community Guidelines', style: AppTextStyles.h1),
        const SizedBox(height: 4),
        Text(
          'A few things to keep in mind before you start.',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
        const SizedBox(height: 18),
        ...rules.map(
          (rule) => Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: _Card(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(rule.$1, style: AppTextStyles.h3),
                  const SizedBox(height: 4),
                  Text(
                    rule.$2,
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textSecondary,
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
        const SizedBox(height: 12),
        _PrimaryButton(label: 'I Agree - Continue', onPressed: _handleContinue),
      ],
    );
  }

  Widget _buildPersonal() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('About You', style: AppTextStyles.h1),
        const SizedBox(height: 4),
        Text(
          'The mobile form matches the web onboarding fields.',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
        const SizedBox(height: 18),
        _Card(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const _FieldLabel('First Name'),
              const SizedBox(height: 6),
              _Input(
                controller: _firstNameController,
                hintText: 'Your first name',
                maxLength: 22,
              ),
              const SizedBox(height: 14),
              const _FieldLabel('Date of Birth'),
              const SizedBox(height: 6),
              Row(
                children: [
                  Expanded(
                    child: _Input(
                      controller: _dobDayController,
                      hintText: 'DD',
                      keyboardType: TextInputType.number,
                      textAlign: TextAlign.center,
                      inputFormatters: [
                        FilteringTextInputFormatter.digitsOnly,
                        LengthLimitingTextInputFormatter(2),
                      ],
                      onChanged: (value) {
                        if (value.length == 2) _dobMonthFocus.requestFocus();
                      },
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: _Input(
                      controller: _dobMonthController,
                      hintText: 'MM',
                      focusNode: _dobMonthFocus,
                      keyboardType: TextInputType.number,
                      textAlign: TextAlign.center,
                      inputFormatters: [
                        FilteringTextInputFormatter.digitsOnly,
                        LengthLimitingTextInputFormatter(2),
                      ],
                      onChanged: (value) {
                        if (value.length == 2) _dobYearFocus.requestFocus();
                      },
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: _Input(
                      controller: _dobYearController,
                      hintText: 'YYYY',
                      focusNode: _dobYearFocus,
                      keyboardType: TextInputType.number,
                      textAlign: TextAlign.center,
                      inputFormatters: [
                        FilteringTextInputFormatter.digitsOnly,
                        LengthLimitingTextInputFormatter(4),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 14),
              const _FieldLabel('Gender'),
              const SizedBox(height: 8),
              _choiceWrap(
                const [
                  ('male', 'Man'),
                  ('female', 'Woman'),
                  ('non_binary', 'Non-Binary'),
                  ('other', 'Other'),
                ],
                _gender,
                (value) => setState(() => _gender = value),
              ),
              const SizedBox(height: 14),
              const _FieldLabel('Interested In'),
              const SizedBox(height: 8),
              _choiceWrap(
                const [
                  ('everyone', 'Everyone'),
                  ('male', 'Men'),
                  ('female', 'Women'),
                ],
                _lookingFor,
                (value) => setState(() => _lookingFor = value),
              ),
              const SizedBox(height: 14),
              const _FieldLabel('Looking For'),
              const SizedBox(height: 8),
              _choiceWrap(
                const [
                  ('long_term', 'Long-term partner'),
                  ('marriage', 'Marriage'),
                  ('casual', 'Something casual'),
                  ('figuring_out', 'Still exploring'),
                ],
                _intent,
                (value) => setState(() => _intent = value),
                wide: true,
              ),
            ],
          ),
        ),
        const SizedBox(height: 12),
        _PrimaryButton(label: 'Continue', onPressed: _handleContinue),
      ],
    );
  }

  Widget _buildMedia() {
    final mediaBusy = _loading || _verifyingFace;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Photos & Selfie', style: AppTextStyles.h1),
        const SizedBox(height: 4),
        Text(
          'Upload profile photos and take a live selfie. We verify the selfie on-device before upload.',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
        const SizedBox(height: 18),
        _Card(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Text('Profile Photos', style: AppTextStyles.h3),
                  const Spacer(),
                  Text('${_photos.length}/6', style: AppTextStyles.labelSmall),
                ],
              ),
              const SizedBox(height: 10),
              GridView.builder(
                shrinkWrap: true,
                physics: const NeverScrollableScrollPhysics(),
                gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                  crossAxisCount: 3,
                  crossAxisSpacing: 8,
                  mainAxisSpacing: 8,
                  childAspectRatio: 0.78,
                ),
                itemCount: 6,
                itemBuilder: (context, index) {
                  if (index < _photos.length) {
                    final photo = _photos[index];
                    return _PhotoTile(
                      file: photo.file,
                      primary: index == 0,
                      uploading: photo.uploading,
                      done: photo.done,
                      onRemove: mediaBusy ? null : () => _removePhotoAt(index),
                    );
                  }
                  return _AddTile(
                    onTap: mediaBusy || _photos.length == 6
                        ? null
                        : _pickPhotos,
                  );
                },
              ),
            ],
          ),
        ),
        const SizedBox(height: 12),
        _Card(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Text('Selfie Verification', style: AppTextStyles.h3),
                  const Spacer(),
                  Text(
                    _faceVerificationStatusLabel(),
                    style: AppTextStyles.labelSmall.copyWith(
                      color: _faceVerificationAccentColor(),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 6),
              Text(
                'Required. Your selfie must match at least 1 clear solo profile photo.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
              if (_selfie != null) ...[
                const SizedBox(height: 12),
                _VerificationNotice(
                  icon: _faceVerificationIcon(),
                  color: _faceVerificationAccentColor(),
                  message: _faceVerificationSummary(),
                ),
              ],
              const SizedBox(height: 10),
              if (_selfie == null)
                _SelfiePrompt(
                  onTap: mediaBusy ? null : _captureSelfie,
                  enabled: !mediaBusy,
                )
              else
                _SelfiePreview(
                  file: _selfie!.file,
                  uploading: _selfie!.uploading,
                  statusLabel: _selfie!.done
                      ? 'Selfie uploaded'
                      : _faceVerificationStatusLabel(),
                  accentColor: _selfie!.done
                      ? AppColors.statusSuccess
                      : _faceVerificationAccentColor(),
                  onRetake: mediaBusy ? null : _captureSelfie,
                  onRemove: mediaBusy ? null : _removeSelfie,
                  enabled: !mediaBusy,
                ),
            ],
          ),
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _SecondaryButton(
                label: 'Back',
                onPressed: mediaBusy ? null : _goBack,
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              flex: 2,
              child: _PrimaryButton(
                label: _loading
                    ? 'Uploading...'
                    : _verifyingFace
                    ? 'Checking...'
                    : 'Continue',
                onPressed: mediaBusy ? null : _handleContinue,
              ),
            ),
          ],
        ),
      ],
    );
  }

  Widget _buildLocation() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Find People Near You', style: AppTextStyles.h1),
        const SizedBox(height: 4),
        Text(
          'Location is optional but recommended for nearby discovery.',
          style: AppTextStyles.bodySmall.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
        const SizedBox(height: 18),
        _Card(
          child: Column(
            children: [
              const Icon(
                Icons.location_on_outlined,
                size: 52,
                color: AppColors.postbookPrimary,
              ),
              const SizedBox(height: 14),
              Text(
                switch (_locationStatus) {
                  _LocationStatus.idle => 'Enable location',
                  _LocationStatus.requesting => 'Waiting for permission...',
                  _LocationStatus.granted => 'Location enabled',
                  _LocationStatus.denied => 'Location unavailable',
                },
                style: AppTextStyles.h3,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 6),
              Text(
                switch (_locationStatus) {
                  _LocationStatus.idle =>
                    'This helps sort matches by distance.',
                  _LocationStatus.requesting =>
                    'Approve the location prompt on your device.',
                  _LocationStatus.granted =>
                    'Distance-based discovery is now enabled.',
                  _LocationStatus.denied =>
                    'You can continue without it and enable it later.',
                },
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 16),
              if (_locationStatus == _LocationStatus.idle)
                _PrimaryButton(
                  label: 'Enable Location',
                  onPressed: _requestLocation,
                )
              else if (_locationStatus == _LocationStatus.requesting)
                const CircularProgressIndicator(
                  color: AppColors.postbookPrimary,
                )
              else
                _PrimaryButton(
                  label: _loading ? 'Saving...' : 'Find Matches',
                  onPressed: _loading ? null : _handleContinue,
                ),
              TextButton(
                onPressed: _loading ? null : _handleContinue,
                child: Text(
                  _locationStatus == _LocationStatus.granted
                      ? 'Continue'
                      : 'Skip for now',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _choiceWrap(
    List<(String, String)> options,
    String selected,
    ValueChanged<String> onTap, {
    bool wide = false,
  }) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: options.map((option) {
        final active = selected == option.$1;
        return SizedBox(
          width: wide ? (MediaQuery.of(context).size.width - 52) / 2 : null,
          child: InkWell(
            onTap: () => onTap(option.$1),
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            child: Ink(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
              decoration: BoxDecoration(
                color: active
                    ? AppColors.postbookPrimary.withValues(alpha: 0.14)
                    : AppColors.bgTertiary,
                borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                border: Border.all(
                  color: active
                      ? AppColors.postbookPrimary
                      : AppColors.borderSubtle,
                ),
              ),
              child: Text(
                option.$2,
                style: AppTextStyles.label.copyWith(
                  color: active
                      ? AppColors.postbookPrimary
                      : AppColors.textSecondary,
                ),
              ),
            ),
          ),
        );
      }).toList(),
    );
  }
}

class _Card extends StatelessWidget {
  const _Card({required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: child,
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.statusError.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(
          color: AppColors.statusError.withValues(alpha: 0.26),
        ),
      ),
      child: Text(
        message,
        style: AppTextStyles.bodySmall.copyWith(color: AppColors.textPrimary),
      ),
    );
  }
}

class _FieldLabel extends StatelessWidget {
  const _FieldLabel(this.text);

  final String text;

  @override
  Widget build(BuildContext context) {
    return Text(text, style: AppTextStyles.labelSmall);
  }
}

class _Input extends StatelessWidget {
  const _Input({
    required this.controller,
    required this.hintText,
    this.focusNode,
    this.keyboardType,
    this.textAlign = TextAlign.start,
    this.maxLength,
    this.inputFormatters,
    this.onChanged,
  });

  final TextEditingController controller;
  final String hintText;
  final FocusNode? focusNode;
  final TextInputType? keyboardType;
  final TextAlign textAlign;
  final int? maxLength;
  final List<TextInputFormatter>? inputFormatters;
  final ValueChanged<String>? onChanged;

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: controller,
      focusNode: focusNode,
      keyboardType: keyboardType,
      textAlign: textAlign,
      maxLength: maxLength,
      inputFormatters: inputFormatters,
      onChanged: onChanged,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        counterText: '',
        hintText: hintText,
        hintStyle: AppTextStyles.bodySmall.copyWith(color: AppColors.textMuted),
        filled: true,
        fillColor: AppColors.bgTertiary,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 14,
          vertical: 14,
        ),
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
          borderSide: const BorderSide(
            color: AppColors.postbookPrimary,
            width: 1.4,
          ),
        ),
      ),
    );
  }
}

class _VerificationNotice extends StatelessWidget {
  const _VerificationNotice({
    required this.icon,
    required this.color,
    required this.message,
  });

  final IconData icon;
  final Color color;
  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: color.withValues(alpha: 0.24)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 1),
            child: Icon(icon, size: 16, color: color),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              message,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _PhotoTile extends StatelessWidget {
  const _PhotoTile({
    required this.file,
    required this.primary,
    required this.uploading,
    required this.done,
    required this.onRemove,
  });

  final XFile file;
  final bool primary;
  final bool uploading;
  final bool done;
  final VoidCallback? onRemove;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Stack(
        fit: StackFit.expand,
        children: [
          Image.file(File(file.path), fit: BoxFit.cover),
          if (uploading)
            Container(
              color: Colors.black38,
              child: const Center(
                child: CircularProgressIndicator(
                  color: Colors.white,
                  strokeWidth: 2,
                ),
              ),
            ),
          Positioned(
            top: 6,
            right: 6,
            child: GestureDetector(
              onTap: onRemove,
              child: Container(
                width: 24,
                height: 24,
                decoration: BoxDecoration(
                  color: Colors.black54,
                  borderRadius: BorderRadius.circular(999),
                ),
                child: const Icon(Icons.close, size: 14, color: Colors.white),
              ),
            ),
          ),
          if (done)
            const Positioned(
              top: 6,
              left: 6,
              child: Icon(Icons.check_circle, color: AppColors.statusSuccess),
            ),
          if (primary)
            Positioned(
              left: 0,
              right: 0,
              bottom: 0,
              child: Container(
                color: Colors.black54,
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
                child: Text(
                  'PRIMARY',
                  style: AppTextStyles.labelTiny.copyWith(color: Colors.white),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _AddTile extends StatelessWidget {
  const _AddTile({required this.onTap});

  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Opacity(
        opacity: onTap == null ? 0.55 : 1,
        child: Ink(
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: const Center(
            child: Icon(Icons.add, color: AppColors.textMuted),
          ),
        ),
      ),
    );
  }
}

class _SelfiePrompt extends StatelessWidget {
  const _SelfiePrompt({required this.onTap, required this.enabled});

  final VoidCallback? onTap;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
      child: Opacity(
        opacity: enabled ? 1 : 0.55,
        child: Ink(
          width: double.infinity,
          padding: const EdgeInsets.all(18),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            children: [
              const Icon(
                Icons.camera_alt_outlined,
                size: 34,
                color: AppColors.postbookPrimary,
              ),
              const SizedBox(height: 10),
              Text('Take a selfie', style: AppTextStyles.h3),
              const SizedBox(height: 4),
              Text(
                'Opens your front camera for a live capture.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _SelfiePreview extends StatelessWidget {
  const _SelfiePreview({
    required this.file,
    required this.uploading,
    required this.statusLabel,
    required this.accentColor,
    required this.onRetake,
    required this.onRemove,
    required this.enabled,
  });

  final XFile file;
  final bool uploading;
  final String statusLabel;
  final Color accentColor;
  final VoidCallback? onRetake;
  final VoidCallback? onRemove;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Stack(
          alignment: Alignment.center,
          children: [
            Container(
              width: 150,
              height: 150,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(color: accentColor, width: 3),
              ),
              child: ClipOval(
                child: Image.file(File(file.path), fit: BoxFit.cover),
              ),
            ),
            if (uploading)
              const CircularProgressIndicator(
                color: Colors.white,
                strokeWidth: 2,
              ),
          ],
        ),
        const SizedBox(height: 10),
        Text(
          statusLabel,
          style: AppTextStyles.label.copyWith(color: accentColor),
        ),
        const SizedBox(height: 6),
        Wrap(
          spacing: 12,
          children: [
            TextButton(
              onPressed: onRetake,
              child: Text(
                'Retake',
                style: AppTextStyles.label.copyWith(
                  color: enabled
                      ? AppColors.textSecondary
                      : AppColors.textMuted,
                ),
              ),
            ),
            TextButton(
              onPressed: onRemove,
              child: Text(
                'Remove',
                style: AppTextStyles.label.copyWith(
                  color: enabled ? AppColors.statusError : AppColors.textMuted,
                ),
              ),
            ),
          ],
        ),
      ],
    );
  }
}

class _PrimaryButton extends StatelessWidget {
  const _PrimaryButton({required this.label, required this.onPressed});

  final String label;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: ElevatedButton(
        onPressed: onPressed,
        style: ElevatedButton.styleFrom(
          backgroundColor: AppColors.postbookPrimary,
          foregroundColor: Colors.white,
          disabledBackgroundColor: AppColors.postbookPrimary.withValues(
            alpha: 0.5,
          ),
          padding: const EdgeInsets.symmetric(vertical: 15),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
        ),
        child: Text(label, style: AppTextStyles.label),
      ),
    );
  }
}

class _SecondaryButton extends StatelessWidget {
  const _SecondaryButton({required this.label, required this.onPressed});

  final String label;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: OutlinedButton(
        onPressed: onPressed,
        style: OutlinedButton.styleFrom(
          foregroundColor: AppColors.textSecondary,
          side: const BorderSide(color: AppColors.borderSubtle),
          padding: const EdgeInsets.symmetric(vertical: 15),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          ),
        ),
        child: Text(label, style: AppTextStyles.label),
      ),
    );
  }
}
