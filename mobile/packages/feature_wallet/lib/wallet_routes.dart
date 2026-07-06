import 'package:feature_wallet/wallet/wallet_aadhaar_verification_screen.dart';
import 'package:feature_wallet/wallet/wallet_home_screen.dart';
import 'package:feature_wallet/wallet/wallet_kyc_screen.dart';
import 'package:feature_wallet/wallet/wallet_send_screen.dart';
import 'package:feature_wallet/wallet/wallet_top_up_screen.dart';
import 'package:feature_wallet/wallet/wallet_transaction_detail_screen.dart';
import 'package:feature_wallet/wallet/wallet_transactions_screen.dart';
import 'package:go_router/go_router.dart';

/// Wallet route table (Phase 2 Sprint 1 — consumer wallet, BC of the
/// partner-bank PPI). The app router spreads this into its shell.
List<RouteBase> walletRoutes() => [
  GoRoute(
    path: '/wallet',
    builder: (_, _) => const WalletHomeScreen(),
  ),
  GoRoute(
    path: '/wallet/top-up',
    builder: (_, _) => const WalletTopUpScreen(),
  ),
  GoRoute(
    path: '/wallet/send',
    builder: (context, state) {
      final extra = state.extra is Map
          ? Map<String, dynamic>.from(state.extra as Map)
          : null;
      return WalletSendScreen(preset: extra);
    },
  ),
  GoRoute(
    path: '/wallet/transactions',
    builder: (_, _) => const WalletTransactionsScreen(),
  ),
  GoRoute(
    path: '/wallet/transactions/:id',
    builder: (context, state) => WalletTransactionDetailScreen(
      transactionId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/wallet/kyc',
    builder: (_, _) => const WalletKycScreen(),
  ),
  GoRoute(
    path: '/wallet/kyc/aadhaar',
    builder: (_, _) => const WalletAadhaarVerificationScreen(),
  ),
  GoRoute(
    path: '/wallet/kyc/aadhaar/callback',
    builder: (context, state) => WalletAadhaarVerificationScreen(
      incomingCode: state.uri.queryParameters['code'],
      incomingState: state.uri.queryParameters['state'],
    ),
  ),
];
