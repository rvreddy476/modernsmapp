import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/social/widgets/friends_common.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// SURFACE 3 — Add Friends bottom sheet.
///
/// A QR call-to-action, a 2×2 grid of add-paths (QR / Contacts / Username
/// / Nearby), and a "Matched for you" list driven by
/// [friendSuggestionsProvider]. QR / Contacts / Nearby backends are not
/// built — those tiles show a "Soon" treatment. "Username" focuses the
/// home search.
void showAddFriendsSheet(
  BuildContext context, {
  required int contactsOnAtpost,
  required VoidCallback onSearchUsername,
}) {
  showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (_) => AddFriendsSheet(
      contactsOnAtpost: contactsOnAtpost,
      onSearchUsername: onSearchUsername,
    ),
  );
}

class AddFriendsSheet extends ConsumerStatefulWidget {
  const AddFriendsSheet({
    super.key,
    required this.contactsOnAtpost,
    required this.onSearchUsername,
  });

  final int contactsOnAtpost;
  final VoidCallback onSearchUsername;

  @override
  ConsumerState<AddFriendsSheet> createState() => _AddFriendsSheetState();
}

class _AddFriendsSheetState extends ConsumerState<AddFriendsSheet> {
  /// Suggestions the user already actioned this session.
  final Set<String> _added = {};

  void _snack(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  Future<void> _add(FriendSuggestion s) async {
    setState(() => _added.add(s.userId));
    try {
      await ref.read(userRepositoryProvider).sendConnectionRequest(s.userId);
      _snack('Friend request sent to ${s.displayName}.');
    } catch (_) {
      if (mounted) setState(() => _added.remove(s.userId));
      _snack('Could not send the request.');
    }
  }

  void _comingSoon(String label) => _snack('$label — coming soon');

  void _focusSearch() {
    Navigator.of(context).pop();
    widget.onSearchUsername();
  }

  @override
  Widget build(BuildContext context) {
    final suggestionsAsync = ref.watch(friendSuggestionsProvider);
    final suggestions = (suggestionsAsync.valueOrNull ?? const [])
        .where((s) => !_added.contains(s.userId))
        .toList();

    return DraggableScrollableSheet(
      initialChildSize: 0.85,
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
              _header(context),
              const Divider(height: 1, color: AppColors.borderSubtle),
              Expanded(
                child: ListView(
                  controller: controller,
                  padding: const EdgeInsets.only(bottom: 24),
                  children: [
                    const SizedBox(height: 14),
                    _qrCard(),
                    const SizedBox(height: 12),
                    _grid(),
                    const SizedBox(height: 4),
                    _matchedHeader(suggestions.length),
                    ...suggestionsAsync.when(
                      loading: () => [_loader()],
                      error: (_, _) =>
                          [_hint('Could not load suggestions.')],
                      data: (_) {
                        if (suggestions.isEmpty) {
                          return [_hint('No matches right now.')];
                        }
                        return suggestions
                            .map((s) => _suggestionRow(s))
                            .toList();
                      },
                    ),
                    const SizedBox(height: 12),
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

  Widget _header(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 10, 14, 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 38,
            height: 38,
            decoration: BoxDecoration(
              color: AppColors.postbookPrimary.withValues(alpha: 0.14),
              borderRadius: BorderRadius.circular(11),
            ),
            alignment: Alignment.center,
            child: const Icon(Icons.person_add_alt_1_rounded,
                size: 20, color: AppColors.postbookPrimary),
          ),
          const SizedBox(width: 12),
          const Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Add friends',
                  style: TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 18,
                    fontWeight: FontWeight.w800,
                    letterSpacing: -0.3,
                  ),
                ),
                SizedBox(height: 2),
                Text(
                  'Bring people into your orbit.',
                  style: TextStyle(
                    color: AppColors.textTertiary,
                    fontSize: 12,
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

  Widget _qrCard() {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 18),
      child: GestureDetector(
        onTap: () => _comingSoon('Your QR code'),
        child: Container(
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
              colors: [
                AppColors.postbookPrimary.withValues(alpha: 0.18),
                AppColors.accentPurple.withValues(alpha: 0.16),
              ],
            ),
            borderRadius: BorderRadius.circular(20),
            border: Border.all(color: AppColors.borderMedium),
          ),
          child: Row(
            children: [
              Container(
                width: 56,
                height: 56,
                decoration: BoxDecoration(
                  color: AppColors.bgPrimary.withValues(alpha: 0.5),
                  borderRadius: BorderRadius.circular(14),
                ),
                alignment: Alignment.center,
                child: const Icon(Icons.qr_code_2_rounded,
                    size: 32, color: AppColors.textPrimary),
              ),
              const SizedBox(width: 14),
              const Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Show my QR',
                      style: TextStyle(
                        color: AppColors.textPrimary,
                        fontSize: 15,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    SizedBox(height: 3),
                    Text(
                      'Hold up — they scan — you’re connected.',
                      style: TextStyle(
                        color: AppColors.textTertiary,
                        fontSize: 11.5,
                        height: 1.4,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 6),
              const Icon(Icons.chevron_right_rounded,
                  color: AppColors.textMuted),
            ],
          ),
        ),
      ),
    );
  }

  Widget _grid() {
    final tiles = <_AddTile>[
      _AddTile(
        icon: Icons.qr_code_scanner_rounded,
        label: 'Scan QR',
        sub: 'Point at their code',
        soon: true,
        onTap: () => _comingSoon('Scan QR'),
      ),
      _AddTile(
        icon: Icons.contacts_rounded,
        label: 'Contacts',
        sub: '${widget.contactsOnAtpost} already on VChat',
        soon: true,
        onTap: () => _comingSoon('Contacts'),
      ),
      _AddTile(
        icon: Icons.alternate_email_rounded,
        label: 'Username',
        sub: 'Search by @handle',
        soon: false,
        onTap: _focusSearch,
      ),
      _AddTile(
        icon: Icons.near_me_rounded,
        label: 'Nearby',
        sub: 'Bluetooth + wi-fi handshake',
        soon: true,
        onTap: () => _comingSoon('Nearby'),
      ),
    ];
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 18),
      child: Column(
        children: [
          Row(
            children: [
              Expanded(child: _gridTile(tiles[0])),
              const SizedBox(width: 10),
              Expanded(child: _gridTile(tiles[1])),
            ],
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(child: _gridTile(tiles[2])),
              const SizedBox(width: 10),
              Expanded(child: _gridTile(tiles[3])),
            ],
          ),
        ],
      ),
    );
  }

  Widget _gridTile(_AddTile t) {
    return GestureDetector(
      onTap: t.onTap,
      child: Container(
        padding: const EdgeInsets.all(13),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  width: 36,
                  height: 36,
                  decoration: BoxDecoration(
                    color: AppColors.bgTertiary,
                    borderRadius: BorderRadius.circular(11),
                  ),
                  alignment: Alignment.center,
                  child: Icon(t.icon,
                      size: 18, color: AppColors.textSecondary),
                ),
                const Spacer(),
                if (t.soon)
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 7, vertical: 2),
                    decoration: BoxDecoration(
                      color: AppColors.bgTertiary,
                      borderRadius: BorderRadius.circular(6),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: const Text(
                      'SOON',
                      style: TextStyle(
                        color: AppColors.textMuted,
                        fontSize: 8.5,
                        fontWeight: FontWeight.w800,
                        letterSpacing: 0.6,
                      ),
                    ),
                  ),
              ],
            ),
            const SizedBox(height: 10),
            Text(
              t.label,
              style: const TextStyle(
                color: AppColors.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 2),
            Text(
              t.sub,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppColors.textMuted,
                fontSize: 10.5,
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _matchedHeader(int count) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 16, 18, 8),
      child: Row(
        children: [
          Expanded(child: EyebrowLabel('MATCHED FOR YOU · $count')),
          GestureDetector(
            onTap: () {
              Navigator.of(context).pop();
              context.push('/discover');
            },
            child: const Text(
              'See all',
              style: TextStyle(
                color: AppColors.postbookPrimary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _suggestionRow(FriendSuggestion s) {
    final reason = s.explainText.trim().isNotEmpty
        ? s.explainText.trim()
        : _reasonLabel(s.reasonCodes);
    // Suggestion score clamped to a 0–100 match percentage; fall back to
    // the mutual-friend count when the score is unavailable.
    final pct = (s.score * 100).clamp(0, 100).round();
    final hasScore = s.score > 0;

    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 5, 18, 5),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 11),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            FriendAvatar(
              name: s.displayName,
              url: s.avatarUrl,
              size: 42,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Flexible(
                        child: Text(
                          s.displayName,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppColors.textPrimary,
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 6, vertical: 1),
                        decoration: BoxDecoration(
                          color: AppColors.posttubePrimary
                              .withValues(alpha: 0.14),
                          borderRadius: BorderRadius.circular(6),
                          border: Border.all(
                            color: AppColors.posttubePrimary
                                .withValues(alpha: 0.34),
                          ),
                        ),
                        child: Text(
                          hasScore
                              ? '$pct% MATCH'
                              : '${s.mutualFriendCount} MUTUAL',
                          style: const TextStyle(
                            color: AppColors.posttubePrimary,
                            fontSize: 9,
                            fontWeight: FontWeight.w800,
                          ),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 2),
                  Text(
                    reason,
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
              onTap: () => _add(s),
              child: Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 16, vertical: 8),
                decoration: BoxDecoration(
                  color: const Color(0xFFE11D48), // rose-600
                  borderRadius: BorderRadius.circular(100),
                ),
                child: const Text(
                  'Add Friend',
                  style: TextStyle(
                    color: Colors.white,
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _footer() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 4, 18, 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.shield_outlined,
              size: 13, color: AppColors.textMuted),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Matches blend shared groups, hashtag overlap, and mutuals. '
              'You stay hidden from people you haven’t added.',
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

  Widget _loader() {
    return const Padding(
      padding: EdgeInsets.fromLTRB(18, 12, 18, 16),
      child: Center(
        child: SizedBox(
          width: 22,
          height: 22,
          child: CircularProgressIndicator(
            strokeWidth: 2,
            color: AppColors.postbookPrimary,
          ),
        ),
      ),
    );
  }

  Widget _hint(String text) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 4, 18, 8),
      child: Text(
        text,
        style: const TextStyle(color: AppColors.textMuted, fontSize: 12),
      ),
    );
  }
}

class _AddTile {
  const _AddTile({
    required this.icon,
    required this.label,
    required this.sub,
    required this.soon,
    required this.onTap,
  });
  final IconData icon;
  final String label;
  final String sub;
  final bool soon;
  final VoidCallback onTap;
}

String _reasonLabel(List<String> codes) {
  if (codes.isEmpty) return 'Suggested for you';
  switch (codes.first.toUpperCase()) {
    case 'MUTUAL_FRIENDS':
      return 'You have mutual friends';
    case 'SAME_CITY':
    case 'SAME_LOCATION':
      return 'Lives near you';
    case 'SAME_SCHOOL':
      return 'Went to the same school';
    case 'SAME_COMPANY':
      return 'Works at the same place';
    case 'SAME_PROFESSION':
      return 'Does similar work';
    case 'MUTUAL_FOLLOW':
    case 'FRIENDS_FOLLOW':
      return 'In your follow network';
    case 'TRIADIC_CLOSURE':
      return 'Connected to your circle';
    case 'COMMON_GROUPS':
      return 'In groups you are in';
    case 'CONTACT_MATCH':
      return 'From your contacts';
    case 'NEW_CREATOR':
      return 'New creator to discover';
    case 'POPULAR':
    case 'TRENDING_REGION':
      return 'Popular right now';
    default:
      return 'Suggested for you';
  }
}
