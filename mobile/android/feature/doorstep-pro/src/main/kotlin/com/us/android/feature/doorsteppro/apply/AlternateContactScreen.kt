package com.us.android.feature.doorsteppro.apply

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.*
import com.us.android.feature.doorsteppro.ui.*
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import javax.inject.Inject

data class AlternateContactState(val application: PartnerApplicationDto? = null, val loading: Boolean = true, val busy: Boolean = false, val code: String = "", val sent: Boolean = false, val verified: Boolean = false, val cooldown: Int = 0, val development: Boolean = false, val message: UsMessage? = null)
@HiltViewModel
class AlternateContactViewModel @Inject constructor(private val repo: DoorstepProRepository) : ViewModel() {
    private val mutable = MutableStateFlow(AlternateContactState())
    val state = mutable.asStateFlow()
    init { load() }
    fun load() { viewModelScope.launch { when (val r = repo.me()) {
        is ProResult.Success -> mutable.update { it.copy(loading = false, application = r.value.application, verified = r.value.application?.alternatePhoneVerifiedAt != null) }
        is ProResult.Failure -> mutable.update { it.copy(loading = false, message = r.error.asMessage()) }
    } } }
    fun code(v: String) { if (!state.value.busy) mutable.update { it.copy(code = v.filter(Char::isDigit).take(6)) } }
    fun request() {
        if (state.value.busy || state.value.cooldown > 0) return
        mutable.update { it.copy(busy = true, message = null) }
        viewModelScope.launch { when (val r = repo.requestAlternatePhone()) {
            is ProResult.Failure -> mutable.update { it.copy(busy = false, message = r.error.asMessage()) }
            is ProResult.Success -> {
                mutable.update { it.copy(busy = false, sent = true, code = "", cooldown = r.value.resendAfter, development = r.value.delivery == "development_log") }
                while (state.value.cooldown > 0) { delay(1000); mutable.update { it.copy(cooldown = (it.cooldown - 1).coerceAtLeast(0)) } }
            }
        } }
    }
    fun verify() {
        val s = state.value
        if (s.busy || !Regex("[0-9]{6}").matches(s.code)) return
        mutable.update { it.copy(busy = true, message = null) }
        viewModelScope.launch { when (val r = repo.verifyAlternatePhone(s.code)) {
            is ProResult.Failure -> mutable.update { it.copy(busy = false, message = r.error.asMessage()) }
            is ProResult.Success -> mutable.update { it.copy(busy = false, verified = r.value.verified, code = "") }
        } }
    }
}

@Composable
fun AlternateContactScreen(onBack: () -> Unit, vm: AlternateContactViewModel = hiltViewModel()) {
    val s by vm.state.collectAsStateWithLifecycle()
    ProScreen(title = "Alternate contact", onBack = onBack, message = s.message) { padding ->
        Column(Modifier.fillMaxSize().padding(listPadding(padding)), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            when {
                s.loading -> Text("Loading contact…")
                s.application == null -> UsPillButton("Try again", vm::load)
                s.application?.alternatePhoneLast4 == null -> Text("No alternate contact was added to your application.")
                s.verified -> Text("Alternate contact verified · ••••${s.application?.alternatePhoneLast4}")
                else -> {
                    Text("Mobile ending ${s.application?.alternatePhoneLast4}")
                    if (s.development) Text("Development testing: the code is in server logs. No SMS was sent.")
                    if (s.sent) {
                        UsTextField(s.code, vm::code, label = "Verification code", keyboardType = KeyboardType.Number)
                        UsButton(text = "Verify contact", onClick = vm::verify, enabled = !s.busy && s.code.length == 6)
                    }
                    UsButton(text = if (s.cooldown > 0) "Resend in ${s.cooldown}s" else if (s.sent) "Resend code" else "Send code", onClick = vm::request, enabled = !s.busy && s.cooldown == 0)
                }
            }
        }
    }
}
