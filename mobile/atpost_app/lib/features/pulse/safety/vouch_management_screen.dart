import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/features/pulse/safety/send_vouch_sheet.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — vouch management hub. Two tabs:
///  - "For me": vouches I've received (or pending requests where I'm the
///     vouchee).
///  - "Sent": vouches I asked others for.
///
/// "Revoke" is offered for any active row. The backend decides who is
/// allowed to revoke (we just surface the action).
class VouchManagementScreen extends ConsumerWidget {
  const VouchManagementScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return DefaultTabController(
      length: 2,
      child: Scaffold(
        backgroundColor: AppColors.bgPrimary,
        appBar: AppBar(
          backgroundColor: AppColors.bgPrimary,
          elevation: 0,
          title: Text('Vouches', style: AppTextStyles.h2),
          leading: IconButton(
            onPressed: () => context.pop(),
            icon: const Icon(Icons.arrow_back_ios_new_rounded,
                color: AppColors.textPrimary, size: 18),
          ),
          actions: [
            IconButton(
              tooltip: 'Inbox',
              onPressed: () => context.push('/pulse/safety/vouches/inbox'),
              icon: const Icon(Icons.inbox_outlined,
                  color: AppColors.textPrimary),
            ),
          ],
          bottom: const TabBar(
            indicatorColor: AppColors.postbookPrimary,
            labelColor: AppColors.textPrimary,
            unselectedLabelColor: AppColors.textTertiary,
            tabs: [
              Tab(text: 'For me'),
              Tab(text: 'Sent'),
            ],
          ),
        ),
        floatingActionButton: FloatingActionButton.extended(
          onPressed: () {
            showModalBottomSheet<bool>(
              context: context,
              isScrollControlled: true,
              backgroundColor: AppColors.bgSecondary,
              builder: (_) => const SendVouchSheet(),
            );
          },
          backgroundColor: AppColors.postbookPrimary,
          icon: const Icon(Icons.send),
          label: const Text('Ask for a vouch'),
        ),
        body: TabBarView(
          children: const [
            _ForMeTab(),
            _SentTab(),
          ],
        ),
      ),
    );
  }
}

class _ForMeTab extends ConsumerWidget {
  const _ForMeTab();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(vouchesForMeProvider);
    return async.when(
      loading: () => const Center(
        child: CircularProgressIndicator(
          color: AppColors.postbookPrimary,
        ),
      ),
      error: (_, _) => _Error(
          onRetry: () => ref.invalidate(vouchesForMeProvider)),
      data: (rows) => _VouchList(
        rows: rows,
        emptyText:
            'Nobody has vouched for you yet. Ask a friend or community member to vouch.',
        onRefresh: () async => ref.invalidate(vouchesForMeProvider),
        showRevoke: false,
      ),
    );
  }
}

class _SentTab extends ConsumerWidget {
  const _SentTab();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(vouchesSentProvider);
    return async.when(
      loading: () => const Center(
        child: CircularProgressIndicator(
          color: AppColors.postbookPrimary,
        ),
      ),
      error: (_, _) =>
          _Error(onRetry: () => ref.invalidate(vouchesSentProvider)),
      data: (rows) => _VouchList(
        rows: rows,
        emptyText: 'You haven\'t asked anyone to vouch for you yet.',
        onRefresh: () async => ref.invalidate(vouchesSentProvider),
        showRevoke: true,
      ),
    );
  }
}

class _VouchList extends ConsumerWidget {
  const _VouchList({
    required this.rows,
    required this.emptyText,
    required this.onRefresh,
    required this.showRevoke,
  });

  final List<Vouch> rows;
  final String emptyText;
  final Future<void> Function() onRefresh;
  final bool showRevoke;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (rows.isEmpty) {
      return RefreshIndicator(
        onRefresh: onRefresh,
        child: ListView(
          padding: AppSpacing.pagePadding.copyWith(top: 32),
          children: [
            Text(emptyText,
                textAlign: TextAlign.center,
                style: AppTextStyles.bodySmall),
          ],
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: onRefresh,
      child: ListView.separated(
        padding: AppSpacing.pagePadding.copyWith(top: 14, bottom: 96),
        itemCount: rows.length,
        separatorBuilder: (_, _) => const SizedBox(height: 10),
        itemBuilder: (context, index) => _VouchTile(
          vouch: rows[index],
          showRevoke: showRevoke,
        ),
      ),
    );
  }
}

class _VouchTile extends ConsumerStatefulWidget {
  const _VouchTile({required this.vouch, required this.showRevoke});

  final Vouch vouch;
  final bool showRevoke;

  @override
  ConsumerState<_VouchTile> createState() => _VouchTileState();
}

class _VouchTileState extends ConsumerState<_VouchTile> {
  bool _busy = false;

  Future<void> _revoke() async {
    setState(() => _busy = true);
    try {
      await ref.read(pulseRepositoryProvider).revokeVouch(widget.vouch.id);
      if (!mounted) return;
      ref.invalidate(vouchesForMeProvider);
      ref.invalidate(vouchesSentProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Vouch revoked.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not revoke this vouch.')),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final v = widget.vouch;
    final statusColor = switch (v.status) {
      'accepted' || 'active' => AppColors.statusSuccess,
      'pending' => AppColors.statusWarning,
      'declined' || 'revoked' => AppColors.statusError,
      _ => AppColors.textMuted,
    };
    final dt = v.decidedAt ?? v.updatedAt ?? v.createdAt;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CircleAvatar(
                radius: 16,
                backgroundColor: AppColors.bgCardHover,
                child: Text(
                  (v.voucherName ?? v.voucherUserId).isNotEmpty
                      ? (v.voucherName ?? v.voucherUserId)[0].toUpperCase()
                      : '?',
                  style: AppTextStyles.label,
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      v.voucherName ?? 'A friend',
                      style: AppTextStyles.h3,
                    ),
                    Text(
                      '${_relLabel(v.relationship)}'
                      '${v.communityName != null ? ' · ${v.communityName}' : ''}',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                decoration: BoxDecoration(
                  color: statusColor.withAlpha(40),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                ),
                child: Text(v.status,
                    style: AppTextStyles.labelSmall.copyWith(
                        color: statusColor)),
              ),
            ],
          ),
          if (v.note != null && v.note!.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text('"${v.note!}"',
                style: AppTextStyles.bodySmall
                    .copyWith(fontStyle: FontStyle.italic)),
          ],
          if (dt != null) ...[
            const SizedBox(height: 6),
            Text(_relativeTime(dt), style: AppTextStyles.labelSmall),
          ],
          if (widget.showRevoke && v.isAccepted) ...[
            const SizedBox(height: 10),
            Align(
              alignment: Alignment.centerRight,
              child: TextButton.icon(
                onPressed: _busy ? null : _revoke,
                icon: const Icon(Icons.delete_outline,
                    color: AppColors.statusError, size: 18),
                label: Text('Revoke',
                    style: AppTextStyles.label
                        .copyWith(color: AppColors.statusError)),
              ),
            ),
          ],
        ],
      ),
    );
  }

  static String _relLabel(String rel) {
    switch (rel) {
      case 'community_member':
        return 'Community member';
      case 'colleague':
        return 'Colleague';
      case 'family':
        return 'Family';
      case 'friend':
      default:
        return 'Friend';
    }
  }

  static String _relativeTime(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inMinutes < 1) return 'just now';
    if (diff.inHours < 1) return '${diff.inMinutes}m ago';
    if (diff.inDays < 1) return '${diff.inHours}h ago';
    if (diff.inDays < 30) return '${diff.inDays}d ago';
    return '${(diff.inDays / 30).floor()}mo ago';
  }
}

class _Error extends StatelessWidget {
  const _Error({required this.onRetry});

  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: AppSpacing.pagePadding,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('Could not load vouches.', style: AppTextStyles.body),
            const SizedBox(height: 12),
            FilledButton.tonal(
                onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}
