package com.us.android.feature.auth.partner

import android.os.SystemClock
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.*
import com.us.android.core.auth.dto.*
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.feature.auth.login.LoginNavigation
import com.us.android.feature.auth.stepup.StepUpRoute
import com.us.android.feature.auth.twofactor.TwoFactorRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

data class PartnerPhoneState(
    val register: Boolean = false,
    val first: String = "",
    val last: String = "",
    val gender: String = "",
    val phone: String = "",
    val consent: Boolean = false,
    val code: String = "",
    val challenge: PartnerPhoneChallengeDto? = null,
    val busy: Boolean = false,
    val cooldown: Int = 0,
    val error: String? = null,
    /** The code was right but the server wants a second step first (`requires_2fa` / `requires_step_up`). */
    val pendingStep: LoginNavigation? = null,
)

/**
 * Where a verified code goes when it is not yet a session — the same mapping
 * as `LoginViewModel.applySession`, so a partner with 2FA or an unusual
 * sign-in can finish instead of being stuck on the code screen (L7).
 */
internal fun partnerSignInStep(session: SessionState): LoginNavigation? = when (session) {
    is SessionState.PendingTwoFactor -> LoginNavigation.TwoFactor(
        userId = session.userId,
        pendingToken = session.pendingToken,
        method = session.method,
        emailMasked = session.emailMasked,
    )
    is SessionState.PendingStepUp -> LoginNavigation.StepUp(
        pendingToken = session.pendingToken,
        methods = session.methods,
    )
    else -> null
}

private const val ADDITIONAL_VERIFICATION_TEXT =
    "Additional account verification is required. Complete it before continuing."

fun partnerPhoneError(s: PartnerPhoneState): String? = when {
    !Regex("[6-9][0-9]{9}").matches(s.phone) -> "Enter a valid 10-digit mobile number."
    s.register && (s.first.isBlank() || s.last.isBlank()) -> "Enter your first and last names."
    s.register && s.gender !in listOf("male", "female", "others") -> "Select your gender."
    s.register && !s.consent -> "Accept the terms and privacy policy to continue."
    else -> null
}

@HiltViewModel
class PartnerPhoneViewModel @Inject constructor(private val auth: AuthRepository) : ViewModel() {
    private val mutable = MutableStateFlow(PartnerPhoneState())
    val state = mutable.asStateFlow()
    private val deviceId = UUID.randomUUID().toString()
    private var cooldownJob: Job? = null

    fun changeNumber() {
        if (mutable.value.busy) return
        cooldownJob?.cancel()
        mutable.value = mutable.value.copy(challenge = null, code = "", cooldown = 0, error = null)
    }

    fun switchMode() {
        if (mutable.value.busy) return
        cooldownJob?.cancel()
        mutable.value = PartnerPhoneState(register = !mutable.value.register)
    }
    fun edit(update: (PartnerPhoneState) -> PartnerPhoneState) {
        if (!mutable.value.busy) mutable.value = update(mutable.value).copy(error = null)
    }
    fun request() {
        val s = mutable.value
        if (s.busy || s.cooldown > 0) return
        partnerPhoneError(s)?.let { mutable.value = s.copy(error = it); return }
        mutable.value = s.copy(busy = true, error = null)
        viewModelScope.launch {
            when (val r = auth.requestPartnerPhone(PartnerPhoneRequestDto(
                if (s.register) "register" else "login", "+91${s.phone}", s.first.trim(), s.last.trim(),
                s.gender, s.consent, RegistrationRules.TERMS_VERSION,
            ))) {
                is AppResult.Failure -> mutable.value = s.copy(error = AuthErrorText.forLogin(r.error))
                is AppResult.Success -> {
                    mutable.value = s.copy(challenge = r.data, code = "", cooldown = r.data.resendAfter)
                    cooldownJob?.cancel()
                    val deadline = SystemClock.elapsedRealtime() + r.data.resendAfter.coerceAtLeast(0) * 1000L
                    cooldownJob = viewModelScope.launch {
                        while (mutable.value.cooldown > 0) {
                            delay(1000)
                            mutable.value = mutable.value.copy(cooldown = resendSeconds(deadline, SystemClock.elapsedRealtime()))
                        }
                    }
                }
            }
        }
    }
    fun verify() {
        val s = mutable.value
        val challenge = s.challenge ?: return
        if (s.busy) return
        if (!Regex("[0-9]{6}").matches(s.code)) { mutable.value = s.copy(error = "Enter the six-digit code."); return }
        mutable.value = s.copy(busy = true, error = null)
        viewModelScope.launch {
            when (val r = auth.verifyPartnerPhone(challenge.challengeId, s.code, deviceId)) {
                // A wrong or expired code is 401 AUTH_FAILED: say so about the
                // code, not about an email and password nobody typed here.
                is AppResult.Failure -> mutable.value = mutable.value.copy(
                    busy = false,
                    error = AuthErrorText.forPhoneCode(r.error),
                )
                is AppResult.Success -> {
                    val step = partnerSignInStep(r.data)
                    val stuck = r.data !is SessionState.Authenticated && step == null
                    mutable.value = mutable.value.copy(
                        busy = false,
                        code = "",
                        pendingStep = step,
                        error = if (stuck) ADDITIONAL_VERIFICATION_TEXT else null,
                    )
                }
            }
        }
    }

    /** Back from the 2FA / step-up screen: start again from the number. */
    fun leavePendingStep() {
        cooldownJob?.cancel()
        mutable.value = mutable.value.copy(pendingStep = null, challenge = null, code = "", cooldown = 0, error = null)
    }
}

@Composable
fun PartnerPhoneRoute(viewModel: PartnerPhoneViewModel = hiltViewModel()) {
    val s by viewModel.state.collectAsStateWithLifecycle()
    val uri = androidx.compose.ui.platform.LocalUriHandler.current
    // The second step is drawn in place: the partner apps host this route
    // with no nav graph of their own. Success needs no navigation — the
    // session flips to Authenticated and the host swaps the whole screen.
    when (val step = s.pendingStep) {
        is LoginNavigation.TwoFactor -> TwoFactorRoute(
            userId = step.userId,
            pendingToken = step.pendingToken,
            method = step.method,
            emailMasked = step.emailMasked,
            onBackToLogin = viewModel::leavePendingStep,
        )
        is LoginNavigation.StepUp -> StepUpRoute(
            pendingToken = step.pendingToken,
            methods = step.methods,
            onBackToLogin = viewModel::leavePendingStep,
        )
        else -> PartnerPhoneScreen(s, viewModel::edit, viewModel::request, viewModel::verify, viewModel::changeNumber,
            viewModel::switchMode, onPolicy = { uri.openUri("https://cleestudio.com/$it") })
    }
}

internal fun resendSeconds(deadline: Long, now: Long): Int = ((deadline - now).coerceAtLeast(0) + 999).div(1000).toInt()

@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun PartnerPhoneScreen(
    s: PartnerPhoneState, edit: ((PartnerPhoneState) -> PartnerPhoneState) -> Unit,
    request: () -> Unit, verify: () -> Unit, changeNumber: () -> Unit,
    switchMode: () -> Unit, onPolicy: (String) -> Unit,
) {
    var help by remember { mutableStateOf(false) }
    UsScaffold(contentMaxWidth = 560.dp) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(vertical = UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxxxl)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (s.challenge != null) IconButton(changeNumber, enabled = !s.busy) { Icon(UsIcons.Back, "Change number") }
                Column {
                    Text("Cleestudio Partner", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
                    Text("One account. Multiple businesses.", color = UsTheme.extended.textSecondary)
                }
            }
            UsFeatureBanner(if (s.challenge != null) "You're almost in!" else "More ways to work",
                "Secure access to all your businesses.", UsIcons.Phone)
            UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text(if (s.challenge != null) "Verify your mobile number" else if (s.register) "Create your partner account" else "Welcome back",
                    style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                if (s.challenge == null) {
                    if (s.register) {
                        UsTextField(s.first, { v -> edit { it.copy(first = v.take(50)) } }, "First name", enabled = !s.busy)
                        UsTextField(s.last, { v -> edit { it.copy(last = v.take(50)) } }, "Last name", enabled = !s.busy)
                        Text("Gender", style = MaterialTheme.typography.labelLarge)
                        FlowRow { listOf("male" to "Male", "female" to "Female", "others" to "Other").forEach { (value, label) ->
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                RadioButton(s.gender == value, { edit { it.copy(gender = value) } }, enabled = !s.busy); Text(label)
                            }
                        } }
                    }
                    UsTextField(s.phone, { v -> edit { it.copy(phone = v.filter { c -> c in '0'..'9' }.take(10)) } },
                        "Mobile number", placeholder = "10-digit number", enabled = !s.busy, keyboardType = KeyboardType.Phone)
                    Text("Country code +91", style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textSecondary)
                    if (s.register) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Checkbox(s.consent, { v -> edit { it.copy(consent = v) } }, enabled = !s.busy)
                            Text("I accept the terms and privacy policy.", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
                        }
                        Row { TextButton({ onPolicy("terms") }) { Text("Terms") }; TextButton({ onPolicy("privacy") }) { Text("Privacy") } }
                    }
                    s.error?.let { Text(it, color = UsTheme.extended.statusDanger) }
                    UsButton("Continue", request, Modifier.fillMaxWidth(), loading = s.busy)
                    TextButton(switchMode, enabled = !s.busy) { Text(if (s.register) "Already registered? Sign in" else "Create a partner account") }
                } else {
                    Text(if (s.challenge.delivery == "development_log") "Enter the six-digit development code for" else "Enter the six-digit code sent to",
                        color = UsTheme.extended.textSecondary)
                    Text("+91 •••••• ${s.phone.takeLast(4)}", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                    if (s.challenge.delivery == "development_log") Text("Development testing: the code is in server logs. No SMS was sent.", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary)
                    Spacer(Modifier.height(UsTheme.spacing.m))
                    UsOtpField(s.code, { v -> edit { it.copy(code = v) } }, enabled = !s.busy, errorText = s.error)
                    UsButton("Verify & continue", verify, Modifier.fillMaxWidth(), enabled = s.code.length == 6, loading = s.busy)
                    HorizontalDivider(color = UsTheme.extended.borderSubtle)
                    Text("Didn't receive the code?", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                        TextButton(request, enabled = !s.busy && s.cooldown == 0) {
                            Icon(UsIcons.RotateCw, null); Spacer(Modifier.width(UsTheme.spacing.m)); Text(if (s.cooldown > 0) "Resend in ${s.cooldown}s" else "Resend code")
                        }
                        TextButton(changeNumber, enabled = !s.busy) { Icon(UsIcons.Pencil, null); Spacer(Modifier.width(UsTheme.spacing.m)); Text("Change number") }
                    }
                }
            }
            UsSurfaceCard(Modifier.fillMaxWidth(), onClick = { help = true }) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                    Icon(UsIcons.CircleHelp, null, tint = UsTheme.extended.accentSolid)
                    Text("Need help signing in?", Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
                    Icon(UsIcons.ChevronRight, null)
                }
            }
        }
    }
    if (help) AlertDialog(onDismissRequest = { help = false }, title = { Text("Sign-in help") },
        text = { Text("Use the mobile number linked to your partner account. Check the number, then request another code when the timer ends. Never share your verification code.") },
        confirmButton = { TextButton({ help = false }) { Text("Got it") } })
}
