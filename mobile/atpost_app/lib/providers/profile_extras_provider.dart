import 'package:atpost_app/data/models/profile_extras.dart';
import 'package:atpost_app/data/repositories/profile_extras_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final myPinsProvider =
    FutureProvider.autoDispose<List<ProfilePin>>((ref) async {
  return ref.watch(profileExtrasRepositoryProvider).getMyPins();
});

final userPinsProvider = FutureProvider.autoDispose
    .family<List<ProfilePin>, String>((ref, userId) async {
  return ref.watch(profileExtrasRepositoryProvider).getUserPins(userId);
});

final myPortfolioProvider =
    FutureProvider.autoDispose<List<PortfolioItem>>((ref) async {
  return ref.watch(profileExtrasRepositoryProvider).getMyPortfolio();
});

final userPortfolioProvider = FutureProvider.autoDispose
    .family<List<PortfolioItem>, String>((ref, userId) async {
  return ref.watch(profileExtrasRepositoryProvider).getUserPortfolio(userId);
});

final myQrCodeProvider =
    FutureProvider.autoDispose<ProfileQrCode>((ref) async {
  return ref.watch(profileExtrasRepositoryProvider).getMyQrCode();
});
