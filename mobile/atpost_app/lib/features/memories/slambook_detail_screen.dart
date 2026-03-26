import 'dart:async';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/slambook.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/memories_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/memories/slambook_data.dart';
import 'package:atpost_app/features/memories/slambook_response_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SlambookDetailScreen extends ConsumerStatefulWidget {
  const SlambookDetailScreen({super.key, required this.slambookId});

  final String slambookId;

  @override
  ConsumerState<SlambookDetailScreen> createState() => _SlambookDetailScreenState();
}

class _SlambookDetailScreenState extends ConsumerState<SlambookDetailScreen> {
  bool _working = false;

  Future<void> _refresh() async {
    ref.invalidate(slambookDetailProvider(widget.slambookId));
    ref.invalidate(slambookOpinionSpaceProvider(widget.slambookId));
    ref.invalidate(slambookModerationQueueProvider(widget.slambookId));
  }

  Future<void> _createShareLink() async {
    if (_working) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _working = true);
    try {
      final invite = await ref
          .read(memoriesRepositoryProvider)
          .createSlambookShareLink(widget.slambookId);
      final token = invite.shareToken;
      final shareUrl = token == null || token.isEmpty
          ? null
          : '${Environment.apiBaseUrl}/memories/slambooks/share/$token';
      if (!mounted) return;
      if (shareUrl != null) {
        await Clipboard.setData(ClipboardData(text: shareUrl));
      }
      ref.invalidate(mySlambooksProvider);
      messenger.showSnackBar(
        SnackBar(
          content: Text(
            shareUrl == null
                ? 'Share link prepared.'
                : 'Share link copied: $shareUrl',
          ),
        ),
      );
      await _refresh();
    } catch (_) {
      if (!mounted) return;
      messenger.showSnackBar(
        const SnackBar(content: Text('Could not create share link.')),
      );
    } finally {
      if (mounted) {
        setState(() => _working = false);
      }
    }
  }

  Future<void> _createDirectInvites() async {
    if (_working) return;
    final result = await showModalBottomSheet<_InviteSheetResult>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (context) => const _InvitePickerSheet(),
    );
    if (result == null) return;

    setState(() => _working = true);
    try {
      final invites = await ref.read(memoriesRepositoryProvider).createSlambookInvites(
            widget.slambookId,
            targetUserIds: result.targetUserIds,
            message: result.message.isEmpty ? null : result.message,
          );
      ref.invalidate(mySlambooksProvider);
      await _refresh();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('${invites.length} direct invites created.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not create direct invites.')),
      );
    } finally {
      if (mounted) {
        setState(() => _working = false);
      }
    }
  }

  Future<void> _archive() async {
    if (_working) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Archive SlamBook'),
        content: const Text('This moves the SlamBook out of the active flow.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Archive'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    setState(() => _working = true);
    try {
      await ref.read(memoriesRepositoryProvider).archiveSlambook(widget.slambookId);
      ref.invalidate(mySlambooksProvider);
      await _refresh();
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('SlamBook archived.')));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not archive the SlamBook.')),
      );
    } finally {
      if (mounted) {
        setState(() => _working = false);
      }
    }
  }

  Future<void> _moderate(String sessionId, String action) async {
    if (_working) return;
    setState(() => _working = true);
    try {
      await ref.read(memoriesRepositoryProvider).moderateSlambookSession(
            widget.slambookId,
            sessionId,
            action: action,
          );
      ref.invalidate(mySlambooksProvider);
      await _refresh();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Session ${action == 'approve' ? 'approved' : 'updated'}.')),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update moderation state.')),
      );
    } finally {
      if (mounted) {
        setState(() => _working = false);
      }
    }
  }

  Future<void> _togglePinned(SlambookOpinionSpaceItem item) async {
    if (_working) return;
    setState(() => _working = true);
    try {
      await ref.read(memoriesRepositoryProvider).pinSlambookOpinionItem(
            widget.slambookId,
            item.id,
            pinned: !item.isPinned,
          );
      ref.invalidate(mySlambooksProvider);
      ref.invalidate(slambookOpinionSpaceProvider(widget.slambookId));
      ref.invalidate(slambookDetailProvider(widget.slambookId));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update pinned state.')),
      );
    } finally {
      if (mounted) {
        setState(() => _working = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final detailAsync = ref.watch(slambookDetailProvider(widget.slambookId));
    final opinionAsync = ref.watch(slambookOpinionSpaceProvider(widget.slambookId));
    AsyncValue<List<SlambookResponseSession>>? moderationAsync;
    final canModerate = detailAsync.valueOrNull?.slambook.viewerCanModerate ?? false;
    if (canModerate) {
      moderationAsync = ref.watch(slambookModerationQueueProvider(widget.slambookId));
    }

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: const Text('SlamBook'),
      ),
      body: detailAsync.when(
        data: (detail) {
          final slambook = detail.slambook;
          final accent = slambookAccentColor(slambook.themeKey);
          return RefreshIndicator(
            color: AppColors.postbookPrimary,
            onRefresh: _refresh,
            child: ListView(
              padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 32),
              children: [
                Container(
                  padding: const EdgeInsets.all(16),
                  decoration: BoxDecoration(
                    color: AppColors.bgCard,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                    border: Border.all(color: AppColors.borderSubtle),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Container(
                            width: 44,
                            height: 44,
                            decoration: BoxDecoration(
                              color: accent.withValues(alpha: 0.18),
                              borderRadius: BorderRadius.circular(14),
                            ),
                            child: Icon(Icons.auto_stories, color: accent),
                          ),
                          const SizedBox(width: 12),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(slambook.title, style: AppTextStyles.h2),
                                if ((slambook.subtitle ?? '').trim().isNotEmpty)
                                  Text(slambook.subtitle!, style: AppTextStyles.bodySmall),
                              ],
                            ),
                          ),
                        ],
                      ),
                      if ((slambook.description ?? '').trim().isNotEmpty) ...[
                        const SizedBox(height: 12),
                        Text(slambook.description!, style: AppTextStyles.bodySmall),
                      ],
                      const SizedBox(height: 12),
                      Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        children: [
                          _ChipText(text: slambookVisibilityLabel(slambook.visibility)),
                          _ChipText(text: slambookIdentityLabel(slambook.responseIdentityMode)),
                          _ChipText(text: '${detail.cards.length} prompts'),
                          _ChipText(text: '${slambook.approvedCount} approved'),
                        ],
                      ),
                      const SizedBox(height: 14),
                      Row(
                        children: [
                          if (slambook.viewerCanRespond &&
                              slambook.status == 'active' &&
                              detail.viewerSession?.status != 'approved')
                            Expanded(
                              child: ElevatedButton.icon(
                                onPressed: _working
                                    ? null
                                    : () => Navigator.of(context).push(
                                          MaterialPageRoute<void>(
                                            builder: (_) => SlambookResponseScreen(
                                              slambookId: widget.slambookId,
                                            ),
                                          ),
                                        ),
                                style: ElevatedButton.styleFrom(
                                  backgroundColor: AppColors.postbookPrimary,
                                  foregroundColor: Colors.white,
                                ),
                                icon: const Icon(Icons.reply_outlined),
                                label: Text(
                                  detail.viewerSession == null ? 'Answer now' : 'Continue response',
                                ),
                              ),
                            ),
                          if (slambook.viewerCanModerate) ...[
                            if (slambook.viewerCanRespond &&
                                slambook.status == 'active' &&
                                detail.viewerSession?.status != 'approved')
                              const SizedBox(width: 10),
                            Expanded(
                              child: OutlinedButton.icon(
                                onPressed: _working ? null : _createShareLink,
                                icon: const Icon(Icons.link_outlined),
                                label: const Text('Share link'),
                              ),
                            ),
                            const SizedBox(width: 10),
                            Expanded(
                              child: OutlinedButton.icon(
                                onPressed: _working ? null : _createDirectInvites,
                                icon: const Icon(Icons.person_add_alt_1_outlined),
                                label: const Text('Invite users'),
                              ),
                            ),
                          ],
                        ],
                      ),
                      const SizedBox(height: 12),
                      Container(
                        padding: const EdgeInsets.all(12),
                        decoration: BoxDecoration(
                          color: AppColors.bgSecondary,
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Text(
                          slambook.viewerCanModerate
                              ? 'Use the controls above to create a share link or search and invite specific people directly.'
                              : 'If the owner shared a tokenized invite, you can continue that response flow from the shared link.',
                          style: AppTextStyles.bodySmall,
                        ),
                      ),
                      if (detail.viewerSession != null) ...[
                        const SizedBox(height: 12),
                        Container(
                          padding: const EdgeInsets.all(12),
                          decoration: BoxDecoration(
                            color: AppColors.bgSecondary,
                            borderRadius: BorderRadius.circular(12),
                          ),
                          child: Row(
                            children: [
                              const Icon(Icons.task_alt_outlined, color: AppColors.textSecondary),
                              const SizedBox(width: 10),
                              Expanded(
                                child: Text(
                                  'Your response status: ${detail.viewerSession!.status}',
                                  style: AppTextStyles.bodySmall,
                                ),
                              ),
                            ],
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
                const SizedBox(height: 18),
                Text('Prompt cards', style: AppTextStyles.h2),
                const SizedBox(height: 10),
                ...detail.cards.map(
                  (card) => Padding(
                    padding: const EdgeInsets.only(bottom: 10),
                    child: _PromptCard(card: card),
                  ),
                ),
                const SizedBox(height: 18),
                Row(
                  children: [
                    Text('Opinion board', style: AppTextStyles.h2),
                    const Spacer(),
                    Text(
                      '${slambook.pinnedCount} pinned',
                      style: AppTextStyles.labelSmall,
                    ),
                  ],
                ),
                const SizedBox(height: 10),
                opinionAsync.when(
                  data: (items) {
                    if (items.isEmpty) {
                      return const _InlineStateCard(
                        icon: Icons.push_pin_outlined,
                        message: 'No opinion board items are visible yet.',
                      );
                    }
                    return Column(
                      children: items
                          .map(
                            (item) => Padding(
                              padding: const EdgeInsets.only(bottom: 10),
                              child: _OpinionItemCard(
                                item: item,
                                canModerate: slambook.viewerCanModerate,
                                onTogglePin: slambook.viewerCanModerate
                                    ? () => _togglePinned(item)
                                    : null,
                              ),
                            ),
                          )
                          .toList(),
                    );
                  },
                  loading: () => const Center(
                    child: Padding(
                      padding: EdgeInsets.symmetric(vertical: 20),
                      child: CircularProgressIndicator(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  ),
                  error: (_, _) => const _InlineStateCard(
                    icon: Icons.push_pin_outlined,
                    message: 'Could not load the opinion board.',
                  ),
                ),
                if (slambook.viewerCanModerate) ...[
                  const SizedBox(height: 18),
                  Row(
                    children: [
                      Text('Moderation queue', style: AppTextStyles.h2),
                      const Spacer(),
                      if (_working)
                        const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                    ],
                  ),
                  const SizedBox(height: 10),
                  moderationAsync!.when(
                    data: (sessions) {
                      if (sessions.isEmpty) {
                        return const _InlineStateCard(
                          icon: Icons.rate_review_outlined,
                          message: 'No pending responses right now.',
                        );
                      }
                      return Column(
                        children: sessions
                            .map(
                              (session) => Padding(
                                padding: const EdgeInsets.only(bottom: 10),
                                child: _ModerationCard(
                                  session: session,
                                  onApprove: _working
                                      ? null
                                      : () => _moderate(session.id, 'approve'),
                                  onReject: _working
                                      ? null
                                      : () => _moderate(session.id, 'reject'),
                                ),
                              ),
                            )
                            .toList(),
                      );
                    },
                    loading: () => const Center(
                      child: Padding(
                        padding: EdgeInsets.symmetric(vertical: 20),
                        child: CircularProgressIndicator(
                          color: AppColors.postbookPrimary,
                        ),
                      ),
                    ),
                    error: (_, _) => const _InlineStateCard(
                      icon: Icons.rate_review_outlined,
                      message: 'Could not load the moderation queue.',
                    ),
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton.icon(
                    onPressed: _working ? null : _archive,
                    icon: const Icon(Icons.archive_outlined),
                    label: const Text('Archive SlamBook'),
                  ),
                ],
              ],
            ),
          );
        },
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => const Center(
          child: Text('Could not load the SlamBook.'),
        ),
      ),
    );
  }
}

class _PromptCard extends StatelessWidget {
  const _PromptCard({required this.card});

  final SlambookCard card;

  @override
  Widget build(BuildContext context) {
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
              Expanded(child: Text(card.title, style: AppTextStyles.h3)),
              if (card.isRequired)
                Text(
                  'Required',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 6),
          Text(card.prompt, style: AppTextStyles.bodySmall),
          if ((card.helpText ?? '').trim().isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(card.helpText!, style: AppTextStyles.labelSmall),
          ],
        ],
      ),
    );
  }
}

class _InviteSheetResult {
  const _InviteSheetResult({
    required this.targetUserIds,
    required this.message,
  });

  final List<String> targetUserIds;
  final String message;
}

class _InvitePickerSheet extends ConsumerStatefulWidget {
  const _InvitePickerSheet();

  @override
  ConsumerState<_InvitePickerSheet> createState() => _InvitePickerSheetState();
}

class _InvitePickerSheetState extends ConsumerState<_InvitePickerSheet> {
  final _searchController = TextEditingController();
  final _messageController = TextEditingController();
  final List<User> _selectedUsers = <User>[];
  final List<User> _results = <User>[];
  Timer? _debounce;
  int _searchVersion = 0;
  bool _searching = false;

  @override
  void dispose() {
    _debounce?.cancel();
    _searchController.dispose();
    _messageController.dispose();
    super.dispose();
  }

  void _onSearchChanged(String value) {
    _debounce?.cancel();
    final query = value.trim();
    if (query.length < 2) {
      setState(() {
        _results.clear();
        _searching = false;
      });
      return;
    }

    setState(() => _searching = true);

    _debounce = Timer(const Duration(milliseconds: 300), () async {
      final version = ++_searchVersion;
      try {
        final result = await ref.read(userRepositoryProvider).searchUsers(query, limit: 8);
        if (!mounted || version != _searchVersion) return;
        final selectedIds = _selectedUsers.map((user) => user.id).toSet();
        setState(() {
          _results
            ..clear()
            ..addAll(
              result.users.where(
                (user) => user.id.isNotEmpty && !selectedIds.contains(user.id),
              ),
            );
          _searching = false;
        });
      } catch (_) {
        if (!mounted || version != _searchVersion) return;
        setState(() {
          _results.clear();
          _searching = false;
        });
      }
    });
  }

  void _selectUser(User user) {
    if (_selectedUsers.any((item) => item.id == user.id)) return;
    setState(() {
      _selectedUsers.add(user);
      _results.removeWhere((item) => item.id == user.id);
      _searchController.clear();
      _searching = false;
    });
  }

  void _removeUser(String userId) {
    setState(() {
      _selectedUsers.removeWhere((user) => user.id == userId);
    });
  }

  @override
  Widget build(BuildContext context) {
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;
    return SafeArea(
      top: false,
      child: Padding(
        padding: EdgeInsets.only(
          left: 16,
          right: 16,
          top: 16,
          bottom: bottomInset + 16,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Direct invites', style: AppTextStyles.h2),
            const SizedBox(height: 8),
            Text(
              'Search people by name or username, then add them to the invite list.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _searchController,
              onChanged: _onSearchChanged,
              decoration: const InputDecoration(
                hintText: 'Search by name or username',
                prefixIcon: Icon(Icons.search),
              ),
            ),
            const SizedBox(height: 10),
            if (_selectedUsers.isNotEmpty)
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: _selectedUsers
                    .map(
                      (user) => InputChip(
                        avatar: _UserAvatar(user: user),
                        label: Text(
                          user.displayName.isEmpty ? user.username : user.displayName,
                        ),
                        onDeleted: () => _removeUser(user.id),
                        backgroundColor: AppColors.bgCard,
                        side: const BorderSide(color: AppColors.borderSubtle),
                      ),
                    )
                    .toList(),
              ),
            if (_selectedUsers.isNotEmpty) const SizedBox(height: 10),
            Container(
              constraints: const BoxConstraints(maxHeight: 240),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius: BorderRadius.circular(16),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: _searchController.text.trim().length < 2
                  ? Padding(
                      padding: const EdgeInsets.all(14),
                      child: Text(
                        'Type at least 2 characters to search.',
                        style: AppTextStyles.bodySmall,
                      ),
                    )
                  : _searching
                      ? const Padding(
                          padding: EdgeInsets.all(20),
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        )
                      : _results.isEmpty
                          ? Padding(
                              padding: const EdgeInsets.all(14),
                              child: Text(
                                'No matching people found.',
                                style: AppTextStyles.bodySmall,
                              ),
                            )
                          : ListView.separated(
                              shrinkWrap: true,
                              itemCount: _results.length,
                              separatorBuilder: (_, _) =>
                                  const Divider(height: 1, color: AppColors.borderSubtle),
                              itemBuilder: (context, index) {
                                final user = _results[index];
                                return ListTile(
                                  leading: _UserAvatar(user: user, radius: 20),
                                  title: Text(
                                    user.displayName.isEmpty ? 'PostBook user' : user.displayName,
                                    style: AppTextStyles.body,
                                  ),
                                  subtitle: Text(
                                    user.username.isEmpty ? user.id : '@${user.username}',
                                    style: AppTextStyles.bodySmall,
                                  ),
                                  trailing: const Icon(Icons.add_circle_outline),
                                  onTap: () => _selectUser(user),
                                );
                              },
                            ),
            ),
            const SizedBox(height: 10),
            TextField(
              controller: _messageController,
              minLines: 2,
              maxLines: 3,
              decoration: const InputDecoration(
                hintText: 'Optional note for invited friends',
              ),
            ),
            const SizedBox(height: 14),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton.icon(
                onPressed: _selectedUsers.isEmpty
                    ? null
                    : () => Navigator.of(context).pop(
                          _InviteSheetResult(
                            targetUserIds: _selectedUsers.map((user) => user.id).toList(),
                            message: _messageController.text.trim(),
                          ),
                        ),
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  foregroundColor: Colors.white,
                ),
                icon: const Icon(Icons.person_add_alt_1_outlined),
                label: Text('Send invites (${_selectedUsers.length})'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _UserAvatar extends StatelessWidget {
  const _UserAvatar({
    required this.user,
    this.radius = 16,
  });

  final User user;
  final double radius;

  @override
  Widget build(BuildContext context) {
    if (user.hasAvatar) {
      return CircleAvatar(
        radius: radius,
        backgroundImage: NetworkImage(user.avatarUrl),
        backgroundColor: AppColors.bgSecondary,
      );
    }
    final label = user.displayName.isNotEmpty
        ? user.displayName
        : (user.username.isNotEmpty ? user.username : 'P');
    return CircleAvatar(
      radius: radius,
      backgroundColor: AppColors.postbookPrimary.withValues(alpha: 0.16),
      child: Text(
        label.substring(0, 1).toUpperCase(),
        style: AppTextStyles.label.copyWith(color: AppColors.postbookPrimary),
      ),
    );
  }
}

class _OpinionItemCard extends StatelessWidget {
  const _OpinionItemCard({
    required this.item,
    required this.canModerate,
    this.onTogglePin,
  });

  final SlambookOpinionSpaceItem item;
  final bool canModerate;
  final VoidCallback? onTogglePin;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: item.isPinned ? AppColors.postbookPrimary : AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(item.cardTitle, style: AppTextStyles.h3),
              ),
              if (item.isPinned)
                Text(
                  'Pinned',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: AppColors.postbookPrimary,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 6),
          Text(slambookBoardPreview(item), style: AppTextStyles.bodySmall),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: Text(
                  item.anonymous
                      ? 'Anonymous'
                      : (item.responderDisplayName?.trim().isNotEmpty ?? false)
                          ? item.responderDisplayName!
                          : 'PostBook user',
                  style: AppTextStyles.labelSmall,
                ),
              ),
              if (canModerate)
                TextButton.icon(
                  onPressed: onTogglePin,
                  icon: Icon(item.isPinned ? Icons.push_pin : Icons.push_pin_outlined),
                  label: Text(item.isPinned ? 'Unpin' : 'Pin'),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _ModerationCard extends StatelessWidget {
  const _ModerationCard({
    required this.session,
    this.onApprove,
    this.onReject,
  });

  final SlambookResponseSession session;
  final VoidCallback? onApprove;
  final VoidCallback? onReject;

  @override
  Widget build(BuildContext context) {
    final answers = session.items.map(slambookAnswerPreview).take(3).join(' | ');
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
          Text(
            session.displayName?.trim().isNotEmpty ?? false
                ? session.displayName!
                : 'Anonymous responder',
            style: AppTextStyles.h3,
          ),
          const SizedBox(height: 6),
          Text(
            answers.isEmpty ? 'No answers captured' : answers,
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: onReject,
                  child: const Text('Reject'),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: ElevatedButton(
                  onPressed: onApprove,
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                    foregroundColor: Colors.white,
                  ),
                  child: const Text('Approve'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _ChipText extends StatelessWidget {
  const _ChipText({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      ),
      child: Text(text, style: AppTextStyles.labelSmall),
    );
  }
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({
    required this.icon,
    required this.message,
  });

  final IconData icon;
  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(18),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textSecondary),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
        ],
      ),
    );
  }
}
