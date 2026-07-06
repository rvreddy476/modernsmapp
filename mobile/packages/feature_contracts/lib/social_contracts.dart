import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Host-owned relationship actions on *another* user, invoked from the
/// feed / video surfaces (follow toggle in a post header, "don't
/// recommend this channel" mute). Features depend on this instead of the
/// app's UserRepository so the post-rendering widgets stay decoupled from
/// the app. The app binds a real implementation at its root
/// ProviderScope; the default is a no-op so widgets render in isolation.
abstract interface class AppUserActions {
  Future<void> follow(String userId);
  Future<void> unfollow(String userId);
  Future<void> mute(String userId);
}

class _NoopUserActions implements AppUserActions {
  const _NoopUserActions();
  @override
  Future<void> follow(String userId) async {}
  @override
  Future<void> unfollow(String userId) async {}
  @override
  Future<void> mute(String userId) async {}
}

final appUserActionsProvider =
    Provider<AppUserActions>((_) => const _NoopUserActions());

/// Tapping a hashtag inside a post body. The host decides where it goes —
/// on the home shell it switches to the #Hashtag tab and selects the tag;
/// elsewhere it pushes `/hashtag/:tag`. Features fire this without knowing
/// the shell's tab state. Default is a no-op.
typedef AppHashtagTap = void Function(BuildContext context, String normalized);
final appHashtagTapProvider =
    Provider<AppHashtagTap>((_) => (_, _) {});

/// Builds the monetization paywall preview shown in place of a post body
/// the backend redacted for this viewer (tier-gated content). Injected as
/// a builder so the social UI never imports the monetization feature.
/// Default renders nothing (the body simply collapses).
typedef AppPaywallBuilder = Widget Function(
  BuildContext context, {
  required String creatorId,
  String? creatorName,
});
final appPaywallBuilderProvider = Provider<AppPaywallBuilder>(
  (_) => (_, {required creatorId, creatorName}) => const SizedBox.shrink(),
);
