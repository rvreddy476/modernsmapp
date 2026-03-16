import 'package:atpost_app/data/models/editor.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class TrimmerWidget extends ConsumerStatefulWidget {
  final VideoClip clip;
  final Function(Duration start, Duration end) onTrimChanged;

  const TrimmerWidget({
    super.key,
    required this.clip,
    required this.onTrimChanged,
  });

  @override
  ConsumerState<TrimmerWidget> createState() => _TrimmerWidgetState();
}

class _TrimmerWidgetState extends ConsumerState<TrimmerWidget> {
  double _leftFraction = 0.0;
  double _rightFraction = 1.0;

  @override
  void initState() {
    super.initState();
    final totalMicros = widget.clip.originalDuration.inMicroseconds;
    if (totalMicros > 0) {
      _leftFraction = widget.clip.trimStart.inMicroseconds / totalMicros;
      _rightFraction = widget.clip.trimEnd.inMicroseconds / totalMicros;
    }
  }

  String _formatDuration(Duration d) {
    final m = d.inMinutes.remainder(60).toString().padLeft(2, '0');
    final s = d.inSeconds.remainder(60).toString().padLeft(2, '0');
    return '$m:$s';
  }

  void _notifyChange() {
    final totalMicros = widget.clip.originalDuration.inMicroseconds;
    final start = Duration(microseconds: (_leftFraction * totalMicros).round());
    final end = Duration(microseconds: (_rightFraction * totalMicros).round());
    widget.onTrimChanged(start, end);
  }

  @override
  Widget build(BuildContext context) {
    final totalDuration = widget.clip.originalDuration;
    final trimmedDuration = Duration(
      microseconds: ((_rightFraction - _leftFraction) * totalDuration.inMicroseconds).round(),
    );
    final thumbnailCount = (totalDuration.inSeconds.clamp(1, 10)).toInt();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          child: Text(
            '${_formatDuration(trimmedDuration)} / ${_formatDuration(totalDuration)}',
            style: const TextStyle(color: Colors.white70, fontSize: 12),
          ),
        ),
        SizedBox(
          height: 80,
          child: LayoutBuilder(
            builder: (context, constraints) {
              final width = constraints.maxWidth;
              final leftX = _leftFraction * width;
              final rightX = _rightFraction * width;
              const handleWidth = 16.0;

              return Stack(
                children: [
                  // Background gradient
                  Container(
                    decoration: const BoxDecoration(
                      gradient: LinearGradient(
                        colors: [Color(0xFF2A2A2A), Colors.black],
                      ),
                    ),
                  ),
                  // Thumbnail strip
                  Row(
                    children: List.generate(thumbnailCount, (i) {
                      return Expanded(
                        child: Container(
                          height: 80,
                          margin: const EdgeInsets.symmetric(horizontal: 1),
                          color: Colors.grey[800],
                          child: const Center(
                            child: Icon(Icons.image, color: Colors.white24, size: 18),
                          ),
                        ),
                      );
                    }),
                  ),
                  // Dimmed area left of trim start
                  if (leftX > 0)
                    Positioned(
                      left: 0,
                      top: 0,
                      width: leftX,
                      height: 80,
                      child: Container(color: Colors.black54),
                    ),
                  // Dimmed area right of trim end
                  if (rightX < width)
                    Positioned(
                      left: rightX,
                      top: 0,
                      width: width - rightX,
                      height: 80,
                      child: Container(color: Colors.black54),
                    ),
                  // Orange trim region outline (top + bottom borders)
                  Positioned(
                    left: leftX,
                    top: 0,
                    width: (rightX - leftX).clamp(0.0, width),
                    height: 80,
                    child: Container(
                      decoration: BoxDecoration(
                        border: Border(
                          top: const BorderSide(color: Colors.orange, width: 2),
                          bottom: const BorderSide(color: Colors.orange, width: 2),
                        ),
                      ),
                    ),
                  ),
                  // Left handle
                  Positioned(
                    left: (leftX - handleWidth / 2).clamp(0.0, width - handleWidth),
                    top: 0,
                    width: handleWidth,
                    height: 80,
                    child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onHorizontalDragUpdate: (details) {
                        setState(() {
                          _leftFraction = ((_leftFraction * width + details.delta.dx) / width)
                              .clamp(0.0, _rightFraction - 0.05);
                        });
                        _notifyChange();
                      },
                      child: Container(
                        decoration: BoxDecoration(
                          color: Colors.orange,
                          borderRadius: const BorderRadius.only(
                            topLeft: Radius.circular(4),
                            bottomLeft: Radius.circular(4),
                          ),
                        ),
                        child: const Center(
                          child: Icon(Icons.drag_handle, color: Colors.white, size: 14),
                        ),
                      ),
                    ),
                  ),
                  // Right handle
                  Positioned(
                    left: (rightX - handleWidth / 2).clamp(0.0, width - handleWidth),
                    top: 0,
                    width: handleWidth,
                    height: 80,
                    child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onHorizontalDragUpdate: (details) {
                        setState(() {
                          _rightFraction = ((_rightFraction * width + details.delta.dx) / width)
                              .clamp(_leftFraction + 0.05, 1.0);
                        });
                        _notifyChange();
                      },
                      child: Container(
                        decoration: BoxDecoration(
                          color: Colors.orange,
                          borderRadius: const BorderRadius.only(
                            topRight: Radius.circular(4),
                            bottomRight: Radius.circular(4),
                          ),
                        ),
                        child: const Center(
                          child: Icon(Icons.drag_handle, color: Colors.white, size: 14),
                        ),
                      ),
                    ),
                  ),
                ],
              );
            },
          ),
        ),
      ],
    );
  }
}
