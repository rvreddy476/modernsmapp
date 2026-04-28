import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

/// Renders [text] with `#hashtag` substrings styled and tappable.
///
/// Uses a StatefulWidget so the [TapGestureRecognizer] instances created for
/// each hashtag can be disposed cleanly — building recognizers from a stateless
/// helper leaks them, per spec §10.1.
class ClickableHashtagText extends StatefulWidget {
  const ClickableHashtagText({
    super.key,
    required this.text,
    required this.normalStyle,
    required this.hashtagStyle,
    required this.onHashtagTap,
    this.maxLines,
    this.textAlign,
  });

  final String text;
  final TextStyle normalStyle;
  final TextStyle hashtagStyle;
  final void Function(String normalizedHashtag) onHashtagTap;
  final int? maxLines;
  final TextAlign? textAlign;

  @override
  State<ClickableHashtagText> createState() => _ClickableHashtagTextState();
}

class _ClickableHashtagTextState extends State<ClickableHashtagText> {
  final List<TapGestureRecognizer> _recognizers = [];

  static final _hashtagRegex = RegExp(r'#([a-zA-Z0-9_]+)');

  @override
  void dispose() {
    _disposeRecognizers();
    super.dispose();
  }

  void _disposeRecognizers() {
    for (final r in _recognizers) {
      r.dispose();
    }
    _recognizers.clear();
  }

  @override
  Widget build(BuildContext context) {
    // Recognizers need to be rebuilt each render so callbacks bind to the
    // current widget's onHashtagTap (in case the post or callback changes).
    _disposeRecognizers();

    final spans = <InlineSpan>[];
    var lastEnd = 0;

    for (final match in _hashtagRegex.allMatches(widget.text)) {
      if (match.start > lastEnd) {
        spans.add(TextSpan(
          text: widget.text.substring(lastEnd, match.start),
          style: widget.normalStyle,
        ));
      }

      final fullTag = match.group(0)!; // e.g. "#Cricket"
      final normalized = match.group(1)!.toLowerCase();
      final recognizer = TapGestureRecognizer()
        ..onTap = () => widget.onHashtagTap(normalized);
      _recognizers.add(recognizer);

      spans.add(TextSpan(
        text: fullTag,
        style: widget.hashtagStyle,
        recognizer: recognizer,
      ));

      lastEnd = match.end;
    }

    if (lastEnd < widget.text.length) {
      spans.add(TextSpan(
        text: widget.text.substring(lastEnd),
        style: widget.normalStyle,
      ));
    }

    return Text.rich(
      TextSpan(children: spans),
      maxLines: widget.maxLines,
      overflow:
          widget.maxLines == null ? null : TextOverflow.ellipsis,
      textAlign: widget.textAlign,
    );
  }
}
