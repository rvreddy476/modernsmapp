import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:atpost_app/data/models/editor.dart';
import 'package:atpost_app/services/api_client.dart';

class EditorNotifier extends StateNotifier<EditorModel> {
  final ApiClient _api;
  Timer? _autoSaveTimer;

  EditorNotifier(this._api) : super(EditorModel.empty()) {
    // Auto-save every 10 seconds
    _autoSaveTimer = Timer.periodic(const Duration(seconds: 10), (_) => _autoSave());
  }

  @override
  void dispose() {
    _autoSaveTimer?.cancel();
    super.dispose();
  }

  void initEditor({required EditorMode mode, VideoClip? initialClip}) {
    state = EditorModel.empty(mode: mode);
    if (initialClip != null) {
      state = state.copyWith(clips: [initialClip]);
    }
  }

  // --- Clips ---
  void addClip(VideoClip clip) {
    if (state.clips.length >= 10) return;
    state = state.copyWith(clips: [...state.clips, clip]);
  }

  void removeClip(String clipId) {
    state = state.copyWith(clips: state.clips.where((c) => c.id != clipId).toList());
  }

  void trimClip(String clipId, Duration start, Duration end) {
    state = state.copyWith(
      clips: state.clips.map((c) => c.id == clipId ? c.copyWith(trimStart: start, trimEnd: end) : c).toList(),
    );
  }

  void setClipSpeed(String clipId, double speed) {
    state = state.copyWith(
      clips: state.clips.map((c) => c.id == clipId ? c.copyWith(speed: speed) : c).toList(),
    );
  }

  void reorderClips(int from, int to) {
    final clips = List<VideoClip>.from(state.clips);
    final clip = clips.removeAt(from);
    clips.insert(to > from ? to - 1 : to, clip);
    state = state.copyWith(clips: clips);
  }

  // --- Audio ---
  void setBackgroundAudio(AudioTrack track) => state = state.copyWith(backgroundAudio: track);
  void clearBackgroundAudio() => state = state.copyWith(backgroundAudio: null);
  void setAudioVolumes(double original, double background) =>
      state = state.copyWith(originalVolume: original, backgroundVolume: background);

  // --- Overlays ---
  void addTextOverlay(TextOverlay overlay) {
    state = state.copyWith(textOverlays: [...state.textOverlays, overlay]);
  }

  void updateTextOverlay(String id, TextOverlay updated) {
    state = state.copyWith(
      textOverlays: state.textOverlays.map((o) => o.id == id ? updated : o).toList(),
    );
  }

  void removeTextOverlay(String id) {
    state = state.copyWith(textOverlays: state.textOverlays.where((o) => o.id != id).toList());
  }

  // --- Filter & Speed ---
  void setFilter(VideoFilter filter) => state = state.copyWith(activeFilter: filter);
  void setPlaybackSpeed(double speed) => state = state.copyWith(playbackSpeed: speed);
  void setCoverFrame(int ms) => state = state.copyWith(coverFrameMs: ms);

  // --- Export progress ---
  void setExporting(bool v) => state = state.copyWith(isExporting: v);
  void setExportProgress(double p) => state = state.copyWith(exportProgress: p);
  void setExportedPath(String path) => state = state.copyWith(exportedPath: path);

  // --- Auto-save ---
  Future<void> _autoSave() async {
    if (state.clips.isEmpty) return;
    try {
      final stateJson = {
        'mode': state.mode.name,
        'clipCount': state.clips.length,
        'filter': state.activeFilter.name,
        'coverFrameMs': state.coverFrameMs,
      };
      if (state.sessionId == null) {
        final res = await _api.post('/v1/studio/sessions', data: {
          'mode': state.mode.name,
          'state_json': stateJson,
        });
        final data = res.data['data'] ?? res.data;
        state = state.copyWith(sessionId: data['id'] as String?);
      } else {
        await _api.put('/v1/studio/sessions/${state.sessionId}', data: {
          'state_json': stateJson,
        });
      }
    } catch (_) {
      // Auto-save is best-effort
    }
  }

  Future<void> deleteSession() async {
    if (state.sessionId == null) return;
    try {
      await _api.delete('/v1/studio/sessions/${state.sessionId}');
    } catch (_) {}
  }
}

final editorProvider = StateNotifierProvider.autoDispose<EditorNotifier, EditorModel>((ref) {
  return EditorNotifier(ref.watch(apiClientProvider));
});
