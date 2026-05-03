import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// MentionField — composer text field with @username autocomplete.
/// A composer text field that surfaces a popover of `@username`
/// suggestions whenever the caret follows an unbroken `@token`. The
/// suggestions come from `userRepository.searchUsers(query)`; backend
/// already parses mentions in `post-service/internal/service/posts.go`
/// so once the user inserts `@username` no extra plumbing is needed.
///
/// Wraps the existing TextField pattern in `create_post_screen` so
/// callers don't have to refactor; the field is a drop-in replacement
/// that owns its own focus node and forwards `onChanged` after each
/// edit.
class MentionField extends ConsumerStatefulWidget {
  const MentionField({
    super.key,
    required this.controller,
    required this.focusNode,
    required this.onChanged,
    this.hintText,
    this.style,
    this.hintStyle,
    this.maxLines,
    this.maxLength,
    this.enabled = true,
  });

  final TextEditingController controller;
  final FocusNode focusNode;
  final ValueChanged<String> onChanged;
  final String? hintText;
  final TextStyle? style;
  final TextStyle? hintStyle;
  final int? maxLines;
  final int? maxLength;
  final bool enabled;

  @override
  ConsumerState<MentionField> createState() => _MentionFieldState();
}

class _MentionFieldState extends ConsumerState<MentionField> {
  final LayerLink _link = LayerLink();
  OverlayEntry? _overlay;
  Timer? _debounce;
  List<User> _suggestions = const <User>[];
  bool _loading = false;
  String _activeQuery = '';
  // Range of the active `@token` in `controller.text`. Used so we can
  // splice the chosen username back into the right slot.
  int _tokenStart = -1;
  int _tokenEnd = -1;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_handleControllerChange);
    widget.focusNode.addListener(_handleFocusChange);
  }

  @override
  void dispose() {
    _debounce?.cancel();
    widget.controller.removeListener(_handleControllerChange);
    widget.focusNode.removeListener(_handleFocusChange);
    _hideOverlay();
    super.dispose();
  }

  void _handleFocusChange() {
    if (!widget.focusNode.hasFocus) {
      _hideOverlay();
    }
  }

  void _handleControllerChange() {
    final text = widget.controller.text;
    final selection = widget.controller.selection;
    widget.onChanged(text);
    if (!selection.isValid || !selection.isCollapsed) {
      _hideOverlay();
      return;
    }
    final cursor = selection.baseOffset;
    final token = _findActiveMentionToken(text, cursor);
    if (token == null) {
      _hideOverlay();
      return;
    }
    _tokenStart = token.start;
    _tokenEnd = token.end;
    _activeQuery = token.query;
    _scheduleSearch(token.query);
  }

  void _scheduleSearch(String query) {
    _debounce?.cancel();
    if (query.isEmpty) {
      // Show the overlay anchored, but with an empty hint state so
      // the user understands typing more characters refines.
      setState(() {
        _suggestions = const <User>[];
        _loading = false;
      });
      _showOverlay();
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 220), () async {
      setState(() => _loading = true);
      _showOverlay();
      try {
        final result = await ref
            .read(userRepositoryProvider)
            .searchUsers(query, limit: 8);
        if (!mounted || _activeQuery != query) return;
        setState(() {
          _suggestions = result.users;
          _loading = false;
        });
        _showOverlay();
      } catch (_) {
        if (!mounted) return;
        setState(() {
          _suggestions = const <User>[];
          _loading = false;
        });
      }
    });
  }

  void _showOverlay() {
    _overlay?.remove();
    final overlay = Overlay.of(context, rootOverlay: true);
    _overlay = OverlayEntry(
      builder: (overlayCtx) => Positioned(
        width: 280,
        child: CompositedTransformFollower(
          link: _link,
          showWhenUnlinked: false,
          // Anchor the popover above the field. Material design parity
          // with the standard autocomplete affordance.
          offset: const Offset(0, -260),
          child: Material(
            color: Colors.transparent,
            child: _MentionSuggestionsPanel(
              loading: _loading,
              suggestions: _suggestions,
              onSelected: _applySuggestion,
            ),
          ),
        ),
      ),
    );
    overlay.insert(_overlay!);
  }

  void _hideOverlay() {
    _overlay?.remove();
    _overlay = null;
  }

  void _applySuggestion(User user) {
    // The User model exposes `username` (the @handle) plus
    // `displayName`. Prefer the username so the inserted token
    // matches what post-service mention extractor will resolve.
    final username = (user.username.isNotEmpty
            ? user.username
            : user.displayName)
        .replaceAll(' ', '');
    if (username.isEmpty) {
      _hideOverlay();
      return;
    }
    final text = widget.controller.text;
    if (_tokenStart < 0 || _tokenEnd < _tokenStart) {
      _hideOverlay();
      return;
    }
    final before = text.substring(0, _tokenStart);
    final after = _tokenEnd >= text.length ? '' : text.substring(_tokenEnd);
    // Always end with a single space so the user can keep typing.
    final replacement = '@$username ';
    final next = '$before$replacement$after';
    widget.controller.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(
        offset: (before + replacement).length,
      ),
    );
    _hideOverlay();
  }

  @override
  Widget build(BuildContext context) {
    final hintStyle = widget.hintStyle ??
        AppTextStyles.body.copyWith(color: Colors.white24);
    return CompositedTransformTarget(
      link: _link,
      child: TextField(
        controller: widget.controller,
        focusNode: widget.focusNode,
        enabled: widget.enabled,
        maxLines: widget.maxLines,
        maxLength: widget.maxLength,
        style: widget.style ?? AppTextStyles.body.copyWith(color: Colors.white),
        decoration: InputDecoration(
          hintText: widget.hintText ?? "What's on your mind?",
          hintStyle: hintStyle,
          border: InputBorder.none,
          counterText: '',
        ),
      ),
    );
  }
}

/// Result of locating an active `@token` in the composer text.
class _MentionToken {
  const _MentionToken({
    required this.start,
    required this.end,
    required this.query,
  });

  final int start;
  final int end;
  final String query;
}

/// Walk back from [cursor] until we hit `@`. The token is active when:
/// - `@` is preceded by start-of-text or whitespace, AND
/// - the text between `@` and [cursor] contains no whitespace.
///
/// Returns null when no active token is present.
_MentionToken? _findActiveMentionToken(String text, int cursor) {
  if (cursor < 0 || cursor > text.length) return null;
  if (cursor == 0) return null;
  var i = cursor - 1;
  while (i >= 0) {
    final ch = text[i];
    if (ch == '@') {
      // Check char before `@`.
      if (i == 0 || _isMentionBoundary(text[i - 1])) {
        final query = text.substring(i + 1, cursor);
        if (query.contains(RegExp(r'\s'))) return null;
        return _MentionToken(start: i, end: cursor, query: query);
      }
      return null;
    }
    if (_isMentionBoundary(ch)) return null;
    i -= 1;
  }
  return null;
}

bool _isMentionBoundary(String ch) {
  return ch == ' ' || ch == '\n' || ch == '\t' || ch == '\r';
}

class _MentionSuggestionsPanel extends StatelessWidget {
  const _MentionSuggestionsPanel({
    required this.loading,
    required this.suggestions,
    required this.onSelected,
  });

  final bool loading;
  final List<User> suggestions;
  final ValueChanged<User> onSelected;

  @override
  Widget build(BuildContext context) {
    final empty = !loading && suggestions.isEmpty;
    return Container(
      constraints: const BoxConstraints(maxHeight: 240),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppColors.borderSubtle),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.4),
            blurRadius: 18,
            offset: const Offset(0, 8),
          ),
        ],
      ),
      clipBehavior: Clip.antiAlias,
      child: loading
          ? const Padding(
              padding: EdgeInsets.all(16),
              child: SizedBox(
                height: 18,
                width: 18,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            )
          : empty
              ? Padding(
                  padding: const EdgeInsets.all(14),
                  child: Text(
                    'Keep typing to find a person...',
                    style: AppTextStyles.labelSmall
                        .copyWith(color: Colors.white38),
                  ),
                )
              : ListView.separated(
                  shrinkWrap: true,
                  itemCount: suggestions.length,
                  separatorBuilder: (_, _) => const Divider(
                    height: 1,
                    color: Colors.white10,
                  ),
                  itemBuilder: (ctx, i) {
                    final user = suggestions[i];
                    return ListTile(
                      dense: true,
                      onTap: () => onSelected(user),
                      leading: CircleAvatar(
                        radius: 16,
                        backgroundColor: Colors.white10,
                        backgroundImage: user.hasAvatar
                            ? NetworkImage(user.avatarUrl)
                            : null,
                        child: user.hasAvatar
                            ? null
                            : Text(
                                user.displayName.isNotEmpty
                                    ? user.displayName[0].toUpperCase()
                                    : '?',
                                style: AppTextStyles.label.copyWith(
                                  color: Colors.white70,
                                ),
                              ),
                      ),
                      title: Text(
                        user.displayName,
                        style:
                            AppTextStyles.label.copyWith(color: Colors.white),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      subtitle: user.username.isNotEmpty
                          ? Text(
                              '@${user.username}',
                              style: AppTextStyles.labelSmall
                                  .copyWith(color: Colors.white38),
                            )
                          : null,
                    );
                  },
                ),
    );
  }
}
