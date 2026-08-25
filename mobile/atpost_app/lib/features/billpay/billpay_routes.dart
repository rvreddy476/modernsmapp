import 'package:atpost_app/features/billpay/billpay_account_detail_screen.dart';
import 'package:atpost_app/features/billpay/billpay_add_account_screen.dart';
import 'package:atpost_app/features/billpay/billpay_category_screen.dart';
import 'package:atpost_app/features/billpay/billpay_home_screen.dart';
import 'package:atpost_app/features/billpay/billpay_payments_screen.dart';
import 'package:atpost_app/features/billpay/billpay_receipt_screen.dart';
import 'package:atpost_app/features/billpay/billpay_recharge_screen.dart';
import 'package:atpost_app/features/billpay/billpay_reminders_screen.dart';
import 'package:atpost_app/features/billpay/billpay_scheduled_screen.dart';
import 'package:atpost_app/features/figo/figo_home_screen.dart';
import 'package:atpost_app/features/figo/figo_rewards_screen.dart';
import 'package:atpost_app/features/wallet/wallet_aadhaar_verification_screen.dart';
import 'package:atpost_app/features/wallet/wallet_home_screen.dart';
import 'package:atpost_app/features/wallet/wallet_kyc_screen.dart';
import 'package:atpost_app/features/wallet/wallet_send_screen.dart';
import 'package:atpost_app/features/wallet/wallet_top_up_screen.dart';
import 'package:atpost_app/features/wallet/wallet_transaction_detail_screen.dart';
import 'package:atpost_app/features/wallet/wallet_transactions_screen.dart';
import 'package:go_router/go_router.dart';

class BillPayRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/figo',
          builder: (context, state) => const FigoHomeScreen(),
        ),
        GoRoute(
          path: '/figo/rewards',
          builder: (context, state) => const FigoRewardsScreen(),
        ),
        GoRoute(path: '/wallet', builder: (_, _) => const WalletHomeScreen()),
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
        GoRoute(
          path: '/billpay',
          builder: (_, _) => const BillPayHomeScreen(),
        ),
        GoRoute(
          path: '/billpay/category/:id',
          builder: (context, state) =>
              BillPayCategoryScreen(categoryId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/billpay/add-account',
          builder: (context, state) => BillPayAddAccountScreen(
            providerId:
                state.uri.queryParameters['providerId'] ??
                state.uri.queryParameters['provider'] ??
                '',
          ),
        ),
        GoRoute(
          path: '/billpay/account/:id',
          builder: (context, state) => BillPayAccountDetailScreen(
            accountId: state.pathParameters['id']!,
          ),
        ),
        GoRoute(
          path: '/billpay/recharge',
          builder: (_, _) => const BillPayRechargeScreen(),
        ),
        GoRoute(
          path: '/billpay/payments',
          builder: (_, _) => const BillPayPaymentsScreen(),
        ),
        GoRoute(
          path: '/billpay/payments/:id',
          builder: (context, state) =>
              BillPayReceiptScreen(paymentId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/billpay/reminders',
          builder: (_, _) => const BillPayRemindersScreen(),
        ),
        GoRoute(
          path: '/billpay/scheduled',
          builder: (_, _) => const BillPayScheduledScreen(),
        ),
      ];
}
