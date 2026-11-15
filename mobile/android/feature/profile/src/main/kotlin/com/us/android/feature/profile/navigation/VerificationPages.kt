package com.us.android.feature.profile.navigation

import androidx.activity.compose.BackHandler
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.feature.profile.data.PersonVerificationReturns
import com.us.android.feature.profile.ui.OfficialMarkScreen
import com.us.android.feature.profile.ui.PersonVerificationScreen
import com.us.android.feature.profile.ui.VerificationScreen
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import javax.inject.Inject

/** The three verification pages: Request verification, Verify you're a real person, Official mark. */
internal enum class VerificationPage { Request, Person, Official }

/**
 * One verification destination ([base]) plus the other pages opened IN PLACE over it when `:app`
 * has not passed their callback ([SettingsSections.onPersonVerification],
 * [SettingsSections.onOfficialMark]). Back from an in-place page returns to [base].
 */
@Composable
internal fun VerificationPages(base: VerificationPage, destinations: SettingsDestinations) {
    var overlay by rememberSaveable { mutableStateOf<VerificationPage?>(null) }
    val sections = destinations.sections
    val openPerson = sections.onPersonVerification ?: { overlay = VerificationPage.Person }
    val openOfficial = sections.onOfficialMark ?: { overlay = VerificationPage.Official }
    val page = overlay ?: base
    val back: () -> Unit = if (overlay != null) ({ overlay = null }) else destinations.onBack
    if (overlay != null) BackHandler { overlay = null }
    when (page) {
        VerificationPage.Request -> VerificationScreen(
            onBack = back,
            onOpenOfficialMark = openOfficial,
            onOpenPersonVerification = openPerson,
        )
        VerificationPage.Person -> PersonVerificationScreen(onBack = back, onOpenOfficialMark = openOfficial)
        VerificationPage.Official -> OfficialMarkScreen(onBack = back, onOpenPersonVerification = openPerson)
    }
}

/** Holds the app-wide DigiLocker return slot for [PersonVerificationReturnEffect]. */
@HiltViewModel
class PersonReturnRouterViewModel @Inject constructor(returns: PersonVerificationReturns) : ViewModel() {
    val waiting: StateFlow<Boolean> = returns.pending
        .map { it != null }
        .stateIn(viewModelScope, SharingStarted.Eagerly, returns.pending.value != null)
}

/**
 * For `:app`'s nav host, inside the signed-in graph: when a DigiLocker return App Link is
 * waiting, calls [onReturn] (which should be [navigateToPersonVerification]) so the page that
 * posts it is open even after the process was killed while the browser was up. The page takes
 * the return; this only navigates.
 */
@Composable
fun PersonVerificationReturnEffect(
    onReturn: () -> Unit,
    viewModel: PersonReturnRouterViewModel = hiltViewModel(),
) {
    val waiting by viewModel.waiting.collectAsStateWithLifecycle()
    LaunchedEffect(waiting) { if (waiting) onReturn() }
}
