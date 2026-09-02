// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph and navigation extensions that use them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.settings.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.settings.onboarding.OnboardingScreen
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
