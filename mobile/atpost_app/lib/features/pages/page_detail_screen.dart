import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/business_page.dart';
import 'package:atpost_app/data/page_types.dart';
import 'package:atpost_app/data/repositories/pages_repository.dart';
import 'package:atpost_app/providers/pages_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

const _statusLabels = {
  'draft': 'Draft',
  'pending_review': 'In review',
  'approved': 'Approved',
  'rejected': 'Rejected',
  'suspended': 'Suspended',
  'disabled': 'Disabled',
};

class PageDetailScreen extends ConsumerStatefulWidget {
  const PageDetailScreen({super.key, required this.handle});
  final String handle;

  @override
  ConsumerState<PageDetailScreen> createState() => _PageDetailScreenState();
}

class _PageDetailScreenState extends ConsumerState<PageDetailScreen> {
  bool _busy = false;

  Future<void> _toggleFollow(BusinessPage page) async {
    if (_busy) return;
    setState(() => _busy = true);
    final repo = ref.read(pagesRepositoryProvider);
    try {
      if (page.isFollowing == true || page.actions.canUnfollow) {
        await repo.unfollow(page.id);
      } else {
        await repo.follow(page.id);
      }
      ref.invalidate(pageDetailProvider(widget.handle));
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_friendly(e))),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  String _friendly(Object e) {
    final s = e.toString();
    if (s.contains('cannot_follow_own_page')) return "You can't follow a page you manage.";
    if (s.contains('page_not_followable')) return 'This page is not open for followers yet.';
    if (s.contains('already_following')) return "You're already following this page.";
    return 'Something went wrong. Try again.';
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(pageDetailProvider(widget.handle));
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: async.when(
        loading: () => const Center(child: CircularProgressIndicator(color: AppColors.postbookPrimary)),
        error: (_, _) => _NotFound(onBrowse: () => context.go('/pages')),
        data: (page) => _Body(
          page: page,
          busy: _busy,
          onToggleFollow: () => _toggleFollow(page),
        ),
      ),
    );
  }
}

class _Body extends ConsumerWidget {
  const _Body({required this.page, required this.busy, required this.onToggleFollow});
  final BusinessPage page;
  final bool busy;
  final VoidCallback onToggleFollow;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cover = page.coverMediaId != null
        ? '${Environment.apiBaseUrl}/v1/media/${page.coverMediaId}/serve'
        : null;
    final avatar = page.avatarMediaId != null
        ? '${Environment.apiBaseUrl}/v1/media/${page.avatarMediaId}/serve'
        : null;
    final a = page.actions;

    return CustomScrollView(
      slivers: [
        SliverAppBar(
          backgroundColor: AppColors.bgPrimary,
          pinned: true,
          expandedHeight: 150,
          leading: IconButton(
            icon: const Icon(Icons.arrow_back, color: Colors.white),
            onPressed: () => Navigator.of(context).maybePop(),
          ),
          flexibleSpace: FlexibleSpaceBar(
            background: cover != null
                ? Image.network(cover, fit: BoxFit.cover)
                : Container(color: AppColors.bgTertiary),
          ),
        ),
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    CircleAvatar(
                      radius: 34,
                      backgroundColor: AppColors.bgTertiary,
                      backgroundImage: avatar != null ? NetworkImage(avatar) : null,
                      child: avatar == null
                          ? Text(page.pageName.isNotEmpty ? page.pageName[0].toUpperCase() : '?', style: AppTextStyles.h1)
                          : null,
                    ),
                    const Spacer(),
                    if (a.canManage && _statusLabels.containsKey(page.status))
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                        decoration: BoxDecoration(
                          color: AppColors.bgSecondary,
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(_statusLabels[page.status]!, style: AppTextStyles.labelSmall),
                      ),
                  ],
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Flexible(child: Text(page.pageName, style: AppTextStyles.h1)),
                    if (page.isVerified) ...[
                      const SizedBox(width: 6),
                      const Icon(Icons.verified, size: 18, color: Color(0xFF2563EB)),
                    ],
                  ],
                ),
                const SizedBox(height: 4),
                Text(
                  '${page.displayType ?? page.category} · ${page.followerCount} ${page.followerCount == 1 ? "follower" : "followers"}',
                  style: AppTextStyles.label.copyWith(color: AppColors.textTertiary),
                ),
                if (page.description.isNotEmpty) ...[
                  const SizedBox(height: 10),
                  Text(page.description, style: AppTextStyles.body),
                ],
                if (page.bannerMessage != null) ...[
                  const SizedBox(height: 12),
                  Container(
                    padding: const EdgeInsets.all(10),
                    decoration: BoxDecoration(
                      color: const Color(0xFF4A2E12),
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: Row(children: [
                      const Icon(Icons.warning_amber_rounded, size: 16, color: Color(0xFFF59E0B)),
                      const SizedBox(width: 6),
                      Expanded(child: Text(page.bannerMessage!, style: AppTextStyles.labelSmall.copyWith(color: const Color(0xFFF59E0B)))),
                    ]),
                  ),
                ],
                const SizedBox(height: 16),
                _ActionRow(page: page, busy: busy, onToggleFollow: onToggleFollow),
                if (a.canManage) ...[
                  const SizedBox(height: 20),
                  _OwnerPanel(page: page),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _ActionRow extends StatelessWidget {
  const _ActionRow({required this.page, required this.busy, required this.onToggleFollow});
  final BusinessPage page;
  final bool busy;
  final VoidCallback onToggleFollow;

  @override
  Widget build(BuildContext context) {
    final a = page.actions;
    final children = <Widget>[];

    if (a.canFollow) {
      children.add(Expanded(
        child: _PrimaryButton(
          label: 'Follow',
          icon: Icons.person_add_alt_1,
          busy: busy,
          onTap: onToggleFollow,
        ),
      ));
    } else if (a.canUnfollow) {
      children.add(Expanded(
        child: _SecondaryButton(label: 'Following', icon: Icons.check, busy: busy, onTap: onToggleFollow),
      ));
    }

    if (a.canManage) {
      if (children.isNotEmpty) children.add(const SizedBox(width: 10));
      children.add(Expanded(
        child: _SecondaryButton(
          label: 'Manage',
          icon: Icons.settings,
          busy: false,
          onTap: () => context.push('/pages'),
        ),
      ));
    }

    // Website / call hint buttons.
    final hints = page.actionButtons.where((b) => !b.gated).toList();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (children.isNotEmpty) Row(children: children),
        if (hints.isNotEmpty) ...[
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: hints.map((b) => _HintChip(button: b, page: page)).toList(),
          ),
        ],
      ],
    );
  }
}

class _HintChip extends StatelessWidget {
  const _HintChip({required this.button, required this.page});
  final PageActionButton button;
  final BusinessPage page;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: () {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('${button.label} is coming soon.')),
        );
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(999),
        ),
        child: Text(button.label, style: AppTextStyles.labelSmall),
      ),
    );
  }
}

class _OwnerPanel extends ConsumerStatefulWidget {
  const _OwnerPanel({required this.page});
  final BusinessPage page;

  @override
  ConsumerState<_OwnerPanel> createState() => _OwnerPanelState();
}

class _OwnerPanelState extends ConsumerState<_OwnerPanel> {
  bool _submitting = false;

  Future<void> _upload(String docType) async {
    final url = await _promptUrl(docType);
    if (url == null || url.isEmpty) return;
    try {
      await ref.read(pagesRepositoryProvider).addDocument(widget.page.id, docType, url);
      ref.invalidate(pageDocumentsProvider(widget.page.id));
      ref.invalidate(pageDetailProvider(widget.page.pageHandle));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Upload failed.')));
      }
    }
  }

  Future<String?> _promptUrl(String docType) {
    final ctrl = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text(documentLabel(docType), style: AppTextStyles.h3),
        content: TextField(
          controller: ctrl,
          style: AppTextStyles.body,
          decoration: const InputDecoration(hintText: 'Document URL (PDF / image)'),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          TextButton(onPressed: () => Navigator.pop(ctx, ctrl.text.trim()), child: const Text('Save')),
        ],
      ),
    );
  }

  Future<void> _submit() async {
    setState(() => _submitting = true);
    try {
      await ref.read(pagesRepositoryProvider).submitForReview(widget.page.id);
      ref.invalidate(pageDetailProvider(widget.page.pageHandle));
      ref.invalidate(myPagesProvider);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.toString().contains('unprocessable')
              ? 'Upload all required documents first.'
              : 'Could not submit for review.')),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final page = widget.page;
    final typeDef = pageTypeByValue(page.pageType);
    final required = typeDef?.requiredDocuments ?? const [];
    final optional = typeDef?.optionalDocuments ?? const [];
    final docsAsync = ref.watch(pageDocumentsProvider(page.id));

    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(16),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('VERIFICATION', style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary)),
          if (page.status == 'rejected' && page.rejectionReason != null) ...[
            const SizedBox(height: 8),
            Container(
              padding: const EdgeInsets.all(8),
              decoration: BoxDecoration(color: const Color(0xFF3A1620), borderRadius: BorderRadius.circular(8)),
              child: Text('Rejected: ${page.rejectionReason}', style: AppTextStyles.labelSmall.copyWith(color: AppColors.statusError)),
            ),
          ],
          const SizedBox(height: 10),
          docsAsync.when(
            loading: () => const Padding(padding: EdgeInsets.all(8), child: LinearProgressIndicator()),
            error: (_, _) => Text('Could not load documents.', style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary)),
            data: (docs) {
              final have = {for (final d in docs) if (d.status != 'rejected') d.documentType};
              final allReq = required.every(have.contains);
              return Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  ...[
                    ...required.map((d) => (d, true)),
                    ...optional.map((d) => (d, false)),
                  ].map((e) {
                    final dt = e.$1;
                    final req = e.$2;
                    final uploaded = have.contains(dt);
                    final row = docs.where((x) => x.documentType == dt).toList();
                    return Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: Row(
                        children: [
                          Icon(uploaded ? Icons.check_circle : Icons.upload_file,
                              size: 16, color: uploaded ? AppColors.statusSuccess : AppColors.textTertiary),
                          const SizedBox(width: 8),
                          Expanded(
                            child: Text(
                              '${documentLabel(dt)}${req ? " *" : ""}${row.isNotEmpty ? "  (${row.first.status})" : ""}',
                              style: AppTextStyles.body,
                            ),
                          ),
                          TextButton(
                            onPressed: () => _upload(dt),
                            child: Text(uploaded ? 'Replace' : 'Upload', style: AppTextStyles.labelSmall.copyWith(color: AppColors.postbookPrimary)),
                          ),
                        ],
                      ),
                    );
                  }),
                  if (required.isEmpty && optional.isEmpty)
                    Text('No documents required for this page type.',
                        style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary)),
                  const SizedBox(height: 6),
                  if (page.status == 'draft' || page.status == 'rejected')
                    SizedBox(
                      width: double.infinity,
                      child: _PrimaryButton(
                        label: 'Submit for review',
                        icon: Icons.send,
                        busy: _submitting,
                        disabled: !allReq,
                        onTap: _submit,
                      ),
                    ),
                  if (page.status == 'pending_review')
                    Container(
                      width: double.infinity,
                      padding: const EdgeInsets.all(10),
                      decoration: BoxDecoration(color: const Color(0xFF3A2E12), borderRadius: BorderRadius.circular(10)),
                      child: Text('Submitted — awaiting admin review.',
                          textAlign: TextAlign.center,
                          style: AppTextStyles.labelSmall.copyWith(color: const Color(0xFFF59E0B))),
                    ),
                ],
              );
            },
          ),
        ],
      ),
    );
  }
}

class _PrimaryButton extends StatelessWidget {
  const _PrimaryButton({required this.label, required this.icon, required this.busy, required this.onTap, this.disabled = false});
  final String label;
  final IconData icon;
  final bool busy;
  final bool disabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: (busy || disabled) ? null : onTap,
      child: Container(
        height: 44,
        alignment: Alignment.center,
        decoration: BoxDecoration(
          color: disabled ? AppColors.bgTertiary : AppColors.postbookPrimary,
          borderRadius: BorderRadius.circular(12),
        ),
        child: busy
            ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
            : Row(mainAxisSize: MainAxisSize.min, children: [
                Icon(icon, size: 16, color: Colors.white),
                const SizedBox(width: 6),
                Text(label, style: AppTextStyles.label.copyWith(color: Colors.white)),
              ]),
      ),
    );
  }
}

class _SecondaryButton extends StatelessWidget {
  const _SecondaryButton({required this.label, required this.icon, required this.busy, required this.onTap});
  final String label;
  final IconData icon;
  final bool busy;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: busy ? null : onTap,
      child: Container(
        height: 44,
        alignment: Alignment.center,
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: AppColors.bgTertiary),
        ),
        child: busy
            ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
            : Row(mainAxisSize: MainAxisSize.min, children: [
                Icon(icon, size: 16, color: AppColors.textPrimary),
                const SizedBox(width: 6),
                Text(label, style: AppTextStyles.label),
              ]),
      ),
    );
  }
}

class _NotFound extends StatelessWidget {
  const _NotFound({required this.onBrowse});
  final VoidCallback onBrowse;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('Page not found', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          TextButton(onPressed: onBrowse, child: const Text('Browse pages')),
        ],
      ),
    );
  }
}
