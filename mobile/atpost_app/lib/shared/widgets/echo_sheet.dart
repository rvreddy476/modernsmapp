import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/post_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Bottom sheet that confirms an Echo (repost) action and optionally
/// captures a quote-Echo comment + visibility selection. The recon
/// (§B.1, §G) confirms the backend route is `POST /v1/posts/:id/repost`
/// with `type: 'plain' | 'quote'` and an optional `quote_text`.
///
/// Returned future resolves to:
///   - `null`  if the user dismissed the sheet,
///   - `true`  on a successful Echo,
///   - `false` if the backend rejected the Echo.
Future<bool?> showEchoSheet(
  BuildContext context, {
  required String postId,
  required String authorName,
  String? sourceContextType,
}) {
  return showModalBottomSheet<bool>(
    context: context,
    backgroundColor: AppColors.bgSecondary,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
    ),
    builder: (sheetCtx) => Padding(
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(sheetCtx).viewInsets.bottom,
      ),
      child: _EchoSheetBody(
        postId: postId,
        authorName: authorName,
        sourceContextType: sourceContextType,
      ),
    ),
  );
}

class _EchoSheetBody extends ConsumerStatefulWidget {
  const _EchoSheetBody({
    required this.postId,
    required this.authorName,
    this.sourceContextType,
  });

  final String postId;
  final String authorName;
  final String? sourceContextType;

  @override
  ConsumerState<_EchoSheetBody> createState() => _EchoSheetBodyState();
}

/// Visibility scope offered for a quote-Echo. Plain Echo always
/// inherits the original post's visibility on the backend, so the
/// scope picker is informational unless the user toggles "Add a
/// thought".
enum _EchoScope { followers, public }

class _EchoSheetBodyState extends ConsumerState<_EchoSheetBody> {
  final TextEditingController _quoteCtrl = TextEditingController();
  bool _withQuote = false;
  bool _submitting = false;
  String? _error;
  // Scope is captured but not yet sent to the backend; the post-service
  // CreateRepostRequest does not expose a visibility field today (the
  // repost inherits from the original post). Keeping the picker ready
  // for the moment that field lands so we don't have to re-do the UI.
  _EchoScope _scope = _EchoScope.followers;

  @override
  void dispose() {
    _quoteCtrl.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_submitting) return;
    final quote = _quoteCtrl.text.trim();
    if (_withQuote && quote.isEmpty) {
      setState(() {
        _error = 'Add a thought to share with your Echo.';
      });
      return;
    }
    if (quote.length > 500) {
      setState(() {
        _error = 'Quote must be 500 characters or fewer.';
      });
      return;
    }

    setState(() {
      _submitting = true;
      _error = null;
    });

    try {
      await ref.read(postRepositoryProvider).echoPost(
            widget.postId,
            type: _withQuote ? 'quote' : 'plain',
            quoteText: _withQuote ? quote : null,
            sourceContextType: widget.sourceContextType,
          );
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = _humaniseEchoError(e);
      });
    }
  }

  String _humaniseEchoError(Object err) {
    final msg = err.toString();
    if (msg.contains('ALREADY_REPOSTED')) {
      return 'You have already echoed this post.';
    }
    if (msg.contains('NOT_ELIGIBLE')) {
      return 'This post cannot be echoed.';
    }
    if (msg.contains('RATE_LIMITED')) {
      return 'Too many echoes — please slow down.';
    }
    if (msg.contains('QUOTE_TEXT_TOO_LONG')) {
      return 'Quote must be 500 characters or fewer.';
    }
    return 'Could not echo this post. Please try again.';
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 12, 20, 24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: Colors.white24,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 16),
          Row(
            children: [
              const Icon(
                Icons.repeat_rounded,
                color: AppColors.postbookPrimary,
              ),
              const SizedBox(width: 10),
              Text('Echo this post', style: AppTextStyles.h2),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            'Share ${widget.authorName}’s post with your followers.',
            style: AppTextStyles.body.copyWith(color: Colors.white60),
          ),
          const SizedBox(height: 18),
          SwitchListTile.adaptive(
            value: _withQuote,
            onChanged: _submitting
                ? null
                : (v) => setState(() {
                      _withQuote = v;
                      if (!v) _error = null;
                    }),
            contentPadding: EdgeInsets.zero,
            title: Text(
              'Add a thought',
              style: AppTextStyles.label.copyWith(color: Colors.white),
            ),
            subtitle: Text(
              'Your followers see your comment above the original post.',
              style: AppTextStyles.labelSmall.copyWith(color: Colors.white38),
            ),
            activeColor: AppColors.postbookPrimary,
          ),
          if (_withQuote) ...[
            const SizedBox(height: 8),
            TextField(
              controller: _quoteCtrl,
              maxLines: 4,
              maxLength: 500,
              enabled: !_submitting,
              style: AppTextStyles.body.copyWith(color: Colors.white),
              decoration: InputDecoration(
                hintText: 'Add your thought...',
                hintStyle: AppTextStyles.body.copyWith(color: Colors.white24),
                filled: true,
                fillColor: Colors.white.withValues(alpha: 0.04),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(12),
                  borderSide: BorderSide.none,
                ),
              ),
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                _ScopeChip(
                  icon: Icons.people_outline,
                  label: 'Followers',
                  selected: _scope == _EchoScope.followers,
                  onTap: _submitting
                      ? null
                      : () => setState(() => _scope = _EchoScope.followers),
                ),
                const SizedBox(width: 8),
                _ScopeChip(
                  icon: Icons.public,
                  label: 'Public',
                  selected: _scope == _EchoScope.public,
                  onTap: _submitting
                      ? null
                      : () => setState(() => _scope = _EchoScope.public),
                ),
              ],
            ),
          ],
          if (_error != null) ...[
            const SizedBox(height: 12),
            Text(
              _error!,
              style: AppTextStyles.labelSmall.copyWith(color: Colors.redAccent),
            ),
          ],
          const SizedBox(height: 18),
          Row(
            children: [
              TextButton(
                onPressed: _submitting
                    ? null
                    : () => Navigator.of(context).pop(null),
                child: Text(
                  'Cancel',
                  style: AppTextStyles.body.copyWith(color: Colors.white60),
                ),
              ),
              const Spacer(),
              ElevatedButton.icon(
                onPressed: _submitting ? null : _submit,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(24),
                  ),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 22,
                    vertical: 12,
                  ),
                  elevation: 0,
                ),
                icon: _submitting
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Icon(Icons.send_rounded, size: 18),
                label: Text(
                  _withQuote ? 'Echo with thought' : 'Echo',
                  style: AppTextStyles.label.copyWith(color: Colors.white),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _ScopeChip extends StatelessWidget {
  const _ScopeChip({
    required this.icon,
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final color = selected ? AppColors.postbookPrimary : Colors.white24;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withValues(alpha: 0.12)
              : Colors.white.withValues(alpha: 0.03),
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: color),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 16, color: color),
            const SizedBox(width: 6),
            Text(
              label,
              style: AppTextStyles.labelSmall.copyWith(color: color),
            ),
          ],
        ),
      ),
    );
  }
}
