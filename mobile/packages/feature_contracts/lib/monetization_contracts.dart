import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Open the creator subscription tier picker for [creatorId]; returns the
/// chosen tier id on success, or null if dismissed. Injected as a callback
/// so a surface (e.g. a profile's Subscribe button) can offer subscriptions
/// without importing the monetization feature. Default: null.
typedef AppShowTierPicker = Future<String?> Function(
  BuildContext context, {
  required String creatorId,
  String? creatorName,
});
final appShowTierPickerProvider = Provider<AppShowTierPicker>(
  (_) => (_, {required creatorId, creatorName}) async => null,
);

/// Open the tip sheet for [creatorId] (optionally attributed to [postId]).
/// The result is not surfaced to the caller. Default: no-op.
typedef AppShowTip = Future<void> Function(
  BuildContext context, {
  required String creatorId,
  String? creatorName,
  String? postId,
});
final appShowTipProvider = Provider<AppShowTip>(
  (_) => (_, {required creatorId, creatorName, postId}) async {},
);
