// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph and navigation extensions that use them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.settings.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.settings.account.AccountControlScreen
import com.us.android.feature.settings.account.ManageAccountScreen
import com.us.android.feature.settings.content.ContentPreferencesScreen
import com.us.android.feature.settings.onboarding.OnboardingScreen
import com.us.android.feature.settings.screentime.ScreenTimeScreen
import kotlinx.serialization.Serializable

/**
 * The first-login module picker. A shell start destination, never pushed:
 * the graph starts here while the shell is NeedsOnboarding and moves on when
 * the save lands, so there is no Back out of it and no bar under it.
 */
@Serializable
data object OnboardingRoute

/** The same picker, pushed from the settings hub with a back arrow. */
@Serializable
data object ModulesSettingsRoute

/**
 * Registers the onboarding start. No callbacks: completion is a state
 * change the shell observes through the repository, not a navigation event.
 */
fun NavGraphBuilder.onboardingScreen() {
    composable<OnboardingRoute> {
        OnboardingScreen(
            title = "Choose your experience",
            actionLabel = "Continue",
            onBack = null,
            // The shell swaps the start destination when the shell state
            // becomes Ready; nothing to do here.
            onSaved = {},
        )
    }
}

/** Registers the settings-hub edit form for the same choices. */
fun NavGraphBuilder.modulesSettingsScreen(onBack: () -> Unit) {
    composable<ModulesSettingsRoute> {
        OnboardingScreen(
            title = "Modules and home page",
            actionLabel = "Save",
            onBack = onBack,
            onSaved = onBack,
        )
    }
}

/** Type-safe navigation to the module picker from the settings hub. */
fun NavController.navigateToModulesSettings() = navigate(ModulesSettingsRoute)

// ── Manage account ────────────────────────────────────────────────────

@Serializable data object ManageAccountRoute

/** Deactivate / delete, one level under Manage account. */
@Serializable data object AccountControlRoute

fun NavGraphBuilder.manageAccountScreen(onBack: () -> Unit, onAccountControl: () -> Unit) {
    composable<ManageAccountRoute> { ManageAccountScreen(onBack, onAccountControl) }
}

fun NavGraphBuilder.accountControlScreen(onBack: () -> Unit, onSignedOut: () -> Unit) {
    composable<AccountControlRoute> { AccountControlScreen(onBack, onSignedOut) }
}

fun NavController.navigateToManageAccount() = navigate(ManageAccountRoute)
fun NavController.navigateToAccountControl() = navigate(AccountControlRoute)

// ── Screen time ────────────────────────────────────────────────────────

@Serializable data object ScreenTimeRoute

fun NavGraphBuilder.screenTimeScreen(onBack: () -> Unit) {
    composable<ScreenTimeRoute> { ScreenTimeScreen(onBack) }
}

fun NavController.navigateToScreenTime() = navigate(ScreenTimeRoute)

// ── Content preferences ───────────────────────────────────────────────

@Serializable data object ContentPreferencesRoute

fun NavGraphBuilder.contentPreferencesScreen(onBack: () -> Unit) {
    composable<ContentPreferencesRoute> { ContentPreferencesScreen(onBack) }
}

fun NavController.navigateToContentPreferences() = navigate(ContentPreferencesRoute)
