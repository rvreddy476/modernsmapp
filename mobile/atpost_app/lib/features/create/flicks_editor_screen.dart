import 'dart:io';

import 'package:atpost_app/data/models/editor.dart';
import 'package:atpost_app/features/create/widgets/cover_frame_picker.dart';
import 'package:atpost_app/features/create/widgets/trimmer_widget.dart';
import 'package:atpost_app/providers/editor_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';
import 'package:video_player/video_player.dart';

class FlicksEditorScreen extends ConsumerStatefulWidget {
  const FlicksEditorScreen({super.key});

  @override
  ConsumerState<FlicksEditorScreen> createState() => _FlicksEditorScreenState();
}

class _FlicksEditorScreenState extends ConsumerState<FlicksEditorScreen>
    with SingleTickerProviderStateMixin {
  VideoPlayerController? _playerController;
  int _selectedClipIndex = 0;
  bool _isPlaying = false;
  late TabController _tabController;

  static const _tabs = ['Trim', 'Speed', 'Audio', 'Text', 'Cover'];
  static const _speedOptions = [0.3, 0.5, 1.0, 1.5, 2.0, 3.0];

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: _tabs.length, vsync: this);
    SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
  }

  @override
  void dispose() {
    _playerController?.dispose();
    _tabController.dispose();
    SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    super.dispose();
  }

  Future<void> _loadClip(VideoClip clip) async {
    await _playerController?.dispose();
    _playerController = null;
    final controller = VideoPlayerController.file(File(clip.filePath));
    await controller.initialize();
    controller.addListener(() {
      if (mounted) setState(() => _isPlaying = controller.value.isPlaying);
    });
    if (mounted) {
      setState(() => _playerController = controller);
    }
  }

  Future<void> _pickAndAddClip() async {
    final picker = ImagePicker();
    final video = await picker.pickVideo(source: ImageSource.gallery);
    if (video == null) return;

    // Build a placeholder duration — VideoPlayerController.file needs initialize()
    // We initialize briefly to get the real duration.
    final tempCtrl = VideoPlayerController.file(File(video.path));
    await tempCtrl.initialize();
    final duration = tempCtrl.value.duration;
    await tempCtrl.dispose();

    final clip = VideoClip(
      id: UniqueKey().toString(),
      filePath: video.path,
      originalDuration: duration,
    );

    ref.read(editorProvider.notifier).addClip(clip);

    final clips = ref.read(editorProvider).clips;
    final newIndex = clips.length - 1;
    setState(() => _selectedClipIndex = newIndex);
    await _loadClip(clip);
  }

  void _togglePlayback() {
    if (_playerController == null) return;
    if (_isPlaying) {
      _playerController!.pause();
    } else {
      _playerController!.play();
    }
  }

  String _formatDuration(Duration d) {
    final m = d.inMinutes.remainder(60).toString().padLeft(2, '0');
    final s = d.inSeconds.remainder(60).toString().padLeft(2, '0');
    return '$m:$s';
  }

  void _showAudioBrowser(BuildContext context) {
    showModalBottomSheet(
      context: context,
      backgroundColor: const Color(0xFF1A1A2E),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => const Padding(
        padding: EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.music_note, color: Colors.orange, size: 40),
            SizedBox(height: 12),
            Text(
              'Music library coming soon',
              style: TextStyle(color: Colors.white, fontSize: 16, fontWeight: FontWeight.w600),
            ),
            SizedBox(height: 8),
            Text(
              'You\'ll be able to browse and add background music to your Flicks.',
              textAlign: TextAlign.center,
              style: TextStyle(color: Colors.white54, fontSize: 13),
            ),
            SizedBox(height: 24),
          ],
        ),
      ),
    );
  }

  void _showTextEditor(BuildContext context) {
    final textCtrl = TextEditingController();
    Color selectedColor = Colors.white;
    TextAnimation selectedAnimation = TextAnimation.none;

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: const Color(0xFF1A1A2E),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (ctx) {
        return StatefulBuilder(
          builder: (ctx, setModalState) {
            return Padding(
              padding: EdgeInsets.only(
                left: 16,
                right: 16,
                top: 16,
                bottom: MediaQuery.of(ctx).viewInsets.bottom + 24,
              ),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text(
                    'Add Text',
                    style: TextStyle(color: Colors.white, fontSize: 16, fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: textCtrl,
                    style: const TextStyle(color: Colors.white),
                    maxLength: 100,
                    decoration: InputDecoration(
                      hintText: 'Enter text...',
                      hintStyle: const TextStyle(color: Colors.white38),
                      filled: true,
                      fillColor: Colors.white10,
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(12),
                        borderSide: BorderSide.none,
                      ),
                      counterStyle: const TextStyle(color: Colors.white38),
                    ),
                  ),
                  const SizedBox(height: 12),
                  const Text('Color', style: TextStyle(color: Colors.white54, fontSize: 12)),
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      Colors.white,
                      Colors.yellow,
                      Colors.orange,
                      Colors.red,
                      Colors.cyan,
                    ].map((c) {
                      final isSelected = selectedColor == c;
                      return GestureDetector(
                        onTap: () => setModalState(() => selectedColor = c),
                        child: Container(
                          width: 32,
                          height: 32,
                          margin: const EdgeInsets.only(right: 8),
                          decoration: BoxDecoration(
                            color: c,
                            shape: BoxShape.circle,
                            border: Border.all(
                              color: isSelected ? Colors.orange : Colors.transparent,
                              width: 3,
                            ),
                          ),
                        ),
                      );
                    }).toList(),
                  ),
                  const SizedBox(height: 12),
                  const Text('Animation', style: TextStyle(color: Colors.white54, fontSize: 12)),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    children: TextAnimation.values.map((anim) {
                      final isSelected = selectedAnimation == anim;
                      return GestureDetector(
                        onTap: () => setModalState(() => selectedAnimation = anim),
                        child: Container(
                          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                          decoration: BoxDecoration(
                            color: isSelected ? Colors.orange : Colors.white10,
                            borderRadius: BorderRadius.circular(20),
                          ),
                          child: Text(
                            anim.name,
                            style: TextStyle(
                              color: isSelected ? Colors.white : Colors.white54,
                              fontSize: 12,
                            ),
                          ),
                        ),
                      );
                    }).toList(),
                  ),
                  const SizedBox(height: 16),
                  SizedBox(
                    width: double.infinity,
                    child: ElevatedButton(
                      style: ElevatedButton.styleFrom(
                        backgroundColor: Colors.orange,
                        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                      ),
                      onPressed: () {
                        if (textCtrl.text.trim().isEmpty) return;
                        final editorState = ref.read(editorProvider);
                        final overlay = TextOverlay(
                          id: UniqueKey().toString(),
                          text: textCtrl.text.trim(),
                          textColor: selectedColor,
                          animation: selectedAnimation,
                          disappearsAt: editorState.totalDuration > Duration.zero
                              ? editorState.totalDuration
                              : const Duration(seconds: 5),
                        );
                        ref.read(editorProvider.notifier).addTextOverlay(overlay);
                        Navigator.of(ctx).pop();
                      },
                      child: const Text('Add to Video', style: TextStyle(color: Colors.white)),
                    ),
                  ),
                ],
              ),
            );
          },
        );
      },
    );
  }

  // --- Build helpers ---

  Widget _buildVideoPreview(EditorModel editorState) {
    final hasClips = editorState.clips.isNotEmpty;

    return Expanded(
      flex: 6,
      child: GestureDetector(
        onTap: hasClips ? _togglePlayback : null,
        child: Container(
          color: Colors.black,
          child: hasClips && _playerController != null && _playerController!.value.isInitialized
              ? Stack(
                  fit: StackFit.expand,
                  children: [
                    Center(
                      child: AspectRatio(
                        aspectRatio: 9 / 16,
                        child: ColorFiltered(
                          colorFilter: ColorFilter.matrix(
                            VideoFilterMatrix.forFilter(editorState.activeFilter),
                          ),
                          child: VideoPlayer(_playerController!),
                        ),
                      ),
                    ),
                    // Text overlays
                    ...editorState.textOverlays.map((overlay) {
                      return Positioned(
                        left: overlay.position.dx *
                            MediaQuery.of(context).size.width,
                        top: overlay.position.dy *
                            (MediaQuery.of(context).size.height * 0.6),
                        child: Draggable(
                          feedback: _buildTextOverlayWidget(overlay),
                          childWhenDragging: const SizedBox.shrink(),
                          onDragEnd: (details) {
                            final screenW = MediaQuery.of(context).size.width;
                            final screenH = MediaQuery.of(context).size.height * 0.6;
                            ref.read(editorProvider.notifier).updateTextOverlay(
                              overlay.id,
                              overlay.copyWith(
                                position: Offset(
                                  (details.offset.dx / screenW).clamp(0.0, 1.0),
                                  (details.offset.dy / screenH).clamp(0.0, 1.0),
                                ),
                              ),
                            );
                          },
                          child: _buildTextOverlayWidget(overlay),
                        ),
                      );
                    }),
                    // Play/pause icon hint
                    if (!_isPlaying)
                      Center(
                        child: Container(
                          width: 60,
                          height: 60,
                          decoration: BoxDecoration(
                            color: Colors.black45,
                            shape: BoxShape.circle,
                          ),
                          child: const Icon(Icons.play_arrow, color: Colors.white, size: 36),
                        ),
                      ),
                  ],
                )
              : hasClips
                  ? const Center(child: CircularProgressIndicator(color: Colors.orange))
                  : Center(
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          Icon(
                            Icons.add_circle_outline,
                            size: 80,
                            color: Colors.white.withValues(alpha: 0.35),
                          ),
                          const SizedBox(height: 12),
                          const Text(
                            'Tap to add clips',
                            style: TextStyle(color: Colors.white54, fontSize: 16),
                          ),
                        ],
                      ),
                    ),
        ),
      ),
    );
  }

  Widget _buildTextOverlayWidget(TextOverlay overlay) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: overlay.backgroundColor,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        overlay.text,
        style: TextStyle(
          color: overlay.textColor,
          fontSize: 18 * overlay.scale,
          fontWeight: FontWeight.bold,
        ),
      ),
    );
  }

  Widget _buildClipRail(EditorModel editorState) {
    return SizedBox(
      height: 80,
      child: ListView.builder(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        itemCount: editorState.clips.length + 1,
        itemBuilder: (context, index) {
          // "+" add button at end
          if (index == editorState.clips.length) {
            return GestureDetector(
              onTap: _pickAndAddClip,
              child: Container(
                width: 56,
                height: 72,
                margin: const EdgeInsets.only(right: 6),
                decoration: BoxDecoration(
                  color: Colors.white10,
                  border: Border.all(color: Colors.white24, width: 1),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: const Icon(Icons.add, color: Colors.white54, size: 28),
              ),
            );
          }

          final clip = editorState.clips[index];
          final isSelected = index == _selectedClipIndex;

          return GestureDetector(
            onTap: () async {
              setState(() => _selectedClipIndex = index);
              await _loadClip(clip);
            },
            onLongPress: () => _showClipOptions(context, index, editorState),
            child: Container(
              width: 56,
              height: 72,
              margin: const EdgeInsets.only(right: 6),
              decoration: BoxDecoration(
                border: Border.all(
                  color: isSelected ? Colors.orange : Colors.transparent,
                  width: 2,
                ),
                borderRadius: BorderRadius.circular(6),
                color: Colors.grey[800],
              ),
              child: Stack(
                children: [
                  ClipRRect(
                    borderRadius: BorderRadius.circular(4),
                    child: Image.file(
                      File(clip.filePath),
                      fit: BoxFit.cover,
                      width: double.infinity,
                      height: double.infinity,
                      errorBuilder: (_, _, _) => Container(
                        color: Colors.grey[700],
                        child: const Icon(Icons.videocam, color: Colors.white38, size: 20),
                      ),
                    ),
                  ),
                  Positioned(
                    bottom: 2,
                    right: 2,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 3, vertical: 1),
                      color: Colors.black54,
                      child: Text(
                        _formatDuration(clip.usedDuration),
                        style: const TextStyle(color: Colors.white, fontSize: 9),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }

  void _showClipOptions(BuildContext context, int index, EditorModel editorState) {
    showModalBottomSheet(
      context: context,
      backgroundColor: const Color(0xFF1A1A2E),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (_) => Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(height: 8),
          if (index > 0)
            ListTile(
              leading: const Icon(Icons.arrow_upward, color: Colors.white70),
              title: const Text('Move earlier', style: TextStyle(color: Colors.white)),
              onTap: () {
                ref.read(editorProvider.notifier).reorderClips(index, index - 1);
                Navigator.of(context).pop();
              },
            ),
          if (index < editorState.clips.length - 1)
            ListTile(
              leading: const Icon(Icons.arrow_downward, color: Colors.white70),
              title: const Text('Move later', style: TextStyle(color: Colors.white)),
              onTap: () {
                ref.read(editorProvider.notifier).reorderClips(index, index + 1);
                Navigator.of(context).pop();
              },
            ),
          ListTile(
            leading: const Icon(Icons.delete, color: Colors.redAccent),
            title: const Text('Remove clip', style: TextStyle(color: Colors.redAccent)),
            onTap: () {
              final clip = editorState.clips[index];
              ref.read(editorProvider.notifier).removeClip(clip.id);
              if (_selectedClipIndex >= index && _selectedClipIndex > 0) {
                setState(() => _selectedClipIndex = _selectedClipIndex - 1);
              }
              Navigator.of(context).pop();
            },
          ),
          const SizedBox(height: 8),
        ],
      ),
    );
  }

  Widget _buildFilterStrip(EditorModel editorState) {
    return SizedBox(
      height: 90,
      child: ListView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 8),
        children: VideoFilter.values.map((f) {
          final isSelected = editorState.activeFilter == f;
          return GestureDetector(
            onTap: () => ref.read(editorProvider.notifier).setFilter(f),
            child: Padding(
              padding: const EdgeInsets.only(right: 10),
              child: Column(
                children: [
                  Container(
                    width: 60,
                    height: 60,
                    decoration: BoxDecoration(
                      border: Border.all(
                        color: isSelected ? Colors.orange : Colors.transparent,
                        width: 2,
                      ),
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(6),
                      child: ColorFiltered(
                        colorFilter: ColorFilter.matrix(
                          VideoFilterMatrix.forFilter(f),
                        ),
                        child: Container(
                          color: Colors.grey[700],
                          child: const Icon(Icons.movie, color: Colors.white38, size: 28),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    f.name,
                    style: TextStyle(
                      color: isSelected ? Colors.orange : Colors.white54,
                      fontSize: 10,
                    ),
                  ),
                ],
              ),
            ),
          );
        }).toList(),
      ),
    );
  }

  Widget _buildTrimTab(EditorModel editorState) {
    if (editorState.clips.isEmpty) {
      return const Center(
        child: Text('Add a clip to trim', style: TextStyle(color: Colors.white38)),
      );
    }
    final clip = editorState.clips[_selectedClipIndex.clamp(0, editorState.clips.length - 1)];
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 8),
      child: TrimmerWidget(
        clip: clip,
        onTrimChanged: (start, end) {
          ref.read(editorProvider.notifier).trimClip(clip.id, start, end);
        },
      ),
    );
  }

  Widget _buildSpeedTab(EditorModel editorState) {
    if (editorState.clips.isEmpty) {
      return const Center(
        child: Text('Add a clip to adjust speed', style: TextStyle(color: Colors.white38)),
      );
    }
    final clip = editorState.clips[_selectedClipIndex.clamp(0, editorState.clips.length - 1)];
    return Center(
      child: Wrap(
        spacing: 10,
        children: _speedOptions.map((speed) {
          final isSelected = (clip.speed - speed).abs() < 0.01;
          return GestureDetector(
            onTap: () => ref.read(editorProvider.notifier).setClipSpeed(clip.id, speed),
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 10),
              decoration: BoxDecoration(
                color: isSelected ? Colors.orange : Colors.white10,
                borderRadius: BorderRadius.circular(20),
              ),
              child: Text(
                '$speed×',
                style: TextStyle(
                  color: isSelected ? Colors.white : Colors.white54,
                  fontWeight: FontWeight.w600,
                  fontSize: 14,
                ),
              ),
            ),
          );
        }).toList(),
      ),
    );
  }

  Widget _buildAudioTab(EditorModel editorState) {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ElevatedButton.icon(
            style: ElevatedButton.styleFrom(
              backgroundColor: Colors.white10,
              foregroundColor: Colors.white,
              shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
            ),
            icon: const Icon(Icons.music_note, color: Colors.orange),
            label: const Text('Browse Music'),
            onPressed: () => _showAudioBrowser(context),
          ),
          const SizedBox(height: 20),
          const Text('Original Volume', style: TextStyle(color: Colors.white54, fontSize: 12)),
          Slider(
            value: editorState.originalVolume,
            min: 0.0,
            max: 1.0,
            activeColor: Colors.orange,
            inactiveColor: Colors.white12,
            onChanged: (v) => ref.read(editorProvider.notifier).setAudioVolumes(
              v,
              editorState.backgroundVolume,
            ),
          ),
          const SizedBox(height: 8),
          const Text('Music Volume', style: TextStyle(color: Colors.white54, fontSize: 12)),
          Slider(
            value: editorState.backgroundVolume,
            min: 0.0,
            max: 1.0,
            activeColor: Colors.orange,
            inactiveColor: Colors.white12,
            onChanged: (v) => ref.read(editorProvider.notifier).setAudioVolumes(
              editorState.originalVolume,
              v,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildTextTab(EditorModel editorState) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: ElevatedButton.icon(
            style: ElevatedButton.styleFrom(
              backgroundColor: Colors.orange,
              shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
            ),
            icon: const Icon(Icons.text_fields, color: Colors.white),
            label: const Text('Add Text', style: TextStyle(color: Colors.white)),
            onPressed: () => _showTextEditor(context),
          ),
        ),
        Expanded(
          child: editorState.textOverlays.isEmpty
              ? const Center(
                  child: Text('No text overlays yet', style: TextStyle(color: Colors.white38)),
                )
              : ListView.builder(
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  itemCount: editorState.textOverlays.length,
                  itemBuilder: (context, index) {
                    final overlay = editorState.textOverlays[index];
                    return ListTile(
                      contentPadding: EdgeInsets.zero,
                      leading: Container(
                        width: 36,
                        height: 36,
                        decoration: BoxDecoration(
                          color: overlay.backgroundColor ?? Colors.white10,
                          borderRadius: BorderRadius.circular(6),
                        ),
                        child: Center(
                          child: Text(
                            'T',
                            style: TextStyle(
                              color: overlay.textColor,
                              fontWeight: FontWeight.bold,
                            ),
                          ),
                        ),
                      ),
                      title: Text(
                        overlay.text,
                        style: const TextStyle(color: Colors.white, fontSize: 13),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      subtitle: Text(
                        overlay.animation.name,
                        style: const TextStyle(color: Colors.white38, fontSize: 11),
                      ),
                      trailing: IconButton(
                        icon: const Icon(Icons.delete_outline, color: Colors.redAccent),
                        onPressed: () =>
                            ref.read(editorProvider.notifier).removeTextOverlay(overlay.id),
                      ),
                    );
                  },
                ),
        ),
      ],
    );
  }

  Widget _buildCoverTab(EditorModel editorState) {
    return Column(
      children: [
        const SizedBox(height: 8),
        const CoverFramePicker(),
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    final editorState = ref.watch(editorProvider);

    return Scaffold(
      backgroundColor: Colors.black,
      extendBodyBehindAppBar: true,
      appBar: AppBar(
        backgroundColor: Colors.transparent,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios, color: Colors.white),
          onPressed: () => context.pop(),
        ),
        actions: [
          if (editorState.clips.isNotEmpty)
            TextButton.icon(
              style: TextButton.styleFrom(foregroundColor: Colors.white),
              icon: const Icon(Icons.arrow_forward, size: 18),
              label: const Text('Next', style: TextStyle(fontWeight: FontWeight.w700)),
              onPressed: () => context.push('/flicks/caption'),
            ),
          const SizedBox(width: 8),
        ],
      ),
      body: Column(
        children: [
          // Video preview (60%)
          _buildVideoPreview(editorState),

          // Clip rail
          Container(
            color: const Color(0xFF0D0D0D),
            child: _buildClipRail(editorState),
          ),

          // Filter strip
          Container(
            color: const Color(0xFF0D0D0D),
            child: _buildFilterStrip(editorState),
          ),

          // Bottom tool area
          Expanded(
            flex: 4,
            child: Container(
              color: const Color(0xFF0A0A0A),
              child: Column(
                children: [
                  TabBar(
                    controller: _tabController,
                    indicatorColor: Colors.orange,
                    labelColor: Colors.orange,
                    unselectedLabelColor: Colors.white38,
                    labelStyle: const TextStyle(fontSize: 12, fontWeight: FontWeight.w600),
                    tabs: _tabs
                        .map((t) => Tab(text: t))
                        .toList(),
                  ),
                  Expanded(
                    child: TabBarView(
                      controller: _tabController,
                      children: [
                        _buildTrimTab(editorState),
                        _buildSpeedTab(editorState),
                        _buildAudioTab(editorState),
                        _buildTextTab(editorState),
                        _buildCoverTab(editorState),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
