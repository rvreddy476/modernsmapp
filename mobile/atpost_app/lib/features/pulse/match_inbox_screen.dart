import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 3 — Match inbox.
///
/// Tabs: All / Active / Quiet / Sparks waiting. Each tab is a `FutureProvider`
/// keyed on the status string. Rows show avatar, last message preview,
/// time-ago, intent chip, expiry pill (red if < 24h). Swipe right is "Pin"
/// (S4 — stub for now); swipe left mutes then offers to close.
///
/// Spec §6.7.
class MatchInboxScreen extends ConsumerStatefulWidget {
  const MatchInboxScreen({super.key, this.initialTab});

  /// Optional deep-link tab. Accepts `all`, `active`, `quiet`, `sparks`.
  final String? initialTab;

  @override
  ConsumerState<MatchInboxScreen> createState() => _MatchInboxScreenState();
}

class _MatchInboxScreenState extends ConsumerState<MatchInboxScreen>
    with SingleTickerProviderStateMixin {
  static const _tabs = <_TabSpec>[
    _TabSpec(label: 'All', status: 'all'),
    _TabSpec(label: 'Active', status: 'active'),
    _TabSpec(label: 'Quiet', status: 'quiet'),
    _TabSpec(label: 'Sparks waiting', status: 'sparks-waiting'),
  ];

  late final TabController _tabController;
  bool _gateChecked = false;

  @override
  void initState() {
    super.initState();
    PulseBreadcrumbs.matchInboxOpen();
    final initialIndex = _initialIndexFromTab(widget.initialTab);
    _tabController = TabController(
      length: _tabs.length,
      vsync: this,
      initialIndex: initialIndex,
    );
    _checkGate();
  }

  int _initialIndexFromTab(String? key) {
    if (key == null || key.isEmpty) return 0;
    final normalized = key.toLowerCase();
    for (var i = 0; i < _tabs.length; i++) {
      if (_tabs[i].status.startsWith(normalized) ||
          normalized.startsWith(_tabs[i].status)) {
        return i;
      }
    }
    if (normalized == 'sparks') return 3;
    return 0;
  }

  Future<void> _checkGate() async {
    final auth = ref.read(pulseAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    if (!auth.isReady) {
      context.go('/pulse/onboarding');
      return;
    }
    setState(() => _gateChecked = true);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Matches', style: AppTextStyles.h2),
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(48),
          child: TabBar(
            controller: _tabController,
            isScrollable: true,
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textTertiary,
            indicatorColor: AppColors.postbookPrimary,
            tabs: [for (final t in _tabs) Tab(text: t.label)],
          ),
        ),
      ),
      body: !_gateChecked
          ? const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            )
          : TabBarView(
              controller: _tabController,
              children: [
                for (final tab in _tabs) _MatchListTab(status: tab.status),
              ],
            ),
    );
  }
}

class _TabSpec {
  const _TabSpec({required this.label, required this.status});

  final String label;
  final String status;
}

class _MatchListTab extends ConsumerWidget {
  const _MatchListTab({required this.status});

  final String status;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(pulseMatchesProvider(status));
    return RefreshIndicator(
      color: AppColors.postbookPrimary,
      onRefresh: () async {
        ref.invalidate(pulseMatchesProvider(status));
        await ref.read(pulseMatchesProvider(status).future);
      },
      child: async.when(
        loading: () => const Center(
          child: CircularProgressIndicator(
            color: AppColors.postbookPrimary,
          ),
        ),
        error: (_, _) => ListView(
          children: [
            const SizedBox(height: 80),
            Center(
              child: Text(
                'Could not load matches.',
                style: AppTextStyles.body,
              ),
            ),
          ],
        ),
        data: (matches) {
          if (matches.isEmpty) {
            return ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                const SizedBox(height: 80),
                Center(
                  child: Text(
                    _emptyText(status),
                    style: AppTextStyles.body,
                    textAlign: TextAlign.center,
                  ),
                ),
              ],
            );
          }
          return ListView.separated(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 24),
            itemCount: matches.length,
            separatorBuilder: (_, _) => const SizedBox(height: 8),
            itemBuilder: (context, i) => _MatchRow(
              match: matches[i],
              status: status,
            ),
          );
        },
      ),
    );
  }
}

String _emptyText(String status) {
  switch (status) {
    case 'sparks-waiting':
      return 'No sparks waiting.\nWhen someone sparks you, they show up here.';
    case 'active':
      return 'No active conversations yet.';
    case 'quiet':
      return 'Nothing has gone quiet — keep it up.';
    default:
      return 'No matches yet. Spark a candidate to start.';
  }
}

class _MatchRow extends ConsumerWidget {
  const _MatchRow({required this.match, required this.status});

  final MatchSummary match;
  final String status;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Dismissible(
      key: ValueKey('match-${match.id}'),
      background: _swipeBg(
        align: Alignment.centerLeft,
        icon: Icons.push_pin_outlined,
        color: AppColors.accentPurple,
        label: 'Pin',
      ),
      secondaryBackground: _swipeBg(
        align: Alignment.centerRight,
        icon: Icons.notifications_off_outlined,
        color: AppColors.statusError,
        label: 'Mute',
      ),
      confirmDismiss: (direction) async {
        if (direction == DismissDirection.startToEnd) {
          // Pin — S4. Stub.
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              content: Text('Pin coming in S4.'),
            ),
          );
          return false;
        }
        // Mute -> close confirmation.
        final shouldClose = await showDialog<bool>(
          context: context,
          builder: (ctx) => AlertDialog(
            backgroundColor: AppColors.bgSecondary,
            title: Text(
              'Mute & close?',
              style: AppTextStyles.h2,
            ),
            content: Text(
              'Muting and closing this match removes it from your inbox. '
              'You can\'t undo this.',
              style: AppTextStyles.body,
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(ctx).pop(false),
                child: const Text('Cancel'),
              ),
              TextButton(
                onPressed: () => Navigator.of(ctx).pop(true),
                child: Text(
                  'Close',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
              ),
            ],
          ),
        );
        if (shouldClose != true) return false;
        try {
          await ref.read(pulseRepositoryProvider).closeMatch(match.id);
          ref.invalidate(pulseMatchesProvider(status));
          return true;
        } catch (_) {
          if (!context.mounted) return false;
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Could not close match.')),
          );
          return false;
        }
      },
      onDismissed: (_) {},
      child: _MatchTile(match: match),
    );
  }

  Widget _swipeBg({
    required Alignment align,
    required IconData icon,
    required Color color,
    required String label,
  }) {
    return Container(
      alignment: align,
      padding: const EdgeInsets.symmetric(horizontal: 24),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.18),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, color: color),
          const SizedBox(width: 6),
          Text(
            label,
            style: AppTextStyles.label.copyWith(color: color),
          ),
        ],
      ),
    );
  }
}

class _MatchTile extends ConsumerWidget {
  const _MatchTile({required this.match});

  final MatchSummary match;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final intent = match.otherIntent ?? '';
    final intentColor = _intentColor(intent);
    final preview = match.lastMessagePreview ??
        "It's a match — say hi!";
    final ttl = match.timeUntilExpiry;
    final urgent = ttl != null && ttl.inHours < 24 && ttl.inSeconds > 0;

    return InkWell(
      onTap: () {
        // Sprint 5: telemetry. match_id only — no message text or preview.
        ref.read(pulseTelemetryProvider).matchOpened(matchId: match.id);
        PulseBreadcrumbs.matchInboxSelect(matchId: match.id);
        final convo = match.conversationId;
        if (convo != null && convo.isNotEmpty) {
          context.push('/pulse/chat/$convo');
        } else {
          context.push('/pulse/matches/${match.id}');
        }
      },
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Ink(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            CircleAvatar(
              radius: 24,
              backgroundColor: AppColors.bgTertiary,
              backgroundImage: match.otherAvatarUrl != null
                  ? NetworkImage(match.otherAvatarUrl!)
                  : null,
              child: match.otherAvatarUrl == null
                  ? Text(
                      match.otherFirstName.isEmpty
                          ? '?'
                          : match.otherFirstName.substring(0, 1),
                      style: AppTextStyles.label.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    )
                  : null,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          match.otherFirstName,
                          style: AppTextStyles.label,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      if (intent.isNotEmpty)
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 8,
                            vertical: 2,
                          ),
                          decoration: BoxDecoration(
                            color: intentColor.withValues(alpha: 0.18),
                            borderRadius:
                                BorderRadius.circular(AppSpacing.radiusFull),
                          ),
                          child: Text(
                            intent,
                            style: AppTextStyles.labelTiny.copyWith(
                              color: intentColor,
                            ),
                          ),
                        ),
                    ],
                  ),
                  const SizedBox(height: 4),
                  Text(
                    preview,
                    style: AppTextStyles.bodySmall.copyWith(
                      color: AppColors.textSecondary,
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 6),
                  Row(
                    children: [
                      Text(
                        _relativeTime(match.lastMessageAt),
                        style: AppTextStyles.labelTiny.copyWith(
                          color: AppColors.textTertiary,
                        ),
                      ),
                      const Spacer(),
                      if (ttl != null)
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 8,
                            vertical: 2,
                          ),
                          decoration: BoxDecoration(
                            color: (urgent
                                    ? AppColors.statusError
                                    : AppColors.textTertiary)
                                .withValues(alpha: 0.16),
                            borderRadius:
                                BorderRadius.circular(AppSpacing.radiusFull),
                          ),
                          child: Text(
                            _formatTtl(ttl),
                            style: AppTextStyles.labelTiny.copyWith(
                              color: urgent
                                  ? AppColors.statusError
                                  : AppColors.textTertiary,
                            ),
                          ),
                        ),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

String _relativeTime(DateTime? dt) {
  if (dt == null) return '';
  final diff = DateTime.now().difference(dt);
  if (diff.inSeconds < 45) return 'just now';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m';
  if (diff.inHours < 24) return '${diff.inHours}h';
  if (diff.inDays < 7) return '${diff.inDays}d';
  return '${(diff.inDays / 7).floor()}w';
}

String _formatTtl(Duration d) {
  if (d.isNegative || d.inSeconds <= 0) return 'expired';
  if (d.inDays >= 1) return '${d.inDays}d left';
  if (d.inHours >= 1) return '${d.inHours}h left';
  return '${d.inMinutes}m left';
}

Color _intentColor(String intent) {
  switch (intent) {
    case 'casual':
      return AppColors.statusWarning;
    case 'serious':
      return AppColors.accentPurple;
    case 'marriage':
      return AppColors.postgramPrimary;
    default:
      return AppColors.textTertiary;
  }
}
