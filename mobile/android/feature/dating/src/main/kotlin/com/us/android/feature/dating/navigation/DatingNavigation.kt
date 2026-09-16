// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph extension that uses them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.dating.navigation

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import androidx.navigation.toRoute
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.OnboardingStep
import com.us.android.feature.dating.home.DatingHomeScreen
import com.us.android.feature.dating.home.HomeTab
import com.us.android.feature.dating.home.MatchDetailScreen
import com.us.android.feature.dating.home.PersonScreen
import com.us.android.feature.dating.onboarding.DatingRootState
import com.us.android.feature.dating.onboarding.DatingRootViewModel
import com.us.android.feature.dating.onboarding.DraftStepScreen
import com.us.android.feature.dating.onboarding.PhotosScreen
import com.us.android.feature.dating.onboarding.PromptsScreen
import com.us.android.feature.dating.onboarding.StatusPane
import com.us.android.feature.dating.premium.DatingPaymentRequest
import com.us.android.feature.dating.premium.PremiumScreen
import com.us.android.feature.dating.privacy.BlocksScreen
import com.us.android.feature.dating.privacy.PrivacyScreen
import com.us.android.feature.dating.safety.SafetyScreen
import com.us.android.feature.dating.safety.SharedLocationScreen
import com.us.android.feature.dating.selfie.SelfieScreen
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import kotlinx.serialization.Serializable

/** Dating, as one graph with its own back stack: Back from its root leaves Dating. */
@Serializable
data object DatingGraph

/** The gate: access probe → onboarding step → home. [tab] is a [HomeTab] name. */
@Serializable
data class DatingRootRoute(val tab: String = HomeTab.PULSE.name)

@Serializable
data class DatingMatchRoute(val matchId: String, val openChat: Boolean = false)

/** Someone else's card, with the pre-match detail the server allows. */
@Serializable
data class DatingPersonRoute(val userId: String)

@Serializable
data object DatingPhotosRoute

@Serializable
data object DatingPromptsRoute

/** [shareWith] preselects a live-location recipient. */
@Serializable
data class DatingSafetyRoute(val shareWith: String? = null)

@Serializable
data class DatingSharedLocationRoute(val shareId: String)

@Serializable
data object DatingPremiumRoute

@Serializable
data object DatingPrivacyRoute

@Serializable
data object DatingBlocksRoute

/**
 * Registers Dating.
 *
 * [onOpenChat] opens a match's conversation in chat — `:app` owns that edge,
 * because features never depend on each other. [onOpenPayment] and
 * [onAbandonPayment] are supplied by `:app`, whose Activity the payment sheet
 * opens onto; this module never names the provider.
 */
fun NavGraphBuilder.datingScreens(
    navController: NavController,
    onOpenChat: (conversationId: String, title: String) -> Unit,
    onOpenPayment: (DatingPaymentRequest) -> Unit,
    onAbandonPayment: (DatingPaymentRequest) -> Unit,
) {
    navigation<DatingGraph>(startDestination = DatingRootRoute()) {
        composable<DatingRootRoute> { entry ->
            val route = entry.toRoute<DatingRootRoute>()
            DatingRoot(
                initialTab = HomeTab.entries.firstOrNull { it.name == route.tab } ?: HomeTab.PULSE,
                onBack = { navController.popBackStack<DatingGraph>(inclusive = true) },
                onOpenMatch = { navController.navigate(DatingMatchRoute(it)) },
                onOpenPerson = { navController.navigate(DatingPersonRoute(it)) },
                onOpenSafety = { navController.navigate(DatingSafetyRoute()) },
                onOpenPremium = { navController.navigate(DatingPremiumRoute) },
                onOpenPrivacy = { navController.navigate(DatingPrivacyRoute) },
                onOpenPrompts = { navController.navigate(DatingPromptsRoute) },
            )
        }

        composable<DatingMatchRoute> {
            MatchDetailScreen(
                onBack = navController::popBackStack,
                onOpenChat = onOpenChat,
                onShareLocation = { navController.navigate(DatingSafetyRoute(shareWith = it)) },
            )
        }

        composable<DatingPersonRoute> {
            PersonScreen(onBack = navController::popBackStack)
        }

        composable<DatingPhotosRoute> {
            PhotosScreen(onBack = navController::popBackStack, onContinue = null, onOpenPrompts = { navController.navigate(DatingPromptsRoute) })
        }

        composable<DatingPromptsRoute> {
            PromptsScreen(onBack = navController::popBackStack)
        }

        composable<DatingSafetyRoute> { entry ->
            SafetyScreen(
                shareWith = entry.toRoute<DatingSafetyRoute>().shareWith,
                onBack = navController::popBackStack,
                onOpenSharedLocation = { navController.navigate(DatingSharedLocationRoute(it)) },
            )
        }

        composable<DatingSharedLocationRoute> {
            SharedLocationScreen(onBack = navController::popBackStack)
        }

        composable<DatingPremiumRoute> {
            PremiumScreen(onBack = navController::popBackStack, onOpenPayment = onOpenPayment, onAbandonPayment = onAbandonPayment)
        }

        composable<DatingPrivacyRoute> {
            PrivacyScreen(
                onBack = navController::popBackStack,
                onEditPhotos = { navController.navigate(DatingPhotosRoute) },
                onEditPrompts = { navController.navigate(DatingPromptsRoute) },
                onOpenBlocks = { navController.navigate(DatingBlocksRoute) },
                // The profile is gone: leave Dating entirely.
                onDeleted = { navController.popBackStack<DatingGraph>(inclusive = true) },
            )
        }

        composable<DatingBlocksRoute> {
            BlocksScreen(onBack = navController::popBackStack)
        }
    }
}

@Composable
private fun DatingRoot(
    initialTab: HomeTab,
    onBack: () -> Unit,
    onOpenMatch: (String) -> Unit,
    onOpenPerson: (String) -> Unit,
    onOpenSafety: () -> Unit,
    onOpenPremium: () -> Unit,
    onOpenPrivacy: () -> Unit,
    onOpenPrompts: () -> Unit,
    viewModel: DatingRootViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    when (val s = state) {
        DatingRootState.Loading -> DatingScreen(title = "Dating", onBack = onBack) { LoadingPane() }
        // The pilot allowlist said no: a calm screen, never an error.
        DatingRootState.NotAvailable -> DatingScreen(title = "Dating", onBack = onBack) {
            MessagePane(
                title = DatingCopy.NOT_AVAILABLE_TITLE,
                body = DatingCopy.NOT_AVAILABLE_BODY,
                icon = UsIcons.HeartHandshake,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
        }
        is DatingRootState.Failed -> DatingScreen(title = "Dating", onBack = onBack) {
            MessagePane(title = "Dating didn't load", body = s.message, primaryLabel = "Try again", onPrimary = viewModel::reload)
        }
        is DatingRootState.Step -> when (s.step) {
            OnboardingStep.CREATE, OnboardingStep.BASICS, OnboardingStep.LOCATION, OnboardingStep.PREFERENCES ->
                DraftStepScreen(
                    step = s.step,
                    profile = s.profile,
                    preferences = s.preferences,
                    identityIncomplete = s.identityIncomplete,
                    onBack = onBack,
                    onSaved = viewModel::reload,
                )
            OnboardingStep.PHOTOS -> PhotosScreen(onBack = onBack, onContinue = viewModel::reload, onOpenPrompts = onOpenPrompts)
            OnboardingStep.SELFIE -> SelfieScreen(onBack = onBack, onDone = viewModel::reload)
            OnboardingStep.REVIEW, OnboardingStep.PAUSED, OnboardingStep.HELD ->
                StatusPane(step = s.step, onBack = onBack, onUnpause = viewModel::unpause, onOpenPrivacy = onOpenPrivacy)
            OnboardingStep.READY -> DatingHomeScreen(
                initialTab = initialTab,
                onBack = onBack,
                onOpenMatch = onOpenMatch,
                onOpenPerson = onOpenPerson,
                onOpenSafety = onOpenSafety,
                onOpenPremium = onOpenPremium,
                onOpenSettings = onOpenPrivacy,
            )
        }
    }
}

fun NavController.navigateToDating() = navigate(DatingGraph)

/** A spark push: Dating's home on the incoming sparks tab. */
fun NavController.navigateToDatingSparks() = navigate(DatingRootRoute(tab = HomeTab.SPARKS.name))

/** A match push; [openChat] continues into the conversation once the match loads. */
fun NavController.navigateToDatingMatch(matchId: String, openChat: Boolean) = navigate(DatingMatchRoute(matchId, openChat))
