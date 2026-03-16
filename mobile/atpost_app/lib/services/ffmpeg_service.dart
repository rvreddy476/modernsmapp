import 'dart:io';
import 'package:atpost_app/core/utils/app_logger.dart';

/// Wrapper around ffmpeg_kit_flutter for video processing operations.
/// All methods return the output file path on success, throw on failure.
class FfmpegService {
  /// Trim a video clip.
  /// [input] — source file path
  /// [outputPath] — destination file path
  /// [startMs] — trim start in milliseconds
  /// [durationMs] — duration in milliseconds
  static Future<String> trimClip({
    required String input,
    required String outputPath,
    required int startMs,
    required int durationMs,
  }) async {
    final startSec = (startMs / 1000.0).toStringAsFixed(3);
    final durSec = (durationMs / 1000.0).toStringAsFixed(3);
    final cmd = '-i "$input" -ss $startSec -t $durSec -c:v copy -c:a copy "$outputPath"';
    await _execute(cmd);
    return outputPath;
  }

  /// Apply speed to a clip.
  static Future<String> applySpeed({
    required String input,
    required String outputPath,
    required double speed,
  }) async {
    final pts = (1 / speed).toStringAsFixed(3);
    String audioFilter;
    if (speed <= 2.0) {
      audioFilter = 'atempo=${speed.toStringAsFixed(2)}';
    } else {
      final f2 = (speed / 2.0).toStringAsFixed(2);
      audioFilter = 'atempo=2.0,atempo=$f2';
    }
    final cmd = '-i "$input" -vf "setpts=$pts*PTS" -af "$audioFilter" "$outputPath"';
    await _execute(cmd);
    return outputPath;
  }

  /// Concatenate multiple clips into one.
  static Future<String> concatenateClips({
    required List<String> inputPaths,
    required String outputPath,
  }) async {
    if (inputPaths.length == 1) {
      await File(inputPaths.first).copy(outputPath);
      return outputPath;
    }
    // Build filter_complex concat
    final inputs = inputPaths.map((p) => '-i "$p"').join(' ');
    final vInputs = List.generate(inputPaths.length, (i) => '[$i:v]').join('');
    final aInputs = List.generate(inputPaths.length, (i) => '[$i:a]').join('');
    final n = inputPaths.length;
    final cmd = '$inputs -filter_complex "${vInputs}concat=n=$n:v=1:a=0[vout];${aInputs}concat=n=$n:v=0:a=1[aout]" -map "[vout]" -map "[aout]" "$outputPath"';
    await _execute(cmd);
    return outputPath;
  }

  /// Mix audio tracks with volumes.
  static Future<String> mixAudio({
    required String videoPath,
    required String outputPath,
    double originalVolume = 1.0,
    String? backgroundAudioPath,
    double backgroundVolume = 0.7,
    String? voiceoverPath,
  }) async {
    if (backgroundAudioPath == null && voiceoverPath == null) {
      await File(videoPath).copy(outputPath);
      return outputPath;
    }

    final inputs = StringBuffer('-i "$videoPath"');
    final filters = StringBuffer('[0:a]volume=${originalVolume.toStringAsFixed(2)}[a0]');
    int inputCount = 1;

    if (backgroundAudioPath != null) {
      inputs.write(' -i "$backgroundAudioPath"');
      filters.write(';[$inputCount:a]volume=${backgroundVolume.toStringAsFixed(2)}[a$inputCount]');
      inputCount++;
    }
    if (voiceoverPath != null) {
      inputs.write(' -i "$voiceoverPath"');
      filters.write(';[$inputCount:a]volume=1.0[a$inputCount]');
      inputCount++;
    }

    final mixInputs = List.generate(inputCount, (i) => '[a$i]').join('');
    filters.write(';${mixInputs}amix=inputs=$inputCount:duration=first[aout]');

    final cmd = '${inputs.toString()} -filter_complex "${filters.toString()}" -map 0:v -map "[aout]" -c:v copy -c:a aac -b:a 128k "$outputPath"';
    await _execute(cmd);
    return outputPath;
  }

  /// Final encode: H.264, AAC, 720p max.
  static Future<String> finalEncode({
    required String input,
    required String outputPath,
  }) async {
    const cmdTemplate = '-c:v libx264 -preset fast -crf 23 -vf "scale=trunc(min(iw\\,1280)/2)*2:-2" -c:a aac -b:a 128k -movflags +faststart';
    final cmd = '-i "$input" $cmdTemplate "$outputPath"';
    await _execute(cmd);
    return outputPath;
  }

  static Future<void> _execute(String cmd) async {
    AppLogger.debug('FFmpeg: $cmd');
    try {
      // ignore: avoid_dynamic_calls
      final rc = await _ffmpegExecute(cmd);
      if (rc != 0) throw Exception('FFmpeg failed with code $rc');
    } catch (e) {
      AppLogger.error('FFmpeg error', error: e);
      rethrow;
    }
  }

  // Isolate the actual ffmpeg_kit call so the rest of the file compiles
  // even before the package is added to pubspec.
  static Future<int> _ffmpegExecute(String cmd) async {
    // Will be replaced with:
    // final session = await FFmpegKit.execute(cmd);
    // return (await session.getReturnCode())?.getValue() ?? -1;
    // For now: stub that always succeeds.
    AppLogger.debug('FFmpeg stub — command not executed: $cmd');
    return 0;
  }
}
