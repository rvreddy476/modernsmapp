// FiGo rewards providers — loyalty balance + ledger, referral code,
// and a small notifier for the apply-code form.

import 'package:atpost_app/data/repositories/figo_rewards_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Loads {balance, ledger} on first watch. autoDispose so the
/// connection stops when the screen leaves view.
final foodLoyaltyProvider =
    FutureProvider.autoDispose<FoodLoyaltySnapshot>((ref) async {
  return ref.read(figoRewardsRepositoryProvider).getLoyalty();
});

/// Loads (or mints) the user's referral code.
final foodReferralCodeProvider =
    FutureProvider.autoDispose<String>((ref) async {
  return ref.read(figoRewardsRepositoryProvider).getReferralCode();
});

/// Apply-referral-code form state. `submit` returns the success flag
/// so the UI can flip into a "thanks" message without waiting on a
/// rebuild from the provider tree.
class FoodReferralApplyNotifier extends StateNotifier<AsyncValue<void>> {
  FoodReferralApplyNotifier(this._ref) : super(const AsyncData(null));

  final Ref _ref;

  Future<bool> submit(String code) async {
    state = const AsyncLoading();
    try {
      await _ref
          .read(figoRewardsRepositoryProvider)
          .applyReferralCode(code.trim());
      state = const AsyncData(null);
      return true;
    } catch (e, st) {
      state = AsyncError(e, st);
      return false;
    }
  }
}

final foodReferralApplyProvider =
    StateNotifierProvider<FoodReferralApplyNotifier, AsyncValue<void>>((ref) {
  return FoodReferralApplyNotifier(ref);
});

/// Loyalty-redeem form state.
class FoodLoyaltyRedeemNotifier extends StateNotifier<AsyncValue<void>> {
  FoodLoyaltyRedeemNotifier(this._ref) : super(const AsyncData(null));

  final Ref _ref;

  Future<bool> redeem(int points) async {
    state = const AsyncLoading();
    try {
      await _ref
          .read(figoRewardsRepositoryProvider)
          .redeemLoyalty(points: points);
      state = const AsyncData(null);
      // Refresh balance so the UI shows the post-redeem state.
      _ref.invalidate(foodLoyaltyProvider);
      return true;
    } catch (e, st) {
      state = AsyncError(e, st);
      return false;
    }
  }
}

final foodLoyaltyRedeemProvider =
    StateNotifierProvider<FoodLoyaltyRedeemNotifier, AsyncValue<void>>((ref) {
  return FoodLoyaltyRedeemNotifier(ref);
});
