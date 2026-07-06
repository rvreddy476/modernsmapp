import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:feature_wallet/host/wallet_host.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// App-side implementation of feature_wallet's host contract — AtPost
/// user search for the "send money" picker. The ONLY place the app's
/// user repository meets the wallet feature.
List<Override> walletHostBindings() => [
  walletUserSearchProvider.overrideWith((ref) {
    return (String query) async {
      final result =
          await ref.read(userRepositoryProvider).searchUsers(query, limit: 20);
      return result.users
          .map((u) => WalletHostUser(
                id: u.id,
                displayName: u.displayName,
                username: u.username,
                avatarUrl: u.hasAvatar ? u.avatarUrl : null,
              ))
          .toList();
    };
  }),
];
