package com.us.android.feature.premium.navigation

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.collectAsState
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.premium.entry.PremiumAvailability
import com.us.android.feature.premium.entry.PremiumEntry
import com.us.android.feature.premium.ui.PremiumScreen
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import javax.inject.Inject

/**
 * Momentum Premium: status, passes, purchase, invoices. From Settings ›
 * Momentum Premium and Echo's "Get Premium" — both drawn only while
 * [rememberPremiumEntry] says so.
 */
@Serializable
data object PremiumRoute

fun NavGraphBuilder.premiumScreens(onBack: () -> Unit, onPostOnEcho: () -> Unit) {
    composable<PremiumRoute> { PremiumScreen(onBack = onBack, onPostOnEcho = onPostOnEcho) }
}

fun NavController.navigateToPremium() = navigate(PremiumRoute) { launchSingleTop = true }

/** The shell's view of [PremiumAvailability]. */
@HiltViewModel
class PremiumEntryViewModel @Inject constructor(
    private val availability: PremiumAvailability,
) : ViewModel() {
    val entry: StateFlow<PremiumEntry> = availability.entry

    fun refresh() {
        viewModelScope.launch { availability.refresh() }
    }

    fun clear() = availability.clear()
}

/**
 * Which ways into Premium may be drawn ([PremiumEntry]), re-read whenever
 * [signedIn] changes. Call once, from the shell (outside the NavHost), and
 * read the [State] lazily where a row or button is drawn.
 */
@Composable
fun rememberPremiumEntry(signedIn: Boolean, viewModel: PremiumEntryViewModel = hiltViewModel()): State<PremiumEntry> {
    LaunchedEffect(signedIn) {
        if (signedIn) viewModel.refresh() else viewModel.clear()
    }
    return viewModel.entry.collectAsState()
}
