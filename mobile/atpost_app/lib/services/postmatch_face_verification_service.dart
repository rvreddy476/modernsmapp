import 'package:face_verification/face_verification.dart';
import 'package:google_mlkit_face_detection/google_mlkit_face_detection.dart';
import 'package:image_picker/image_picker.dart';

enum PostMatchFaceVerificationStatus {
  matched,
  noFace,
  multiFace,
  mismatch,
  noUsablePhotos,
}

class PostMatchFaceVerificationResult {
  const PostMatchFaceVerificationResult({
    required this.status,
    required this.comparablePhotoCount,
    required this.skippedPhotoCount,
  });

  final PostMatchFaceVerificationStatus status;
  final int comparablePhotoCount;
  final int skippedPhotoCount;

  bool get isMatch => status == PostMatchFaceVerificationStatus.matched;
}

class PostMatchFaceVerificationService {
  static const _verificationUserId = '__postmatch_onboarding__';
  static const _matchThreshold = 0.60;

  bool _initialized = false;

  Future<PostMatchFaceVerificationResult> verify({
    required List<XFile> photos,
    required XFile selfie,
  }) async {
    await _ensureInitialized();

    final detector = FaceDetector(
      options: FaceDetectorOptions(
        performanceMode: FaceDetectorMode.accurate,
        enableContours: false,
        enableLandmarks: false,
        enableClassification: false,
      ),
    );

    try {
      final selfieFaceCount = await _detectFaceCount(detector, selfie.path);
      if (selfieFaceCount == 0) {
        return const PostMatchFaceVerificationResult(
          status: PostMatchFaceVerificationStatus.noFace,
          comparablePhotoCount: 0,
          skippedPhotoCount: 0,
        );
      }
      if (selfieFaceCount > 1) {
        return const PostMatchFaceVerificationResult(
          status: PostMatchFaceVerificationStatus.multiFace,
          comparablePhotoCount: 0,
          skippedPhotoCount: 0,
        );
      }

      await _clearVerificationFaces();

      var comparablePhotoCount = 0;
      var skippedPhotoCount = 0;

      for (var index = 0; index < photos.length; index++) {
        final photo = photos[index];
        final faceCount = await _detectFaceCount(detector, photo.path);
        if (faceCount != 1) {
          skippedPhotoCount++;
          continue;
        }

        try {
          await FaceVerification.instance.registerFromImagePath(
            id: _verificationUserId,
            imagePath: photo.path,
            imageId: 'photo_$index',
            replace: false,
          );
          comparablePhotoCount++;
        } catch (_) {
          skippedPhotoCount++;
        }
      }

      if (comparablePhotoCount == 0) {
        return PostMatchFaceVerificationResult(
          status: PostMatchFaceVerificationStatus.noUsablePhotos,
          comparablePhotoCount: comparablePhotoCount,
          skippedPhotoCount: skippedPhotoCount,
        );
      }

      final matchId = await FaceVerification.instance
          .verifyFromImagePathIsolate(
            imagePath: selfie.path,
            threshold: _matchThreshold,
            staffId: _verificationUserId,
          );

      return PostMatchFaceVerificationResult(
        status: matchId == _verificationUserId
            ? PostMatchFaceVerificationStatus.matched
            : PostMatchFaceVerificationStatus.mismatch,
        comparablePhotoCount: comparablePhotoCount,
        skippedPhotoCount: skippedPhotoCount,
      );
    } finally {
      await detector.close();
      await _clearVerificationFaces();
    }
  }

  Future<void> dispose() async {
    await _clearVerificationFaces();
    if (!_initialized) return;
    await FaceVerification.instance.dispose();
    _initialized = false;
  }

  Future<void> _ensureInitialized() async {
    if (_initialized) return;
    await FaceVerification.instance.init();
    _initialized = true;
  }

  Future<void> _clearVerificationFaces() async {
    if (!_initialized) return;
    try {
      await FaceVerification.instance.deleteUserFaces(_verificationUserId);
    } catch (_) {}
  }

  Future<int> _detectFaceCount(FaceDetector detector, String imagePath) async {
    final image = InputImage.fromFilePath(imagePath);
    final faces = await detector.processImage(image);
    return faces.length;
  }
}
