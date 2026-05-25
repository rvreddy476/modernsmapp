import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

// formerly PostMatchProfileScreen
class PulseProfileScreen extends ConsumerStatefulWidget {
  const PulseProfileScreen({super.key});

  @override
  ConsumerState<PulseProfileScreen> createState() =>
      _PulseProfileScreenState();
}

class _PulseProfileScreenState
    extends ConsumerState<PulseProfileScreen> {
  final _nameController = TextEditingController();
  final _bioController = TextEditingController();
  final _cityController = TextEditingController();
  final _occupationController = TextEditingController();
  final _educationController = TextEditingController();
  final _religionController = TextEditingController();
  final _heightController = TextEditingController();

  bool _loading = true;
  bool _savingProfile = false;
  bool _savingPrefs = false;
  String _error = '';

  PulseProfile? _profile;
  PulsePreferences? _preferences;
  List<PulsePhoto> _photos = const [];
  String _intent = 'figuring_out';
  String _prefGender = 'everyone';
  double _minAge = 18;
  double _maxAge = 35;
  double _distanceKm = 50;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _nameController.dispose();
    _bioController.dispose();
    _cityController.dispose();
    _occupationController.dispose();
    _educationController.dispose();
    _religionController.dispose();
    _heightController.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final auth = ref.read(pulseAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    if (!auth.isReady) {
      context.go('/pulse/onboarding');
      return;
    }

    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      final repo = ref.read(pulseRepositoryProvider);
      final results = await Future.wait([
        repo.getProfile(),
        repo.getPreferences(),
        repo.getPhotos(),
      ]);
      if (!mounted) return;
      _profile = results[0] as PulseProfile?;
      _preferences = results[1] as PulsePreferences?;
      _photos = results[2] as List<PulsePhoto>;

      _nameController.text = _profile?.firstName ?? '';
      _bioController.text = _profile?.bio ?? '';
      _cityController.text = _profile?.city ?? '';
      _occupationController.text = _profile?.occupation ?? '';
      _educationController.text = _profile?.education ?? '';
      _religionController.text = _profile?.religion ?? '';
      _heightController.text = (_profile?.heightCm ?? '').toString();
      _intent = _profile?.relationshipIntent ?? 'figuring_out';
      _prefGender = _preferences?.interestedInGender ?? 'everyone';
      _minAge = (_preferences?.minAge ?? 18).toDouble();
      _maxAge = (_preferences?.maxAge ?? 35).toDouble();
      _distanceKm = (_preferences?.distanceKm ?? 50).toDouble();

      setState(() => _loading = false);
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Could not load your Pulse profile.';
        _loading = false;
      });
    }
  }

  Future<void> _saveProfile() async {
    if (_profile == null) return;
    setState(() => _savingProfile = true);
    try {
      await ref.read(pulseRepositoryProvider).updateProfile({
        'first_name': _nameController.text.trim(),
        'date_of_birth': _profile!.dateOfBirth,
        'gender': _profile!.gender,
        'looking_for': _profile!.lookingFor,
        'relationship_intent': _intent,
        'bio': _bioController.text.trim().isEmpty
            ? null
            : _bioController.text.trim(),
        'city': _cityController.text.trim().isEmpty
            ? null
            : _cityController.text.trim(),
        'occupation': _occupationController.text.trim().isEmpty
            ? null
            : _occupationController.text.trim(),
        'education': _educationController.text.trim().isEmpty
            ? null
            : _educationController.text.trim(),
        'religion': _religionController.text.trim().isEmpty
            ? null
            : _religionController.text.trim(),
        'height_cm': int.tryParse(_heightController.text.trim()),
      });
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Profile updated.')));
      }
      await _load();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save profile right now.')),
      );
    } finally {
      if (mounted) setState(() => _savingProfile = false);
    }
  }

  Future<void> _savePreferences() async {
    setState(() => _savingPrefs = true);
    try {
      await ref.read(pulseRepositoryProvider).updatePreferences({
        'min_age': _minAge.round(),
        'max_age': _maxAge.round(),
        'distance_km': _distanceKm.round(),
        'interested_in_gender': _prefGender,
        'relationship_intent': _intent,
      });
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Preferences saved.')));
      }
      await _load();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not save preferences.')),
      );
    } finally {
      if (mounted) setState(() => _savingPrefs = false);
    }
  }

  Future<void> _deletePhoto(String photoId) async {
    await ref.read(pulseRepositoryProvider).deletePhoto(photoId);
    await _load();
  }

  Future<void> _logout() async {
    await ref.read(pulseRepositoryProvider).logout();
    if (!mounted) return;
    context.go('/pulse');
  }

  @override
  Widget build(BuildContext context) {
    final primaryPhoto = _photos.cast<PulsePhoto?>().firstWhere(
      (photo) => photo?.isPrimary ?? false,
      orElse: () => null,
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Your Profile', style: AppTextStyles.h2),
        actions: [
          IconButton(
            onPressed: () => context.push('/pulse/discover'),
            icon: const Icon(
              Icons.explore_outlined,
              color: AppColors.textPrimary,
            ),
          ),
          IconButton(
            onPressed: () => context.push('/pulse/matches'),
            icon: const Icon(
              Icons.favorite_border,
              color: AppColors.textPrimary,
            ),
          ),
        ],
      ),
      body: _loading
          ? const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            )
          : _error.isNotEmpty
          ? Center(child: Text(_error, style: AppTextStyles.body))
          : ListView(
              padding: AppSpacing.pagePadding.copyWith(bottom: 30),
              children: [
                Center(
                  child: Column(
                    children: [
                      CircleAvatar(
                        radius: 42,
                        backgroundColor: AppColors.postbookPrimary.withValues(
                          alpha: 0.18,
                        ),
                        backgroundImage: primaryPhoto?.mediaUrl == null
                            ? null
                            : NetworkImage(primaryPhoto!.mediaUrl!),
                        child: primaryPhoto?.mediaUrl == null
                            ? Text(
                                (_profile?.firstName ?? 'P').substring(0, 1),
                                style: AppTextStyles.h1.copyWith(
                                  fontSize: 26,
                                  color: AppColors.postbookPrimary,
                                ),
                              )
                            : null,
                      ),
                      const SizedBox(height: 8),
                      Text(
                        '${_profile?.profileCompletionPercent ?? 0}% complete',
                        style: AppTextStyles.labelSmall.copyWith(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 18),
                _Section(
                  title: 'Profile',
                  child: Column(
                    children: [
                      _TextField(controller: _nameController, label: 'Name'),
                      _TextField(
                        controller: _bioController,
                        label: 'Bio',
                        maxLines: 3,
                      ),
                      _TextField(controller: _cityController, label: 'City'),
                      _TextField(
                        controller: _occupationController,
                        label: 'Occupation',
                      ),
                      _TextField(
                        controller: _educationController,
                        label: 'Education',
                      ),
                      _TextField(
                        controller: _religionController,
                        label: 'Religion',
                      ),
                      _TextField(
                        controller: _heightController,
                        label: 'Height (cm)',
                        keyboardType: TextInputType.number,
                      ),
                      const SizedBox(height: 8),
                      _ChoiceRow(
                        title: 'Intent',
                        value: _intent,
                        options: const [
                          ('long_term', 'Long-term'),
                          ('marriage', 'Marriage'),
                          ('casual', 'Casual'),
                          ('figuring_out', 'Figuring Out'),
                        ],
                        onChanged: (value) => setState(() => _intent = value),
                      ),
                      const SizedBox(height: 12),
                      ElevatedButton(
                        onPressed: _savingProfile ? null : _saveProfile,
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.postbookPrimary,
                        ),
                        child: Text(
                          _savingProfile ? 'Saving...' : 'Save Profile',
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 12),
                _Section(
                  title: 'Preferences',
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      _ChoiceRow(
                        title: 'Interested In',
                        value: _prefGender,
                        options: const [
                          ('everyone', 'Everyone'),
                          ('male', 'Men'),
                          ('female', 'Women'),
                        ],
                        onChanged: (value) =>
                            setState(() => _prefGender = value),
                      ),
                      const SizedBox(height: 12),
                      Text(
                        'Min Age: ${_minAge.round()}',
                        style: AppTextStyles.label,
                      ),
                      Slider(
                        value: _minAge,
                        min: 18,
                        max: 70,
                        activeColor: AppColors.postbookPrimary,
                        onChanged: (value) => setState(() => _minAge = value),
                      ),
                      Text(
                        'Max Age: ${_maxAge.round()}',
                        style: AppTextStyles.label,
                      ),
                      Slider(
                        value: _maxAge < _minAge ? _minAge : _maxAge,
                        min: _minAge,
                        max: 70,
                        activeColor: AppColors.postbookPrimary,
                        onChanged: (value) => setState(() => _maxAge = value),
                      ),
                      Text(
                        'Max Distance: ${_distanceKm.round()} km',
                        style: AppTextStyles.label,
                      ),
                      Slider(
                        value: _distanceKm,
                        min: 5,
                        max: 200,
                        activeColor: AppColors.postbookPrimary,
                        onChanged: (value) =>
                            setState(() => _distanceKm = value),
                      ),
                      const SizedBox(height: 8),
                      ElevatedButton(
                        onPressed: _savingPrefs ? null : _savePreferences,
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.postbookPrimary,
                        ),
                        child: Text(
                          _savingPrefs ? 'Saving...' : 'Save Preferences',
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 12),
                _Section(
                  title: 'Photos',
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: _photos.isEmpty
                        ? [
                            Text(
                              'No photos uploaded yet.',
                              style: AppTextStyles.bodySmall,
                            ),
                          ]
                        : _photos.map((photo) {
                            // P0-6 owner view: badge pending/rejected
                            // photos. The discovery and detail surfaces
                            // for OTHER viewers already filter to
                            // approved (backend enforces) — here we
                            // surface the moderation status to the owner
                            // so they know what's live and what isn't.
                            final status = photo.moderationStatus;
                            final isApproved = status == 'approved';
                            final isPending = status == 'pending';
                            final isRejected = status == 'rejected';
                            return Stack(
                              children: [
                                ClipRRect(
                                  borderRadius: BorderRadius.circular(16),
                                  child: photo.mediaUrl == null
                                      ? Container(
                                          width: 92,
                                          height: 124,
                                          color: AppColors.bgTertiary,
                                        )
                                      : Stack(
                                          children: [
                                            Image.network(
                                              photo.mediaUrl!,
                                              width: 92,
                                              height: 124,
                                              fit: BoxFit.cover,
                                            ),
                                            if (!isApproved)
                                              Positioned.fill(
                                                child: Container(
                                                  color: Colors.black54,
                                                ),
                                              ),
                                          ],
                                        ),
                                ),
                                if (isPending || isRejected)
                                  Positioned(
                                    left: 0,
                                    right: 0,
                                    bottom: 0,
                                    child: Container(
                                      padding: const EdgeInsets.symmetric(
                                        horizontal: 6,
                                        vertical: 4,
                                      ),
                                      decoration: BoxDecoration(
                                        color: isRejected
                                            ? AppColors.statusError
                                                .withValues(alpha: 0.9)
                                            : AppColors.statusWarning
                                                .withValues(alpha: 0.9),
                                        borderRadius: const BorderRadius
                                            .vertical(
                                          bottom: Radius.circular(16),
                                        ),
                                      ),
                                      child: Text(
                                        isRejected
                                            ? 'Rejected'
                                            : 'Pending review',
                                        textAlign: TextAlign.center,
                                        style: AppTextStyles.labelTiny.copyWith(
                                          color: Colors.white,
                                          fontWeight: FontWeight.w700,
                                        ),
                                      ),
                                    ),
                                  ),
                                Positioned(
                                  top: 4,
                                  right: 4,
                                  child: GestureDetector(
                                    onTap: () => _deletePhoto(photo.id),
                                    child: Container(
                                      width: 24,
                                      height: 24,
                                      decoration: BoxDecoration(
                                        color: Colors.black54,
                                        borderRadius: BorderRadius.circular(
                                          999,
                                        ),
                                      ),
                                      child: const Icon(
                                        Icons.close,
                                        size: 14,
                                        color: Colors.white,
                                      ),
                                    ),
                                  ),
                                ),
                              ],
                            );
                          }).toList(),
                  ),
                ),
                const SizedBox(height: 18),
                TextButton(
                  onPressed: _logout,
                  child: Text(
                    'Sign Out',
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.statusError,
                    ),
                  ),
                ),
              ],
            ),
    );
  }
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: AppTextStyles.h3),
          const SizedBox(height: 12),
          child,
        ],
      ),
    );
  }
}

class _TextField extends StatelessWidget {
  const _TextField({
    required this.controller,
    required this.label,
    this.maxLines = 1,
    this.keyboardType,
  });

  final TextEditingController controller;
  final String label;
  final int maxLines;
  final TextInputType? keyboardType;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: TextField(
        controller: controller,
        maxLines: maxLines,
        keyboardType: keyboardType,
        style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
        decoration: InputDecoration(
          labelText: label,
          labelStyle: AppTextStyles.labelSmall,
          filled: true,
          fillColor: AppColors.bgTertiary,
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            borderSide: BorderSide(color: AppColors.borderSubtle),
          ),
          enabledBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            borderSide: BorderSide(color: AppColors.borderSubtle),
          ),
        ),
      ),
    );
  }
}

class _ChoiceRow extends StatelessWidget {
  const _ChoiceRow({
    required this.title,
    required this.value,
    required this.options,
    required this.onChanged,
  });

  final String title;
  final String value;
  final List<(String, String)> options;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: AppTextStyles.label),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: options.map((option) {
            final active = value == option.$1;
            return InkWell(
              onTap: () => onChanged(option.$1),
              borderRadius: BorderRadius.circular(999),
              child: Ink(
                padding: const EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 10,
                ),
                decoration: BoxDecoration(
                  color: active
                      ? AppColors.postbookPrimary.withValues(alpha: 0.16)
                      : AppColors.bgTertiary,
                  borderRadius: BorderRadius.circular(999),
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
            );
          }).toList(),
        ),
      ],
    );
  }
}
