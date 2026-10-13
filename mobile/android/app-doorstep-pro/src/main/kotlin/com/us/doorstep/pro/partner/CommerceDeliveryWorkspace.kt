package com.us.doorstep.pro.partner

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.LifecycleResumeEffect
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

internal interface CommerceDeliveryApi {
    @GET("v1/commerce/delivery/profile") @Headers("Cache-Control: no-cache")
    suspend fun profile(): Response<JsonObject>
    @PUT("v1/commerce/delivery/profile") suspend fun save(@Body body: JsonObject): Response<JsonObject>
    @PUT("v1/commerce/delivery/documents/{kind}") suspend fun document(@Path("kind") kind: String, @Body body: JsonObject): Response<JsonObject>
    @POST("v1/commerce/delivery/submit") suspend fun submit(): Response<JsonObject>
}

data class CommerceApplicationState(
    val profile: JsonObject? = null, val loading: Boolean = true,
    val missing: Boolean = false, val error: String? = null,
    val pendingUploads: Set<String> = emptySet(),
)

@HiltViewModel
class CommerceDeliveryViewModel @Inject constructor(retrofit: Retrofit, private val uploader: CommerceEvidenceUploader,
    private val savedState: SavedStateHandle, val links: CommerceWorkLinks) : ViewModel() {
    private val api = retrofit.create(CommerceDeliveryApi::class.java)
    private val mutable = MutableStateFlow(CommerceApplicationState(pendingUploads = pendingKinds()))
    val state = mutable.asStateFlow()
    private var running = false
    private fun request(block: suspend () -> Response<JsonObject>) {
        if (running) return
        running = true
        viewModelScope.launch {
            mutable.update { it.copy(loading = true, error = null) }
            try {
                val response = block()
                if (response.isSuccessful) {
                    val profile = response.body()?.get("data") as? JsonObject
                    if (profile?.string("id").isNullOrBlank()) error("Invalid profile response")
                    mutable.value = CommerceApplicationState(profile, loading = false, pendingUploads = pendingKinds())
                } else {
                    val error = response.errorBody()?.string()?.let {
                        runCatching { Json.parseToJsonElement(it).jsonObject["error"]?.jsonObject }.getOrNull()
                    }
                    if (response.code() == 404 && error?.string("code") == "COMMERCE_DELIVERY_PROFILE_NOT_FOUND") {
                        mutable.value = CommerceApplicationState(loading = false, missing = true)
                    } else mutable.update { it.copy(loading = false, pendingUploads = pendingKinds(), error = error?.string("message")?.takeIf(String::isNotBlank)
                        ?: "Commerce Delivery is temporarily unavailable. Please try again.") }
                }
            } catch (cancel: CancellationException) { throw cancel }
            catch (error: EvidenceUploadException) { mutable.update { it.copy(loading = false, error = error.message, pendingUploads = pendingKinds()) } }
            catch (_: Exception) { mutable.update { it.copy(loading = false, error = "Couldn't save or refresh your application. Check your connection and try again.", pendingUploads = pendingKinds()) } }
            finally { running = false }
        }
    }
    fun refresh() = request { api.profile() }
    fun save(name: String, vehicle: String) = request { api.save(buildJsonObject { put("full_name", name.trim()); put("vehicle_type", vehicle) }) }
    fun submit() = request { api.submit() }
    private fun pendingKinds() = savedState.keys().filter { it.startsWith("evidence:") }.map { it.removePrefix("evidence:") }.toSet()
    fun upload(kind: String, uri: String) = request {
        val id = uploader.upload(uri, kind == "introduction") { savedState["evidence:$kind"] = it }
        attach(kind, id)
    }
    fun checkUpload(kind: String) = request {
        val id = savedState.get<String>("evidence:$kind") ?: throw EvidenceUploadException("Please choose your file again.")
        uploader.awaitReady(id)
        attach(kind, id)
    }
    private suspend fun attach(kind: String, id: String): Response<JsonObject> {
        val result = api.document(kind, buildJsonObject { put("media_id", id) })
        if (result.isSuccessful) savedState.remove<String>("evidence:$kind")
        return result
    }
}

internal fun commerceRequiredDocuments(vehicle: String): List<String> = listOf("selfie", "identity", "bank_proof") +
    if (vehicle == "bicycle") emptyList() else listOf("driving_licence", "vehicle_registration")

@Composable
internal fun CommerceDeliveryWorkspace(viewModel: CommerceDeliveryViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var showApplication by rememberSaveable { mutableStateOf(false) }
    var workDestination by remember { mutableStateOf<CommerceDestination?>(null) }
    val destination by viewModel.links.destination.collectAsStateWithLifecycle()
    LaunchedEffect(destination) {
        destination?.let {
            showApplication = it.page == "Application"
            if (!showApplication) workDestination = it
            viewModel.refresh(); viewModel.links.consumed()
        }
    }
    LifecycleResumeEffect(viewModel) { viewModel.refresh(); onPauseOrDispose {} }
    if ((state.profile?.string("status") == "approved" || workDestination != null) && !showApplication) {
        CommerceWorkScreen(onApplication = { showApplication = true }, destination = workDestination)
    } else Column(Modifier.fillMaxSize()) {
        if (state.profile != null) TextButton({ showApplication = false; workDestination = CommerceDestination("Requests") }) { Text("Delivery work") }
        CommerceApplicationScreen(state, viewModel::refresh, viewModel::save, viewModel::upload, viewModel::submit, viewModel::checkUpload)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun CommerceApplicationScreen(state: CommerceApplicationState, refresh: () -> Unit,
    save: (String, String) -> Unit, upload: (String, String) -> Unit, submit: () -> Unit, checkUpload: (String) -> Unit = {}) {
    val p = state.profile
    var name by rememberSaveable(p?.string("id")) { mutableStateOf(p?.string("full_name").orEmpty()) }
    var vehicle by rememberSaveable(p?.string("id")) { mutableStateOf(p?.string("vehicle_type")?.ifBlank { "motorbike" } ?: "motorbike") }
    var pickedKind by rememberSaveable { mutableStateOf("selfie") }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri -> uri?.let { upload(pickedKind, it.toString()) } }
    val status = p?.string("status").orEmpty()
    val editable = state.missing || status in setOf("draft", "changes_requested")
    val documents = (p?.get("documents") as? JsonArray).orEmpty().mapNotNull { it as? JsonObject }.associateBy { it.string("kind") }
    LazyColumn(Modifier.fillMaxSize().imePadding(), contentPadding = PaddingValues(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item {
            Text("Commerce Delivery", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
            Text("Collect shopping orders from sellers and deliver them to customers.", color = UsTheme.extended.textSecondary)
            if (state.loading) LinearProgressIndicator(Modifier.fillMaxWidth())
        }
        state.error?.let { error -> item {
            UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text(error, color = UsTheme.extended.statusWarning)
                if (p != null) Text("The details below are from your last successful refresh.", style = MaterialTheme.typography.bodySmall)
                UsSecondaryButton("Try again", refresh, enabled = !state.loading)
            }
        } }
        if (p != null) item {
            UsSurfaceCard(Modifier.fillMaxWidth()) {
                val summary = businessSummary(PartnerBusiness.COMMERCE_DELIVERY, p)
                Text(summary.status.label, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                if (p.string("review_note").isNotBlank()) Text(p.string("review_note"))
                Text(when (status) {
                    "approved" -> "Your application is approved. Delivery requests will be available when local coverage and payouts are enabled."
                    "submitted" -> "Your application is with the review team. You don't need to submit it again."
                    "suspended" -> "Your account needs attention. Contact support before taking new work."
                    else -> "Complete the details and documents below, then submit once for review."
                }, color = UsTheme.extended.textSecondary)
                UsSecondaryButton("Refresh status", refresh, enabled = !state.loading)
            }
        }
        if (editable) item {
            UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text("Your details", style = MaterialTheme.typography.titleMedium)
                OutlinedTextField(name, { name = it.take(120) }, label = { Text("Full name") }, singleLine = true, modifier = Modifier.fillMaxWidth(), enabled = !state.loading)
                Text("Delivery vehicle", style = MaterialTheme.typography.labelLarge)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                    listOf("bicycle" to "Bicycle", "motorbike" to "Motorbike", "car" to "Car").forEach { (value, label) ->
                        FilterChip(vehicle == value, { vehicle = value }, label = { Text(label) }, enabled = !state.loading)
                    }
                }
                UsButton("Save details", { save(name, vehicle) }, enabled = !state.loading && name.trim().length >= 2)
            }
        }
        if (p != null) {
            item { Text("Documents", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold) }
            (commerceRequiredDocuments(p.string("vehicle_type")) + "introduction").forEach { kind -> item(key = kind) {
                val doc = documents[kind]
                val label = when(kind) {
                    "selfie" -> "Selfie"
                    "identity" -> "Identity proof (PAN, passport or voter ID)"
                    "bank_proof" -> "Bank account proof"
                    "driving_licence" -> "Driving licence"
                    "introduction" -> "One-minute introduction (optional)"
                    else -> "Vehicle registration"
                }
                UsSurfaceCard(Modifier.fillMaxWidth()) {
                    Text(label, style = MaterialTheme.typography.titleSmall)
                    Text(when(doc?.string("status")) { "approved" -> "Approved"; "submitted" -> "Uploaded · awaiting review"; "changes_requested" -> "Changes requested"; else -> if (kind == "introduction") "Optional · tell us about your work" else "Required" }, color = UsTheme.extended.textSecondary)
                    doc?.string("review_note")?.takeIf(String::isNotBlank)?.let { Text(it) }
                    if (editable && kind in state.pendingUploads) UsSecondaryButton("Check upload", { checkUpload(kind) }, enabled = !state.loading)
                    if (editable) UsSecondaryButton(if (kind == "introduction") "Choose video" else if (doc == null) "Upload photo" else "Replace photo", {
                        pickedKind = kind; picker.launch(if (kind == "introduction") "video/*" else "image/*")
                    }, enabled = !state.loading)
                }
            } }
            if (editable) item {
                Text("Do not upload Aadhaar. A one-minute introduction is optional and is not required for approval.", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary)
                UsButton("Submit for review", submit, enabled = !state.loading && commerceRequiredDocuments(p.string("vehicle_type")).all {
                    documents[it]?.string("status") in setOf("submitted", "approved")
                })
            }
        }
    }
}
