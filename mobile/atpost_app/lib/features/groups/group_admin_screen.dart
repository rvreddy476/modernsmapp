import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/models/group_post.dart';
import 'package:atpost_app/data/repositories/group_posts_repository.dart';
import 'package:atpost_app/providers/group_posts_provider.dart';
import 'package:atpost_app/providers/groups_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class GroupAdminScreen extends ConsumerWidget {
  final String groupId;

  const GroupAdminScreen({super.key, required this.groupId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final groupAsync = ref.watch(groupDetailProvider(groupId));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back, color: AppColors.textPrimary),
          onPressed: () => context.pop(),
        ),
        title: Text('Admin Panel', style: AppTextStyles.h2),
      ),
      body: groupAsync.when(
        loading: () => const Center(
          child:
              CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => Center(
          child: Text('Failed to load group.', style: AppTextStyles.body),
        ),
        data: (group) => _AdminBody(groupId: groupId, group: group),
      ),
    );
  }
}

class _AdminBody extends ConsumerWidget {
  final String groupId;
  final Group group;

  const _AdminBody({required this.groupId, required this.group});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final pendingAsync = ref.watch(pendingPostsProvider(groupId));
    final requestsAsync = ref.watch(joinRequestsProvider(groupId));
    final channelsAsync = ref.watch(groupChannelsProvider(groupId));

    return ListView(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
      children: [
        // Overview
        _SectionCard(
          title: 'Overview',
          child: Row(
            children: [
              _StatTile(label: 'Members', value: group.memberCount.toString()),
              _StatTile(label: 'Posts', value: group.postCount.toString()),
              _StatTile(label: 'Privacy', value: group.privacy),
            ],
          ),
        ),
        const SizedBox(height: 12),

        // Pending Posts
        _SectionCard(
          title: 'Pending Posts',
          child: pendingAsync.when(
            loading: () => const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => Padding(
              padding: const EdgeInsets.all(12),
              child: Text('Could not load pending posts.',
                  style: AppTextStyles.bodySmall),
            ),
            data: (posts) {
              if (posts.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.all(12),
                  child: Text('No pending posts.',
                      style: AppTextStyles.bodySmall
                          .copyWith(color: AppColors.textMuted)),
                );
              }
              return Column(
                children: posts
                    .map((p) =>
                        _PendingPostTile(groupId: groupId, post: p))
                    .toList(),
              );
            },
          ),
        ),
        const SizedBox(height: 12),

        // Join Requests
        _SectionCard(
          title: 'Join Requests',
          child: requestsAsync.when(
            loading: () => const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => Padding(
              padding: const EdgeInsets.all(12),
              child: Text('Could not load requests.',
                  style: AppTextStyles.bodySmall),
            ),
            data: (requests) {
              if (requests.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.all(12),
                  child: Text('No pending requests.',
                      style: AppTextStyles.bodySmall
                          .copyWith(color: AppColors.textMuted)),
                );
              }
              return Column(
                children: requests.map((r) {
                  final userId = r['user_id'] as String? ?? '';
                  final name =
                      r['display_name'] as String? ?? r['username'] as String? ?? userId;
                  return _JoinRequestTile(
                    groupId: groupId,
                    userId: userId,
                    displayName: name,
                  );
                }).toList(),
              );
            },
          ),
        ),
        const SizedBox(height: 12),

        // Channels
        _SectionCard(
          title: 'Channels',
          child: channelsAsync.when(
            loading: () => const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: AppColors.postbookPrimary),
              ),
            ),
            error: (_, _) => Padding(
              padding: const EdgeInsets.all(12),
              child: Text('Could not load channels.',
                  style: AppTextStyles.bodySmall),
            ),
            data: (channels) {
              if (channels.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.all(12),
                  child: Text('No channels created.',
                      style: AppTextStyles.bodySmall
                          .copyWith(color: AppColors.textMuted)),
                );
              }
              return Column(
                children: channels
                    .map((ch) => ListTile(
                          dense: true,
                          leading: Icon(
                            ch.isDefault
                                ? Icons.tag
                                : Icons.tag_outlined,
                            color: AppColors.postbookPrimary,
                            size: 18,
                          ),
                          title: Text('#${ch.name}',
                              style: AppTextStyles.label),
                          subtitle: Text(
                            '${ch.postCount} posts · ${ch.type}',
                            style: AppTextStyles.labelSmall
                                .copyWith(color: AppColors.textMuted),
                          ),
                          trailing: ch.isArchived
                              ? Container(
                                  padding: const EdgeInsets.symmetric(
                                      horizontal: 6, vertical: 2),
                                  decoration: BoxDecoration(
                                    color: AppColors.textDim
                                        .withValues(alpha: 0.2),
                                    borderRadius:
                                        BorderRadius.circular(8),
                                  ),
                                  child: Text('Archived',
                                      style: AppTextStyles.labelSmall
                                          .copyWith(fontSize: 10)),
                                )
                              : null,
                        ))
                    .toList(),
              );
            },
          ),
        ),
      ],
    );
  }
}

class _SectionCard extends StatelessWidget {
  final String title;
  final Widget child;

  const _SectionCard({required this.title, required this.child});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 4),
            child: Text(title, style: AppTextStyles.h3),
          ),
          child,
        ],
      ),
    );
  }
}

class _StatTile extends StatelessWidget {
  final String label;
  final String value;

  const _StatTile({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    return Expanded(
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          children: [
            Text(value, style: AppTextStyles.h3),
            const SizedBox(height: 2),
            Text(label,
                style: AppTextStyles.labelSmall
                    .copyWith(color: AppColors.textMuted)),
          ],
        ),
      ),
    );
  }
}

class _PendingPostTile extends ConsumerStatefulWidget {
  final String groupId;
  final GroupPost post;

  const _PendingPostTile({required this.groupId, required this.post});

  @override
  ConsumerState<_PendingPostTile> createState() =>
      _PendingPostTileState();
}

class _PendingPostTileState extends ConsumerState<_PendingPostTile> {
  bool _busy = false;

  Future<void> _approve() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(groupPostsRepositoryProvider)
          .approvePost(widget.groupId, widget.post.id);
      ref.invalidate(pendingPostsProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to approve.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _reject() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(groupPostsRepositoryProvider)
          .rejectPost(widget.groupId, widget.post.id);
      ref.invalidate(pendingPostsProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to reject.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListTile(
      dense: true,
      title: Text(
        widget.post.body ?? widget.post.title ?? '(no content)',
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: AppTextStyles.bodySmall,
      ),
      subtitle: Text(
        'by ${widget.post.authorName ?? 'Unknown'}',
        style:
            AppTextStyles.labelSmall.copyWith(color: AppColors.textMuted),
      ),
      trailing: _busy
          ? const SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(
                  strokeWidth: 2, color: AppColors.postbookPrimary),
            )
          : Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                IconButton(
                  icon: const Icon(Icons.check_circle_outline,
                      color: AppColors.statusSuccess, size: 20),
                  onPressed: _approve,
                  splashRadius: 18,
                ),
                IconButton(
                  icon: const Icon(Icons.cancel_outlined,
                      color: AppColors.statusError, size: 20),
                  onPressed: _reject,
                  splashRadius: 18,
                ),
              ],
            ),
    );
  }
}

class _JoinRequestTile extends ConsumerStatefulWidget {
  final String groupId;
  final String userId;
  final String displayName;

  const _JoinRequestTile({
    required this.groupId,
    required this.userId,
    required this.displayName,
  });

  @override
  ConsumerState<_JoinRequestTile> createState() =>
      _JoinRequestTileState();
}

class _JoinRequestTileState extends ConsumerState<_JoinRequestTile> {
  bool _busy = false;

  Future<void> _approve() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(groupPostsRepositoryProvider)
          .approveJoinRequest(widget.groupId, widget.userId);
      ref.invalidate(joinRequestsProvider(widget.groupId));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to approve request.')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListTile(
      dense: true,
      leading: CircleAvatar(
        radius: 16,
        backgroundColor:
            AppColors.postbookPrimary.withValues(alpha: 0.2),
        child: Text(
          widget.displayName.isNotEmpty
              ? widget.displayName[0].toUpperCase()
              : '?',
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.postbookPrimary),
        ),
      ),
      title:
          Text(widget.displayName, style: AppTextStyles.label),
      trailing: _busy
          ? const SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(
                  strokeWidth: 2, color: AppColors.postbookPrimary),
            )
          : IconButton(
              icon: const Icon(Icons.check_circle_outline,
                  color: AppColors.statusSuccess, size: 20),
              onPressed: _approve,
              splashRadius: 18,
            ),
    );
  }
}
