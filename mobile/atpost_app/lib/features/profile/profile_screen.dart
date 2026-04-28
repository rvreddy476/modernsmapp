import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/providers/profile_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Visibility scope for a profile section.
enum SectionPrivacy { public, followers, onlyMe }

extension SectionPrivacyX on SectionPrivacy {
  String get label {
    switch (this) {
      case SectionPrivacy.public:
        return 'Public';
      case SectionPrivacy.followers:
        return 'Followers';
      case SectionPrivacy.onlyMe:
        return 'Only me';
    }
  }

  IconData get icon {
    switch (this) {
      case SectionPrivacy.public:
        return Icons.public_rounded;
      case SectionPrivacy.followers:
        return Icons.people_rounded;
      case SectionPrivacy.onlyMe:
        return Icons.lock_rounded;
    }
  }

  (Color bg, Color fg) get palette {
    switch (this) {
      case SectionPrivacy.public:
        return (const Color(0xFF1A3A1A), const Color(0xFF4ADE80));
      case SectionPrivacy.followers:
        return (const Color(0xFF1A1A3A), const Color(0xFF818CF8));
      case SectionPrivacy.onlyMe:
        return (const Color(0xFF2A2A2A), const Color(0xFF888888));
    }
  }

  SectionPrivacy get next {
    switch (this) {
      case SectionPrivacy.public:
        return SectionPrivacy.followers;
      case SectionPrivacy.followers:
        return SectionPrivacy.onlyMe;
      case SectionPrivacy.onlyMe:
        return SectionPrivacy.public;
    }
  }
}

/// Local privacy/UI state held until a backend persistence layer ships.
class _ProfileLocalState {
  SectionPrivacy aboutPrivacy = SectionPrivacy.public;
  SectionPrivacy educationPrivacy = SectionPrivacy.followers;
  SectionPrivacy workPrivacy = SectionPrivacy.public;
  SectionPrivacy interestsPrivacy = SectionPrivacy.followers;
  SectionPrivacy contactPrivacy = SectionPrivacy.onlyMe;

  bool showOnlineStatus = true;
  bool showInSearch = true;
  bool privateAccount = false;
  bool showEarningsBadge = true;

  /// 0 = All, 1 = Followers, 2 = None
  int allowMessages = 0;
}

class ProfileScreen extends ConsumerStatefulWidget {
  const ProfileScreen({super.key});

  @override
  ConsumerState<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends ConsumerState<ProfileScreen> {
  final _local = _ProfileLocalState();

  static const _bg = Color(0xFF111111);
  static const _bgCard = Color(0xFF1C1C1C);
  static const _divider = Color(0xFF1E1E1E);
  static const _textPrimary = Colors.white;
  static const _textSecondary = Color(0xFFBBBBBB);
  static const _textMuted = Color(0xFF888888);
  static const _textDim = Color(0xFF666666);

  @override
  Widget build(BuildContext context) {
    final profileAsync = ref.watch(profileProvider);

    return Scaffold(
      backgroundColor: _bg,
      body: profileAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(
            color: AppColors.postbookPrimary,
          ),
        ),
        error: (e, _) => _buildError(),
        data: (state) => state.user == null
            ? _buildError()
            : RefreshIndicator(
                color: AppColors.postbookPrimary,
                backgroundColor: _bgCard,
                onRefresh: () =>
                    ref.read(profileProvider.notifier).refresh(),
                child: _buildBody(state.user!, state.posts.length),
              ),
      ),
    );
  }

  Widget _buildBody(User user, int postsCount) {
    return ListView(
      padding: EdgeInsets.zero,
      children: [
        _buildCover(user),
        Transform.translate(
          offset: const Offset(0, -36),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: _buildAvatar(user),
          ),
        ),
        Transform.translate(
          offset: const Offset(0, -28),
          child: _buildHeader(user, postsCount),
        ),
        Transform.translate(
          offset: const Offset(0, -16),
          child: Column(
            children: [
              _divLine(),
              _buildAbout(user),
              _buildEducation(),
              _buildWork(),
              _buildInterests(),
              _buildContact(user),
              _buildPrivacySettings(),
              const SizedBox(height: 32),
            ],
          ),
        ),
      ],
    );
  }

  // ---------------- Cover + Avatar ----------------

  Widget _buildCover(User user) {
    return Stack(
      children: [
        Container(
          height: 120,
          width: double.infinity,
          decoration: BoxDecoration(
            gradient: const LinearGradient(
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
              colors: [
                Color(0xFF1A1040),
                Color(0xFF2D1060),
                Color(0xFF1A2060),
              ],
            ),
            image: user.hasCover
                ? DecorationImage(
                    image: NetworkImage(user.coverUrl!),
                    fit: BoxFit.cover,
                    onError: (_, _) {},
                  )
                : null,
          ),
        ),
        Positioned(
          right: 8,
          bottom: 8,
          child: _circleIconButton(
            icon: Icons.camera_alt_outlined,
            onTap: () => _toast('Cover photo update — coming soon'),
            background: Colors.black.withValues(alpha: 0.55),
            iconSize: 14,
          ),
        ),
      ],
    );
  }

  Widget _buildAvatar(User user) {
    final initials = _initialsFor(user.displayName);
    return SizedBox(
      width: 72,
      height: 72,
      child: Stack(
        clipBehavior: Clip.none,
        children: [
          Container(
            width: 72,
            height: 72,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              gradient: const LinearGradient(
                begin: Alignment.topLeft,
                end: Alignment.bottomRight,
                colors: [Color(0xFF5C4AFF), Color(0xFFFF6B35)],
              ),
              border: Border.all(color: _bg, width: 3),
              image: user.hasAvatar
                  ? DecorationImage(
                      image: NetworkImage(user.avatarUrl),
                      fit: BoxFit.cover,
                    )
                  : null,
            ),
            alignment: Alignment.center,
            child: user.hasAvatar
                ? null
                : Text(
                    initials,
                    style: const TextStyle(
                      fontSize: 22,
                      fontWeight: FontWeight.w500,
                      color: Colors.white,
                    ),
                  ),
          ),
          Positioned(
            right: 2,
            bottom: 2,
            child: _circleIconButton(
              icon: Icons.camera_alt_rounded,
              onTap: () => _toast('Avatar update — coming soon'),
              background: AppColors.postbookPrimary,
              size: 22,
              iconSize: 11,
            ),
          ),
        ],
      ),
    );
  }

  // ---------------- Header ----------------

  Widget _buildHeader(User user, int postsCount) {
    final handle = user.username.isNotEmpty ? '@${user.username}' : '@user';
    final loc = user.location?.trim();
    final handleLine =
        loc == null || loc.isEmpty ? handle : '$handle · $loc';
    final bio = (user.bio == null || user.bio!.trim().isEmpty)
        ? 'No bio yet — tap "Edit profile" to add one.'
        : user.bio!;

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            user.displayName,
            style: const TextStyle(
              color: _textPrimary,
              fontSize: 18,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 2),
          Text(
            handleLine,
            style: const TextStyle(color: _textDim, fontSize: 12),
          ),
          const SizedBox(height: 8),
          Text(
            bio,
            style: const TextStyle(
              color: _textSecondary,
              fontSize: 13,
              height: 1.5,
            ),
          ),
          const SizedBox(height: 14),
          Row(
            children: [
              _stat(_formatCount(user.followerCount), 'Followers'),
              const SizedBox(width: 24),
              _stat(_formatCount(user.followingCount), 'Following'),
              const SizedBox(width: 24),
              _stat(
                _formatCount(
                  user.postCount > 0 ? user.postCount : postsCount,
                ),
                'Posts',
              ),
            ],
          ),
          const SizedBox(height: 14),
          Row(
            children: [
              Expanded(
                child: ElevatedButton(
                  onPressed: () => context.push('/settings/profile'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                    foregroundColor: Colors.white,
                    padding: const EdgeInsets.symmetric(vertical: 11),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(10),
                    ),
                    elevation: 0,
                  ),
                  child: const Text(
                    'Edit profile',
                    style: TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              _squareIconButton(
                icon: Icons.photo_library_outlined,
                onTap: () => context.push('/profile/media'),
              ),
              const SizedBox(width: 8),
              _squareIconButton(
                icon: Icons.share_outlined,
                onTap: () => _toast('Profile share — coming soon'),
              ),
              const SizedBox(width: 8),
              _squareIconButton(
                icon: Icons.settings_outlined,
                onTap: () => context.push('/settings'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _stat(String value, String label) {
    return Column(
      children: [
        Text(
          value,
          style: const TextStyle(
            color: _textPrimary,
            fontSize: 15,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 1),
        Text(
          label,
          style: const TextStyle(color: _textDim, fontSize: 10),
        ),
      ],
    );
  }

  // ---------------- About ----------------

  Widget _buildAbout(User user) {
    final about = (user.bio == null || user.bio!.trim().isEmpty)
        ? 'Tell people a bit about yourself. Tap the edit pencil to add an "about" blurb that appears on your profile.'
        : user.bio!;
    return _section(
      title: 'About',
      icon: Icons.info_outline_rounded,
      privacy: _local.aboutPrivacy,
      onCyclePrivacy: () => setState(
          () => _local.aboutPrivacy = _local.aboutPrivacy.next),
      onEdit: () => context.push('/settings/profile'),
      child: Text(
        about,
        style: const TextStyle(
          color: Color(0xFFAAAAAA),
          fontSize: 13,
          height: 1.6,
        ),
      ),
    );
  }

  // ---------------- Education ----------------

  Widget _buildEducation() {
    return _section(
      title: 'Education',
      icon: Icons.school_outlined,
      privacy: _local.educationPrivacy,
      onCyclePrivacy: () => setState(
          () => _local.educationPrivacy = _local.educationPrivacy.next),
      onEdit: () => _toast('Edit education — coming soon'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _emptyHint('Add the schools or colleges you attended.'),
          _addButton('Add education',
              onTap: () => _toast('Add education — coming soon')),
        ],
      ),
    );
  }

  // ---------------- Work ----------------

  Widget _buildWork() {
    return _section(
      title: 'Work experience',
      icon: Icons.work_outline_rounded,
      privacy: _local.workPrivacy,
      onCyclePrivacy: () =>
          setState(() => _local.workPrivacy = _local.workPrivacy.next),
      onEdit: () => _toast('Edit work — coming soon'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _emptyHint('Add roles, companies, and what you build.'),
          _addButton('Add experience',
              onTap: () => _toast('Add experience — coming soon')),
        ],
      ),
    );
  }

  // ---------------- Interests ----------------

  Widget _buildInterests() {
    return _section(
      title: 'Interests & hobbies',
      icon: Icons.favorite_border_rounded,
      privacy: _local.interestsPrivacy,
      onCyclePrivacy: () => setState(
          () => _local.interestsPrivacy = _local.interestsPrivacy.next),
      onEdit: () => _toast('Edit interests — coming soon'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _emptyHint('Tag what you love — gets you better recommendations.'),
          _addButton('Add interests',
              onTap: () => _toast('Add interests — coming soon')),
        ],
      ),
    );
  }

  // ---------------- Contact ----------------

  Widget _buildContact(User user) {
    final rows = <Widget>[];

    // We don't have email/phone on the User model yet, so contact is
    // entirely empty for now. Show the empty state + add prompt.
    rows.add(_emptyHint(
        'Share an email or phone visible to people you choose.'));
    rows.add(_addButton('Add contact',
        onTap: () => _toast('Add contact info — coming soon')));

    return _section(
      title: 'Contact',
      icon: Icons.call_outlined,
      privacy: _local.contactPrivacy,
      onCyclePrivacy: () => setState(
          () => _local.contactPrivacy = _local.contactPrivacy.next),
      onEdit: () => _toast('Edit contact — coming soon'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: rows,
      ),
    );
  }

  // ---------------- Privacy settings ----------------

  Widget _buildPrivacySettings() {
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: const [
              Icon(Icons.settings_outlined, size: 14, color: _textMuted),
              SizedBox(width: 6),
              Text(
                'Privacy settings',
                style: TextStyle(
                  color: _textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          _toggleRow(
            label: 'Show online status',
            sub: "Let followers see when you're active",
            value: _local.showOnlineStatus,
            onChanged: (v) => setState(() => _local.showOnlineStatus = v),
          ),
          _settingsRow(
            label: 'Allow messages',
            sub: 'Who can message you directly',
            trailing: _segmented(
              options: const ['All', 'Followers', 'None'],
              selectedIndex: _local.allowMessages,
              onChanged: (i) => setState(() => _local.allowMessages = i),
            ),
          ),
          _toggleRow(
            label: 'Show in search',
            sub: 'Appear in people search results',
            value: _local.showInSearch,
            onChanged: (v) => setState(() => _local.showInSearch = v),
          ),
          _toggleRow(
            label: 'Private account',
            sub: 'Only followers see your posts',
            value: _local.privateAccount,
            onChanged: (v) => setState(() => _local.privateAccount = v),
          ),
          _toggleRow(
            label: 'Show earnings badge',
            sub: 'Display creator earnings on profile',
            value: _local.showEarningsBadge,
            onChanged: (v) => setState(() => _local.showEarningsBadge = v),
            isLast: true,
          ),
        ],
      ),
    );
  }

  // ---------------- Section helpers ----------------

  Widget _section({
    required String title,
    required IconData icon,
    required SectionPrivacy privacy,
    required VoidCallback onCyclePrivacy,
    required VoidCallback onEdit,
    required Widget child,
  }) {
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
      decoration: const BoxDecoration(
        border: Border(bottom: BorderSide(color: _divider)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(icon, size: 14, color: _textMuted),
              const SizedBox(width: 6),
              Text(
                title,
                style: const TextStyle(
                  color: _textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                ),
              ),
              const Spacer(),
              _privacyPill(privacy: privacy, onTap: onCyclePrivacy),
              const SizedBox(width: 8),
              GestureDetector(
                onTap: onEdit,
                child: const Icon(
                  Icons.edit_outlined,
                  size: 13,
                  color: Color(0xFF555555),
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          child,
        ],
      ),
    );
  }

  Widget _privacyPill({
    required SectionPrivacy privacy,
    required VoidCallback onTap,
  }) {
    final (bg, fg) = privacy.palette;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(20),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(privacy.icon, size: 9, color: fg),
            const SizedBox(width: 3),
            Text(
              privacy.label,
              style: TextStyle(
                color: fg,
                fontSize: 10,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _emptyHint(String text) {
    return Text(
      text,
      style: const TextStyle(
        color: _textMuted,
        fontSize: 12,
        height: 1.5,
      ),
    );
  }

  Widget _addButton(String label, {required VoidCallback onTap}) {
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: GestureDetector(
        onTap: onTap,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.add_rounded,
                size: 14, color: AppColors.postbookPrimary),
            const SizedBox(width: 4),
            Text(
              label,
              style: const TextStyle(
                color: AppColors.postbookPrimary,
                fontSize: 12,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }

  // ---------------- Settings row helpers ----------------

  Widget _settingsRow({
    required String label,
    required String sub,
    required Widget trailing,
    bool isLast = false,
  }) {
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 10),
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(
            color: isLast ? Colors.transparent : _divider,
            width: 0.5,
          ),
        ),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  label,
                  style: const TextStyle(
                    color: Color(0xFFDDDDDD),
                    fontSize: 13,
                  ),
                ),
                const SizedBox(height: 1),
                Text(
                  sub,
                  style: const TextStyle(
                    color: Color(0xFF555555),
                    fontSize: 11,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 12),
          trailing,
        ],
      ),
    );
  }

  Widget _toggleRow({
    required String label,
    required String sub,
    required bool value,
    required ValueChanged<bool> onChanged,
    bool isLast = false,
  }) {
    return _settingsRow(
      label: label,
      sub: sub,
      isLast: isLast,
      trailing: _customSwitch(value: value, onChanged: onChanged),
    );
  }

  Widget _customSwitch({
    required bool value,
    required ValueChanged<bool> onChanged,
  }) {
    return GestureDetector(
      onTap: () => onChanged(!value),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 180),
        width: 36,
        height: 20,
        decoration: BoxDecoration(
          color:
              value ? AppColors.postbookPrimary : const Color(0xFF333333),
          borderRadius: BorderRadius.circular(10),
        ),
        child: AnimatedAlign(
          duration: const Duration(milliseconds: 180),
          alignment: value ? Alignment.centerRight : Alignment.centerLeft,
          child: Padding(
            padding: const EdgeInsets.all(2),
            child: Container(
              width: 16,
              height: 16,
              decoration: const BoxDecoration(
                color: Colors.white,
                shape: BoxShape.circle,
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _segmented({
    required List<String> options,
    required int selectedIndex,
    required ValueChanged<int> onChanged,
  }) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: List.generate(options.length, (i) {
        final selected = i == selectedIndex;
        return Padding(
          padding: EdgeInsets.only(left: i == 0 ? 0 : 4),
          child: GestureDetector(
            onTap: () => onChanged(i),
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 160),
              padding: const EdgeInsets.symmetric(
                  horizontal: 8, vertical: 3),
              decoration: BoxDecoration(
                color: selected
                    ? AppColors.postbookPrimary
                    : const Color(0xFF252525),
                borderRadius: BorderRadius.circular(20),
              ),
              child: Text(
                options[i],
                style: TextStyle(
                  color:
                      selected ? Colors.white : const Color(0xFF666666),
                  fontSize: 10,
                  fontWeight:
                      selected ? FontWeight.w600 : FontWeight.w500,
                ),
              ),
            ),
          ),
        );
      }),
    );
  }

  // ---------------- Misc ----------------

  Widget _circleIconButton({
    required IconData icon,
    required VoidCallback onTap,
    required Color background,
    double size = 28,
    double iconSize = 13,
  }) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: size,
        height: size,
        decoration: BoxDecoration(
          color: background,
          shape: BoxShape.circle,
        ),
        alignment: Alignment.center,
        child: Icon(icon, color: Colors.white, size: iconSize),
      ),
    );
  }

  Widget _squareIconButton({
    required IconData icon,
    required VoidCallback onTap,
  }) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 38,
        height: 38,
        decoration: BoxDecoration(
          color: const Color(0xFF252525),
          borderRadius: BorderRadius.circular(10),
        ),
        alignment: Alignment.center,
        child: Icon(icon, color: Colors.white, size: 15),
      ),
    );
  }

  Widget _divLine() => const Divider(height: 1, thickness: 1, color: Color(0xFF222222));

  Widget _buildError() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline,
              color: Colors.redAccent, size: 40),
          const SizedBox(height: 12),
          const Text('Could not load profile.',
              style: TextStyle(color: Colors.white70)),
          const SizedBox(height: 8),
          TextButton(
            onPressed: () =>
                ref.read(profileProvider.notifier).refresh(),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }

  void _toast(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: const Color(0xFF1F1F1F),
        behavior: SnackBarBehavior.floating,
        duration: const Duration(seconds: 2),
      ),
    );
  }

  String _initialsFor(String displayName) {
    final parts = displayName
        .trim()
        .split(RegExp(r'\s+'))
        .where((p) => p.isNotEmpty)
        .toList();
    if (parts.isEmpty) return 'U';
    if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
    return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
  }

  String _formatCount(int n) {
    if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}k';
    return n.toString();
  }
}
