package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsDatePickerField
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.camera.PickedPhoto
import com.us.android.feature.doorsteppro.camera.rememberPhotoSource
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.field
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.KycFormats
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import com.us.android.feature.doorsteppro.domain.ProAgreement
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import com.us.android.feature.doorsteppro.ui.BottomAction
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LabeledValue
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.UploadField
import com.us.android.feature.doorsteppro.ui.UploadUi
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.longDateText
import com.us.android.feature.doorsteppro.upload.PhotoUploads
import com.us.android.feature.doorsteppro.upload.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.LocalDate
import javax.inject.Inject

// ── Bank ────────────────────────────────────────────────────────────────────

enum class BankField { HOLDER, ACCOUNT, CONFIRM, IFSC }

data class BankUiState(
    val holder: String = "",
    val account: String = "",
    val confirm: String = "",
    val ifsc: String = "",
    val errors: Map<BankField, String> = emptyMap(),
    /** What the server echoed (masked) — or what this device saved earlier: the number typed is never kept. */
    val saved: String? = null,
    val editing: Boolean = true,
    val saving: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * The payout account → `PUT /pro/me/bank`. The server seals the number and
 * answers with the last four digits only; this screen then shows the masked
 * account and forgets what was typed. Payouts are OFF (settlements are
 * computed only) — the account is checked and kept for later.
 */
@HiltViewModel
class BankViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val memory: OnboardingMemory,
) : ViewModel() {

    private val _state = MutableStateFlow(BankUiState(saved = memory.bankSummary(), editing = memory.bankSummary() == null))
    val state: StateFlow<BankUiState> = _state.asStateFlow()

    fun onHolder(v: String) = _state.update { it.copy(holder = v.take(MAX_NAME), errors = it.errors - BankField.HOLDER) }

    fun onAccount(v: String) = _state.update { it.copy(account = v.filter(Char::isDigit).take(MAX_DIGITS), errors = it.errors - BankField.ACCOUNT) }

    fun onConfirm(v: String) = _state.update { it.copy(confirm = v.filter(Char::isDigit).take(MAX_DIGITS), errors = it.errors - BankField.CONFIRM) }

    fun onIfsc(v: String) = _state.update { it.copy(ifsc = v.uppercase().take(IFSC_LENGTH), errors = it.errors - BankField.IFSC) }

    fun replace() = _state.update { it.copy(editing = true) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun save() {
        val s = _state.value
        val account = KycFormats.accountNumber(s.account)
        val ifsc = KycFormats.ifsc(s.ifsc)
        val errors = buildMap {
            if (s.holder.isBlank()) put(BankField.HOLDER, "Enter the name on the bank account")
            if (account == null) put(BankField.ACCOUNT, KycFormats.ACCOUNT_MESSAGE)
            if (account != null && s.confirm.trim() != account) put(BankField.CONFIRM, "The account numbers don't match")
            if (ifsc == null) put(BankField.IFSC, KycFormats.IFSC_MESSAGE)
        }
        if (errors.isNotEmpty() || account == null || ifsc == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        if (s.saving) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            when (val result = repository.saveBank(holder = s.holder.trim(), accountNumber = account, ifsc = ifsc)) {
                is ProResult.Success -> {
                    val masked = "${result.value.accountHolder} · ••••${result.value.accountLast4} · ${result.value.ifsc}"
                    memory.saveBankSummary(masked)
                    _state.value = BankUiState(saved = masked, editing = false)
                }
                is ProResult.Failure -> _state.update { it.copy(saving = false, errors = fieldError(result.error), message = result.error.asMessage()) }
            }
        }
    }

    private fun fieldError(error: ProError): Map<BankField, String> = when (error.field) {
        "ifsc" -> mapOf(BankField.IFSC to error.userMessage())
        "account_number" -> mapOf(BankField.ACCOUNT to error.userMessage())
        "account_holder" -> mapOf(BankField.HOLDER to error.userMessage())
        else -> emptyMap()
    }

    private companion object {
        const val MAX_NAME = 120
        const val MAX_DIGITS = 18
        const val IFSC_LENGTH = 11
    }
}

@Composable
fun BankStepScreen(onBack: () -> Unit, viewModel: BankViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    ProScreen(
        title = "Bank account",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.editing) {
                BottomAction(label = "Save account", onClick = viewModel::save, loading = state.saving)
            } else {
                BottomAction(label = "Done", onClick = onBack)
            }
        },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            val saved = state.saved
            if (saved != null && !state.editing) {
                ProCard {
                    CardHeading("Saved account", "Only the last four digits are ever shown.")
                    Text(saved, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                    UsPillButton(text = "Replace account", onClick = viewModel::replace, filled = false)
                }
            } else {
                ProCard {
                    CardHeading("Where your earnings go", "In your own name. Payouts start later; your earnings are recorded from your first job.")
                    UsTextField(value = state.holder, onValueChange = viewModel::onHolder, label = "Account holder name", errorText = state.errors[BankField.HOLDER])
                    UsTextField(
                        value = state.account,
                        onValueChange = viewModel::onAccount,
                        label = "Account number",
                        errorText = state.errors[BankField.ACCOUNT],
                        isPassword = true,
                        keyboardType = KeyboardType.NumberPassword,
                    )
                    UsTextField(
                        value = state.confirm,
                        onValueChange = viewModel::onConfirm,
                        label = "Re-enter account number",
                        errorText = state.errors[BankField.CONFIRM],
                        keyboardType = KeyboardType.Number,
                    )
                    UsTextField(value = state.ifsc, onValueChange = viewModel::onIfsc, label = "IFSC", placeholder = "SBIN0001234", errorText = state.errors[BankField.IFSC])
                }
            }
        }
    }
}

// ── PAN ─────────────────────────────────────────────────────────────────────

data class PanUiState(val pan: String = "", val error: String? = null, val saving: Boolean = false, val saved: Boolean = false, val message: UsMessage? = null)

/** PAN → `PUT /pro/me/pan`. Recommended, never required. */
@HiltViewModel
class PanViewModel @Inject constructor(private val repository: DoorstepProRepository) : ViewModel() {
    private val _state = MutableStateFlow(PanUiState())
    val state: StateFlow<PanUiState> = _state.asStateFlow()

    fun onPan(v: String) = _state.update { it.copy(pan = v.uppercase().filter(Char::isLetterOrDigit).take(PAN_LENGTH), error = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun save() {
        val pan = KycFormats.pan(_state.value.pan)
        if (pan == null) {
            _state.update { it.copy(error = KycFormats.PAN_MESSAGE) }
            return
        }
        if (_state.value.saving) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            when (val result = repository.savePan(pan)) {
                is ProResult.Success -> _state.update { it.copy(saving = false, saved = true) }
                is ProResult.Failure -> _state.update {
                    it.copy(saving = false, error = if (result.error.field == "pan") result.error.userMessage() else null, message = result.error.asMessage())
                }
            }
        }
    }

    private companion object {
        const val PAN_LENGTH = 10
    }
}

@Composable
fun PanStepScreen(onBack: () -> Unit, viewModel: PanViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.saved) { if (state.saved) onBack() }
    ProScreen(
        title = "PAN",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Save PAN", onClick = viewModel::save, loading = state.saving) },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(listPadding(padding))) {
            ProCard {
                CardHeading("Your PAN (recommended)", "Used for tax on your earnings. You can take jobs without it.")
                UsTextField(value = state.pan, onValueChange = viewModel::onPan, label = "PAN", placeholder = "ABCPE1234F", errorText = state.error)
            }
        }
    }
}

// ── Police clearance certificate ────────────────────────────────────────────

data class PoliceUiState(
    val photo: UploadUi = UploadUi.Idle,
    val issuedOn: String = "",
    val number: String = "",
    val submitting: Boolean = false,
    /** The certificate is with Doorstep (sent now, or already under review — 409). */
    val sent: Boolean = false,
    val expiresOn: String? = null,
    val message: UsMessage? = null,
)

/**
 * The background check at launch: a Police Clearance Certificate photo, its
 * issue date (not older than 12 months) and number → `POST
 * /pro/me/police-certificate`. An admin reviews it — the one manual step the
 * founder allowed — and it is valid 12 months from issue.
 */
@HiltViewModel
class PoliceCertificateViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val uploads: PhotoUploads,
    private val memory: OnboardingMemory,
) : ViewModel() {

    private val _state = MutableStateFlow(PoliceUiState())
    val state: StateFlow<PoliceUiState> = _state.asStateFlow()

    fun onPhoto(photo: PickedPhoto) {
        _state.update { it.copy(photo = UploadUi.Uploading(0f)) }
        viewModelScope.launch {
            when (val outcome = uploads.uploadImage(photo.uri) { p -> _state.update { it.copy(photo = UploadUi.Uploading(p)) } }) {
                is UploadOutcome.Ready -> _state.update { it.copy(photo = UploadUi.Uploaded(outcome.mediaId)) }
                is UploadOutcome.Failed -> _state.update { it.copy(photo = UploadUi.Failed(outcome.message)) }
            }
        }
    }

    fun onIssuedOn(date: String) = _state.update { it.copy(issuedOn = date) }

    fun onNumber(number: String) = _state.update { it.copy(number = number.take(MAX_NUMBER)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val s = _state.value
        val mediaId = (s.photo as? UploadUi.Uploaded)?.mediaId
        when {
            mediaId == null -> _state.update { it.copy(message = errorMessage("Add a photo of the certificate.")) }
            s.issuedOn.isBlank() -> _state.update { it.copy(message = errorMessage("Choose the date it was issued.")) }
            s.submitting -> Unit
            else -> {
                _state.update { it.copy(submitting = true) }
                viewModelScope.launch {
                    when (val result = repository.uploadPoliceCertificate(mediaId, s.issuedOn, s.number.trim().ifBlank { null })) {
                        is ProResult.Success -> {
                            memory.markSubmitted(OnboardingStep.POLICE_CERTIFICATE)
                            _state.update { it.copy(submitting = false, sent = true, expiresOn = result.value.expiresOn) }
                        }
                        is ProResult.Failure -> if (result.error.code == ProCodes.CONFLICT) {
                            memory.markSubmitted(OnboardingStep.POLICE_CERTIFICATE)
                            _state.update { it.copy(submitting = false, sent = true) }
                        } else {
                            _state.update { it.copy(submitting = false, message = result.error.asMessage()) }
                        }
                    }
                }
            }
        }
    }

    private companion object {
        const val MAX_NUMBER = 64
    }
}

@Composable
fun PoliceCertificateStepScreen(onBack: () -> Unit, viewModel: PoliceCertificateViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val source = rememberPhotoSource(onPicked = viewModel::onPhoto)
    ProScreen(
        title = "Police certificate",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.sent) {
                BottomAction(label = "Done", onClick = onBack)
            } else {
                BottomAction(label = "Send for review", onClick = viewModel::submit, loading = state.submitting)
            }
        },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            if (state.sent) {
                ProCard {
                    CardHeading("Sent for review", "Doorstep checks it, usually within two working days. We'll notify you.")
                    Pill("In review", Tone.Warning)
                    state.expiresOn?.let { LabeledValue("Valid until", longDateText(it)) }
                }
            } else {
                ProCard {
                    CardHeading(
                        "Police Clearance Certificate",
                        "From your local police station or the Telangana police portal, issued in the last 12 months.",
                    )
                    UploadField(label = "Certificate photo", state = state.photo, onPick = source.pickFromGallery)
                    Row { UsButton(text = "Take a photo", onClick = source.takePhoto, modifier = Modifier.fillMaxWidth()) }
                    UsDatePickerField(
                        value = state.issuedOn,
                        onValueChange = viewModel::onIssuedOn,
                        label = "Issued on",
                        minDate = LocalDate.now().minusMonths(MAX_AGE_MONTHS),
                        maxDate = LocalDate.now(),
                    )
                    UsTextField(value = state.number, onValueChange = viewModel::onNumber, label = "Certificate number (optional)")
                }
                InfoNote("It stays valid for 12 months from the issue date. We'll remind you before it runs out.")
            }
        }
    }
}

private const val MAX_AGE_MONTHS = 12L

// ── Agreement ───────────────────────────────────────────────────────────────

data class AgreementUiState(val accepting: Boolean = false, val accepted: Boolean = false, val outdated: Boolean = false, val message: UsMessage? = null)

/** The professional agreement → `POST /pro/me/agreement {version}`. A version the server no longer takes means the app is outdated. */
@HiltViewModel
class AgreementViewModel @Inject constructor(private val repository: DoorstepProRepository) : ViewModel() {
    private val _state = MutableStateFlow(AgreementUiState())
    val state: StateFlow<AgreementUiState> = _state.asStateFlow()

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun accept() {
        if (_state.value.accepting) return
        _state.update { it.copy(accepting = true) }
        viewModelScope.launch {
            when (val result = repository.acceptAgreement(ProAgreement.VERSION)) {
                is ProResult.Success -> _state.update { it.copy(accepting = false, accepted = true) }
                is ProResult.Failure -> _state.update {
                    val outdated = result.error.field == "version"
                    it.copy(
                        accepting = false,
                        outdated = outdated,
                        message = if (outdated) errorMessage("The agreement has changed. Update Doorstep Pro to read the new one.") else result.error.asMessage(),
                    )
                }
            }
        }
    }
}

@Composable
fun AgreementStepScreen(onBack: () -> Unit, viewModel: AgreementViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.accepted) { if (state.accepted) onBack() }
    ProScreen(
        title = "Professional agreement",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "I agree", onClick = viewModel::accept, loading = state.accepting, enabled = !state.outdated) },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            ProCard {
                CardHeading("Doorstep professional agreement", "Version ${ProAgreement.VERSION}")
                ProAgreement.POINTS.forEachIndexed { i, point ->
                    Text("${i + 1}. $point", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                }
            }
        }
    }
}
