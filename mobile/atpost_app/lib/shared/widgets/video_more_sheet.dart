// Unified video "More" bottom sheet — used by all video surfaces (Reels,
// PostTube, feed video cards). Design: an Instagram-style circular quick-action
// row on top, then YouTube-style sections grouped by meaning — Playback,
// Your feed, Collection, Safety.
//
// Surface-specific actions (Save / Captions / Share / Report) come in as
// callbacks because they touch per-surface engagement state. Generic actions
// (Add to playlist, feed signals, Quality, Autoplay, Feedback, Why-seeing) are
// handled inside via repositories/providers.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/repositories/feed_signal_repository.dart';
import 'package:atpost_app/data/repositories/feedback_repository.dart';
import 'package:atpost_app/data/repositories/playlist_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/autoplay_provider.dart';
import 'package:atpost_app/providers/data_saver_provider.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

const Color _sheetBg = Color(0xFF1B1B1F);

Future<void> showVideoMoreSheet(
  BuildContext context, {
  required Post post,
  String surface = 'reels',
  required bool isSaved,
  required bool captionsAvailable,
  required bool captionsEnabled,
  required VoidCallback onToggleSave,
  required VoidCallback onToggleCaptions,
  required VoidCallback onShare,
  required VoidCallback onReport,
  VoidCallback? onNotInterested,
}) {
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: _sheetBg,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXL)),
    ),
    builder: (_) => _VideoMoreSheet(
      parentContext: context,
      post: post,
      surface: surface,
      isSaved: isSaved,
      captionsAvailable: captionsAvailable,
      captionsEnabled: captionsEnabled,
      onToggleSave: onToggleSave,
      onToggleCaptions: onToggleCaptions,
      onShare: onShare,
      onReport: onReport,
      onNotInterested: onNotInterested,
    ),
  );
}

class _VideoMoreSheet extends ConsumerWidget {
  const _VideoMoreSheet({
    required this.parentContext,
    required this.post,
    required this.surface,
    required this.isSaved,
    required this.captionsAvailable,
    required this.captionsEnabled,
    required this.onToggleSave,
    required this.onToggleCaptions,
    required this.onShare,
    required this.onReport,
    required this.onNotInterested,
  });

  final BuildContext parentContext;
  final Post post;
  final String surface;
  final bool isSaved;
  final bool captionsAvailable;
  final bool captionsEnabled;
  final VoidCallback onToggleSave;
  final VoidCallback onToggleCaptions;
  final VoidCallback onShare;
  final VoidCallback onReport;
  final VoidCallback? onNotInterested;

  void _toast(String msg) {
    ScaffoldMessenger.of(parentContext)
        .showSnackBar(SnackBar(content: Text(msg)));
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final dataSaver = ref.watch(effectiveDataSaverProvider);
    final autoplay = ref.watch(autoplayProvider);

    return SafeArea(
      top: false,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const SizedBox(height: 8),
            Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: Colors.white24,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            const SizedBox(height: 12),

            // ── Quick-action row (Instagram-style circular buttons) ──
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceEvenly,
              children: [
                _QuickAction(
                  icon: isSaved ? Icons.bookmark_rounded : Icons.bookmark_border_rounded,
                  label: 'Save',
                  active: isSaved,
                  onTap: () => _close(context, onToggleSave),
                ),
                _QuickAction(
                  icon: Icons.reply_rounded,
                  label: 'Share',
                  onTap: () => _close(context, onShare),
                ),
                if (captionsAvailable)
                  _QuickAction(
                    icon: Icons.closed_caption_rounded,
                    label: 'Captions',
                    active: captionsEnabled,
                    onTap: () => _close(context, onToggleCaptions),
                  ),
                _QuickAction(
                  icon: Icons.playlist_add_rounded,
                  label: 'Playlist',
                  onTap: () {
                    Navigator.of(context).pop();
                    _showPlaylistPicker(parentContext, ref, post, _toast);
                  },
                ),
              ],
            ),
            const SizedBox(height: 8),
            const Divider(color: Colors.white12, height: 1),

            // ── PLAYBACK ──
            _Section('Playback'),
            _Row(
              icon: Icons.high_quality_rounded,
              label: 'Quality',
              trailing: dataSaver ? 'Data saver' : 'Auto',
              onTap: () => _showQualityPicker(context, ref, dataSaver),
            ),
            if (captionsAvailable)
              _Row(
                icon: Icons.closed_caption_rounded,
                label: 'Captions',
                trailing: captionsEnabled ? 'On' : 'Off',
                onTap: () => _close(context, onToggleCaptions),
              ),
            _ToggleRow(
              icon: Icons.play_circle_outline_rounded,
              label: 'Autoplay',
              value: autoplay,
              onChanged: (v) => ref.read(autoplayProvider.notifier).setEnabled(v),
            ),

            // ── YOUR FEED ──
            _Section('Your feed'),
            _Row(
              icon: Icons.add_circle_outline_rounded,
              label: 'Interested',
              onTap: () {
                Navigator.of(context).pop();
                _safe(() => ref.read(feedSignalRepositoryProvider).seeMore(post.id));
                _toast("Great — we'll show you more like this.");
              },
            ),
            _Row(
              icon: Icons.do_not_disturb_on_outlined,
              label: 'Not interested',
              onTap: () {
                Navigator.of(context).pop();
                _safe(() => ref.read(feedSignalRepositoryProvider).seeLess(post.id));
                onNotInterested?.call();
                _toast("Got it — we'll show you fewer like this.");
              },
            ),
            if (post.authorId.isNotEmpty)
              _Row(
                icon: Icons.block_flipped,
                label: "Don't recommend this channel",
                onTap: () {
                  Navigator.of(context).pop();
                  _safe(() => ref.read(userRepositoryProvider).muteUser(post.authorId));
                  _toast("Got it — we'll recommend this channel less.");
                },
              ),
            _Row(
              icon: Icons.info_outline_rounded,
              label: "Why you're seeing this",
              onTap: () {
                Navigator.of(context).pop();
                _showWhySeeing(parentContext, post);
              },
            ),

            // ── COLLECTION ──
            _Section('Collection'),
            _Row(
              icon: isSaved ? Icons.bookmark_rounded : Icons.bookmark_border_rounded,
              label: isSaved ? 'Saved' : 'Save',
              onTap: () => _close(context, onToggleSave),
            ),
            _Row(
              icon: Icons.playlist_add_rounded,
              label: 'Add to playlist',
              onTap: () {
                Navigator.of(context).pop();
                _showPlaylistPicker(parentContext, ref, post, _toast);
              },
            ),

            // ── SAFETY ──
            _Section('Safety'),
            _Row(
              icon: Icons.flag_outlined,
              label: 'Report',
              danger: true,
              onTap: () => _close(context, onReport),
            ),
            _Row(
              icon: Icons.feedback_outlined,
              label: 'Send feedback',
              onTap: () {
                Navigator.of(context).pop();
                _showFeedbackForm(parentContext, ref, post, _toast);
              },
            ),
            const SizedBox(height: 12),
          ],
        ),
      ),
    );
  }

  void _close(BuildContext ctx, VoidCallback action) {
    Navigator.of(ctx).pop();
    action();
  }

  void _showQualityPicker(BuildContext ctx, WidgetRef ref, bool dataSaver) {
    showModalBottomSheet<void>(
      context: ctx,
      backgroundColor: _sheetBg,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXL)),
      ),
      builder: (sub) => SafeArea(
        top: false,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const SizedBox(height: 12),
            _Section('Quality'),
            _Row(
              icon: Icons.auto_awesome_rounded,
              label: 'Auto (higher quality)',
              trailing: dataSaver ? null : '✓',
              onTap: () {
                ref.read(dataSaverProvider.notifier).setEnabled(false);
                Navigator.of(sub).pop();
              },
            ),
            _Row(
              icon: Icons.data_saver_on_rounded,
              label: 'Data saver (240p)',
              trailing: dataSaver ? '✓' : null,
              onTap: () {
                ref.read(dataSaverProvider.notifier).setEnabled(true);
                Navigator.of(sub).pop();
              },
            ),
            const SizedBox(height: 12),
          ],
        ),
      ),
    );
  }
}

// Fire-and-forget network call; swallow errors so a signal never breaks UX.
void _safe(Future<void> Function() fn) {
  fn().catchError((_) {});
}

// ───────────────────────── Sub-flows ─────────────────────────

void _showWhySeeing(BuildContext context, Post post) {
  showModalBottomSheet<void>(
    context: context,
    backgroundColor: _sheetBg,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXL)),
    ),
    builder: (_) => SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 18, 20, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text("Why you're seeing this", style: AppTextStyles.h3),
            const SizedBox(height: 12),
            Text(
              'This video is recommended based on what you watch and engage '
              'with, videos popular with people who share your interests, and '
              'your "Interested / Not interested" feedback. Use the options in '
              'this menu to tune what you see.',
              style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
          ],
        ),
      ),
    ),
  );
}

void _showFeedbackForm(
  BuildContext context,
  WidgetRef ref,
  Post post,
  void Function(String) toast,
) {
  const types = <String, String>{
    'bug': 'Bug',
    'feature': 'Feature idea',
    'performance': 'Performance',
    'content': 'Content quality',
    'ui': 'Design / UI',
    'other': 'Other',
  };
  String selected = 'other';
  final controller = TextEditingController();

  showModalBottomSheet<void>(
    context: context,
    backgroundColor: _sheetBg,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXL)),
    ),
    builder: (sheetCtx) => Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(sheetCtx).viewInsets.bottom),
      child: StatefulBuilder(
        builder: (sheetCtx, setSheetState) => SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(20, 16, 20, 16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Send feedback', style: AppTextStyles.h3),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: types.entries.map((e) {
                    final sel = e.key == selected;
                    return GestureDetector(
                      onTap: () => setSheetState(() => selected = e.key),
                      child: Container(
                        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
                        decoration: BoxDecoration(
                          color: sel ? AppColors.postgramPrimary : Colors.white10,
                          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                        ),
                        child: Text(
                          e.value,
                          style: AppTextStyles.labelSmall.copyWith(
                            color: sel ? Colors.white : AppColors.textSecondary,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    );
                  }).toList(),
                ),
                const SizedBox(height: 14),
                TextField(
                  controller: controller,
                  maxLines: 4,
                  maxLength: 5000,
                  style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
                  decoration: const InputDecoration(
                    hintText: 'Tell us what could be better…',
                  ),
                ),
                const SizedBox(height: 8),
                SizedBox(
                  width: double.infinity,
                  child: FilledButton(
                    onPressed: () async {
                      final msg = controller.text.trim();
                      if (msg.isEmpty) {
                        toast('Please enter some feedback first.');
                        return;
                      }
                      Navigator.of(sheetCtx).pop();
                      try {
                        await ref.read(feedbackRepositoryProvider).submit(
                              type: selected,
                              message: msg,
                              postId: post.id,
                            );
                        toast('Thanks — your feedback helps us improve.');
                      } catch (_) {
                        toast('Could not send feedback. Please try again.');
                      }
                    },
                    child: const Text('Submit'),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}

void _showPlaylistPicker(
  BuildContext context,
  WidgetRef ref,
  Post post,
  void Function(String) toast,
) {
  final userId = ref.read(authServiceProvider).userId;
  if (userId == null || userId.isEmpty) {
    toast('Please log in to use playlists.');
    return;
  }

  showModalBottomSheet<void>(
    context: context,
    backgroundColor: _sheetBg,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXL)),
    ),
    builder: (sheetCtx) => _PlaylistPicker(
      userId: userId,
      post: post,
      toast: toast,
    ),
  );
}

class _PlaylistPicker extends ConsumerStatefulWidget {
  const _PlaylistPicker({
    required this.userId,
    required this.post,
    required this.toast,
  });

  final String userId;
  final Post post;
  final void Function(String) toast;

  @override
  ConsumerState<_PlaylistPicker> createState() => _PlaylistPickerState();
}

class _PlaylistPickerState extends ConsumerState<_PlaylistPicker> {
  late Future<List<Playlist>> _future;

  @override
  void initState() {
    super.initState();
    _future = ref.read(playlistRepositoryProvider).listByCreator(widget.userId);
  }

  Future<void> _addTo(Playlist p) async {
    Navigator.of(context).pop();
    try {
      await ref.read(playlistRepositoryProvider).addItem(p.id, widget.post.id);
      widget.toast('Added to "${p.title}".');
    } catch (_) {
      widget.toast('Could not add to playlist.');
    }
  }

  Future<void> _createAndAdd() async {
    final title = await _promptNewPlaylist(context);
    if (title == null || title.trim().isEmpty) return;
    try {
      final repo = ref.read(playlistRepositoryProvider);
      final p = await repo.create(title.trim());
      await repo.addItem(p.id, widget.post.id);
      if (mounted) Navigator.of(context).pop();
      widget.toast('Created "${p.title}" and added.');
    } catch (_) {
      widget.toast('Could not create playlist.');
    }
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      top: false,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(height: 12),
          _Section('Add to playlist'),
          _Row(
            icon: Icons.add_rounded,
            label: 'New playlist',
            onTap: _createAndAdd,
          ),
          const Divider(color: Colors.white12, height: 1),
          ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 320),
            child: FutureBuilder<List<Playlist>>(
              future: _future,
              builder: (context, snap) {
                if (snap.connectionState == ConnectionState.waiting) {
                  return const Padding(
                    padding: EdgeInsets.all(24),
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                final lists = snap.data ?? const <Playlist>[];
                if (lists.isEmpty) {
                  return Padding(
                    padding: const EdgeInsets.all(20),
                    child: Text(
                      'No playlists yet. Create one above.',
                      style: AppTextStyles.bodySmall
                          .copyWith(color: AppColors.textSecondary),
                    ),
                  );
                }
                return ListView(
                  shrinkWrap: true,
                  children: lists
                      .map((p) => _Row(
                            icon: Icons.video_library_outlined,
                            label: p.title,
                            trailing: '${p.itemCount}',
                            onTap: () => _addTo(p),
                          ))
                      .toList(),
                );
              },
            ),
          ),
          const SizedBox(height: 12),
        ],
      ),
    );
  }
}

Future<String?> _promptNewPlaylist(BuildContext context) {
  final controller = TextEditingController();
  return showDialog<String>(
    context: context,
    builder: (ctx) => AlertDialog(
      backgroundColor: _sheetBg,
      title: Text('New playlist', style: AppTextStyles.h3),
      content: TextField(
        controller: controller,
        autofocus: true,
        style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
        decoration: const InputDecoration(hintText: 'Playlist name'),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(controller.text),
          child: const Text('Create'),
        ),
      ],
    ),
  );
}

// ───────────────────────── Building blocks ─────────────────────────

class _QuickAction extends StatelessWidget {
  const _QuickAction({
    required this.icon,
    required this.label,
    required this.onTap,
    this.active = false,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final bool active;

  @override
  Widget build(BuildContext context) {
    final color = active ? AppColors.postgramPrimary : Colors.white;
    return GestureDetector(
      onTap: onTap,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 54,
            height: 54,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              border: Border.all(color: Colors.white24),
              color: active ? AppColors.postgramPrimary.withValues(alpha: 0.15) : null,
            ),
            child: Icon(icon, color: color, size: 24),
          ),
          const SizedBox(height: 6),
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(color: AppColors.textSecondary),
          ),
        ],
      ),
    );
  }
}

class _Section extends StatelessWidget {
  const _Section(this.label);
  final String label;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 16, 20, 6),
      child: Align(
        alignment: Alignment.centerLeft,
        child: Text(
          label.toUpperCase(),
          style: AppTextStyles.labelTiny.copyWith(
            color: AppColors.textDim,
            letterSpacing: 0.8,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({
    required this.icon,
    required this.label,
    required this.onTap,
    this.trailing,
    this.danger = false,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final String? trailing;
  final bool danger;

  @override
  Widget build(BuildContext context) {
    final color = danger ? const Color(0xFFFF4D4F) : Colors.white;
    return ListTile(
      dense: true,
      leading: Icon(icon, color: danger ? color : Colors.white70, size: 22),
      title: Text(label, style: AppTextStyles.body.copyWith(color: color)),
      trailing: trailing == null
          ? null
          : Text(
              trailing!,
              style: AppTextStyles.labelSmall.copyWith(color: AppColors.textDim),
            ),
      onTap: onTap,
    );
  }
}

class _ToggleRow extends StatelessWidget {
  const _ToggleRow({
    required this.icon,
    required this.label,
    required this.value,
    required this.onChanged,
  });

  final IconData icon;
  final String label;
  final bool value;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) {
    return SwitchListTile(
      dense: true,
      secondary: Icon(icon, color: Colors.white70, size: 22),
      title: Text(label, style: AppTextStyles.body.copyWith(color: Colors.white)),
      value: value,
      activeThumbColor: AppColors.postgramPrimary,
      onChanged: onChanged,
    );
  }
}
