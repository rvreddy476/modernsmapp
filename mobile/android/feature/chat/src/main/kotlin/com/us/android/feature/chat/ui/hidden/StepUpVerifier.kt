package com.us.android.feature.chat.ui.hidden

import androidx.annotation.StringRes
import com.us.android.core.chat.hidden.StepUpOutcome
import com.us.android.core.chat.hidden.StepUpStart
import com.us.android.feature.chat.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** How "Confirm it's you" works for this account; [Checking] until identity answers. */
enum class StepUpMethod { Checking, Email, Authenticator, None }

/** The emailed-code step's own copy, from resources. */
enum class StepUpNotice(@StringRes val text: Int, val isError: Boolean) {
    CodeSent(R.string.hidden_step_up_code_sent, isError = false),
    WrongCode(R.string.hidden_step_up_wrong_code, isError = true),
    CodeExpired(R.string.hidden_step_up_code_expired, isError = true),
    TooSoon(R.string.hidden_step_up_too_soon, isError = true),
    EmailUnavailable(R.string.hidden_step_up_email_unavailable, isError = true),
    CouldNotSend(R.string.hidden_step_up_could_not_send, isError = true),
}

data class StepUpVerifyState(
    val method: StepUpMethod = StepUpMethod.Checking,
    /** Where the code went, masked by identity; blank when it did not say. */
    val emailMasked: String = "",
    /** Seconds until Resend is allowed again; 0 means now. */
    val resendInSeconds: Int = 0,
    val sending: Boolean = false,
    val notice: StepUpNotice? = null,
) {
    val canResend: Boolean get() = method == StepUpMethod.Email && resendInSeconds == 0 && !sending

    companion object {
        const val RESEND_COOLDOWN_SECONDS = 60
    }
}

/**
 * identity's in-session step-up for the hidden-chats reset (founder, 10 Nov
 * 2026). An emailed-code account gets a code mailed when the step opens and
 * answers with `{challenge_token, code}`; an authenticator account answers
 * with `{otp}` as before. The challenge token lives here, in memory, until
 * the step closes.
 */
class StepUpVerifier(
    private val scope: CoroutineScope,
    private val begin: suspend () -> StepUpStart,
    private val sendAgain: suspend () -> StepUpStart,
    private val verifyEmail: suspend (challengeToken: String, code: String) -> StepUpOutcome,
    private val verifyAuthenticator: suspend (otp: String) -> StepUpOutcome,
) {
    private val _state = MutableStateFlow(StepUpVerifyState())
    val state: StateFlow<StepUpVerifyState> = _state.asStateFlow()

    private var challengeToken: String = ""
    private var countdown: Job? = null
    private var opening: Job? = null

    /** The step opened: find out how this account confirms, and mail a code if it is by email. */
    fun open() {
        close()
        _state.value = StepUpVerifyState(sending = true)
        opening = scope.launch { apply(begin(), resent = false) }
    }

    fun resend() {
        if (!_state.value.canResend) return
        _state.update { it.copy(sending = true, notice = null) }
        opening = scope.launch { apply(sendAgain(), resent = true) }
    }

    /**
     * Sends the code the way this account confirms. The emailed path words
     * its own refusals in [StepUpVerifyState.notice]; every outcome is
     * returned for the caller to act on.
     */
    suspend fun verify(code: String): StepUpOutcome {
        val current = _state.value
        if (current.method != StepUpMethod.Email) return verifyAuthenticator(code)
        if (challengeToken.isEmpty()) {
            _state.update { it.copy(notice = StepUpNotice.CodeExpired) }
            return StepUpOutcome.ChallengeExpired
        }
        _state.update { it.copy(notice = null) }
        val outcome = verifyEmail(challengeToken, code)
        when (outcome) {
            is StepUpOutcome.Ok -> close()
            StepUpOutcome.WrongCode -> _state.update { it.copy(notice = StepUpNotice.WrongCode) }
            StepUpOutcome.ChallengeExpired, StepUpOutcome.Replayed -> {
                challengeToken = ""
                _state.update { it.copy(notice = StepUpNotice.CodeExpired) }
            }
            else -> Unit
        }
        return outcome
    }

    /** The step closed (done, cancelled, or the screen went): forget the challenge. */
    fun close() {
        challengeToken = ""
        countdown?.cancel()
        countdown = null
        opening?.cancel()
        opening = null
    }

    private fun apply(start: StepUpStart, resent: Boolean) {
        when (start) {
            is StepUpStart.Email -> {
                challengeToken = start.challengeToken
                _state.update {
                    it.copy(
                        method = StepUpMethod.Email,
                        emailMasked = start.emailMasked.ifBlank { it.emailMasked },
                        sending = false,
                        notice = StepUpNotice.CodeSent.takeIf { resent },
                    )
                }
                startCountdown(StepUpVerifyState.RESEND_COOLDOWN_SECONDS)
            }
            StepUpStart.Authenticator -> {
                challengeToken = ""
                _state.update { it.copy(method = StepUpMethod.Authenticator, sending = false, notice = null) }
            }
            StepUpStart.NoTwoFactor -> {
                challengeToken = ""
                _state.update { it.copy(method = StepUpMethod.None, sending = false, notice = null) }
            }
            // Only an emailed-code account reaches the send: it stays on that path.
            is StepUpStart.RateLimited -> {
                _state.update { it.copy(method = StepUpMethod.Email, sending = false, notice = StepUpNotice.TooSoon) }
                startCountdown(
                    start.retryAfterSec?.takeIf { it > 0 }?.toInt() ?: StepUpVerifyState.RESEND_COOLDOWN_SECONDS,
                )
            }
            StepUpStart.EmailUnavailable -> _state.update {
                it.copy(method = StepUpMethod.Email, sending = false, notice = StepUpNotice.EmailUnavailable)
            }
            StepUpStart.Failed -> _state.update {
                it.copy(method = StepUpMethod.Email, sending = false, notice = StepUpNotice.CouldNotSend)
            }
        }
    }

    private fun startCountdown(seconds: Int) {
        countdown?.cancel()
        _state.update { it.copy(resendInSeconds = seconds.coerceAtLeast(0)) }
        if (seconds <= 0) return
        countdown = scope.launch {
            while (_state.value.resendInSeconds > 0) {
                delay(TICK_MS)
                _state.update { it.copy(resendInSeconds = (it.resendInSeconds - 1).coerceAtLeast(0)) }
            }
        }
    }

    private companion object {
        const val TICK_MS = 1_000L
    }
}
