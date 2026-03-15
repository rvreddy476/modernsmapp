import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/social_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class FriendRequestsScreen extends ConsumerStatefulWidget {
  const FriendRequestsScreen({super.key});

  @override
  ConsumerState<FriendRequestsScreen> createState() =>
      _FriendRequestsScreenState();
}

class _FriendRequestsScreenState extends ConsumerState<FriendRequestsScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabController;
  final TextEditingController _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    _searchController.dispose();
    super.dispose();
  }

  String _relativeTime(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inSeconds < 60) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    if (diff.inDays < 7) return '${diff.inDays}d ago';
    return '${(diff.inDays / 7).floor()}w ago';
  }

  List<FriendRequest> _filter(List<FriendRequest> requests) {
    final query = _searchController.text.trim().toLowerCase();
    if (query.isEmpty) return requests;

    return requests.where((request) {
      final isReceived = request.direction == 'received';
      final name = isReceived ? request.senderName : request.receiverName;
      final id = isReceived ? request.senderId : request.receiverId;
      return name.toLowerCase().contains(query) ||
          id.toLowerCase().contains(query);
    }).toList();
  }

  @override
  Widget build(BuildContext context) {
    final requestsAsync = ref.watch(friendRequestsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: requestsAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (_, _) => Center(
            child: _InlineStateCard(
              icon: Icons.mail_lock_outlined,
              message: 'Could not load friend requests.',
              action: 'Retry',
              onTap: () => ref.invalidate(friendRequestsProvider),
            ),
          ),
          data: (requests) {
            final receivedAll = requests
                .where((request) => request.direction == 'received')
                .toList();
            final sentAll = requests
                .where((request) => request.direction == 'sent')
                .toList();
            final received = _filter(receivedAll);
            final sent = _filter(sentAll);

            return Column(
              children: [
                Padding(
                  padding: AppSpacing.pagePadding.copyWith(top: 10),
                  child: _HeaderCard(
                    receivedCount: receivedAll.length,
                    sentCount: sentAll.length,
                    onBack: () => context.pop(),
                    onRefresh: () => ref.invalidate(friendRequestsProvider),
                    onViewFriends: () => context.push('/friends'),
                  ),
                ),
                const SizedBox(height: 12),
                Padding(
                  padding: AppSpacing.pagePadding,
                  child: Container(
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge,
                      ),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: TextField(
                      controller: _searchController,
                      onChanged: (_) => setState(() {}),
                      style: AppTextStyles.body,
                      decoration: InputDecoration(
                        border: InputBorder.none,
                        hintText: 'Search requests',
                        hintStyle: AppTextStyles.bodySmall,
                        prefixIcon: const Icon(
                          Icons.search,
                          color: AppColors.textMuted,
                        ),
                        suffixIcon: _searchController.text.isEmpty
                            ? null
                            : IconButton(
                                onPressed: () {
                                  _searchController.clear();
                                  setState(() {});
                                },
                                icon: const Icon(
                                  Icons.close,
                                  color: AppColors.textMuted,
                                ),
                              ),
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 12),
                Padding(
                  padding: AppSpacing.pagePadding,
                  child: Container(
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius: BorderRadius.circular(
                        AppSpacing.radiusLarge,
                      ),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: TabBar(
                      controller: _tabController,
                      labelColor: AppColors.postbookPrimary,
                      unselectedLabelColor: AppColors.textDim,
                      indicatorColor: AppColors.postbookPrimary,
                      tabs: [
                        Tab(text: 'Received (${receivedAll.length})'),
                        Tab(text: 'Sent (${sentAll.length})'),
                      ],
                    ),
                  ),
                ),
                Expanded(
                  child: RefreshIndicator(
                    color: AppColors.postbookPrimary,
                    onRefresh: () async =>
                        ref.invalidate(friendRequestsProvider),
                    child: TabBarView(
                      controller: _tabController,
                      children: [
                        _RequestList(
                          requests: received,
                          emptyText: receivedAll.isEmpty
                              ? 'No incoming requests right now.'
                              : 'No received requests match your search.',
                          emptyActionLabel: _searchController.text.isNotEmpty
                              ? 'Clear'
                              : 'Refresh',
                          onEmptyAction: _searchController.text.isNotEmpty
                              ? () {
                                  _searchController.clear();
                                  setState(() {});
                                }
                              : () => ref.invalidate(friendRequestsProvider),
                          itemBuilder: (request) => _ReceivedRequestTile(
                            request: request,
                            relativeTime: _relativeTime(request.createdAt),
                            onAction: () =>
                                ref.invalidate(friendRequestsProvider),
                          ),
                        ),
                        _RequestList(
                          requests: sent,
                          emptyText: sentAll.isEmpty
                              ? 'No sent requests yet.'
                              : 'No sent requests match your search.',
                          emptyActionLabel: _searchController.text.isNotEmpty
                              ? 'Clear'
                              : 'Refresh',
                          onEmptyAction: _searchController.text.isNotEmpty
                              ? () {
                                  _searchController.clear();
                                  setState(() {});
                                }
                              : () => ref.invalidate(friendRequestsProvider),
                          itemBuilder: (request) => _SentRequestTile(
                            request: request,
                            relativeTime: _relativeTime(request.createdAt),
                            onAction: () =>
                                ref.invalidate(friendRequestsProvider),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _HeaderCard extends StatelessWidget {
  const _HeaderCard({
    required this.receivedCount,
    required this.sentCount,
    required this.onBack,
    required this.onRefresh,
    required this.onViewFriends,
  });

  final int receivedCount;
  final int sentCount;
  final VoidCallback onBack;
  final VoidCallback onRefresh;
  final VoidCallback onViewFriends;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          colors: [Color(0x33FF6B35), Color(0x334ECDC4), Color(0x337B68EE)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Column(
        children: [
          Row(
            children: [
              IconButton(
                onPressed: onBack,
                icon: const Icon(
                  Icons.arrow_back_ios_new_rounded,
                  size: 18,
                  color: AppColors.textPrimary,
                ),
              ),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  'Friend Requests',
                  style: AppTextStyles.h1.copyWith(fontSize: 30),
                ),
              ),
              IconButton(
                onPressed: onRefresh,
                icon: const Icon(
                  Icons.refresh_rounded,
                  color: AppColors.textPrimary,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              _Pill(label: 'Received', value: '$receivedCount'),
              const SizedBox(width: 8),
              _Pill(label: 'Sent', value: '$sentCount'),
              const Spacer(),
              ElevatedButton.icon(
                onPressed: onViewFriends,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10),
                  ),
                ),
                icon: const Icon(Icons.groups_2_outlined, size: 16),
                label: const Text('Friends'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(value, style: AppTextStyles.h3),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _RequestList extends StatelessWidget {
  const _RequestList({
    required this.requests,
    required this.emptyText,
    required this.emptyActionLabel,
    required this.onEmptyAction,
    required this.itemBuilder,
  });

  final List<FriendRequest> requests;
  final String emptyText;
  final String emptyActionLabel;
  final VoidCallback onEmptyAction;
  final Widget Function(FriendRequest request) itemBuilder;

  @override
  Widget build(BuildContext context) {
    if (requests.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          SizedBox(
            height: 300,
            child: Center(
              child: _InlineStateCard(
                icon: Icons.mail_outline_rounded,
                message: emptyText,
                action: emptyActionLabel,
                onTap: onEmptyAction,
              ),
            ),
          ),
        ],
      );
    }

    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 110),
      itemCount: requests.length,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, index) => itemBuilder(requests[index]),
    );
  }
}

class _ReceivedRequestTile extends ConsumerStatefulWidget {
  const _ReceivedRequestTile({
    required this.request,
    required this.relativeTime,
    required this.onAction,
  });

  final FriendRequest request;
  final String relativeTime;
  final VoidCallback onAction;

  @override
  ConsumerState<_ReceivedRequestTile> createState() =>
      _ReceivedRequestTileState();
}

class _ReceivedRequestTileState extends ConsumerState<_ReceivedRequestTile> {
  bool _loading = false;

  Future<void> _accept() async {
    if (_loading) return;

    setState(() => _loading = true);
    try {
      final repo = ref.read(userRepositoryProvider);
      await repo.acceptFriendRequest(widget.request.senderId);
      widget.onAction();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not accept request.')),
      );
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _decline() async {
    if (_loading) return;

    setState(() => _loading = true);
    try {
      final repo = ref.read(userRepositoryProvider);
      await repo.rejectFriendRequest(widget.request.senderId);
      widget.onAction();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not decline request.')),
      );
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final request = widget.request;
    final initial = request.senderName.isEmpty
        ? 'U'
        : request.senderName.substring(0, 1).toUpperCase();

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 22,
            backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
            child: Text(
              initial,
              style: AppTextStyles.label.copyWith(
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  request.senderName,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.label,
                ),
                Row(
                  children: [
                    Text(widget.relativeTime, style: AppTextStyles.labelSmall),
                    if (request.mutualFriendsCount > 0) ...[
                      const SizedBox(width: 6),
                      Icon(
                        Icons.people_outline,
                        size: 12,
                        color: AppColors.textMuted,
                      ),
                      const SizedBox(width: 2),
                      Text(
                        '${request.mutualFriendsCount} mutual',
                        style: AppTextStyles.labelSmall.copyWith(
                          color: AppColors.textMuted,
                        ),
                      ),
                    ],
                  ],
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          _loading
              ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    TextButton(
                      onPressed: _accept,
                      style: TextButton.styleFrom(
                        foregroundColor: AppColors.postbookPrimary,
                      ),
                      child: const Text('Accept'),
                    ),
                    TextButton(
                      onPressed: _decline,
                      style: TextButton.styleFrom(
                        foregroundColor: AppColors.textDim,
                      ),
                      child: const Text('Reject'),
                    ),
                  ],
                ),
        ],
      ),
    );
  }
}

class _SentRequestTile extends ConsumerStatefulWidget {
  const _SentRequestTile({
    required this.request,
    required this.relativeTime,
    required this.onAction,
  });

  final FriendRequest request;
  final String relativeTime;
  final VoidCallback onAction;

  @override
  ConsumerState<_SentRequestTile> createState() => _SentRequestTileState();
}

class _SentRequestTileState extends ConsumerState<_SentRequestTile> {
  bool _loading = false;

  Future<void> _cancel() async {
    if (_loading) return;

    setState(() => _loading = true);
    try {
      final repo = ref.read(userRepositoryProvider);
      await repo.rejectFriendRequest(widget.request.receiverId);
      widget.onAction();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not cancel request.')),
      );
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final request = widget.request;
    final displayName = request.receiverName.isNotEmpty
        ? request.receiverName
        : request.receiverId;
    final initial = displayName.isEmpty
        ? 'U'
        : displayName.substring(0, 1).toUpperCase();

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 22,
            backgroundColor: AppColors.accentPurple.withValues(alpha: 0.2),
            child: Text(
              initial,
              style: AppTextStyles.label.copyWith(
                color: AppColors.accentPurple,
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  displayName,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.label,
                ),
                Text(widget.relativeTime, style: AppTextStyles.labelSmall),
              ],
            ),
          ),
          const SizedBox(width: 8),
          _loading
              ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : TextButton(
                  onPressed: _cancel,
                  style: TextButton.styleFrom(
                    foregroundColor: AppColors.postgramPrimary,
                  ),
                  child: const Text('Cancel'),
                ),
        ],
      ),
    );
  }
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({
    required this.icon,
    required this.message,
    required this.action,
    required this.onTap,
  });

  final IconData icon;
  final String message;
  final String action;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: AppSpacing.pagePadding,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textMuted),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
          TextButton(
            onPressed: onTap,
            child: Text(
              action,
              style: AppTextStyles.label.copyWith(
                color: AppColors.postbookPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
