package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.AccountSummary
import com.us.android.core.profile.data.EmailChallenge
import com.us.android.core.profile.data.SecuritySettingsRepository
import com.us.android.core.profile.data.TwoFactorMethod
import com.us.android.core.profile.data.TwoFactorPurpose
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where the Two-step verification card is. */
sealed interface TwoFactorStep {
    data object Idle : TwoFactorStep

    /** The password, before a code is mailed for [purpose]. */
    data class Password(val purpose: TwoFactorPurpose) : TwoFactorStep

    /** A code is in the inbox at [emailMasked]; entering it finishes [purpose]. */
    data class Code(val purpose: TwoFactorPurpose, val emailMasked: String) : TwoFactorStep

    /** A legacy authenticator-app account turning it off: password and a current app code. */
    data object AuthenticatorOff : TwoFactorStep
}

/** Why the last step did not go through. The screen words each one from resources. */
sealed interface TwoFactorProblem {
    data object WrongPassword : TwoFactorProblem
    data object WrongCode : TwoFactorProblem

    /** The challenge is spent or past its 5 minutes: only a new code helps. */
    data object CodeExpired : TwoFactorProblem

    /** The authenticator turn-off refused the password or the app code (400 `2FA_DISABLE_FAILED`). */
    data object AuthenticatorRejected : TwoFactorProblem
    data object EmailNotVerified : TwoFactorProblem
    data object EmailUnavailable : TwoFactorProblem

    /** The account changed elsewhere (already on, already off, other method); the card reloaded. */
    data object StateChanged : TwoFactorProblem
    data class TooManyTries(val retryAfterSeconds: Long?) : TwoFactorProblem
    data object Offline : TwoFactorProblem
    data object Failed : TwoFactorProblem
}

enum class TwoFactorDone { TurnedOn, TurnedOff, CodeResent }

data class TwoFactorSettingsUiState(
    val method: TwoFactorMethod = TwoFactorMethod.None,
    /** The account's own address, for "codes go to…" before any code was sent. */
    val accountEmail: String = "",
    val emailVerified: Boolean = false,
    /** The server's masked address from the last code sent; preferred over [accountEmail]. */
    val emailMasked: String = "",
    val step: TwoFactorStep = TwoFactorStep.Idle,
    val busy: Boolean = false,
    /** Seconds until Resend is allowed again; 0 means now. */
    val resendInSeconds: Int = 0,
    val problem: TwoFactorProblem? = null,
    val done: TwoFactorDone? = null,
) {
    val canTurnOn: Boolean
        get() = method == TwoFactorMethod.None && emailVerified && step == TwoFactorStep.Idle && !busy
    val canTurnOff: Boolean
        get() = (method == TwoFactorMethod.Email || method == TwoFactorMethod.Authenticator) &&
            step == TwoFactorStep.Idle && !busy
    val canResend: Boolean get() = step is TwoFactorStep.Code && resendInSeconds == 0 && !busy

    companion object {
        const val CODE_LENGTH = 6
        const val RESEND_COOLDOWN_SECONDS = 60
    }
}

/**
 * Settings › Account and security › Two-step verification (founder, 10 Nov
 * 2026): app users get an EMAILED 6-digit code. Turning it on or off is
 * password → code → done; the password is held here only until the flow
 * ends, because Resend asks for a new challenge with it. A legacy
 * authenticator-app account can still turn it off with password and app
 * code; nobody can set an authenticator up any more (the server answers
 * `TOTP_NOT_OFFERED` to app users).
 */
@HiltViewModel
class TwoFactorSettingsViewModel @Inject constructor(
    private val settings: SecuritySettingsRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(TwoFactorSettingsUiState())
    val state: StateFlow<TwoFactorSettingsUiState> = _state.asStateFlow()

    private var password: String = ""
    private var challengeToken: String = ""
    private var countdown: Job? = null

    /** The account as Security settings loaded it. Ignored mid-flow so a reload cannot reset a step. */
    fun bind(account: AccountSummary) = _state.update {
        if (it.step != TwoFactorStep.Idle || it.busy) return@update it
        it.copy(method = account.twoFactorMethod, accountEmail = account.email, emailVerified = account.emailVerified)
    }

    fun turnOn() {
        if (!_state.value.canTurnOn) return
        _state.update { it.copy(step = TwoFactorStep.Password(TwoFactorPurpose.Enable), problem = null, done = null) }
    }

    fun turnOff() {
        val current = _state.value
        if (!current.canTurnOff) return
        val step = if (current.method == TwoFactorMethod.Email) {
            TwoFactorStep.Password(TwoFactorPurpose.Disable)
        } else {
            TwoFactorStep.AuthenticatorOff
        }
        _state.update { it.copy(step = step, problem = null, done = null) }
    }

    fun cancel() {
        endFlow()
        _state.update { it.copy(step = TwoFactorStep.Idle, problem = null, busy = false) }
    }

    fun dismiss() = _state.update { it.copy(problem = null, done = null) }

    /** Password step: mails the code. */
    fun submitPassword(value: String) {
        val current = _state.value
        val step = current.step as? TwoFactorStep.Password ?: return
        if (current.busy || value.isEmpty()) return
        _state.update { it.copy(busy = true, problem = null, done = null) }
        viewModelScope.launch {
            when (val result = settings.startEmailTwoFactor(value, step.purpose)) {
                is AppResult.Success -> {
                    password = value
                    onChallenge(step.purpose, result.data, done = null)
                }
                is AppResult.Failure -> fail(result.error)
            }
        }
    }

    /** Code step: finishes turning it on or off. */
    fun submitCode(code: String) {
        val current = _state.value
        val step = current.step as? TwoFactorStep.Code ?: return
        val digits = code.filter(Char::isDigit)
        if (current.busy || digits.length != TwoFactorSettingsUiState.CODE_LENGTH) return
        _state.update { it.copy(busy = true, problem = null, done = null) }
        viewModelScope.launch {
            val token = challengeToken
            val result = when (step.purpose) {
                TwoFactorPurpose.Enable -> settings.enableEmailTwoFactor(token, digits)
                TwoFactorPurpose.Disable -> settings.disableEmailTwoFactor(token, digits)
            }
            when (result) {
                is AppResult.Success -> finished(step)
                is AppResult.Failure -> fail(result.error)
            }
        }
    }

    /** Code step: a new code (and challenge) with the password already given, once the cooldown is over. */
    fun resend() {
        val current = _state.value
        val step = current.step as? TwoFactorStep.Code ?: return
        if (!current.canResend || password.isEmpty()) return
        _state.update { it.copy(busy = true, problem = null, done = null) }
        viewModelScope.launch {
            when (val result = settings.startEmailTwoFactor(password, step.purpose)) {
                is AppResult.Success -> onChallenge(step.purpose, result.data, TwoFactorDone.CodeResent)
                is AppResult.Failure -> fail(result.error)
            }
        }
    }

    /** Legacy authenticator app: turn it off with the password and a current app code. */
    fun disableAuthenticator(password: String, code: String) {
        val current = _state.value
        if (current.step != TwoFactorStep.AuthenticatorOff || current.busy) return
        if (password.isEmpty() || code.length != TwoFactorSettingsUiState.CODE_LENGTH) return
        _state.update { it.copy(busy = true, problem = null, done = null) }
        viewModelScope.launch {
            when (val result = settings.disableTwoFactor(password, code)) {
                is AppResult.Success -> finished(TwoFactorStep.AuthenticatorOff)
                is AppResult.Failure -> fail(result.error)
            }
        }
    }

    private fun onChallenge(purpose: TwoFactorPurpose, challenge: EmailChallenge, done: TwoFactorDone?) {
        challengeToken = challenge.challengeToken
        _state.update {
            it.copy(
                busy = false,
                step = TwoFactorStep.Code(purpose, challenge.emailMasked.ifBlank { it.emailMasked }),
                emailMasked = challenge.emailMasked.ifBlank { it.emailMasked },
                done = done,
            )
        }
        startCountdown(TwoFactorSettingsUiState.RESEND_COOLDOWN_SECONDS)
    }

    private fun finished(step: TwoFactorStep) {
        endFlow()
        val turnedOn = step is TwoFactorStep.Code && step.purpose == TwoFactorPurpose.Enable
        _state.update {
            it.copy(
                busy = false,
                step = TwoFactorStep.Idle,
                method = if (turnedOn) TwoFactorMethod.Email else TwoFactorMethod.None,
                resendInSeconds = 0,
                done = if (turnedOn) TwoFactorDone.TurnedOn else TwoFactorDone.TurnedOff,
            )
        }
    }

    private fun fail(error: AppError) {
        val problem = twoFactorProblemOf(error)
        _state.update { it.copy(busy = false, problem = problem) }
        when (problem) {
            is TwoFactorProblem.TooManyTries -> if (_state.value.step is TwoFactorStep.Code) {
                startCountdown(
                    problem.retryAfterSeconds?.takeIf { it > 0 }?.toInt()
                        ?: TwoFactorSettingsUiState.RESEND_COOLDOWN_SECONDS,
                )
            }
            // What this card believed about the account is wrong: start over
            // from what the server says now.
            TwoFactorProblem.StateChanged, TwoFactorProblem.EmailNotVerified -> {
                endFlow()
                _state.update { it.copy(step = TwoFactorStep.Idle, resendInSeconds = 0) }
                refreshAccount()
            }
            else -> Unit
        }
    }

    private fun refreshAccount() {
        viewModelScope.launch {
            val account = (settings.account() as? AppResult.Success)?.data ?: return@launch
            bind(account)
        }
    }

    private fun startCountdown(seconds: Int) {
        countdown?.cancel()
        _state.update { it.copy(resendInSeconds = seconds.coerceAtLeast(0)) }
        if (seconds <= 0) return
        countdown = viewModelScope.launch {
            while (_state.value.resendInSeconds > 0) {
                delay(TICK_MS)
                _state.update { it.copy(resendInSeconds = (it.resendInSeconds - 1).coerceAtLeast(0)) }
            }
        }
    }

    /** Forgets the password and the challenge: neither outlives the flow. */
    private fun endFlow() {
        password = ""
        challengeToken = ""
        countdown?.cancel()
        countdown = null
    }

    override fun onCleared() {
        endFlow()
        super.onCleared()
    }

    private companion object {
        const val TICK_MS = 1_000L
    }
}

/**
 * The server's refusal, as the card's [TwoFactorProblem] (auth-service
 * `internal/http/twofa_email.go`). The core [com.us.android.core.network.ErrorMapper]
 * puts a 401 in [AppError.AuthFailed], a 409 or a 400 with its own code in
 * [AppError.Unknown], and a 503 in [AppError.Server], each with the code kept.
 */
internal fun twoFactorProblemOf(error: AppError): TwoFactorProblem = when (error) {
    is AppError.RateLimited -> TwoFactorProblem.TooManyTries(error.retryAfterSeconds)
    is AppError.NoNetwork, is AppError.Timeout -> TwoFactorProblem.Offline
    is AppError.AuthFailed ->
        if (error.code == CODE_INVALID_CREDENTIALS) TwoFactorProblem.WrongPassword else TwoFactorProblem.Failed
    is AppError.Unknown -> codeProblem(error.code)
    is AppError.Forbidden -> codeProblem(error.code)
    is AppError.Server -> codeProblem(error.code)
    else -> TwoFactorProblem.Failed
}

private fun codeProblem(code: String?): TwoFactorProblem = when (code) {
    CODE_INVALID_CREDENTIALS -> TwoFactorProblem.WrongPassword
    CODE_INVALID_CODE -> TwoFactorProblem.WrongCode
    CODE_INVALID_CHALLENGE -> TwoFactorProblem.CodeExpired
    CODE_DISABLE_FAILED -> TwoFactorProblem.AuthenticatorRejected
    CODE_EMAIL_NOT_VERIFIED -> TwoFactorProblem.EmailNotVerified
    CODE_EMAIL_UNAVAILABLE -> TwoFactorProblem.EmailUnavailable
    CODE_ALREADY_ENABLED, CODE_NOT_ENABLED, CODE_METHOD_MISMATCH -> TwoFactorProblem.StateChanged
    else -> TwoFactorProblem.Failed
}

private const val CODE_INVALID_CREDENTIALS = "INVALID_CREDENTIALS"
private const val CODE_INVALID_CODE = "INVALID_CODE"
private const val CODE_INVALID_CHALLENGE = "INVALID_CHALLENGE"
private const val CODE_DISABLE_FAILED = "2FA_DISABLE_FAILED"
private const val CODE_EMAIL_NOT_VERIFIED = "EMAIL_NOT_VERIFIED"
private const val CODE_EMAIL_UNAVAILABLE = "EMAIL_UNAVAILABLE"
private const val CODE_ALREADY_ENABLED = "TWO_FACTOR_ALREADY_ENABLED"
private const val CODE_NOT_ENABLED = "TWO_FACTOR_NOT_ENABLED"
private const val CODE_METHOD_MISMATCH = "TWO_FACTOR_METHOD_MISMATCH"
