package com.us.android.feature.chat.ui.hidden

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.hidden.HiddenChats
import com.us.android.core.chat.hidden.HiddenError
import com.us.android.core.chat.hidden.HiddenResult
import com.us.android.core.chat.hidden.HiddenSettings
import com.us.android.core.chat.hidden.SecretsProblems
import com.us.android.core.chat.hidden.StepUpOutcome
import com.us.android.core.chat.hidden.changeProblems
import com.us.android.core.chat.hidden.lockedMessage
import com.us.android.core.chat.hidden.settingsErrorMessage
import com.us.android.core.chat.hidden.setupProblems
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Forgot: re-auth first, then new secrets. */
enum class ResetStage { None, Verify, Secrets }

data class HiddenSettingsUiState(
    val loading: Boolean = true,
    val loadFailed: Boolean = false,
    val settings: HiddenSettings? = null,
    val busy: Boolean = false,
    val problems: SecretsProblems = SecretsProblems(),
    val error: String? = null,
    val notice: String? = null,
    val resetStage: ResetStage = ResetStage.None,
    /** Set up from "Hide chat": the chat that asked is hidden now; the screen closes. */
    val doneHiding: Boolean = false,
)

/**
 * Settings › Privacy › Hidden chats. Secrets travel from the form's fields to
 * one call and nowhere else: the fields are plain `remember` state (never
 * saved state), nothing here keeps a copy, and the step-up token lives in
 * this ViewModel's memory for its five minutes at most.
 */
@HiltViewModel
class HiddenChatsSettingsViewModel @Inject constructor(
    private val hidden: HiddenChats,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    /** The chat whose "Hide chat" brought the person here, if any. Ids are not secrets. */
    private val hideConversationId: String = savedStateHandle.get<String>(HIDE_CONVERSATION_ID_KEY).orEmpty()

    private val _state = MutableStateFlow(HiddenSettingsUiState())
    val state: StateFlow<HiddenSettingsUiState> = _state.asStateFlow()

    private var stepUpToken: String? = null
    private var stepUpAt: Long = 0L

    init {
        load()
    }

    val hidingAChat: Boolean get() = hideConversationId.isNotBlank()

    fun load() = viewModelScope.launch {
        _state.update { it.copy(loading = true, loadFailed = false) }
        when (val r = hidden.loadSettings()) {
            is HiddenResult.Ok -> _state.update { it.copy(loading = false, settings = r.value) }
            is HiddenResult.Err -> _state.update { it.copy(loading = false, loadFailed = true, error = settingsErrorMessage(r.error)) }
        }
    }

    fun dismissMessages() = _state.update { it.copy(error = null, notice = null) }

    /** First time: both secrets; then the chat that asked, if one did, is hidden. */
    fun setUp(secretKey: String, chatPassword: String, confirm: String, strict: Boolean) {
        val problems = setupProblems(secretKey, chatPassword, confirm)
        if (!problems.none) {
            _state.update { it.copy(problems = problems, error = null) }
            return
        }
        launchBusy {
            when (val r = hidden.setUp(secretKey, chatPassword, strict.takeIf { it })) {
                is HiddenResult.Ok -> {
                    _state.update { it.copy(settings = r.value, problems = SecretsProblems()) }
                    hideWaitingChat()
                }
                is HiddenResult.Err -> fail(r.error)
            }
        }
    }

    /** Change either secret with the current chat password; empty = keep. */
    fun change(current: String, newSecretKey: String, newPassword: String, confirm: String) {
        val problems = changeProblems(current, newSecretKey, newPassword, confirm)
        if (!problems.none) {
            _state.update { it.copy(problems = problems, error = problems.form) }
            return
        }
        launchBusy {
            when (val r = hidden.change(current, newSecretKey.ifEmpty { null }, newPassword.ifEmpty { null })) {
                is HiddenResult.Ok -> _state.update {
                    it.copy(settings = r.value, problems = SecretsProblems(), notice = "Saved. Hidden chats locked again on every device.")
                }
                is HiddenResult.Err -> fail(r.error)
            }
        }
    }

    /** Strict mode on or off; any change once configured needs the current chat password. */
    fun setStrict(current: String, strict: Boolean) {
        if (current.isEmpty()) {
            _state.update { it.copy(error = "Enter your current chat password.") }
            return
        }
        launchBusy {
            when (val r = hidden.change(current, strict = strict)) {
                is HiddenResult.Ok -> _state.update {
                    it.copy(settings = r.value, notice = if (strict) "Strict mode is on." else "Strict mode is off.")
                }
                is HiddenResult.Err -> fail(r.error)
            }
        }
    }

    // ── Forgot ──────────────────────────────────────────────────────────

    fun startReset() = _state.update { it.copy(resetStage = ResetStage.Verify, error = null, notice = null) }

    fun cancelReset() {
        stepUpToken = null
        _state.update { it.copy(resetStage = ResetStage.None, error = null) }
    }

    /** A 2-step code: identity's step-up token, kept here for the reset only. */
    fun verifyCode(otp: String) {
        if (otp.isBlank()) return
        launchBusy {
            when (val r = hidden.stepUp(otp)) {
                is StepUpOutcome.Ok -> {
                    stepUpToken = r.token
                    stepUpAt = System.currentTimeMillis()
                    _state.update { it.copy(resetStage = ResetStage.Secrets) }
                }
                StepUpOutcome.WrongCode -> _state.update { it.copy(error = "That code didn't work. Check it and try again.") }
                StepUpOutcome.Replayed -> _state.update { it.copy(error = "That code was already used. Wait for the next one.") }
                StepUpOutcome.NoTwoFactor -> _state.update {
                    it.copy(error = "You don't have two-step verification. Sign out and sign in again, then reset within 5 minutes.")
                }
                is StepUpOutcome.RateLimited -> _state.update { it.copy(error = lockedMessage(r.retryAfterSec)) }
                StepUpOutcome.Failed -> _state.update { it.copy(error = "Couldn't confirm it's you. Try again.") }
            }
        }
    }

    /** "I signed in again just now": the bearer itself is the fresh sign-in. */
    fun useFreshSignIn() {
        stepUpToken = null
        _state.update { it.copy(resetStage = ResetStage.Secrets, error = null) }
    }

    fun reset(secretKey: String, chatPassword: String, confirm: String, strict: Boolean) {
        val problems = setupProblems(secretKey, chatPassword, confirm)
        if (!problems.none) {
            _state.update { it.copy(problems = problems) }
            return
        }
        val token = stepUpToken?.takeIf { System.currentTimeMillis() - stepUpAt < STEP_UP_LIFETIME_MS }
        launchBusy {
            when (val r = hidden.reset(secretKey, chatPassword, token, strict)) {
                is HiddenResult.Ok -> {
                    stepUpToken = null
                    _state.update {
                        it.copy(resetStage = ResetStage.None, problems = SecretsProblems(), notice = "Your secret key and chat password are reset.")
                    }
                }
                is HiddenResult.Err -> if (r.error == HiddenError.StepUpRequired) {
                    stepUpToken = null
                    _state.update {
                        it.copy(
                            resetStage = ResetStage.Verify,
                            error = "Confirm it's you first: enter a 2-step code, or sign out and sign in again.",
                        )
                    }
                } else {
                    fail(r.error)
                }
            }
        }
    }

    private suspend fun hideWaitingChat() {
        if (!hidingAChat) {
            _state.update { it.copy(notice = "Hidden chats are set up.") }
            return
        }
        when (val r = hidden.hide(hideConversationId)) {
            is HiddenResult.Ok -> _state.update { it.copy(doneHiding = true) }
            is HiddenResult.Err -> fail(r.error)
        }
    }

    private fun fail(error: HiddenError) = _state.update { it.copy(error = settingsErrorMessage(error)) }

    private fun launchBusy(block: suspend () -> Unit) {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true, error = null, notice = null) }
        viewModelScope.launch {
            try {
                block()
            } finally {
                _state.update { it.copy(busy = false) }
            }
        }
    }

    companion object {
        /** Matches [com.us.android.feature.chat.navigation.HiddenChatsSettingsRoute.hideConversationId]. */
        const val HIDE_CONVERSATION_ID_KEY = "hideConversationId"
        private const val STEP_UP_LIFETIME_MS = 5 * 60_000L
    }
}
