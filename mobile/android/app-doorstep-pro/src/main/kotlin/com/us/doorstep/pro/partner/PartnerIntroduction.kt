package com.us.doorstep.pro.partner

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.theme.UsTheme
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import kotlinx.serialization.json.*
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.http.*
import javax.inject.Inject

internal fun introductionContract(business: PartnerBusiness): Pair<String, String>? = when (business) {
    PartnerBusiness.MOPED -> "v1/rider/partners/me/documents" to "introduction_video"
    PartnerBusiness.FOOD_DELIVERY -> "v1/food/delivery/documents" to "INTRODUCTION_VIDEO"
    else -> null
}
private interface IntroductionApi {
    @POST suspend fun attach(@Url path: String, @Body body: JsonObject): Response<JsonObject>
}
data class IntroductionState(val busy: Boolean = false, val saved: Boolean = false, val error: String? = null, val pending: Boolean = false)
@HiltViewModel
class PartnerIntroductionViewModel @Inject constructor(retrofit: Retrofit, private val uploader: CommerceEvidenceUploader, private val savedState: SavedStateHandle) : ViewModel() {
    private val api = retrofit.create(IntroductionApi::class.java)
    private val mutable = MutableStateFlow(IntroductionState(pending = savedState.contains("introduction-media")))
    val state = mutable.asStateFlow()
    fun upload(business: PartnerBusiness, uri: String?) {
        val contract = introductionContract(business) ?: return
        if (mutable.value.busy) return
        mutable.update { it.copy(busy = true, saved = false, error = null) }
        viewModelScope.launch {
            try {
                val id = if (uri != null) uploader.upload(uri, true) { savedState["introduction-media"] = it }
                    else savedState.get<String>("introduction-media")?.also { uploader.awaitReady(it) }
                        ?: throw EvidenceUploadException("Choose your video again.")
                val response = api.attach(contract.first, buildJsonObject { put("document_type", contract.second); put("media_id", id) })
                commerceData(response) ?: throw EvidenceUploadException("Couldn't confirm your introduction. Check upload to retry safely.")
                savedState.remove<String>("introduction-media")
                mutable.value = IntroductionState(saved = true)
            } catch (cancel: CancellationException) { throw cancel }
            catch (error: Exception) {
                mutable.value = IntroductionState(error = if (error is EvidenceUploadException || error is CommerceWorkException) error.message else "Couldn't finish the upload. Check your connection and retry.", pending = savedState.contains("introduction-media"))
            }
        }
    }
}

@Composable
internal fun PartnerIntroduction(business: PartnerBusiness, onBack: () -> Unit, viewModel: PartnerIntroductionViewModel = hiltViewModel(key = "introduction-${business.name}")) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri -> uri?.let { viewModel.upload(business, it.toString()) } }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(UsTheme.spacing.pageHorizontal), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        TextButton(onBack) { Text("Back to ${business.title}") }
        UsSurfaceCard(Modifier.fillMaxWidth()) {
            Text("Introduce yourself", style = MaterialTheme.typography.titleLarge)
            Text("Optional · up to one minute", color = UsTheme.extended.textSecondary)
            Text("Tell the review team about your work and experience. You can skip this; it is not required for approval or receiving work.")
            if (state.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (state.saved) Text("Introduction uploaded for review.", color = UsTheme.extended.statusSuccess)
            state.error?.let { Text(it, color = UsTheme.extended.statusWarning) }
            if (state.pending) UsSecondaryButton("Check upload", { viewModel.upload(business, null) }, enabled = !state.busy)
            UsButton(if (state.saved) "Replace video" else "Choose video", { picker.launch("video/*") }, enabled = !state.busy)
            Text("MP4, WebM or MOV · up to 50 MB. Only share information relevant to your work.", style = MaterialTheme.typography.bodySmall)
        }
    }
}
