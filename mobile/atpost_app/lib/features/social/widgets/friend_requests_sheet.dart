import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/social/widgets/friends_common.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// SURFACE 4 — Friend requests bottom sheet.
///
/// Lists incoming requests with an origin chip derived from each request's
/// `source` field, plus a collapsible "Hidden by trust-safety" section
/// backed by [filteredFriendRequestsProvider]. All accept/decline/unfilter
/// wiring reuses the existing user-repository methods.
void showFriendRequestsSheet(BuildContext context) {
  showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (_) => const FriendRequestsSheet(),
  );
}

/// The requests surface rendered as plain content (no sheet chrome) so it
/// can fill a full screen for the `/friend-requests` route.
class FriendRequestsSheet extends ConsumerStatefulWidget {
  const FriendRequestsSheet({super.key, this.asScreen = false});

  /// When true, renders inside a Scaffold-friendly column instead of a
  /// draggable sheet.
  final bool asScreen;

  @override
  ConsumerState<FriendRequestsSheet> createState() =>
      _FriendRequestsSheetState();
}

class _FriendRequestsSheetState extends ConsumerState<FriendRequestsSheet> {
  /// Requests actioned locally — hidden immediately for an optimistic feel.
  final Set<String> _handled = {};
  bool _showFiltered = false;

  void _snack(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  Future<void> _accept(FriendRequest r) async {
    setState(() => _handled.add(r.id));
    try {
      await ref
          .read(userRepositoryProvider)
          .acceptConnectionRequest(r.senderId);
      ref.invalidate(friendRequestsProvider);
      ref.invalidate(friendsProvider);
    } catch (_) {
      if (mounted) setState(() => _handled.remove(r.id));
      _snack('Could not accept the request.');
    }
  }

  Future<void> _decline(FriendRequest r) async {
    setState(() => _handled.add(r.id));
    try {
      await ref
          .read(userRepositoryProvider)
          .declineConnectionRequest(r.senderId);
      ref.invalidate(friendRequestsProvider);
    } catch (_) {
      if (mounted) setState(() => _handled.remove(r.id));
      _snack('Could not decline the request.');
    }
  }

  @override
  Widget build(BuildContext context) {
    if (widget.asScreen) {
      return Column(
        children: [
          _header(context, screen: true),
          const Divider(height: 1, color: AppColors.borderSubtle),
          Expanded(child: _body(controller: null)),
        ],
      );
    }

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
              _header(context, screen: false),
              const Divider(height: 1, color: AppColors.borderSubtle),
              Expanded(child: _body(controller: controller)),
            ],
          ),
        );
      },
    );
  }

  Widget _header(BuildContext context, {required bool screen}) {
    final requestsAsync = ref.watch(friendRequestsProvider);
    final m = (requestsAsync.valueOrNull ?? const [])
        .where((r) =>
            r.direction == 'received' && !_handled.contains(r.id))
        .length;

    return Padding(
      padding: EdgeInsets.fromLTRB(18, screen ? 8 : 10, 14, 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (screen) ...[
            CircleIconButton(
              icon: Icons.arrow_back_ios_new_rounded,
              size: 15,
              onTap: () => context.pop(),
            ),
            const SizedBox(width: 10),
          ] else ...[
            Container(
              width: 38,
              height: 38,
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary.withValues(alpha: 0.14),
                borderRadius: BorderRadius.circular(11),
              ),
              alignment: Alignment.center,
              child: const Icon(Icons.mail_outline_rounded,
                  size: 19, color: AppColors.postbookPrimary),
            ),
            const SizedBox(width: 12),
          ],
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  'Friend requests',
                  style: TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 18,
                    fontWeight: FontWeight.w800,
                    letterSpacing: -0.3,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  '$m ${m == 1 ? 'person wants' : 'people want'} in. '
                  'Tap a name for the full profile.',
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
            onTap: () =>
                screen ? context.pop() : Navigator.of(context).pop(),
          ),
        ],
      ),
    );
  }

  Widget _body({required ScrollController? controller}) {
    final requestsAsync = ref.watch(friendRequestsProvider);

    return requestsAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => _errorState(),
      data: (all) {
        final incoming = all
            .where((r) =>
                r.direction == 'received' && !_handled.contains(r.id))
            .toList();

        return ListView(
          controller: controller,
          padding: const EdgeInsets.only(bottom: 24),
          children: [
            const SizedBox(height: 12),
            Padding(
              padding: const EdgeInsets.fromLTRB(18, 0, 18, 8),
              child: EyebrowLabel('INCOMING · ${incoming.length}'),
            ),
            if (incoming.isEmpty)
              _emptyIncoming()
            else
              ...incoming.map(
                (r) => _RequestRow(
                  request: r,
                  onTap: () {
                    if (!widget.asScreen) Navigator.of(context).pop();
                    context.push('/profile/${r.senderId}');
                  },
                  onAccept: () => _accept(r),
                  onDecline: () => _decline(r),
                ),
              ),
            const SizedBox(height: 10),
            _filteredSection(),
            const SizedBox(height: 12),
            _footer(),
          ],
        );
      },
    );
  }

  Widget _emptyIncoming() {
    return Container(
      margin: const EdgeInsets.fromLTRB(18, 4, 18, 8),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: const Row(
        children: [
          Icon(Icons.mail_outline_rounded, color: AppColors.textMuted),
          SizedBox(width: 12),
          Expanded(
            child: Text(
              'No incoming requests right now.',
              style: TextStyle(
                color: AppColors.textTertiary,
                fontSize: 13,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _filteredSection() {
    final filteredAsync = ref.watch(filteredFriendRequestsProvider);
    final filtered = filteredAsync.valueOrNull ?? const <FriendRequest>[];

    if (filtered.isEmpty &&
        !filteredAsync.isLoading &&
        !filteredAsync.hasError) {
      return const SizedBox.shrink();
    }
    final f = filtered.length;

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 18),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          children: [
            InkWell(
              borderRadius: BorderRadius.circular(16),
              onTap: () => setState(() => _showFiltered = !_showFiltered),
              child: Padding(
                padding: const EdgeInsets.all(13),
                child: Row(
                  children: [
                    Container(
                      width: 34,
                      height: 34,
                      decoration: BoxDecoration(
                        color: AppColors.bgTertiary,
                        borderRadius: BorderRadius.circular(10),
                      ),
                      alignment: Alignment.center,
                      child: const Icon(Icons.shield_outlined,
                          size: 17, color: AppColors.textMuted),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Text(
                            'Hidden by trust-safety',
                            style: TextStyle(
                              color: AppColors.textPrimary,
                              fontSize: 13,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                          const SizedBox(height: 1),
                          Text(
                            '$f ${f == 1 ? 'request' : 'requests'} · '
                            'new accounts, no shared signals',
                            style: const TextStyle(
                              color: AppColors.textMuted,
                              fontSize: 11,
                            ),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 6),
                    Icon(
                      _showFiltered
                          ? Icons.keyboard_arrow_up_rounded
                          : Icons.chevron_right_rounded,
                      color: AppColors.textMuted,
                    ),
                  ],
                ),
              ),
            ),
            if (_showFiltered) ...[
              const Divider(height: 1, color: AppColors.borderSubtle),
              if (filteredAsync.isLoading)
                const Padding(
                  padding: EdgeInsets.all(16),
                  child: SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                )
              else if (filteredAsync.hasError)
                const Padding(
                  padding: EdgeInsets.all(13),
                  child: Text(
                    'Could not load hidden requests.',
                    style: TextStyle(
                      color: AppColors.textMuted,
                      fontSize: 12,
                    ),
                  ),
                )
              else
                ...filtered.map((r) => _FilteredRow(request: r)),
            ],
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
          const Icon(Icons.lock_outline_rounded,
              size: 13, color: AppColors.textMuted),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Decline is silent. Senders never see they were declined.',
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

  Widget _errorState() {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.mail_lock_outlined,
              color: AppColors.textMuted, size: 36),
          const SizedBox(height: 12),
          const Text(
            'Could not load friend requests.',
            style: TextStyle(color: AppColors.textSecondary),
          ),
          const SizedBox(height: 8),
          TextButton(
            onPressed: () => ref.invalidate(friendRequestsProvider),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}

/// Derives a human-readable origin label from a request's `source` field.
/// Returns null when the source is unknown — the chip is then hidden.
({IconData icon, String label})? _originChip(String source) {
  final s = source.trim().toLowerCase();
  if (s.isEmpty) return null;
  if (s.startsWith('hashtag:') || s.startsWith('#')) {
    final tag = s.replaceFirst('hashtag:', '').replaceFirst('#', '');
    return (icon: Icons.tag_rounded, label: 'FOUND VIA #${tag.toUpperCase()}');
  }
  if (s.startsWith('hashtag')) {
    return (icon: Icons.tag_rounded, label: 'FOUND VIA HASHTAG');
  }
  switch (s) {
    case 'qr':
    case 'qr_code':
    case 'qrcode':
      return (icon: Icons.qr_code_2_rounded, label: 'SCANNED YOUR QR');
    case 'search':
      return (icon: Icons.search_rounded, label: 'FOUND IN SEARCH');
    case 'mutual':
    case 'mutual_friends':
      return (icon: Icons.people_alt_rounded, label: 'VIA MUTUAL FRIENDS');
    case 'contacts':
    case 'contact':
      return (icon: Icons.contacts_rounded, label: 'FROM YOUR CONTACTS');
    case 'nearby':
      return (icon: Icons.near_me_rounded, label: 'FOUND NEARBY');
    case 'suggestion':
    case 'suggested':
      return (icon: Icons.auto_awesome_rounded, label: 'FROM SUGGESTIONS');
    default:
      return (icon: Icons.link_rounded, label: source.toUpperCase());
  }
}

/// A single incoming request row with an origin chip and accept/decline.
class _RequestRow extends StatefulWidget {
  const _RequestRow({
    required this.request,
    required this.onTap,
    required this.onAccept,
    required this.onDecline,
  });

  final FriendRequest request;
  final VoidCallback onTap;
  final Future<void> Function() onAccept;
  final Future<void> Function() onDecline;

  @override
  State<_RequestRow> createState() => _RequestRowState();
}

class _RequestRowState extends State<_RequestRow> {
  bool _busy = false;

  Future<void> _run(Future<void> Function() action) async {
    if (_busy) return;
    setState(() => _busy = true);
    await action();
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final r = widget.request;
    final mutual = r.mutualFriendsCount;
    final sub = mutual > 0
        ? '$mutual mutual ${mutual == 1 ? 'friend' : 'friends'}'
        : 'Wants to connect with you';
    final chip = _originChip(r.source);

    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 5, 18, 5),
      child: GestureDetector(
        onTap: widget.onTap,
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.bgCard,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Column(
            children: [
              Row(
                children: [
                  FriendAvatar(name: r.senderName, size: 44),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          r.senderName,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppColors.textPrimary,
                            fontSize: 14,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                        const SizedBox(height: 1),
                        Text(
                          sub,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppColors.textTertiary,
                            fontSize: 11.5,
                          ),
                        ),
                      ],
                    ),
                  ),
                  if (chip != null) ...[
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 7, vertical: 3),
                      decoration: BoxDecoration(
                        color: AppColors.bgTertiary,
                        borderRadius: BorderRadius.circular(7),
                        border: Border.all(color: AppColors.borderSubtle),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(chip.icon,
                              size: 9, color: AppColors.textMuted),
                          const SizedBox(width: 3),
                          Text(
                            chip.label,
                            style: const TextStyle(
                              color: AppColors.textMuted,
                              fontSize: 8.5,
                              fontWeight: FontWeight.w800,
                              letterSpacing: 0.4,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ],
              ),
              const SizedBox(height: 10),
              if (_busy)
                const SizedBox(
                  height: 32,
                  child: Center(
                    child: SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  ),
                )
              else
                Row(
                  children: [
                    Expanded(
                      child: GestureDetector(
                        onTap: () => _run(widget.onAccept),
                        child: Container(
                          height: 36,
                          alignment: Alignment.center,
                          decoration: BoxDecoration(
                            color: const Color(0xFFE11D48), // rose-600
                            borderRadius: BorderRadius.circular(10),
                          ),
                          child: const Text(
                            'Confirm',
                            style: TextStyle(
                              color: Colors.white,
                              fontSize: 13,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: GestureDetector(
                        onTap: () => _run(widget.onDecline),
                        child: Container(
                          height: 36,
                          alignment: Alignment.center,
                          decoration: BoxDecoration(
                            color: AppColors.bgTertiary,
                            borderRadius: BorderRadius.circular(10),
                            border:
                                Border.all(color: AppColors.borderSubtle),
                          ),
                          child: const Text(
                            'Delete',
                            style: TextStyle(
                              color: AppColors.textSecondary,
                              fontSize: 13,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A single hidden (trust-safety filtered) request with an unfilter action.
class _FilteredRow extends ConsumerStatefulWidget {
  const _FilteredRow({required this.request});

  final FriendRequest request;

  @override
  ConsumerState<_FilteredRow> createState() => _FilteredRowState();
}

class _FilteredRowState extends ConsumerState<_FilteredRow> {
  bool _busy = false;

  Future<void> _unfilter() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(userRepositoryProvider)
          .unfilterConnectionRequest(widget.request.senderId);
      ref.invalidate(filteredFriendRequestsProvider);
      ref.invalidate(friendRequestsProvider);
    } catch (_) {
      if (mounted) {
        setState(() => _busy = false);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not move that request.')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final r = widget.request;
    return Padding(
      padding: const EdgeInsets.fromLTRB(13, 9, 13, 9),
      child: Row(
        children: [
          FriendAvatar(name: r.senderName, size: 36),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              r.senderName,
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
          if (_busy)
            const SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          else
            GestureDetector(
              onTap: _unfilter,
              child: Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 6),
                decoration: BoxDecoration(
                  color: AppColors.bgTertiary,
                  borderRadius: BorderRadius.circular(100),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: const Text(
                  'Move to requests',
                  style: TextStyle(
                    color: AppColors.postbookPrimary,
                    fontSize: 10.5,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
