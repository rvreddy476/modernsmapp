import 'dart:math' as math;

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/social/widgets/friends_common.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// SURFACE 2 — Trusted Circle bottom sheet.
///
/// Shows the inner circle as an orbit visual, the member list with a
/// per-member menu, a friend picker to add people, and the four `tc_*`
/// privacy toggles backed by [userSettingsProvider]. All add/remove and
/// settings wiring reuses the existing user-repository methods.
void showTrustedCircleSheet(
  BuildContext context, {
  required List<User> friends,
}) {
  showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (_) => TrustedCircleSheet(friends: friends),
  );
}

/// One Trusted Circle privacy toggle, backed by the user settings API.
class _TcToggle {
  const _TcToggle(this.key, this.icon, this.label, this.sub);
  final String key;
  final IconData icon;
  final String label;
  final String sub;
}

const List<_TcToggle> _tcToggles = [
  _TcToggle(
    'tc_close_friends_posts',
    Icons.auto_awesome_rounded,
    'Close-friends Flicks & stories',
    'Only this circle sees them',
  ),
  _TcToggle(
    'tc_location_pings',
    Icons.location_on_outlined,
    'Live location pings',
    'Share where you are with the circle',
  ),
  _TcToggle(
    'tc_after_hours_posts',
    Icons.nightlight_round,
    'After-hours posts',
    'Late-night posts stay inside the circle',
  ),
  _TcToggle(
    'tc_audio_room_invite',
    Icons.graphic_eq_rounded,
    'Audio Room auto-invite',
    'Pull the circle in when you go live',
  ),
];

class TrustedCircleSheet extends ConsumerStatefulWidget {
  const TrustedCircleSheet({super.key, required this.friends});

  final List<User> friends;

  @override
  ConsumerState<TrustedCircleSheet> createState() => _TrustedCircleSheetState();
}

class _TrustedCircleSheetState extends ConsumerState<TrustedCircleSheet> {
  final Set<String> _busy = {};

  /// Local optimistic copy of the four Trusted Circle setting flags.
  final Map<String, bool> _tcSettings = {};
  final Set<String> _tcBusy = {};

  /// Optimistic close-friend membership; seeded from [closeFriendsProvider].
  Set<String>? _closeIds;

  Set<String> _resolveCloseIds() {
    // Seed once from the provider, then keep the local optimistic copy.
    final fromProvider = (ref.read(closeFriendsProvider).valueOrNull ??
            const <User>[])
        .map((u) => u.id)
        .toSet();
    return _closeIds ??= fromProvider;
  }

  Future<void> _toggleMember(User user, {required bool add}) async {
    if (_busy.contains(user.id)) return;
    final ids = _resolveCloseIds();
    setState(() {
      _busy.add(user.id);
      if (add) {
        ids.add(user.id);
      } else {
        ids.remove(user.id);
      }
    });
    try {
      final repo = ref.read(userRepositoryProvider);
      if (add) {
        await repo.addCloseFriend(user.id);
      } else {
        await repo.removeCloseFriend(user.id);
      }
      ref.invalidate(closeFriendsProvider);
    } catch (_) {
      if (mounted) {
        setState(() {
          if (add) {
            ids.remove(user.id);
          } else {
            ids.add(user.id);
          }
        });
        _snack('Could not update your Trusted Circle.');
      }
    } finally {
      if (mounted) setState(() => _busy.remove(user.id));
    }
  }

  Future<void> _toggleSetting(String key) async {
    if (_tcBusy.contains(key)) return;
    final previous = _tcSettings[key] ?? false;
    setState(() {
      _tcBusy.add(key);
      _tcSettings[key] = !previous;
    });
    try {
      await ref
          .read(userRepositoryProvider)
          .updateUserSettings({key: !previous});
      ref.invalidate(userSettingsProvider);
    } catch (_) {
      if (mounted) {
        setState(() => _tcSettings[key] = previous);
        _snack('Could not update that setting.');
      }
    } finally {
      if (mounted) setState(() => _tcBusy.remove(key));
    }
  }

  void _snack(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  /// Opens a picker of friends not yet in the circle.
  void _openAddPicker(Set<String> closeIds) {
    final candidates =
        widget.friends.where((u) => !closeIds.contains(u.id)).toList();
    if (candidates.isEmpty) {
      _snack('Everyone you can add is already in your circle.');
      return;
    }
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (_) => _AddToCircleSheet(
        candidates: candidates,
        onAdd: (u) => _toggleMember(u, add: true),
      ),
    );
  }

  void _memberMenu(User user) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (sheetCtx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const SizedBox(height: 12),
            ListTile(
              leading: const Icon(Icons.person_remove_outlined,
                  color: AppColors.statusError),
              title: const Text(
                'Remove from circle',
                style: TextStyle(color: AppColors.textPrimary),
              ),
              onTap: () {
                Navigator.of(sheetCtx).pop();
                _toggleMember(user, add: false);
              },
            ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Seed optimistic toggle map from the latest settings snapshot.
    ref.listen<AsyncValue<Map<String, dynamic>>>(userSettingsProvider,
        (_, next) {
      final data = next.valueOrNull;
      if (data == null) return;
      setState(() {
        for (final t in _tcToggles) {
          if (!_tcBusy.contains(t.key)) {
            _tcSettings[t.key] = data[t.key] == true;
          }
        }
      });
    });
    final settingsSnapshot = ref.read(userSettingsProvider).valueOrNull;
    if (settingsSnapshot != null && _tcSettings.isEmpty) {
      for (final t in _tcToggles) {
        _tcSettings[t.key] = settingsSnapshot[t.key] == true;
      }
    }

    final closeFriendsAsync = ref.watch(closeFriendsProvider);
    final providerMembers = closeFriendsAsync.valueOrNull ?? const <User>[];
    final closeIds = _resolveCloseIds();

    // Merge provider members with anyone added optimistically this session.
    final byId = {for (final u in providerMembers) u.id: u};
    for (final f in widget.friends) {
      if (closeIds.contains(f.id)) byId[f.id] = f;
    }
    final members = byId.values.where((u) => closeIds.contains(u.id)).toList();
    final k = members.length;

    return DraggableScrollableSheet(
      initialChildSize: 0.82,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (context, controller) {
        return Container(
          decoration: const BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
          ),
          child: Column(
            children: [
              const Center(child: SheetGrabber()),
              _header(context, k),
              const Divider(height: 1, color: AppColors.borderSubtle),
              Expanded(
                child: ListView(
                  controller: controller,
                  padding: const EdgeInsets.only(bottom: 24),
                  children: [
                    const SizedBox(height: 18),
                    Center(child: _OrbitVisual(members: members)),
                    const SizedBox(height: 18),
                    _membersHeader(closeIds, k),
                    if (members.isEmpty)
                      _emptyHint(
                        'No one yet — add the people you trust most.',
                      )
                    else
                      ...members.map(_memberRow),
                    const SizedBox(height: 6),
                    const Padding(
                      padding: EdgeInsets.fromLTRB(18, 12, 18, 6),
                      child: EyebrowLabel('WHAT THEY ALONE SEE'),
                    ),
                    closeFriendsAsync.hasError
                        ? _emptyHint('Could not load your circle.')
                        : const SizedBox.shrink(),
                    ..._tcToggles.map(_tcToggleRow),
                    const SizedBox(height: 14),
                    _footer(),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  Widget _header(BuildContext context, int k) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 10, 14, 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 38,
            height: 38,
            decoration: BoxDecoration(
              color: AppColors.posttubePrimary.withValues(alpha: 0.14),
              borderRadius: BorderRadius.circular(11),
            ),
            alignment: Alignment.center,
            child: const Icon(Icons.verified_user_rounded,
                size: 20, color: AppColors.posttubePrimary),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  'Trusted Circle',
                  style: TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 18,
                    fontWeight: FontWeight.w800,
                    letterSpacing: -0.3,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  'Your inner $k — they see what others can’t.',
                  style: const TextStyle(
                    color: AppColors.textTertiary,
                    fontSize: 12,
                    height: 1.4,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          CircleIconButton(
            icon: Icons.close_rounded,
            onTap: () => Navigator.of(context).pop(),
          ),
        ],
      ),
    );
  }

  Widget _membersHeader(Set<String> closeIds, int k) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 0, 18, 6),
      child: Row(
        children: [
          Expanded(child: EyebrowLabel('MEMBERS · $k OF 10')),
          GestureDetector(
            onTap: () => _openAddPicker(closeIds),
            child: Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(100),
                border: Border.all(color: AppColors.borderMedium),
              ),
              child: const Text(
                '+ Add',
                style: TextStyle(
                  color: AppColors.posttubePrimary,
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _memberRow(User u) {
    final busy = _busy.contains(u.id);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 5),
      child: Row(
        children: [
          FriendAvatar(
            name: u.displayName,
            url: u.hasAvatar ? u.avatarUrl : null,
            size: 42,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  u.displayName,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 1),
                Text(
                  '@${u.username}',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppColors.textTertiary,
                    fontSize: 11,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          if (busy)
            const SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          else
            GestureDetector(
              onTap: () => _memberMenu(u),
              child: const Padding(
                padding: EdgeInsets.all(4),
                child: Icon(Icons.more_horiz_rounded,
                    size: 20, color: AppColors.textMuted),
              ),
            ),
        ],
      ),
    );
  }

  Widget _tcToggleRow(_TcToggle toggle) {
    final value = _tcSettings[toggle.key] ?? false;
    final busy = _tcBusy.contains(toggle.key);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 5),
      child: Row(
        children: [
          Container(
            width: 34,
            height: 34,
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(10),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            alignment: Alignment.center,
            child: Icon(toggle.icon,
                size: 16, color: AppColors.textSecondary),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  toggle.label,
                  style: const TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 1),
                Text(
                  toggle.sub,
                  style: const TextStyle(
                    color: AppColors.textMuted,
                    fontSize: 11,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          if (busy)
            const SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          else
            Switch(
              value: value,
              activeThumbColor: AppColors.postbookPrimary,
              onChanged: (_) => _toggleSetting(toggle.key),
            ),
        ],
      ),
    );
  }

  Widget _footer() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 4, 18, 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.lock_outline_rounded,
              size: 13, color: AppColors.textMuted),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Adds are silent. They never get notified they’re in '
              'your circle.',
              style: TextStyle(
                color: AppColors.textMuted,
                fontSize: 11,
                height: 1.45,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _emptyHint(String text) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 4, 18, 8),
      child: Text(
        text,
        style: const TextStyle(color: AppColors.textMuted, fontSize: 12),
      ),
    );
  }
}

/// The orbit visual — a central "you" avatar with the close-friend
/// avatars scattered on two concentric rings around it.
class _OrbitVisual extends StatelessWidget {
  const _OrbitVisual({required this.members});

  final List<User> members;

  @override
  Widget build(BuildContext context) {
    const size = 200.0;
    const center = size / 2;
    final shown = members.take(10).toList();

    return SizedBox(
      width: size,
      height: size,
      child: Stack(
        clipBehavior: Clip.none,
        children: [
          // Two dashed orbital rings.
          Positioned.fill(
            child: CustomPaint(
              painter: _OrbitPainter(
                color: AppColors.posttubePrimary.withValues(alpha: 0.28),
              ),
            ),
          ),
          // Central "you" node.
          Positioned(
            left: center - 26,
            top: center - 26,
            child: Container(
              width: 52,
              height: 52,
              decoration: const BoxDecoration(
                shape: BoxShape.circle,
                gradient: LinearGradient(
                  begin: Alignment.topLeft,
                  end: Alignment.bottomRight,
                  colors: [
                    AppColors.posttubePrimary,
                    AppColors.accentPurple,
                  ],
                ),
              ),
              alignment: Alignment.center,
              child: const Text(
                'You',
                style: TextStyle(
                  color: Colors.white,
                  fontSize: 12,
                  fontWeight: FontWeight.w800,
                ),
              ),
            ),
          ),
          // Scatter the members across the two rings.
          for (var i = 0; i < shown.length; i++)
            _orbitNode(i, shown.length, shown[i], center),
        ],
      ),
    );
  }

  Widget _orbitNode(int i, int total, User u, double center) {
    // Alternate inner / outer rings; offset each ring's start angle so the
    // scatter looks organic rather than perfectly stacked.
    final inner = i.isEven;
    final radius = inner ? 56.0 : 84.0;
    final ringCount = (total / 2).ceil();
    final ringIndex = i ~/ 2;
    final phase = inner ? -math.pi / 2 : -math.pi / 2 + 0.55;
    final angle = phase + (ringIndex / math.max(1, ringCount)) * math.pi * 2;
    const av = 34.0;
    final x = math.cos(angle) * radius + center - av / 2;
    final y = math.sin(angle) * radius + center - av / 2;
    return Positioned(
      left: x,
      top: y,
      child: Container(
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          border: Border.all(color: AppColors.bgSecondary, width: 2),
        ),
        child: FriendAvatar(
          name: u.displayName,
          url: u.hasAvatar ? u.avatarUrl : null,
          size: av,
        ),
      ),
    );
  }
}

class _OrbitPainter extends CustomPainter {
  const _OrbitPainter({required this.color});

  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1;
    final c = size.center(Offset.zero);
    for (final radius in [56.0, 84.0]) {
      final rect = Rect.fromCircle(center: c, radius: radius);
      const dash = 0.22;
      const gap = 0.16;
      var a = 0.0;
      while (a < math.pi * 2) {
        canvas.drawArc(rect, a, dash, false, paint);
        a += dash + gap;
      }
    }
  }

  @override
  bool shouldRepaint(_OrbitPainter oldDelegate) =>
      oldDelegate.color != color;
}

/// Friend picker for adding people to the Trusted Circle.
class _AddToCircleSheet extends StatefulWidget {
  const _AddToCircleSheet({required this.candidates, required this.onAdd});

  final List<User> candidates;
  final Future<void> Function(User user) onAdd;

  @override
  State<_AddToCircleSheet> createState() => _AddToCircleSheetState();
}

class _AddToCircleSheetState extends State<_AddToCircleSheet> {
  final TextEditingController _search = TextEditingController();
  final Set<String> _added = {};

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final q = _search.text.trim().toLowerCase();
    final list = q.isEmpty
        ? widget.candidates
        : widget.candidates
            .where((u) =>
                u.displayName.toLowerCase().contains(q) ||
                u.username.toLowerCase().contains(q))
            .toList();

    return DraggableScrollableSheet(
      initialChildSize: 0.7,
      minChildSize: 0.4,
      maxChildSize: 0.92,
      expand: false,
      builder: (context, controller) {
        return Container(
          decoration: const BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
          ),
          child: Column(
            children: [
              const Center(child: SheetGrabber()),
              Padding(
                padding: const EdgeInsets.fromLTRB(18, 8, 14, 10),
                child: Row(
                  children: [
                    const Expanded(
                      child: Text(
                        'Add to circle',
                        style: TextStyle(
                          color: AppColors.textPrimary,
                          fontSize: 17,
                          fontWeight: FontWeight.w800,
                        ),
                      ),
                    ),
                    CircleIconButton(
                      icon: Icons.close_rounded,
                      onTap: () => Navigator.of(context).pop(),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(18, 0, 18, 10),
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 14),
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(14),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Row(
                    children: [
                      const Icon(Icons.search,
                          size: 18, color: AppColors.textTertiary),
                      const SizedBox(width: 10),
                      Expanded(
                        child: TextField(
                          controller: _search,
                          onChanged: (_) => setState(() {}),
                          style: const TextStyle(
                            color: AppColors.textPrimary,
                            fontSize: 14,
                          ),
                          decoration: const InputDecoration(
                            isCollapsed: true,
                            contentPadding:
                                EdgeInsets.symmetric(vertical: 13),
                            border: InputBorder.none,
                            hintText: 'Search friends…',
                            hintStyle: TextStyle(
                              color: AppColors.textTertiary,
                              fontSize: 14,
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              const Divider(height: 1, color: AppColors.borderSubtle),
              Expanded(
                child: ListView.builder(
                  controller: controller,
                  padding: const EdgeInsets.symmetric(vertical: 6),
                  itemCount: list.length,
                  itemBuilder: (context, i) {
                    final u = list[i];
                    final added = _added.contains(u.id);
                    return Padding(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 18, vertical: 5),
                      child: Row(
                        children: [
                          FriendAvatar(
                            name: u.displayName,
                            url: u.hasAvatar ? u.avatarUrl : null,
                            size: 40,
                          ),
                          const SizedBox(width: 12),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  u.displayName,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: const TextStyle(
                                    color: AppColors.textPrimary,
                                    fontSize: 14,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                                Text(
                                  '@${u.username}',
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: const TextStyle(
                                    color: AppColors.textTertiary,
                                    fontSize: 11,
                                  ),
                                ),
                              ],
                            ),
                          ),
                          const SizedBox(width: 8),
                          GestureDetector(
                            onTap: added
                                ? null
                                : () {
                                    setState(() => _added.add(u.id));
                                    widget.onAdd(u);
                                  },
                            child: Container(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 14, vertical: 7),
                              decoration: BoxDecoration(
                                gradient:
                                    added ? null : AppColors.posttubeGradient,
                                color: added ? AppColors.bgCard : null,
                                borderRadius: BorderRadius.circular(100),
                                border: added
                                    ? Border.all(
                                        color: AppColors.borderSubtle)
                                    : null,
                              ),
                              child: Text(
                                added ? 'Added' : 'Add',
                                style: TextStyle(
                                  color: added
                                      ? AppColors.textMuted
                                      : Colors.white,
                                  fontSize: 11,
                                  fontWeight: FontWeight.w700,
                                ),
                              ),
                            ),
                          ),
                        ],
                      ),
                    );
                  },
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}
