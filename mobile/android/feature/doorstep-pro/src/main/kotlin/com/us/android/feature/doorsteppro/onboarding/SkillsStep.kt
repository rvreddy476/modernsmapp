package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsDatePickerField
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.camera.PickedPhoto
import com.us.android.feature.doorsteppro.camera.rememberPhotoSource
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.ProSkillDto
import com.us.android.feature.doorsteppro.data.SkillDto
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import com.us.android.feature.doorsteppro.ui.BottomAction
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.ChoiceRow
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.MessagePane
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.SectionLabel
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.UploadField
import com.us.android.feature.doorsteppro.ui.UploadUi
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.successMessage
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

/** A trade certificate being filled in for one skill. */
data class CertificateDraft(
    val skillCode: String,
    val photo: UploadUi = UploadUi.Idle,
    val issuedOn: String = "",
    val number: String = "",
    val submitting: Boolean = false,
)

data class SkillsUiState(
    val loading: Boolean = true,
    val catalogue: List<SkillDto> = emptyList(),
    val selected: Set<String> = emptySet(),
    /** The server's answer to the last declaration. */
    val declared: List<ProSkillDto> = emptyList(),
    /** Skills this device sent a certificate for (under review). */
    val certificatesSent: Set<String> = emptySet(),
    val certificate: CertificateDraft? = null,
    val saving: Boolean = false,
    val error: String? = null,
    val message: UsMessage? = null,
) {
    /** Declared skills that need a certificate and are not verified yet. */
    val needingCertificate: List<SkillDto>
        get() = catalogue.filter { skill ->
            skill.requiresCertificate && skill.code in selected &&
                declared.none { it.skillCode == skill.code && it.status == VERIFIED }
        }

    companion object {
        const val VERIFIED = "verified"
    }
}

/**
 * Skills: `GET /pro/skills`, `PUT /pro/me/skills {skill_codes}` (a skill
 * with no certificate is verified on declaration; a women's or men's salon
 * skill the DigiLocker gender does not allow is refused, 403
 * DOORSTEP_GENDER_RULE), then `POST /pro/me/skills/{code}/certificate` for
 * each declared skill that requires a trade certificate (an admin verifies it).
 * The contract has no GET for my declared skills, so this device remembers
 * what it declared.
 */
@HiltViewModel
class SkillsViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val uploads: PhotoUploads,
    private val memory: OnboardingMemory,
) : ViewModel() {

    private val _state = MutableStateFlow(SkillsUiState(selected = memory.declaredSkills(), certificatesSent = memory.certificateSkills()))
    val state: StateFlow<SkillsUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            when (val result = repository.skillCatalogue()) {
                is ProResult.Success -> _state.update { it.copy(loading = false, catalogue = result.value, error = null) }
                is ProResult.Failure -> _state.update { it.copy(loading = false, error = result.error.userMessage()) }
            }
        }
    }

    fun toggle(code: String) = _state.update {
        it.copy(selected = if (code in it.selected) it.selected - code else it.selected + code)
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun save() {
        val codes = _state.value.selected
        if (codes.isEmpty()) {
            _state.update { it.copy(message = errorMessage("Choose at least one skill.")) }
            return
        }
        if (_state.value.saving) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            when (val result = repository.declareSkills(codes.sorted())) {
                is ProResult.Success -> {
                    memory.saveDeclaredSkills(codes)
                    _state.update { it.copy(saving = false, declared = result.value, message = successMessage("Skills saved.")) }
                }
                is ProResult.Failure -> {
                    val text = if (result.error.code == ProCodes.GENDER_RULE) {
                        "A salon skill you chose is only for professionals of another gender (from your Aadhaar). Remove it to continue."
                    } else {
                        result.error.userMessage()
                    }
                    _state.update { it.copy(saving = false, message = errorMessage(text)) }
                }
            }
        }
    }

    fun openCertificate(code: String) = _state.update { it.copy(certificate = CertificateDraft(skillCode = code)) }

    fun closeCertificate() = _state.update { it.copy(certificate = null) }

    fun onCertificatePhoto(photo: PickedPhoto) {
        updateDraft { it.copy(photo = UploadUi.Uploading(0f)) }
        viewModelScope.launch {
            when (val outcome = uploads.uploadImage(photo.uri) { p -> updateDraft { it.copy(photo = UploadUi.Uploading(p)) } }) {
                is UploadOutcome.Ready -> updateDraft { it.copy(photo = UploadUi.Uploaded(outcome.mediaId)) }
                is UploadOutcome.Failed -> updateDraft { it.copy(photo = UploadUi.Failed(outcome.message)) }
            }
        }
    }

    fun onIssuedOn(date: String) = updateDraft { it.copy(issuedOn = date) }

    fun onNumber(number: String) = updateDraft { it.copy(number = number.take(MAX_NUMBER)) }

    fun submitCertificate() {
        val draft = _state.value.certificate ?: return
        val mediaId = (draft.photo as? UploadUi.Uploaded)?.mediaId
        when {
            mediaId == null -> _state.update { it.copy(message = errorMessage("Add a photo of the certificate.")) }
            draft.issuedOn.isBlank() -> _state.update { it.copy(message = errorMessage("Choose the date it was issued.")) }
            draft.submitting -> Unit
            else -> {
                updateDraft { it.copy(submitting = true) }
                viewModelScope.launch {
                    val result = repository.uploadTradeCertificate(draft.skillCode, mediaId, draft.issuedOn, draft.number.trim().ifBlank { null })
                    when (result) {
                        is ProResult.Success -> {
                            memory.markCertificate(draft.skillCode)
                            memory.markSubmitted(OnboardingStep.SKILLS)
                            _state.update {
                                it.copy(
                                    certificate = null,
                                    certificatesSent = it.certificatesSent + draft.skillCode,
                                    message = successMessage("Certificate sent. Doorstep reviews it."),
                                )
                            }
                        }
                        is ProResult.Failure -> {
                            // 409: a certificate for this skill is already under review — the same thing, as far as the professional is concerned.
                            if (result.error.code == ProCodes.CONFLICT) memory.markCertificate(draft.skillCode)
                            updateDraft { it.copy(submitting = false) }
                            _state.update { it.copy(message = result.error.asMessage()) }
                        }
                    }
                }
            }
        }
    }

    private fun updateDraft(transform: (CertificateDraft) -> CertificateDraft) =
        _state.update { s -> s.copy(certificate = s.certificate?.let(transform)) }

    private companion object {
        const val MAX_NUMBER = 64
    }
}

@Composable
fun SkillsStepScreen(onBack: () -> Unit, viewModel: SkillsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val draft = state.certificate
    if (draft != null) {
        CertificateScreen(draft, state, viewModel)
        return
    }
    ProScreen(
        title = "Your skills",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Save skills", onClick = viewModel::save, loading = state.saving, enabled = !state.loading) },
    ) { padding ->
        when {
            state.loading -> LoadingPane()
            state.error != null -> MessagePane(title = "Couldn't load skills", body = state.error.orEmpty(), primaryLabel = "Try again", onPrimary = viewModel::load)
            else -> LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding)) {
                item {
                    InfoNote("Choose the work you are trained for. Skills marked \"certificate\" are verified by Doorstep from a trade certificate.")
                }
                item { SectionLabel("Skills") }
                items(state.catalogue, key = { it.code }) { skill ->
                    val selected = skill.code in state.selected
                    ChoiceRow(selected = selected, onClick = { viewModel.toggle(skill.code) }, modifier = Modifier.fillMaxWidth().padding(bottom = UsTheme.spacing.m)) {
                        Checkbox(
                            checked = selected,
                            onCheckedChange = { viewModel.toggle(skill.code) },
                            colors = CheckboxDefaults.colors(checkedColor = UsTheme.extended.accentSolid),
                        )
                        Spacer(Modifier.width(UsTheme.spacing.s))
                        Column(Modifier.weight(1f)) {
                            Text(skill.name, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                            if (skill.description.isNotBlank()) {
                                Text(skill.description, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                            }
                        }
                        val status = state.declared.firstOrNull { it.skillCode == skill.code }?.status
                        when {
                            status == SkillsUiState.VERIFIED -> Pill("Verified", Tone.Positive)
                            skill.code in state.certificatesSent -> Pill("In review", Tone.Warning)
                            skill.requiresCertificate -> Pill("Certificate", Tone.Neutral)
                        }
                    }
                }
                if (state.needingCertificate.isNotEmpty()) {
                    item { SectionLabel("Trade certificates") }
                    items(state.needingCertificate, key = { "cert-" + it.code }) { skill ->
                        ProCard(modifier = Modifier.padding(bottom = UsTheme.spacing.m)) {
                            CardHeading(skill.name, "Upload your trade certificate. Doorstep verifies the skill from it.")
                            if (skill.code in state.certificatesSent) {
                                Pill("Sent · in review", Tone.Warning)
                            } else {
                                UsButton(text = "Upload certificate", onClick = { viewModel.openCertificate(skill.code) }, modifier = Modifier.fillMaxWidth())
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun CertificateScreen(draft: CertificateDraft, state: SkillsUiState, viewModel: SkillsViewModel) {
    val source = rememberPhotoSource(onPicked = viewModel::onCertificatePhoto)
    val skillName = state.catalogue.firstOrNull { it.code == draft.skillCode }?.name ?: draft.skillCode
    ProScreen(
        title = "Trade certificate",
        onBack = viewModel::closeCertificate,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Send for review", onClick = viewModel::submitCertificate, loading = draft.submitting) },
    ) { padding ->
        LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding)) {
            item {
                ProCard {
                    CardHeading(skillName, "A clear photo of the whole certificate, with your name visible.")
                    UploadField(label = "Certificate photo", state = draft.photo, onPick = source.pickFromGallery)
                    Row { UsButton(text = "Take a photo", onClick = source.takePhoto, modifier = Modifier.fillMaxWidth()) }
                    UsDatePickerField(
                        value = draft.issuedOn,
                        onValueChange = viewModel::onIssuedOn,
                        label = "Issued on",
                        maxDate = LocalDate.now(),
                    )
                    UsTextField(value = draft.number, onValueChange = viewModel::onNumber, label = "Certificate number (optional)")
                }
            }
        }
    }
}
