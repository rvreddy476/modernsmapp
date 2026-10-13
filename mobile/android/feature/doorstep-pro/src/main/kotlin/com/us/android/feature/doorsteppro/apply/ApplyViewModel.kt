package com.us.android.feature.doorsteppro.apply

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.doorsteppro.camera.PickedPhoto
import com.us.android.feature.doorsteppro.data.*
import com.us.android.feature.doorsteppro.onboarding.BankFormData
import com.us.android.feature.doorsteppro.ui.*
import com.us.android.feature.doorsteppro.upload.PhotoUploads
import com.us.android.feature.doorsteppro.upload.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ApplyUiState(
    val step: Int = 0, val first: String = "", val last: String = "", val gender: String = "",
    val alternate: String = "", val stateCode: String = "", val town: String = "", val search: String = "",
    val skills: List<String> = emptyList(), val catalogue: List<SkillDto> = emptyList(), val states: List<ApplicationStateDto> = emptyList(),
    val pan: String = "", val identityKind: String = "passport", val identity: UploadUi = UploadUi.Idle,
    val experience: UploadUi = UploadUi.Idle, val bank: BankFormData = BankFormData(), val consent: Boolean = false,
    val selfie: UploadUi = UploadUi.Idle,
    val errors: Map<String, String> = emptyMap(), val submitting: Boolean = false, val done: Boolean = false,
    val loading: Boolean = true, val message: UsMessage? = null,
) {
    fun errorsFor(index: Int): Map<String, String> = buildMap {
        when (index) {
            0 -> {
                if (first.trim().isEmpty()) put("first", "Enter your first name")
                if (last.trim().isEmpty()) put("last", "Enter your last name")
                if (gender !in listOf("male", "female", "others")) put("gender", "Choose your gender")
                if (alternate.isNotBlank() && !Regex("[6-9][0-9]{9}").matches(alternate)) put("alternate", "Enter a valid 10-digit mobile number")
            }
            1 -> {
                if (skills.isEmpty() || skills.size > 20 || skills.any { code -> catalogue.none { it.code == code } }) put("skills", "Choose 1–20 services")
                if (states.none { it.code == stateCode }) put("state", "Choose your state")
                if (town.trim().length !in 2..100) put("town", "Enter your town")
            }
            2 -> {
                if (!Regex("[A-Z]{3}P[A-Z][0-9]{4}[A-Z]").matches(pan)) put("pan", "Enter your individual PAN")
                if (identity !is UploadUi.Uploaded) put("identity", "Upload your identity proof")
                if (selfie !is UploadUi.Uploaded) put("selfie", "Add a clear selfie")
                if (experience is UploadUi.Uploading || experience is UploadUi.Failed) put("experience", "Complete or remove the certificate upload")
            }
            3 -> putAll(bank.errors())
            4 -> if (!consent) put("consent", "Confirm your details and consent to verification")
        }
    }
    override fun toString() = "ApplyUiState(step=$step, redacted)"
}

@HiltViewModel
class ApplyViewModel @Inject constructor(private val repository: DoorstepProRepository, private val uploads: PhotoUploads) : ViewModel() {
    private val mutable = MutableStateFlow(ApplyUiState())
    val state = mutable.asStateFlow()
    private val versions = mutableMapOf<String, Int>()
    init { loadOptions() }
    fun loadOptions() { viewModelScope.launch {
        mutable.update { it.copy(loading = true) }
        when (val states = repository.applicationStates()) {
            is ProResult.Success -> mutable.update { it.copy(states = states.value.sortedBy { item -> item.name }) }
            is ProResult.Failure -> mutable.update { it.copy(message = states.error.asMessage()) }
        }
        when (val skills = repository.skillCatalogue()) {
            is ProResult.Success -> mutable.update { it.copy(catalogue = skills.value) }
            is ProResult.Failure -> mutable.update { it.copy(message = skills.error.asMessage()) }
        }
        mutable.update { it.copy(loading = false) }
    } }
    fun edit(change: (ApplyUiState) -> ApplyUiState) {
        if (state.value.submitting) return
        val previous = state.value
        val next = change(previous)
        if (next.identityKind != previous.identityKind) versions["identity"] = (versions["identity"] ?: 0) + 1
        if (next.bank.proofKind != previous.bank.proofKind) versions["bank"] = (versions["bank"] ?: 0) + 1
        mutable.value = next.copy(errors = emptyMap())
    }
    fun back() = edit { it.copy(step = (it.step - 1).coerceAtLeast(0)) }
    fun dismissMessage() = mutable.update { it.copy(message = null) }
    fun photo(kind: String, photo: PickedPhoto) {
        if (state.value.submitting) return
        val version = (versions[kind] ?: 0) + 1; versions[kind] = version
        fun set(value: UploadUi) { if (versions[kind] == version) mutable.update { when (kind) {
            "identity" -> it.copy(identity = value)
            "selfie" -> it.copy(selfie = value)
            "experience" -> it.copy(experience = value)
            else -> it.copy(bank = it.bank.copy(proof = value))
        } } }
        set(UploadUi.Uploading(0f))
        viewModelScope.launch { set(when (val result = uploads.uploadImage(photo.uri) { set(UploadUi.Uploading(it)) }) {
            is UploadOutcome.Ready -> UploadUi.Uploaded(result.mediaId)
            is UploadOutcome.Failed -> UploadUi.Failed(result.message)
        }) }
    }
    fun removeExperience() { versions["experience"] = (versions["experience"] ?: 0) + 1; edit { it.copy(experience = UploadUi.Idle) } }
    fun submit() {
        val s = state.value
        if (s.submitting || s.loading) return
        val indices = if (s.step == 4) 0..4 else s.step..s.step
        for (index in indices) { val errors = s.errorsFor(index); if (errors.isNotEmpty()) { mutable.update { it.copy(step = index, errors = errors) }; return } }
        if (s.step < 4) { mutable.update { it.copy(step = it.step + 1, errors = emptyMap()) }; return }
        mutable.update { it.copy(submitting = true) }
        viewModelScope.launch {
            val request = PartnerApplicationRequest(true, s.first.trim(), s.last.trim(), s.gender, s.stateCode, s.town.trim(), s.alternate, s.skills, s.identityKind, (s.identity as UploadUi.Uploaded).mediaId, (s.experience as? UploadUi.Uploaded)?.mediaId.orEmpty(), s.pan, s.bank.request())
            when (val result = repository.submitApplication(request.copy(selfieMediaId = (s.selfie as UploadUi.Uploaded).mediaId))) {
                is ProResult.Success -> mutable.value = ApplyUiState(done = true, loading = false)
                is ProResult.Failure -> mutable.update { it.copy(submitting = false, message = result.error.asMessage()) }
            }
        }
    }
}
