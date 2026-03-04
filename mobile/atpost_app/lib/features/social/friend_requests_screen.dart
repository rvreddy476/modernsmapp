import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// ---------------------------------------------------------------------------
// Local model (not exported as a separate file)
// ---------------------------------------------------------------------------

class _FriendRequest {
  final String id;
  final String senderId;
  final String senderName;
  final String receiverId;
  final String? senderAvatarId;
  final DateTime createdAt;
  final String direction; // 'received' | 'sent'

  const _FriendRequest({
    required this.id,
    required this.senderId,
    required this.senderName,
    required this.receiverId,
    this.senderAvatarId,
    required this.createdAt,
    required this.direction,
  });

  factory _FriendRequest.fromJson(Map<String, dynamic> json) {
    return _FriendRequest(
      id: json['id'] as String? ?? '',
      senderId: json['sender_id'] as String? ?? '',
      senderName: json['sender_name'] as String? ?? json['sender_id'] as String? ?? '',
      receiverId: json['receiver_id'] as String? ?? '',
      senderAvatarId: json['sender_avatar_id'] as String?,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
      direction: json['direction'] as String? ?? 'received',
    );
  }
}

// ---------------------------------------------------------------------------
// Providers (scoped to this file)
// ---------------------------------------------------------------------------

final _friendRequestsProvider =
    FutureProvider.autoDispose<List<_FriendRequest>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/graph/friend-requests');
  final items = (response.data['data'] as List<dynamic>?) ?? [];
  return items
      .map((e) => _FriendRequest.fromJson(e as Map<String, dynamic>))
      .toList();
});

// ---------------------------------------------------------------------------
// Screen
// ---------------------------------------------------------------------------

class FriendRequestsScreen extends ConsumerStatefulWidget {
  const FriendRequestsScreen({super.key});

  @override
  ConsumerState<FriendRequestsScreen> createState() =>
      _FriendRequestsScreenState();
}

class _FriendRequestsScreenState extends ConsumerState<FriendRequestsScreen>
    with SingleTickerProviderStateMixin {
  late TabController _tabController;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
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

  Widget _buildEmptyState(String message) {
    return Center(
      child: Text(
        message,
        style: AppTextStyles.body.copyWith(color: AppColors.textDim),
      ),
    );
  }

  Widget _buildReceivedTab(List<_FriendRequest> requests) {
    final received =
        requests.where((r) => r.direction == 'received').toList();
    if (received.isEmpty) return _buildEmptyState('No friend requests');
    return ListView.separated(
      itemCount: received.length,
      separatorBuilder: (_, _) =>
          Divider(height: 1, color: AppColors.borderSubtle),
      itemBuilder: (context, index) =>
          _ReceivedRequestTile(
        request: received[index],
        relativeTime: _relativeTime(received[index].createdAt),
        onAction: () => ref.invalidate(_friendRequestsProvider),
      ),
    );
  }

  Widget _buildSentTab(List<_FriendRequest> requests) {
    final sent = requests.where((r) => r.direction == 'sent').toList();
    if (sent.isEmpty) return _buildEmptyState('No sent requests');
    return ListView.separated(
      itemCount: sent.length,
      separatorBuilder: (_, _) =>
          Divider(height: 1, color: AppColors.borderSubtle),
      itemBuilder: (context, index) => _SentRequestTile(
        request: sent[index],
        relativeTime: _relativeTime(sent[index].createdAt),
        onAction: () => ref.invalidate(_friendRequestsProvider),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final asyncRequests = ref.watch(_friendRequestsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Friend Requests', style: AppTextStyles.h2),
        leading: BackButton(color: AppColors.textPrimary),
        bottom: TabBar(
          controller: _tabController,
          labelStyle: AppTextStyles.label,
          unselectedLabelStyle:
              AppTextStyles.label.copyWith(color: AppColors.textDim),
          labelColor: AppColors.postbookPrimary,
          unselectedLabelColor: AppColors.textDim,
          indicatorColor: AppColors.postbookPrimary,
          tabs: const [
            Tab(text: 'Received'),
            Tab(text: 'Sent'),
          ],
        ),
      ),
      body: RefreshIndicator(
        color: AppColors.postbookPrimary,
        onRefresh: () async => ref.invalidate(_friendRequestsProvider),
        child: asyncRequests.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (err, _) => Center(
            child: Text(
              'Failed to load friend requests',
              style: AppTextStyles.body.copyWith(color: AppColors.textDim),
            ),
          ),
          data: (requests) => TabBarView(
            controller: _tabController,
            children: [
              _buildReceivedTab(requests),
              _buildSentTab(requests),
            ],
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Private tile widgets
// ---------------------------------------------------------------------------

class _ReceivedRequestTile extends ConsumerStatefulWidget {
  const _ReceivedRequestTile({
    required this.request,
    required this.relativeTime,
    required this.onAction,
  });

  final _FriendRequest request;
  final String relativeTime;
  final VoidCallback onAction;

  @override
  ConsumerState<_ReceivedRequestTile> createState() =>
      _ReceivedRequestTileState();
}

class _ReceivedRequestTileState extends ConsumerState<_ReceivedRequestTile> {
  bool _loading = false;

  Future<void> _accept() async {
    setState(() => _loading = true);
    try {
      await ref
          .read(apiClientProvider)
          .post('/v1/graph/friend-requests/${widget.request.id}/accept');
      widget.onAction();
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _decline() async {
    setState(() => _loading = true);
    try {
      await ref
          .read(apiClientProvider)
          .post('/v1/graph/friend-requests/${widget.request.id}/decline');
      widget.onAction();
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final req = widget.request;
    return ListTile(
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 18, vertical: 6),
      leading: CircleAvatar(
        backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
        child: Text(
          req.senderName.isNotEmpty ? req.senderName[0].toUpperCase() : '?',
          style: AppTextStyles.h3.copyWith(color: AppColors.postbookPrimary),
        ),
      ),
      title: Text(
        req.senderName,
        style: AppTextStyles.body.copyWith(
          fontWeight: FontWeight.bold,
          color: AppColors.textPrimary,
        ),
      ),
      subtitle: Text(widget.relativeTime, style: AppTextStyles.labelSmall),
      trailing: _loading
          ? const SizedBox(
              width: 24,
              height: 24,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextButton(
                  onPressed: _accept,
                  style: TextButton.styleFrom(
                    foregroundColor: AppColors.postbookPrimary,
                    padding: const EdgeInsets.symmetric(horizontal: 10),
                  ),
                  child: const Text('Accept'),
                ),
                TextButton(
                  onPressed: _decline,
                  style: TextButton.styleFrom(
                    foregroundColor: AppColors.textDim,
                    padding: const EdgeInsets.symmetric(horizontal: 10),
                  ),
                  child: const Text('Decline'),
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

  final _FriendRequest request;
  final String relativeTime;
  final VoidCallback onAction;

  @override
  ConsumerState<_SentRequestTile> createState() => _SentRequestTileState();
}

class _SentRequestTileState extends ConsumerState<_SentRequestTile> {
  bool _loading = false;

  Future<void> _cancel() async {
    setState(() => _loading = true);
    try {
      await ref
          .read(apiClientProvider)
          .delete('/v1/graph/friend-requests/${widget.request.id}');
      widget.onAction();
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final req = widget.request;
    return ListTile(
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 18, vertical: 6),
      leading: CircleAvatar(
        backgroundColor: AppColors.accentPurple.withValues(alpha: 0.2),
        child: Text(
          req.receiverId.isNotEmpty ? req.receiverId[0].toUpperCase() : '?',
          style: AppTextStyles.h3.copyWith(color: AppColors.accentPurple),
        ),
      ),
      title: Text(
        req.receiverId,
        style: AppTextStyles.body.copyWith(
          fontWeight: FontWeight.bold,
          color: AppColors.textPrimary,
        ),
      ),
      subtitle: Text(widget.relativeTime, style: AppTextStyles.labelSmall),
      trailing: _loading
          ? const SizedBox(
              width: 24,
              height: 24,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : TextButton(
              onPressed: _cancel,
              style: TextButton.styleFrom(
                foregroundColor: AppColors.postgramPrimary,
                padding: const EdgeInsets.symmetric(horizontal: 10),
              ),
              child: const Text('Cancel'),
            ),
    );
  }
}
