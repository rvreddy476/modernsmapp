package com.us.android.feature.rider.root

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.deeplink.RiderDeepLink
import com.us.android.feature.rider.digilocker.DigiLockerScreen
import com.us.android.feature.rider.documents.DocumentsScreen
import com.us.android.feature.rider.earnings.EarningsScreen
import com.us.android.feature.rider.home.HomeScreen
import com.us.android.feature.rider.job.ActiveJobScreen
import com.us.android.feature.rider.navigation.ActiveJobRoute
import com.us.android.feature.rider.navigation.DigiLockerRoute
import com.us.android.feature.rider.navigation.DocumentsRoute
import com.us.android.feature.rider.navigation.EarningsRoute
import com.us.android.feature.rider.navigation.HomeRoute
import com.us.android.feature.rider.navigation.OfferRoute
import com.us.android.feature.rider.navigation.PayoutRoute
import com.us.android.feature.rider.navigation.ProfileRoute
import com.us.android.feature.rider.navigation.SelfieRoute
import com.us.android.feature.rider.navigation.VerificationRoute
import com.us.android.feature.rider.navigation.stepRoute
import com.us.android.feature.rider.offers.OfferScreen
import com.us.android.feature.rider.onboarding.VerificationScreen
import com.us.android.feature.rider.payout.PayoutScreen
import com.us.android.feature.rider.profile.ProfileScreen
import com.us.android.feature.rider.selfie.SelfieScreen
import com.us.android.feature.rider.ui.MessagePane

/**
 * Feast Rider's signed-in entry point. :app-rider shows this once a session
 * exists; everything behind it is gated by [RiderRootViewModel].
 */
@Composable
fun RiderRoot(viewModel: RiderRootViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    when (val current = state) {
        RiderRootState.Loading -> RiderSplash()
        RiderRootState.NotRider -> UsScaffold { padding ->
            MessagePane(
                title = "Deliver with Feast",
                body = "Feast Rider is for delivery partners. This account isn't one yet. " +
                    "Tell us about you and your vehicle to start — then verify your documents and Feast reviews them. " +
                    "To order food, use the Momentum app.",
                icon = UsIcons.MapPin,
                primaryLabel = "Become a rider",
                onPrimary = viewModel::becomeRider,
                secondaryLabel = "Sign out",
                onSecondary = viewModel::signOut,
                modifier = Modifier.padding(padding),
            )
        }
        RiderRootState.BecomingRider -> ProfileScreen(onBack = viewModel::cancelBecomingRider, onSaved = viewModel::load)
        RiderRootState.SessionExpired -> {
            LaunchedEffect(Unit) { viewModel.signOut() }
            RiderSplash()
        }
        is RiderRootState.Unavailable -> UsScaffold { padding ->
            MessagePane(
                title = "Couldn't open Feast Rider",
                body = current.message,
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
                secondaryLabel = "Sign out",
                onSecondary = viewModel::signOut,
                modifier = Modifier.padding(padding),
            )
        }
        is RiderRootState.Ready -> RiderNavHost(ready = current, viewModel = viewModel)
    }
}

/** The brand mark while the gate decides. Shared with :app-rider's pre-session splash. */
@Composable
fun RiderSplash() {
    UsScaffold { _ ->
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(text = "Feast", fontFamily = MomentumWordmarkFontFamily, fontSize = 44.sp, color = UsTheme.extended.textPrimary)
            Text(
                text = "RIDER",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.accentSolid,
            )
        }
    }
}

@Composable
private fun RiderNavHost(ready: RiderRootState.Ready, viewModel: RiderRootViewModel) {
    val navController = rememberNavController()
    val start: Any = remember { if (ready.verified) HomeRoute(ready.userId) else VerificationRoute }
    val back: () -> Unit = { navController.popBackStack() }
    val home: () -> Unit = {
        navController.navigate(HomeRoute(ready.userId)) {
            popUpTo(navController.graph.id) { inclusive = true }
            launchSingleTop = true
        }
    }

    // Links: an offer opens its screen; a DigiLocker return opens verification,
    // whose ViewModel consumes the link itself.
    LaunchedEffect(navController) {
        viewModel.links.collect { link ->
            when (link) {
                is RiderDeepLink.Offer -> {
                    navController.navigate(OfferRoute(link.offerId)) { launchSingleTop = true }
                    viewModel.linkHandled()
                }
                is RiderDeepLink.DigiLocker -> navController.navigate(DigiLockerRoute) { launchSingleTop = true }
            }
        }
    }

    NavHost(navController = navController, startDestination = start) {
        composable<HomeRoute> {
            HomeScreen(
                onOpenOffer = { navController.navigate(OfferRoute(it)) },
                onOpenJob = { navController.navigate(ActiveJobRoute) },
                onOpenVerification = { navController.navigate(VerificationRoute) },
                onOpenEarnings = { navController.navigate(EarningsRoute) },
                onSignOut = viewModel::signOut,
            )
        }
        composable<VerificationRoute> {
            VerificationScreen(
                onBack = if (navController.previousBackStackEntry != null) back else null,
                onOpenStep = { step -> navController.navigate(stepRoute(step)) },
                onOpenHome = home,
                onSignOut = viewModel::signOut,
            )
        }
        composable<ProfileRoute> { ProfileScreen(onBack = back, onSaved = back) }
        composable<DigiLockerRoute> { DigiLockerScreen(onBack = back) }
        composable<DocumentsRoute> { DocumentsScreen(onBack = back) }
        composable<SelfieRoute> { SelfieScreen(onBack = back) }
        composable<PayoutRoute> { PayoutScreen(onBack = back) }
        composable<OfferRoute> {
            OfferScreen(
                onBack = back,
                onAccepted = {
                    navController.navigate(ActiveJobRoute) {
                        popUpTo<OfferRoute> { inclusive = true }
                    }
                },
            )
        }
        composable<ActiveJobRoute> { ActiveJobScreen(onBack = back, onFinished = back) }
        composable<EarningsRoute> { EarningsScreen(onBack = back) }
    }
}
