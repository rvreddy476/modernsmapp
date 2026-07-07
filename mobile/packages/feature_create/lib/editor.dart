import 'package:flutter/material.dart';

// --- Enums ---

enum EditorMode { flick, story, carousel, longVideo }

enum VideoFilter {
  original, warm, cool, fade, vivid, noir, golden, matte, moody, fresh, drama, retro
}

enum TextAnimation { none, fade, typewriter, slide, pop }

enum StickerType { static, gif, lottie, interactive }

// --- Models ---

class VideoClip {
  final String id;
  final String filePath;
  final Duration originalDuration;
  final Duration trimStart;
  final Duration trimEnd;
  final double speed; // 0.3 – 3.0
  final String? audioTrackPath;

  const VideoClip({
    required this.id,
    required this.filePath,
    required this.originalDuration,
    Duration? trimStart,
    Duration? trimEnd,
    this.speed = 1.0,
    this.audioTrackPath,
  }) : trimStart = trimStart ?? Duration.zero,
       trimEnd = trimEnd ?? originalDuration;

  Duration get usedDuration {
    final raw = trimEnd - trimStart;
    return Duration(microseconds: (raw.inMicroseconds / speed).round());
  }

  VideoClip copyWith({String? filePath, Duration? trimStart, Duration? trimEnd, double? speed}) {
    return VideoClip(
      id: id,
      filePath: filePath ?? this.filePath,
      originalDuration: originalDuration,
      trimStart: trimStart ?? this.trimStart,
      trimEnd: trimEnd ?? this.trimEnd,
      speed: speed ?? this.speed,
      audioTrackPath: audioTrackPath,
    );
  }
}

class AudioTrack {
  final String id;
  final String title;
  final String artistName;
  final String filePath;      // local or CDN URL
  final Duration duration;
  final String? coverUrl;

  const AudioTrack({
    required this.id,
    required this.title,
    required this.artistName,
    required this.filePath,
    required this.duration,
    this.coverUrl,
  });
}

class TextOverlay {
  final String id;
  final String text;
  final Offset position;     // 0.0–1.0 relative
  final double scale;
  final double rotation;
  final String fontFamily;   // 'Outfit' | 'Bold' | 'Handwrite' | 'Mono'
  final Color textColor;
  final Color? backgroundColor;
  final TextAnimation animation;
  final Duration appearsAt;
  final Duration disappearsAt;

  const TextOverlay({
    required this.id,
    required this.text,
    this.position = const Offset(0.5, 0.5),
    this.scale = 1.0,
    this.rotation = 0.0,
    this.fontFamily = 'Outfit',
    this.textColor = Colors.white,
    this.backgroundColor,
    this.animation = TextAnimation.none,
    this.appearsAt = Duration.zero,
    required this.disappearsAt,
  });

  TextOverlay copyWith({String? text, Offset? position, double? scale, double? rotation, Color? textColor, Color? backgroundColor, TextAnimation? animation}) {
    return TextOverlay(
      id: id,
      text: text ?? this.text,
      position: position ?? this.position,
      scale: scale ?? this.scale,
      rotation: rotation ?? this.rotation,
      fontFamily: fontFamily,
      textColor: textColor ?? this.textColor,
      backgroundColor: backgroundColor ?? this.backgroundColor,
      animation: animation ?? this.animation,
      appearsAt: appearsAt,
      disappearsAt: disappearsAt,
    );
  }
}

class StickerOverlay {
  final String id;
  final String assetUrl;
  final StickerType type;
  final Offset position;
  final double scale;
  final double rotation;
  final Duration appearsAt;
  final Duration disappearsAt;
  final Map<String, dynamic>? interactiveData;

  const StickerOverlay({
    required this.id,
    required this.assetUrl,
    this.type = StickerType.static,
    this.position = const Offset(0.5, 0.5),
    this.scale = 1.0,
    this.rotation = 0.0,
    this.appearsAt = Duration.zero,
    required this.disappearsAt,
    this.interactiveData,
  });
}

class EditorModel {
  final List<VideoClip> clips;
  final List<TextOverlay> textOverlays;
  final List<StickerOverlay> stickerOverlays;
  final AudioTrack? backgroundAudio;
  final String? voiceoverPath;
  final double originalVolume;
  final double backgroundVolume;
  final VideoFilter activeFilter;
  final double playbackSpeed;
  final int coverFrameMs;
  final EditorMode mode;
  final bool isExporting;
  final double exportProgress; // 0.0–1.0
  final String? exportedPath;
  final String? sessionId;   // auto-save session ID

  const EditorModel({
    this.clips = const [],
    this.textOverlays = const [],
    this.stickerOverlays = const [],
    this.backgroundAudio,
    this.voiceoverPath,
    this.originalVolume = 1.0,
    this.backgroundVolume = 0.7,
    this.activeFilter = VideoFilter.original,
    this.playbackSpeed = 1.0,
    this.coverFrameMs = 0,
    this.mode = EditorMode.flick,
    this.isExporting = false,
    this.exportProgress = 0.0,
    this.exportedPath,
    this.sessionId,
  });

  Duration get totalDuration => clips.fold(Duration.zero, (sum, c) => sum + c.usedDuration);

  EditorModel copyWith({
    List<VideoClip>? clips,
    List<TextOverlay>? textOverlays,
    List<StickerOverlay>? stickerOverlays,
    AudioTrack? backgroundAudio,
    String? voiceoverPath,
    double? originalVolume,
    double? backgroundVolume,
    VideoFilter? activeFilter,
    double? playbackSpeed,
    int? coverFrameMs,
    EditorMode? mode,
    bool? isExporting,
    double? exportProgress,
    String? exportedPath,
    String? sessionId,
  }) {
    return EditorModel(
      clips: clips ?? this.clips,
      textOverlays: textOverlays ?? this.textOverlays,
      stickerOverlays: stickerOverlays ?? this.stickerOverlays,
      backgroundAudio: backgroundAudio ?? this.backgroundAudio,
      voiceoverPath: voiceoverPath ?? this.voiceoverPath,
      originalVolume: originalVolume ?? this.originalVolume,
      backgroundVolume: backgroundVolume ?? this.backgroundVolume,
      activeFilter: activeFilter ?? this.activeFilter,
      playbackSpeed: playbackSpeed ?? this.playbackSpeed,
      coverFrameMs: coverFrameMs ?? this.coverFrameMs,
      mode: mode ?? this.mode,
      isExporting: isExporting ?? this.isExporting,
      exportProgress: exportProgress ?? this.exportProgress,
      exportedPath: exportedPath ?? this.exportedPath,
      sessionId: sessionId ?? this.sessionId,
    );
  }

  static EditorModel empty({EditorMode mode = EditorMode.flick}) => EditorModel(mode: mode);
}

// Filter color matrices for ColorFiltered widget preview
class VideoFilterMatrix {
  static const List<double> original = [1,0,0,0,0, 0,1,0,0,0, 0,0,1,0,0, 0,0,0,1,0];
  static const List<double> warm = [1.2,0,0,0,0.05, 0,1.0,0,0,0.02, 0,0,0.8,0,-0.05, 0,0,0,1,0];
  static const List<double> cool = [0.85,0,0,0,-0.03, 0,1.0,0,0,0.02, 0,0,1.2,0,0.08, 0,0,0,1,0];
  static const List<double> fade = [0.85,0,0,0,0.08, 0,0.85,0,0,0.08, 0,0,0.85,0,0.08, 0,0,0,1,0];
  static const List<double> vivid = [1.3,0,0,0,-0.15, 0,1.3,0,0,-0.15, 0,0,1.3,0,-0.15, 0,0,0,1,0];
  static const List<double> noir = [0.33,0.59,0.11,0,0, 0.33,0.59,0.11,0,0, 0.33,0.59,0.11,0,0, 0,0,0,1,0];
  static const List<double> golden = [1.2,0.1,0,0,0.05, 0.05,1.0,0,0,0.02, 0,0,0.7,0,-0.05, 0,0,0,1,0];
  static const List<double> matte = [0.9,0,0,0,0.05, 0,0.85,0,0,0.05, 0,0,0.8,0,0.05, 0,0,0,1,0];
  static const List<double> moody = [0.8,0,0,0,-0.05, 0,0.75,0,0,-0.05, 0,0,0.85,0,0, 0,0,0,1,0];
  static const List<double> fresh = [0.95,0,0,0,0, 0,1.05,0.05,0,0.02, 0,0,0.95,0,0.02, 0,0,0,1,0];
  static const List<double> drama = [1.4,0,0,0,-0.2, 0,1.1,0,0,-0.05, 0,0,1.2,0,-0.1, 0,0,0,1,0];
  static const List<double> retro = [1.1,0.1,0,0,0.03, 0.05,0.9,0.05,0,0.02, 0,0.1,0.8,0,0.02, 0,0,0,1,0];

  static List<double> forFilter(VideoFilter f) {
    switch (f) {
      case VideoFilter.warm: return warm;
      case VideoFilter.cool: return cool;
      case VideoFilter.fade: return fade;
      case VideoFilter.vivid: return vivid;
      case VideoFilter.noir: return noir;
      case VideoFilter.golden: return golden;
      case VideoFilter.matte: return matte;
      case VideoFilter.moody: return moody;
      case VideoFilter.fresh: return fresh;
      case VideoFilter.drama: return drama;
      case VideoFilter.retro: return retro;
      case VideoFilter.original: return original;
    }
  }
}
