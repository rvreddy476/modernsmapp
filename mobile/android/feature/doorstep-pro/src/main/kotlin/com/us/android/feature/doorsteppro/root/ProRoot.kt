package com.us.android.feature.doorsteppro.root

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.account.AccountScreen
import com.us.android.feature.doorsteppro.apply.ApplyScreen
import com.us.android.feature.doorsteppro.chat.ChatScreen
import com.us.android.feature.doorsteppro.deeplink.ProDeepLink
import com.us.android.feature.doorsteppro.earnings.EarningsScreen
import com.us.android.feature.doorsteppro.home.HomeScreen
import com.us.android.feature.doorsteppro.job.JobScreen
import com.us.android.feature.doorsteppro.jobs.JobsScreen
import com.us.android.feature.doorsteppro.navigation.AccountRoute
import com.us.android.feature.doorsteppro.navigation.AgreementRoute
import com.us.android.feature.doorsteppro.navigation.AreaRoute
import com.us.android.feature.doorsteppro.navigation.BankRoute
import com.us.android.feature.doorsteppro.navigation.ChatRoute
import com.us.android.feature.doorsteppro.navigation.DigiLockerRoute
import com.us.android.feature.doorsteppro.navigation.EarningsRoute
import com.us.android.feature.doorsteppro.navigation.HomeRoute
import com.us.android.feature.doorsteppro.navigation.HoursRoute
import com.us.android.feature.doorsteppro.navigation.JobRoute
import com.us.android.feature.doorsteppro.navigation.JobsRoute
import com.us.android.feature.doorsteppro.navigation.OfferRoute
import com.us.android.feature.doorsteppro.navigation.OnboardingRoute
import com.us.android.feature.doorsteppro.navigation.PanRoute
import com.us.android.feature.doorsteppro.navigation.PoliceCertificateRoute
import com.us.android.feature.doorsteppro.navigation.ProfileStepRoute
import com.us.android.feature.doorsteppro.navigation.SelfieRoute
import com.us.android.feature.doorsteppro.navigation.SkillsRoute
import com.us.android.feature.doorsteppro.navigation.stepRoute
import com.us.android.feature.doorsteppro.offers.OfferScreen
import com.us.android.feature.doorsteppro.onboarding.AgreementStepScreen
import com.us.android.feature.doorsteppro.onboarding.AreaStepScreen
import com.us.android.feature.doorsteppro.onboarding.BankStepScreen
import com.us.android.feature.doorsteppro.onboarding.DigiLockerStepScreen
import com.us.android.feature.doorsteppro.onboarding.HoursStepScreen
import com.us.android.feature.doorsteppro.onboarding.OnboardingScreen
import com.us.android.feature.doorsteppro.onboarding.PanStepScreen
import com.us.android.feature.doorsteppro.onboarding.PoliceCertificateStepScreen
import com.us.android.feature.doorsteppro.onboarding.ProfileStepScreen
import com.us.android.feature.doorsteppro.onboarding.SelfieStepScreen
import com.us.android.feature.doorsteppro.onboarding.SkillsStepScreen
import com.us.android.feature.doorsteppro.ui.MessagePane

/**
 * Doorstep Pro's signed-in entry point. :app-doorstep-pro shows this once a
 * session exists; everything behind it is gated by [ProRootViewModel].
 */
@Composable
fun ProRoot(
    viewModel: ProRootViewModel = hiltViewModel(),
    onProfessionalChanged: (com.us.android.feature.doorsteppro.data.ProfessionalDto) -> Unit = {},
    incomingPopups: Boolean = true,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val refreshing by viewModel.refreshing.collectAsStateWithLifecycle()
    val error by viewModel.refreshError.collectAsStateWithLifecycle()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LifecycleResumeEffect(Unit) { viewModel.refreshStatus(); onPauseOrDispose { } }
    // Silent reconciliation only on the review receipt, never a timed navigation reset.
    LaunchedEffect(state is ProRootState.Submitted, lifecycle) {
        if (state is ProRootState.Submitted) lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) { delay(30_000); viewModel.refreshStatus() }
        }
    }
    val professional = when (val s = state) {
        is ProRootState.Ready -> s.professional
        is ProRootState.Submitted -> s.professional
        else -> null
    }
    LaunchedEffect(professional) { professional?.let(onProfessionalChanged) }
    Column(Modifier.fillMaxSize()) {
      error?.let { Text(it, color = UsTheme.extended.statusDanger, modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l)) }
      Box(Modifier.weight(1f)) {
    when (val current = state) {
        ProRootState.Loading -> ProSplash()
        ProRootState.NotApplied -> UsScaffold { padding ->
            MessagePane(
                title = "Work with Doorstep",
                body = "Doorstep Pro is for home-service professionals — cleaning, repairs, painting, pest control and salon at home. " +
                    "Apply, verify your identity and documents, and Doorstep sends you jobs near you. " +
                    "To book a service, use the Momentum app.",
                icon = UsIcons.Wrench,
                primaryLabel = "Apply",
                onPrimary = viewModel::startApplying,
                secondaryLabel = "Sign out",
                onSecondary = viewModel::signOut,
                modifier = Modifier.padding(padding),
            )
        }
        ProRootState.Applying -> ApplyScreen(onBack = viewModel::cancelApplying, onApplied = viewModel::load)
        is ProRootState.Submitted -> com.us.android.feature.doorsteppro.apply.ApplicationReceiptScreen(
            professional = current.professional, refreshing = refreshing,
            onRefresh = viewModel::refreshStatus,
        )
        ProRootState.SessionExpired -> {
            LaunchedEffect(Unit) { viewModel.signOut() }
            ProSplash()
        }
        is ProRootState.Unavailable -> UsScaffold { padding ->
            MessagePane(
                title = current.title,
                body = current.message,
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
                secondaryLabel = "Sign out",
                onSecondary = viewModel::signOut,
                modifier = Modifier.padding(padding),
            )
        }
        is ProRootState.Ready -> ProNavHost(ready = current, viewModel = viewModel, incomingPopups = incomingPopups)
    }
      }
    }
}

/** The brand mark while the gate decides. Shared with :app-doorstep-pro's pre-session splash. */
@Composable
fun ProSplash() {
    UsScaffold { _ ->
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(text = "Doorstep", fontFamily = MomentumWordmarkFontFamily, fontSize = 44.sp, color = UsTheme.extended.textPrimary)
            Text(text = "PRO", style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.accentSolid)
        }
    }
}

@Composable
@Suppress("LongMethod")
private fun ProNavHost(ready: ProRootState.Ready, viewModel: ProRootViewModel, incomingPopups: Boolean) {
    val navController = rememberNavController()
    val requests: com.us.android.feature.doorsteppro.offers.IncomingRequestsViewModel = hiltViewModel(key = "incoming-requests-${ready.professional.userId}")
    val requestState by requests.state.collectAsStateWithLifecycle()
    val start: Any = remember { if (ready.onboarded) HomeRoute else OnboardingRoute }
    val back: () -> Unit = { if (!navController.popBackStack()) navController.navigate(start) }
    val home: () -> Unit = {
        navController.navigate(HomeRoute) {
            popUpTo(navController.graph.id) { inclusive = true }
            launchSingleTop = true
        }
    }

    LaunchedEffect(navController) {
        viewModel.links.collect { link ->
            route(navController, link)
            // DigiLocker returns are consumed by their own screen's ViewModel.
            if (link !is ProDeepLink.DigiLocker) viewModel.linkHandled()
        }
    }

    com.us.android.feature.doorsteppro.offers.IncomingRequestsHost(
        userId = ready.professional.userId,
        vm = requests,
        onAccepted = { navController.navigate(JobRoute(it)) { launchSingleTop = true } },
        showPopup = incomingPopups,
    )

    NavHost(navController = navController, startDestination = start) {
        composable<HomeRoute> {
            HomeScreen(
                requests = requestState.offers,
                onOpenOffer = { navController.navigate(OfferRoute(it)) },
                onOpenJob = { navController.navigate(JobRoute(it)) },
                onOpenJobs = { navController.navigate(JobsRoute) },
                onOpenChecklist = { navController.navigate(OnboardingRoute) { launchSingleTop = true } },
                onOpenEarnings = { navController.navigate(EarningsRoute) },
                onOpenAccount = { navController.navigate(AccountRoute) },
            )
        }
        composable<OnboardingRoute> {
            OnboardingScreen(
                onBack = if (navController.previousBackStackEntry != null) back else null,
                onOpenStep = { step -> navController.navigate(stepRoute(step)) },
                onOpenHome = home,
                onOpenAccount = { navController.navigate(AccountRoute) },
            )
        }
        composable<ProfileStepRoute> { ProfileStepScreen(onBack = back) }
        composable<DigiLockerRoute> { com.us.android.feature.doorsteppro.onboarding.ManualIdentityStepScreen(onBack = back) }
        composable<SelfieRoute> { SelfieStepScreen(onBack = back) }
        composable<SkillsRoute> { SkillsStepScreen(onBack = back) }
        composable<AreaRoute> { AreaStepScreen(onBack = back) }
        composable<HoursRoute> { HoursStepScreen(onBack = back) }
        composable<BankRoute> { BankStepScreen(onBack = back) }
        composable<PoliceCertificateRoute> { PoliceCertificateStepScreen(onBack = back) }
        composable<AgreementRoute> { AgreementStepScreen(onBack = back) }
        composable<PanRoute> { PanStepScreen(onBack = back) }
        composable<OfferRoute> {
            OfferScreen(
                onBack = back,
                onAccepted = { bookingId ->
                    navController.navigate(JobRoute(bookingId)) { popUpTo<OfferRoute> { inclusive = true } }
                },
            )
        }
        composable<JobsRoute> { JobsScreen(onBack = back, onOpenJob = { navController.navigate(JobRoute(it)) }) }
        composable<JobRoute> { JobScreen(onBack = back, onOpenChat = { navController.navigate(ChatRoute(it)) }) }
        composable<ChatRoute> { ChatScreen(onBack = back) }
        composable<EarningsRoute> { EarningsScreen(onBack = back) }
        composable<AccountRoute> {
            AccountScreen(
                onBack = back,
                onOpenStep = { step -> navController.navigate(stepRoute(step)) },
                onOpenChecklist = { navController.navigate(OnboardingRoute) { launchSingleTop = true } },
                onSignOut = viewModel::signOut,
            )
        }
    }
}

/** Where each link lands. Pure navigation; the screens re-read the server. */
private fun route(navController: NavHostController, link: ProDeepLink) {
    when (link) {
        ProDeepLink.Home, ProDeepLink.Offers -> navController.navigate(HomeRoute) { launchSingleTop = true }
        is ProDeepLink.Offer -> navController.navigate(OfferRoute(link.offerId)) { launchSingleTop = true }
        is ProDeepLink.Job -> navController.navigate(JobRoute(link.bookingId)) { launchSingleTop = true }
        is ProDeepLink.JobChat -> navController.navigate(ChatRoute(link.bookingId)) { launchSingleTop = true }
        ProDeepLink.Onboarding -> navController.navigate(OnboardingRoute) { launchSingleTop = true }
        ProDeepLink.PoliceCertificate -> navController.navigate(PoliceCertificateRoute) { launchSingleTop = true }
        ProDeepLink.Account -> navController.navigate(AccountRoute) { launchSingleTop = true }
        ProDeepLink.Earnings -> navController.navigate(EarningsRoute) { launchSingleTop = true }
        is ProDeepLink.DigiLocker -> navController.navigate(DigiLockerRoute) { launchSingleTop = true }
    }
}
