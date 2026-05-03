// Inbox tab — AtPost super-app shell.
//
// Federates five upstream sources (notifications, mentions, Pulse,
// commerce, system) into one unified inbox. Each tab pulls from
// `unifiedInboxProvider(filter)` (see `lib/providers/inbox_providers.dart`).
//
// Each row: avatar/icon, title, snippet, time. Tap opens the right deep
// link. Section headers divide the list by day. Unread badges sit on the
// tab strip.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/inbox_providers.dart';
import 'package:atpost_app/services/shell_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class InboxTab extends ConsumerStatefulWidget {
  const InboxTab({super.key});

  @override
  ConsumerState<InboxTab> createState() => _InboxTabState();
}

class _InboxTabState extends ConsumerState<InboxTab>
    with TickerProviderStateMixin {
  static const _filters = InboxFilter.values;
  late final TabController _tabController;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: _filters.length, vsync: this);
    _tabController.addListener(_onTabChanged);
  }

  @override
  void dispose() {
    _tabController.removeListener(_onTabChanged);
    _tabController.dispose();
    super.dispose();
  }

  void _onTabChanged() {
    if (!_tabController.indexIsChanging) return;
    ref
        .read(shellTelemetryProvider)
        .shellInboxTabSelected(_filters[_tabController.index].key);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Inbox', style: AppTextStyles.h2),
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(48),
          child: TabBar(
            controller: _tabController,
            isScrollable: true,
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textTertiary,
            indicatorColor: AppColors.postbookPrimary,
            labelStyle: AppTextStyles.label,
            tabs: [
              for (final f in _filters) _InboxTabHeader(filter: f),
            ],
          ),
        ),
      ),
      body: TabBarView(
        controller: _tabController,
        children: [
          for (final f in _filters) _InboxList(filter: f),
        ],
      ),
    );
  }
}

class _InboxTabHeader extends ConsumerWidget {
  const _InboxTabHeader({required this.filter});

  final InboxFilter filter;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final unread = ref.watch(unreadCountByCategoryProvider(filter));
    final n = unread.valueOrNull ?? 0;
    return Tab(
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(filter.label),
          if (n > 0) ...[
            const SizedBox(width: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary,
                borderRadius: BorderRadius.circular(99),
              ),
              child: Text(
                n > 99 ? '99+' : '$n',
                style: AppTextStyles.labelTiny.copyWith(
                  color: Colors.white,
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _InboxList extends ConsumerWidget {
  const _InboxList({required this.filter});

  final InboxFilter filter;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncItems = ref.watch(unifiedInboxProvider(filter));
    return RefreshIndicator(
      onRefresh: () async {
        ref.invalidate(unifiedInboxProvider(filter));
        await ref.read(unifiedInboxProvider(filter).future);
      },
      child: asyncItems.when(
        data: (items) {
          if (items.isEmpty) return const _InboxEmpty();
          final grouped = _groupByDay(items);
          return ListView.builder(
            padding: const EdgeInsets.only(bottom: 80),
            itemCount: grouped.length,
            itemBuilder: (context, i) {
              final entry = grouped[i];
              if (entry is _DayHeader) {
                return Padding(
                  padding: const EdgeInsets.fromLTRB(16, 16, 16, 6),
                  child: Text(
                    entry.label,
                    style: AppTextStyles.labelSmall.copyWith(
                      color: AppColors.textTertiary,
                    ),
                  ),
                );
              }
              final row = entry as _ItemEntry;
              return _InboxRow(item: row.item);
            },
          );
        },
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              'Could not load inbox.\n$e',
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.center,
            ),
          ),
        ),
      ),
    );
  }

  /// Inserts `_DayHeader` markers between items whose dates differ.
  List<Object> _groupByDay(List<InboxItem> items) {
    final out = <Object>[];
    String? currentDay;
    for (final item in items) {
      final day = _dayLabel(item.time);
      if (day != currentDay) {
        out.add(_DayHeader(day));
        currentDay = day;
      }
      out.add(_ItemEntry(item));
    }
    return out;
  }
}

class _DayHeader {
  const _DayHeader(this.label);
  final String label;
}

class _ItemEntry {
  const _ItemEntry(this.item);
  final InboxItem item;
}

String _dayLabel(DateTime t) {
  final now = DateTime.now();
  final today = DateTime(now.year, now.month, now.day);
  final that = DateTime(t.year, t.month, t.day);
  final diff = today.difference(that).inDays;
  if (diff == 0) return 'Today';
  if (diff == 1) return 'Yesterday';
  if (diff < 7) return '$diff days ago';
  return '${that.year}-${that.month.toString().padLeft(2, '0')}-${that.day.toString().padLeft(2, '0')}';
}

class _InboxRow extends StatelessWidget {
  const _InboxRow({required this.item});

  final InboxItem item;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: item.unread
          ? AppColors.postbookPrimary.withValues(alpha: 0.04)
          : Colors.transparent,
      child: InkWell(
        onTap: () {
          final link = item.deepLink;
          if (link != null && link.isNotEmpty) {
            context.push(link);
          }
        },
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _Avatar(item: item),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Expanded(
                          child: Text(
                            item.title,
                            style: AppTextStyles.label.copyWith(
                              color: AppColors.textPrimary,
                              fontWeight: item.unread
                                  ? FontWeight.w700
                                  : FontWeight.w500,
                            ),
                          ),
                        ),
                        Text(
                          _shortTime(item.time),
                          style: AppTextStyles.labelSmall,
                        ),
                      ],
                    ),
                    if (item.snippet.isNotEmpty) ...[
                      const SizedBox(height: 4),
                      Text(
                        item.snippet,
                        style: AppTextStyles.bodySmall,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                  ],
                ),
              ),
              if (item.unread) ...[
                const SizedBox(width: 8),
                Container(
                  width: 8,
                  height: 8,
                  margin: const EdgeInsets.only(top: 6),
                  decoration: const BoxDecoration(
                    color: AppColors.postbookPrimary,
                    shape: BoxShape.circle,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  String _shortTime(DateTime t) {
    final diff = DateTime.now().difference(t);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
  }
}

class _Avatar extends StatelessWidget {
  const _Avatar({required this.item});

  final InboxItem item;

  @override
  Widget build(BuildContext context) {
    if (item.avatarUrl != null && item.avatarUrl!.isNotEmpty) {
      return CircleAvatar(
        radius: 22,
        backgroundColor: AppColors.bgTertiary,
        backgroundImage: NetworkImage(item.avatarUrl!),
      );
    }
    return CircleAvatar(
      radius: 22,
      backgroundColor: _bgForKind(item.kind),
      child: Icon(
        _iconForName(item.iconName),
        size: 20,
        color: Colors.white,
      ),
    );
  }
}

Color _bgForKind(InboxKind kind) {
  switch (kind) {
    case InboxKind.pulseMatch:
    case InboxKind.pulseSpark:
    case InboxKind.pulseMessage:
      return AppColors.postgramPrimary;
    case InboxKind.commerceOrderUpdate:
    case InboxKind.commerceShipped:
    case InboxKind.commerceDelivered:
      return AppColors.statusWarning;
    case InboxKind.system:
      return AppColors.posttubePrimary;
    case InboxKind.mention:
    case InboxKind.follow:
    case InboxKind.like:
    case InboxKind.comment:
    case InboxKind.other:
      return AppColors.postbookPrimary;
  }
}

IconData _iconForName(String? name) {
  switch (name) {
    case 'alternate_email':
      return Icons.alternate_email;
    case 'person_add':
      return Icons.person_add;
    case 'favorite':
      return Icons.favorite;
    case 'mode_comment':
      return Icons.mode_comment;
    case 'campaign':
      return Icons.campaign;
    case 'flash_on':
      return Icons.flash_on;
    case 'shopping_bag':
      return Icons.shopping_bag;
    default:
      return Icons.notifications;
  }
}

class _InboxEmpty extends StatelessWidget {
  const _InboxEmpty();

  @override
  Widget build(BuildContext context) {
    return ListView(
      children: [
        const SizedBox(height: 80),
        const Icon(
          Icons.inbox,
          color: AppColors.textDim,
          size: 48,
        ),
        const SizedBox(height: 12),
        Center(
          child: Text('All caught up', style: AppTextStyles.h2),
        ),
        const SizedBox(height: 4),
        Center(
          child: Text(
            'New activity will land here.',
            style: AppTextStyles.bodySmall,
          ),
        ),
      ],
    );
  }
}
