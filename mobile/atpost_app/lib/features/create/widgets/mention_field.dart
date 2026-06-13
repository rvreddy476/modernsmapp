import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/hashtag_feed/data/hashtag_repository.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// MentionField — composer text field with @username AND #hashtag
/// autocomplete in the same widget. Whichever trigger (`@` or `#`) is
/// the active token under the caret drives the popover:
///   - `@token` → people search via `userRepository.searchUsers`
///   - `#token` → tag prefix-match via `hashtagRepository.search`
/// Selecting a suggestion splices the canonical form back into the
/// caption so the backend regex in post-service picks it up.
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

/// Which trigger token is currently active under the caret.
enum _TokenKind { mention, hashtag }

class _MentionFieldState extends ConsumerState<MentionField> {
  final LayerLink _link = LayerLink();
  OverlayEntry? _overlay;
  Timer? _debounce;
  List<User> _userSuggestions = const <User>[];
  List<HashtagModel> _tagSuggestions = const <HashtagModel>[];
  bool _loading = false;
  String _activeQuery = '';
  _TokenKind _activeKind = _TokenKind.mention;
  // Range of the active token in `controller.text`. Used so we can
  // splice the chosen username / hashtag back into the right slot.
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
    // Either `@token` or `#token` can be active under the caret. Prefer
    // whichever one is closest to the cursor (mention scanner runs first
    // and walks backwards; hashtag is checked the same way and we take
    // the one with the later start position so we always pick the inner
    // token if the user typed something like `@foo #bar|`).
    final mention = _findActiveTriggerToken(text, cursor, '@');
    final hashtag = _findActiveTriggerToken(text, cursor, '#');
    final token = _pickInnerToken(mention, hashtag);
    if (token == null) {
      _hideOverlay();
      return;
    }
    _tokenStart = token.start;
    _tokenEnd = token.end;
    _activeQuery = token.query;
    _activeKind = token.prefix == '#' ? _TokenKind.hashtag : _TokenKind.mention;
    _scheduleSearch(token.query);
  }

  void _scheduleSearch(String query) {
    _debounce?.cancel();
    final kind = _activeKind;
    if (query.isEmpty) {
      // Show the overlay anchored, but with an empty hint state so
      // the user understands typing more characters refines.
      setState(() {
        _userSuggestions = const <User>[];
        _tagSuggestions = const <HashtagModel>[];
        _loading = false;
      });
      _showOverlay();
      return;
    }
    // Hashtag endpoint requires q >= 2 chars; mention search starts
    // at 1 char (the username repo is permissive). Mirror the backend
    // constraint here to avoid wasted requests + 400s.
    if (kind == _TokenKind.hashtag && query.length < 2) {
      setState(() {
        _tagSuggestions = const <HashtagModel>[];
        _loading = false;
      });
      _showOverlay();
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 220), () async {
      setState(() => _loading = true);
      _showOverlay();
      try {
        if (kind == _TokenKind.mention) {
          final result = await ref
              .read(userRepositoryProvider)
              .searchUsers(query, limit: 8);
          if (!mounted || _activeQuery != query || _activeKind != kind) return;
          setState(() {
            _userSuggestions = result.users;
            _loading = false;
          });
        } else {
          final tags = await ref
              .read(hashtagRepositoryProvider)
              .search(query, limit: 8);
          if (!mounted || _activeQuery != query || _activeKind != kind) return;
          setState(() {
            _tagSuggestions = tags;
            _loading = false;
          });
        }
        _showOverlay();
      } catch (_) {
        if (!mounted) return;
        setState(() {
          _userSuggestions = const <User>[];
          _tagSuggestions = const <HashtagModel>[];
          _loading = false;
        });
      }
    });
  }

  void _showOverlay() {
    _overlay?.remove();
    final overlay = Overlay.of(context, rootOverlay: true);
    final kind = _activeKind;
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
            child: kind == _TokenKind.mention
                ? _MentionSuggestionsPanel(
                    loading: _loading,
                    suggestions: _userSuggestions,
                    onSelected: _applyMentionSuggestion,
                  )
                : _HashtagSuggestionsPanel(
                    loading: _loading,
                    suggestions: _tagSuggestions,
                    onSelected: _applyHashtagSuggestion,
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

  void _applyMentionSuggestion(User user) {
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
    _spliceReplacement('@$username ');
  }

  void _applyHashtagSuggestion(HashtagModel tag) {
    // Strip any spaces just in case and prefer the normalized name so
    // duplicates collapse (#Foo and #foo both land on `foo`).
    final name = tag.normalizedName.isNotEmpty
        ? tag.normalizedName
        : tag.displayName;
    final cleaned = name.replaceAll(' ', '').replaceAll('#', '');
    if (cleaned.isEmpty) {
      _hideOverlay();
      return;
    }
    _spliceReplacement('#$cleaned ');
  }

  void _spliceReplacement(String replacement) {
    final text = widget.controller.text;
    if (_tokenStart < 0 || _tokenEnd < _tokenStart) {
      _hideOverlay();
      return;
    }
    final before = text.substring(0, _tokenStart);
    final after = _tokenEnd >= text.length ? '' : text.substring(_tokenEnd);
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

/// Result of locating an active trigger token (`@…` or `#…`) in the
/// composer text. `prefix` is the trigger character so callers can
/// branch on which kind of suggestion to fetch.
class _TriggerToken {
  const _TriggerToken({
    required this.start,
    required this.end,
    required this.query,
    required this.prefix,
  });

  final int start;
  final int end;
  final String query;
  final String prefix;
}

/// Walk back from [cursor] until we hit [prefix]. The token is active
/// when:
/// - [prefix] is preceded by start-of-text or whitespace, AND
/// - the text between [prefix] and [cursor] contains no whitespace and
///   no other trigger character.
///
/// Returns null when no active token is present.
_TriggerToken? _findActiveTriggerToken(String text, int cursor, String prefix) {
  if (cursor < 0 || cursor > text.length) return null;
  if (cursor == 0) return null;
  var i = cursor - 1;
  while (i >= 0) {
    final ch = text[i];
    if (ch == prefix) {
      if (i == 0 || _isTokenBoundary(text[i - 1])) {
        final query = text.substring(i + 1, cursor);
        if (query.contains(RegExp(r'\s'))) return null;
        return _TriggerToken(
          start: i,
          end: cursor,
          query: query,
          prefix: prefix,
        );
      }
      return null;
    }
    if (_isTokenBoundary(ch)) return null;
    // The other trigger char also terminates this scan — `#tag@foo|`
    // is a `@foo` mention, not a hashtag that swallows the @.
    if (ch == '@' || ch == '#') return null;
    i -= 1;
  }
  return null;
}

/// When both `@` and `#` produced a token candidate, return whichever
/// starts later (i.e. is the inner / closer-to-the-cursor one).
_TriggerToken? _pickInnerToken(_TriggerToken? a, _TriggerToken? b) {
  if (a == null) return b;
  if (b == null) return a;
  return a.start >= b.start ? a : b;
}

bool _isTokenBoundary(String ch) {
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

/// Hashtag-suggestion popover. Same chrome as the mention panel so the
/// composer feels consistent; the only differences are leading-icon
/// (`#`) and subtitle (post count instead of @handle).
class _HashtagSuggestionsPanel extends StatelessWidget {
  const _HashtagSuggestionsPanel({
    required this.loading,
    required this.suggestions,
    required this.onSelected,
  });

  final bool loading;
  final List<HashtagModel> suggestions;
  final ValueChanged<HashtagModel> onSelected;

  String _formatCount(int n) {
    if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M posts';
    if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}K posts';
    if (n == 1) return '1 post';
    return '$n posts';
  }

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
                    'Keep typing to find a tag...',
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
                    final tag = suggestions[i];
                    final display = tag.displayName.isNotEmpty
                        ? tag.displayName
                        : tag.normalizedName;
                    return ListTile(
                      dense: true,
                      onTap: () => onSelected(tag),
                      leading: Container(
                        width: 32,
                        height: 32,
                        alignment: Alignment.center,
                        decoration: BoxDecoration(
                          color: Colors.white10,
                          borderRadius: BorderRadius.circular(8),
                        ),
                        child: Text(
                          '#',
                          style: AppTextStyles.h3.copyWith(
                            color: tag.isTrending
                                ? AppColors.postbookPrimary
                                : Colors.white70,
                          ),
                        ),
                      ),
                      title: Text(
                        '#$display',
                        style:
                            AppTextStyles.label.copyWith(color: Colors.white),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      subtitle: Text(
                        tag.isTrending
                            ? '🔥 trending · ${_formatCount(tag.postCount)}'
                            : _formatCount(tag.postCount),
                        style: AppTextStyles.labelSmall
                            .copyWith(color: Colors.white38),
                      ),
                    );
                  },
                ),
    );
  }
}
