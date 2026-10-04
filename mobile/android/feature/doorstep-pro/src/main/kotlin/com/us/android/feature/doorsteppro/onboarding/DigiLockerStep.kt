package com.us.android.feature.doorsteppro.onboarding

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.deeplink.DigiLockerReturnPolicy
import com.us.android.feature.doorsteppro.deeplink.DigiLockerStateStore
import com.us.android.feature.doorsteppro.deeplink.ProDeepLink
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinkBus
import com.us.android.feature.doorsteppro.deeplink.ReturnCheck
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class DigiLockerUiState(
    val verified: Boolean = false,
    val busy: Boolean = false,
    /** A link came back that only a fresh start can recover from. */
    val startAgain: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * Aadhaar via DigiLocker (PKCE on the server): `start` returns the
 * authorization URL and a `state`; this device keeps the state, opens the URL,
 * and when DigiLocker sends the professional back to the app link it posts
 * `{code, state}` to the callback — only if the state is the one it kept.
 * Gender is recorded from Aadhaar on the server; nothing here asks for it.
 */
@HiltViewModel
class DigiLockerViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val stateStore: DigiLockerStateStore,
    private val deepLinks: ProDeepLinkBus,
) : ViewModel() {

    private val _state = MutableStateFlow(DigiLockerUiState())
    val state: StateFlow<DigiLockerUiState> = _state.asStateFlow()

    private val _open = MutableSharedFlow<String>(extraBufferCapacity = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)

    /** An authorization URL for the screen to open in the browser. */
    val open: SharedFlow<String> = _open.asSharedFlow()

    init {
        viewModelScope.launch {
            (repository.readiness() as? ProResult.Success)?.value?.let { readiness ->
                if (OnboardingStep.AADHAAR.wire in readiness.completedSteps) _state.update { it.copy(verified = true) }
            }
        }
        viewModelScope.launch {
            deepLinks.incoming.collect { link ->
                if (link is ProDeepLink.DigiLocker) {
                    deepLinks.consumed()
                    onReturn(link)
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun onBrowserMissing() = _state.update { it.copy(message = errorMessage("No browser on this phone can open DigiLocker.")) }

    fun start() {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true, startAgain = false) }
        viewModelScope.launch {
            when (val result = repository.startDigiLocker()) {
                is ProResult.Success -> {
                    val url = result.value.authorizationUrl
                    if (!url.startsWith("https://") && !url.startsWith("http://")) {
                        _state.update { it.copy(busy = false, message = errorMessage("DigiLocker returned a link the app can't open.")) }
                        return@launch
                    }
                    stateStore.save(result.value.state)
                    _state.update { it.copy(busy = false) }
                    _open.tryEmit(url)
                }
                is ProResult.Failure -> _state.update { it.copy(busy = false, message = result.error.asMessage()) }
            }
        }
    }

    private suspend fun onReturn(link: ProDeepLink.DigiLocker) {
        when (val check = DigiLockerReturnPolicy.check(link.link, stateStore.load())) {
            ReturnCheck.NothingPending -> _state.update {
                it.copy(message = errorMessage("No DigiLocker check was started on this phone. Start it again."), startAgain = true)
            }
            ReturnCheck.StateMismatch -> _state.update {
                it.copy(message = errorMessage("That DigiLocker link isn't from this phone's request. Start again."), startAgain = true)
            }
            is ReturnCheck.Declined -> {
                stateStore.clear()
                _state.update { it.copy(message = errorMessage("DigiLocker didn't share your Aadhaar. Try again when you're ready."), startAgain = true) }
            }
            ReturnCheck.MissingCode -> _state.update { it.copy(message = errorMessage("DigiLocker sent you back without a code. Start again."), startAgain = true) }
            is ReturnCheck.Complete -> {
                _state.update { it.copy(busy = true) }
                when (val result = repository.completeDigiLocker(code = check.code, state = check.state)) {
                    is ProResult.Success -> {
                        stateStore.clear()
                        _state.update { it.copy(busy = false, verified = true, message = successMessage("Aadhaar verified.")) }
                    }
                    is ProResult.Failure -> {
                        val restart = result.error.code == ProCodes.INVALID_REQUEST
                        if (restart) stateStore.clear()
                        _state.update { it.copy(busy = false, startAgain = restart, message = result.error.asMessage()) }
                    }
                }
            }
        }
    }
}

@Composable
fun DigiLockerStepScreen(onBack: () -> Unit, viewModel: DigiLockerViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(viewModel) {
        viewModel.open.collect { url ->
            try {
                context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (e: ActivityNotFoundException) {
                viewModel.onBrowserMissing()
            }
        }
    }
    ProScreen(title = "Aadhaar via DigiLocker", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            ProCard {
                CardHeading(
                    "Verify with DigiLocker",
                    "DigiLocker shares your Aadhaar name, photo and gender with Doorstep. Your Aadhaar number is never stored.",
                )
                if (state.verified) {
                    Pill("Verified", Tone.Positive)
                    UsButton(text = "Done", onClick = onBack, modifier = Modifier.fillMaxWidth())
                } else {
                    UsButton(
                        text = if (state.startAgain) "Start again" else "Open DigiLocker",
                        onClick = viewModel::start,
                        loading = state.busy,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    InfoNote("You'll sign in to DigiLocker in your browser and come straight back here.")
                }
            }
            InfoNote(
                "Women's salon jobs go only to women professionals and men's salon jobs only to men — " +
                    "that is why gender comes from Aadhaar and can't be typed.",
            )
        }
    }
}
