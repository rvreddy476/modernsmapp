package com.us.android.feature.auth.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthErrorText
import com.us.android.core.auth.AuthRepository
import com.us.android.core.auth.OAuthSignInOutcome
import com.us.android.core.auth.oauth.GoogleSignIn
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Where a sign-in attempt goes next when it is not (yet) a session.
 *
 * Each carries exactly what the next screen's request binds `required`, so
 * the screen never has to reach back into a shared holder for it.
 */
sealed interface LoginNavigation {
    /** `requires_2fa`: the authenticator code screen. */
    data class TwoFactor(val userId: String, val pendingToken: String) : LoginNavigation

    /** `requires_step_up`: the unusual-sign-in screen, with the channels the server accepts. */
    data class StepUp(val pendingToken: String, val methods: List<String>) : LoginNavigation

    /** A 202 from the Google sign-in: the account needs a phone number before it exists. */
    data class OAuthCompleteSignup(
        val pendingToken: String,
        val provider: String,
        val email: String,
        val name: String,
    ) : LoginNavigation
}

data class LoginUiState(
    val identifier: String = "",
    val password: String = "",
    val identifierError: String? = null,
    val passwordError: String? = null,
    /** Transient feedback shown in the shared message host. */
    val message: UsMessage? = null,
    val isSubmitting: Boolean = false,
    /** True from the Google tap until the browser hands back a result (or the person returns without one). */
    val isGoogleSubmitting: Boolean = false,
    /** False when the build has no Google client id: the button is not drawn. */
    val googleSignInAvailable: Boolean = false,
    /** The authorization URL to open in the browser; the screen opens it once and clears it. */
    val googleAuthUrl: String? = null,
    /**
     * Set when sign-in reveals the account is unverified.
     *
     * The 403 carries a FRESH verification token, which is the only way a
     * user who closed the app mid-signup can finish. Surfacing it here is
     * what turns that error into a recoverable path instead of a dead end.
     */
    val needsVerificationToken: String? = null,
    /** A pending second step: the route navigates and then calls [LoginViewModel.consumeNavigation]. */
    val navigation: LoginNavigation? = null,
) {
    val canSubmit: Boolean
        get() = !isSubmitting && !isGoogleSubmitting && identifier.isNotBlank() && password.isNotBlank()
}

@HiltViewModel
class LoginViewModel @Inject constructor(
    private val authRepository: AuthRepository,
    private val googleSignIn: GoogleSignIn,
) : ViewModel() {

    private val _state = MutableStateFlow(LoginUiState(googleSignInAvailable = googleSignIn.isAvailable))
    val state: StateFlow<LoginUiState> = _state.asStateFlow()

    init {
        // The browser's redirect lands on MainActivity, which offers it to
        // the flow; this is the screen that finishes it. Collected for the
        // ViewModel's life, so a redirect that arrives while the screen is
        // being recreated is not lost.
        viewModelScope.launch {
            googleSignIn.pendingRedirect.filterNotNull().collect { finishGoogleSignIn(it) }
        }
    }

    fun onIdentifierChange(value: String) =
        _state.update { it.copy(identifier = value, identifierError = null) }

    fun onPasswordChange(value: String) =
        _state.update { it.copy(password = value, passwordError = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** The route has navigated; a return to this screen must not navigate again. */
    fun consumeNavigation() = _state.update { it.copy(navigation = null, needsVerificationToken = null) }

    /** The route has opened the browser. */
    fun onGoogleAuthUrlOpened() = _state.update { it.copy(googleAuthUrl = null) }

    /**
     * No destination was wired for a pending step (a host app without the
     * security screens): say what the server asked for rather than nothing.
     */
    fun showNavigationFallback(navigation: LoginNavigation) = _state.update {
        it.copy(navigation = null, message = navigation.fallbackMessage())
    }

    fun submit() {
        val current = _state.value
        if (!current.identifier.contains('@')) {
            _state.value = current.copy(identifierError = "Enter a valid email address")
            return
        }

        _state.value = current.copy(isSubmitting = true, message = null)
        viewModelScope.launch {
            val result = authRepository.login(current.identifier.trim(), current.password)
            _state.update { state ->
                when (result) {
                    // Success does NOT mean signed in. Two of the five
                    // outcomes are HTTP 200 with no tokens, and one is a 403
                    // that is recoverable. Signed-in navigation is driven by
                    // SessionState; the pending outcomes route from here.
                    is AppResult.Success -> state.copy(isSubmitting = false).applySession(result.data)
                    is AppResult.Failure -> state.copy(
                        isSubmitting = false,
                        message = UsMessage(
                            text = AuthErrorText.forLogin(result.error),
                            type = result.error.messageType(),
                        ),
                    )
                }
            }
        }
    }

    /** Opens the Google consent page. The result arrives through [GoogleSignIn.pendingRedirect]. */
    fun signInWithGoogle() {
        val current = _state.value
        if (!current.googleSignInAvailable || current.isSubmitting || current.isGoogleSubmitting) return
        _state.update { it.copy(isGoogleSubmitting = true, message = null, googleAuthUrl = googleSignIn.begin()) }
    }

    /**
     * The screen resumed with no redirect in hand: the person came back from
     * the browser with Back or closed the tab. The browser's redirect is
     * delivered (onNewIntent) BEFORE the activity resumes, so a resume that
     * finds nothing pending and no exchange running is a genuine return.
     */
    fun cancelGoogleSignIn() = _state.update {
        val redirectPending = googleSignIn.pendingRedirect.value != null
        if (it.googleAuthUrl == null && !redirectPending && !exchanging) it.copy(isGoogleSubmitting = false) else it
    }

    @Volatile
    private var exchanging = false

    private suspend fun finishGoogleSignIn(redirect: String) {
        exchanging = true
        _state.update { it.copy(isGoogleSubmitting = true, googleAuthUrl = null, message = null) }
        val outcome = try {
            when (val token = googleSignIn.complete(redirect)) {
                is AppResult.Success ->
                    authRepository.signInWithOAuth(AuthRepository.OAUTH_PROVIDER_GOOGLE, token.data)
                is AppResult.Failure -> AppResult.Failure(token.error)
            }
        } finally {
            exchanging = false
        }
        _state.update { state ->
            val next = state.copy(isGoogleSubmitting = false)
            when (outcome) {
                is AppResult.Success -> when (val result = outcome.data) {
                    is OAuthSignInOutcome.SignedIn -> next.applySession(result.state)
                    is OAuthSignInOutcome.PendingSignup -> next.copy(
                        navigation = LoginNavigation.OAuthCompleteSignup(
                            pendingToken = result.pendingToken,
                            provider = result.provider,
                            email = result.email,
                            name = result.name,
                        ),
                    )
                }
                is AppResult.Failure -> when {
                    // Closing the consent page is not a failure to report.
                    outcome.error.isCancelled() -> next
                    else -> next.copy(
                        message = UsMessage(AuthErrorText.forOAuth(outcome.error), outcome.error.messageType()),
                    )
                }
            }
        }
    }

    private fun LoginUiState.applySession(session: SessionState): LoginUiState = when (session) {
        is SessionState.PendingTwoFactor -> copy(
            navigation = LoginNavigation.TwoFactor(userId = session.userId, pendingToken = session.pendingToken),
        )
        is SessionState.PendingStepUp -> copy(
            navigation = LoginNavigation.StepUp(pendingToken = session.pendingToken, methods = session.methods),
        )
        is SessionState.PendingVerification -> copy(
            needsVerificationToken = session.token.takeIf { it.isNotBlank() },
            message = if (session.token.isBlank()) {
                UsMessage("Your email isn't verified yet. Check your inbox to finish setting up.", UsMessageType.Warning)
            } else {
                null
            },
        )
        else -> this
    }

    private fun LoginNavigation.fallbackMessage(): UsMessage = when (this) {
        is LoginNavigation.TwoFactor -> UsMessage(
            "Enter the code from your authenticator app to finish signing in.",
            UsMessageType.Info,
        )
        is LoginNavigation.StepUp -> UsMessage(
            "This sign-in looks unusual. Confirm it via ${methods.humanList()}.",
            UsMessageType.Warning,
        )
        is LoginNavigation.OAuthCompleteSignup -> UsMessage(
            "Google couldn't confirm your email. Finish signing up on the web, then sign in here.",
            UsMessageType.Warning,
        )
    }

    /** "email or authenticator app", not "[email_otp, totp]". */
    private fun List<String>.humanList(): String {
        val friendly = map { method ->
            when (method) {
                AuthRepository.STEP_UP_METHOD_EMAIL -> "email"
                AuthRepository.STEP_UP_METHOD_TOTP -> "your authenticator app"
                "sms_otp" -> "SMS"
                else -> method.replace('_', ' ')
            }
        }
        return when (friendly.size) {
            0 -> "another method"
            1 -> friendly.first()
            else -> friendly.dropLast(1).joinToString(", ") + " or " + friendly.last()
        }
    }

    private fun AppError.isCancelled(): Boolean =
        this is AppError.AuthFailed && code == GoogleSignIn.CODE_CANCELLED

    private fun AppError.messageType(): UsMessageType = when (this) {
        is AppError.NoNetwork, is AppError.Timeout, is AppError.RateLimited ->
            UsMessageType.Warning
        else -> UsMessageType.Error
    }
}
