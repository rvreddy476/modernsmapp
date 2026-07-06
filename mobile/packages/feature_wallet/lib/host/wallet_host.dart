import 'package:flutter_riverpod/flutter_riverpod.dart';

/// What feature_wallet needs from its host app — the AtPost user search
/// used by the "send money to an AtPost user" picker. The host binds
/// [walletUserSearchProvider] at its root ProviderScope; the feature
/// never imports the app's User model or user repository directly.

/// Minimal projection of an AtPost user for the send-money picker.
class WalletHostUser {
  const WalletHostUser({
    required this.id,
    required this.displayName,
    this.username = '',
    this.avatarUrl,
  });

  final String id;
  final String displayName;
  final String username;
  final String? avatarUrl;

  bool get hasAvatar => (avatarUrl ?? '').isNotEmpty;
}

/// Search AtPost users by query. Defaults to no results so the wallet
/// screens still work (empty picker) without host wiring.
typedef WalletUserSearch = Future<List<WalletHostUser>> Function(String query);
final walletUserSearchProvider =
    Provider<WalletUserSearch>((_) => (_) async => const []);
