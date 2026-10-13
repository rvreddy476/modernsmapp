package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.camera.PickedPhoto
import com.us.android.feature.doorsteppro.camera.rememberPhotoSource
import com.us.android.feature.doorsteppro.data.*
import com.us.android.feature.doorsteppro.ui.*
import com.us.android.feature.doorsteppro.upload.PhotoUploads
import com.us.android.feature.doorsteppro.upload.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import javax.inject.Inject

data class BankUiState(val form: BankFormData = BankFormData(), val errors: Map<String, String> = emptyMap(), val saved: PayoutAccountDto? = null, val loading: Boolean = true, val editing: Boolean = false, val saving: Boolean = false, val message: UsMessage? = null)

@HiltViewModel
class BankViewModel @Inject constructor(private val repository: DoorstepProRepository, private val uploads: PhotoUploads) : ViewModel() {
    private val mutable = MutableStateFlow(BankUiState())
    val state = mutable.asStateFlow()
    private var proofVersion = 0
    init { load() }
    fun load() { viewModelScope.launch {
        when (val result = repository.bank()) {
            is ProResult.Success -> mutable.update { it.copy(saved = result.value, loading = false, editing = result.value == null || result.value.status == "failed" || result.value.proofDocumentId == null) }
            is ProResult.Failure -> mutable.update { it.copy(loading = false, message = result.error.asMessage()) }
        }
    } }
    fun edit(value: BankFormData) { if (state.value.saving) return; if (value.proofKind != state.value.form.proofKind) proofVersion++; mutable.update { it.copy(form = value, errors = emptyMap()) } }
    fun replace() = mutable.update { it.copy(editing = true) }
    fun dismissMessage() = mutable.update { it.copy(message = null) }
    fun onPhoto(photo: PickedPhoto) {
        if (state.value.saving) return
        val version = ++proofVersion
        mutable.update { it.copy(form = it.form.copy(proof = UploadUi.Uploading(0f))) }
        viewModelScope.launch {
            val result = uploads.uploadImage(photo.uri) { progress -> if (version == proofVersion) mutable.update { it.copy(form = it.form.copy(proof = UploadUi.Uploading(progress))) } }
            if (version == proofVersion) mutable.update { it.copy(form = it.form.copy(proof = when (result) { is UploadOutcome.Ready -> UploadUi.Uploaded(result.mediaId); is UploadOutcome.Failed -> UploadUi.Failed(result.message) })) }
        }
    }
    fun save() {
        val s = state.value
        if (s.saving) return
        val errors = s.form.errors()
        if (errors.isNotEmpty()) { mutable.update { it.copy(errors = errors) }; return }
        mutable.update { it.copy(saving = true) }
        viewModelScope.launch { when (val result = repository.saveBank(s.form.request())) {
            is ProResult.Success -> mutable.value = BankUiState(saved = result.value, loading = false)
            is ProResult.Failure -> mutable.update { it.copy(saving = false, message = result.error.asMessage()) }
        } }
    }
}

@Composable
fun BankStepScreen(onBack: () -> Unit, viewModel: BankViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val photo = rememberPhotoSource(viewModel::onPhoto)
    ProScreen(title = "Bank details", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(if (state.editing) "Submit for verification" else "Done", if (state.editing) viewModel::save else onBack, loading = state.saving, enabled = !state.loading) }) { padding ->
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            if (state.loading) Text("Loading bank details…")
            state.saved?.let { bank -> ProCard {
                CardHeading(when (bank.status) { "verified" -> "Verified"; "failed" -> "Changes required"; else -> "Pending review" })
                Text("${bank.bankName.orEmpty()} · ••••${bank.accountLast4}")
                bank.reason?.let { Text(it) }
                if (!state.editing) UsPillButton("Replace account", viewModel::replace, filled = false)
            } }
            if (state.editing) ProCard { BankFields(state.form, state.errors, viewModel::edit, photo.pickFromGallery) }
            if (!state.loading && state.saved == null && !state.editing) UsPillButton("Try again", viewModel::load, filled = false)
        }
    }
}
