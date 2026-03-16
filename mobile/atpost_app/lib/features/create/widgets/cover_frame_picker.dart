import 'package:atpost_app/providers/editor_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CoverFramePicker extends ConsumerStatefulWidget {
  const CoverFramePicker({super.key});

  @override
  ConsumerState<CoverFramePicker> createState() => _CoverFramePickerState();
}

class _CoverFramePickerState extends ConsumerState<CoverFramePicker> {
  static const int _frameCount = 10;

  @override
  Widget build(BuildContext context) {
    final editorState = ref.watch(editorProvider);
    final totalMs = editorState.totalDuration.inMilliseconds;
    final selectedMs = editorState.coverFrameMs;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Padding(
          padding: EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: Text(
            'Tap to select cover frame',
            style: TextStyle(color: Colors.white70, fontSize: 13),
          ),
        ),
        SizedBox(
          height: 80,
          child: ListView.builder(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 8),
            itemCount: _frameCount,
            itemBuilder: (context, index) {
              final frameMs = totalMs > 0
                  ? ((index / _frameCount) * totalMs).round()
                  : index * 1000;
              final isSelected = selectedMs == frameMs ||
                  (index == 0 && selectedMs == 0 && totalMs == 0);

              return GestureDetector(
                onTap: () {
                  ref.read(editorProvider.notifier).setCoverFrame(frameMs);
                },
                child: Container(
                  width: 50,
                  height: 70,
                  margin: const EdgeInsets.only(right: 6),
                  decoration: BoxDecoration(
                    color: Colors.grey[700],
                    border: Border.all(
                      color: isSelected ? Colors.orange : Colors.transparent,
                      width: 2,
                    ),
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Stack(
                    children: [
                      const Center(
                        child: Icon(Icons.image, color: Colors.white38, size: 22),
                      ),
                      if (isSelected)
                        Positioned(
                          bottom: 2,
                          right: 2,
                          child: Container(
                            width: 14,
                            height: 14,
                            decoration: const BoxDecoration(
                              color: Colors.orange,
                              shape: BoxShape.circle,
                            ),
                            child: const Icon(Icons.check, color: Colors.white, size: 10),
                          ),
                        ),
                    ],
                  ),
                ),
              );
            },
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          child: Text(
            selectedMs > 0
                ? 'Cover at ${(selectedMs / 1000).toStringAsFixed(1)}s'
                : 'Cover at start',
            style: const TextStyle(color: Colors.white38, fontSize: 11),
          ),
        ),
      ],
    );
  }
}
