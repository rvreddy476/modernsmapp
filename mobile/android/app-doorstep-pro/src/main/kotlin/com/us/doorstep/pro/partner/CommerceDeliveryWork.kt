package com.us.doorstep.pro.partner

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.location.Location
import android.location.LocationManager
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.core.content.ContextCompat
import androidx.core.location.LocationManagerCompat
import androidx.core.os.CancellationSignal
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.theme.UsTheme
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.*
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.http.*
import java.time.Instant
import java.text.NumberFormat
import java.util.Locale
import javax.inject.Inject
import kotlin.coroutines.resume

internal interface CommerceWorkApi {
    @GET("v1/partner/work-session") suspend fun workSession(): Response<JsonObject>
    @GET("v1/commerce/delivery/coverage") suspend fun coverage(): Response<JsonObject>
    @GET("v1/commerce/delivery/availability") suspend fun availability(): Response<JsonObject>
    @PUT("v1/commerce/delivery/availability") suspend fun availability(@Body body: JsonObject): Response<JsonObject>
    @PUT("v1/commerce/delivery/location") suspend fun location(@Body body: JsonObject): Response<JsonObject>
    @GET("v1/commerce/delivery/offers") suspend fun offers(): Response<JsonObject>
    @POST("v1/commerce/delivery/offers/{id}/{decision}") suspend fun decide(@Path("id") id: String, @Path("decision") decision: String, @Body body: JsonObject): Response<JsonObject>
    @GET("v1/commerce/delivery/jobs/current") suspend fun current(): Response<JsonObject>
    @GET("v1/commerce/delivery/jobs/{id}") suspend fun job(@Path("id") id: String): Response<JsonObject>
    @GET("v1/commerce/delivery/jobs") suspend fun jobs(): Response<JsonObject>
    @GET("v1/commerce/delivery/earnings") suspend fun earnings(): Response<JsonObject>
    @POST("v1/commerce/delivery/jobs/{id}/{action}") suspend fun action(@Path("id") id: String, @Path("action") action: String, @Body body: JsonObject): Response<JsonObject>
    @GET("v1/commerce/delivery/jobs/{id}/contact") suspend fun contact(@Path("id") id: String): Response<JsonObject>
}

internal data class CommerceWorkState(
    val coverage: List<JsonObject> = emptyList(), val availability: JsonObject? = null,
    val offers: List<JsonObject> = emptyList(), val current: JsonObject? = null,
    val history: List<JsonObject> = emptyList(), val earnings: List<JsonObject> = emptyList(),
    val loading: Boolean = true, val busy: Boolean = false, val error: String? = null,
    val message: String? = null, val contact: JsonObject? = null,
    val actionError: String? = null,
    val locationError: String? = null,
    val focusedJob: JsonObject? = null,
    val jobsConfirmed: Boolean = false, val offersConfirmed: Boolean = false, val availabilityConfirmed: Boolean = false,
)

internal class CommerceWorkException(message: String) : Exception(message)
internal fun commerceData(response: Response<JsonObject>): JsonElement? {
    if (!response.isSuccessful) {
        val problem = response.errorBody()?.string()?.let { runCatching { Json.parseToJsonElement(it).jsonObject["error"]?.jsonObject }.getOrNull() }
        throw CommerceWorkException(problem?.string("message")?.takeIf { it.isNotBlank() }
            ?: "Couldn't refresh Commerce Delivery. Please try again.")
    }
    val body = response.body() ?: throw CommerceWorkException("The server returned an incomplete response. Please refresh.")
    if (!body.containsKey("data")) throw CommerceWorkException("The server returned an incomplete response. Please refresh.")
    return body["data"].takeUnless { it is JsonNull }
}
internal fun JsonElement?.commerceRows(): List<JsonObject> = (this as? JsonArray)?.map { it as? JsonObject
    ?: throw CommerceWorkException("The server returned an incomplete list. Please refresh.") }
    ?: if (this == null) emptyList() else throw CommerceWorkException("The server returned an incomplete list. Please refresh.")
internal fun JsonElement?.commerceObject(nullable: Boolean = false): JsonObject? = when {
    this is JsonObject -> this
    this == null && nullable -> null
    else -> throw CommerceWorkException("The server returned incomplete delivery information. Please refresh.")
}
internal fun commerceOfferSeconds(offer: JsonObject, now: Instant): Long = runCatching {
    val deadline = Instant.parse(offer.string("expires_at"))
    java.time.Duration.between(now, deadline).toMillis().let { if (it <= 0) 0 else (it + 999) / 1000 }
}.getOrDefault(0)
internal fun commerceMoney(paise: Long): String = NumberFormat.getCurrencyInstance(Locale("en", "IN")).format(paise / 100.0)
internal fun commerceAreaSelected(coverage: List<JsonObject>, id: String): Boolean =
    id.isNotBlank() && coverage.any { it.string("id") == id }
internal fun JsonObject.long(key: String): Long = (get(key) as? JsonPrimitive)?.longOrNull ?: 0
internal fun commerceJobActions(status: String): List<String> = when (status) {
    "assigned" -> listOf("pickup", "failed", "support")
    "picked_up" -> listOf("deliver", "failed", "support")
    "delivered", "failed", "cancelled" -> listOf("support")
    else -> emptyList()
}
internal fun commerceStatus(status: String): String = when (status) {
    "assigned" -> "Ready for pickup"; "picked_up" -> "On the way to customer"
    "delivered" -> "Delivered"; "failed" -> "Delivery needs assistance"; "cancelled" -> "Cancelled"
    "unassigned" -> "Awaiting a partner"; "awaiting_ready" -> "Seller is preparing the order"
    else -> "Refresh delivery status"
}

@HiltViewModel
internal class CommerceWorkViewModel @Inject constructor(retrofit: Retrofit, @ApplicationContext private val context: Context, private val locationStatus:CommerceLocationStatus) : ViewModel() {
    private val api = retrofit.create(CommerceWorkApi::class.java)
    private val mutable = MutableStateFlow(CommerceWorkState())
    val state = mutable.asStateFlow()
    val locationState = locationStatus.state
    private var refreshJob: Job? = null
    private var visibleJob: Job? = null
    fun visible(value: Boolean) {
        visibleJob?.cancel()
        if (value) visibleJob = viewModelScope.launch {
            launch { var tick = 0; while (isActive) { refresh(full = tick++ % 10 == 0); delay(3_000) } }
        }
    }
    fun refresh(full: Boolean = true) {
        if (refreshJob?.isActive == true || mutable.value.busy) return
        refreshJob = viewModelScope.launch {
            try {
                coroutineScope {
                    suspend fun <T> fetch(block: suspend () -> T): Result<T> = try { Result.success(block()) }
                        catch (cancel: CancellationException) { throw cancel }
                        catch (error: Exception) { Result.failure(error) }
                    val coverage = async { if (full) fetch { commerceData(api.coverage()).commerceRows() } else Result.success(mutable.value.coverage) }
                    val availability = async { fetch { commerceData(api.availability()).commerceObject() } }
                    val offers = async { fetch { commerceData(api.offers()).commerceRows() } }
                    val current = async { fetch { commerceData(api.current()).commerceObject(nullable = true) } }
                    val history = async { if (full) fetch { commerceData(api.jobs()).commerceRows() } else Result.success(mutable.value.history) }
                    val earnings = async { if (full) fetch { commerceData(api.earnings()).commerceRows() } else Result.success(mutable.value.earnings) }
                    val a = coverage.await(); val b = availability.await(); val c = offers.await(); val d = current.await(); val e = history.await(); val f = earnings.await()
                    val failures = listOf(a, b, c, d, e, f).mapNotNull { it.exceptionOrNull() }
                    mutable.update { old -> old.copy(
                        coverage = a.getOrDefault(old.coverage), availability = b.getOrElse { old.availability },
                        offers = c.getOrDefault(old.offers), current = d.getOrElse { old.current },
                        history = e.getOrDefault(old.history), earnings = f.getOrDefault(old.earnings),
                        loading = false, error = failures.firstOrNull()?.let { (it as? Exception)?.userMessage() },
                        jobsConfirmed = d.isSuccess && e.isSuccess, offersConfirmed = c.isSuccess, availabilityConfirmed = a.isSuccess && b.isSuccess,
                    ) }
                }
            } catch (cancel: CancellationException) { throw cancel }
            catch (error: Exception) { mutable.update { it.copy(loading = false, error = error.userMessage()) } }
        }
    }
    private fun mutate(block: suspend () -> String?) {
        if (mutable.value.busy) return
        refreshJob?.cancel()
        mutable.update { it.copy(busy = true, actionError = null, message = null, contact = null) }
        viewModelScope.launch {
            try { val message = block(); mutable.update { it.copy(message = message) } }
            catch (cancel: CancellationException) { throw cancel }
            catch (error: Exception) { mutable.update { it.copy(actionError = error.userMessage()) } }
            finally { mutable.update { it.copy(busy = false) }; refresh() }
        }
    }
    fun availability(areaId: String, online: Boolean) = mutate {
        val location = if (online) currentLocation(context) else null
        val work = parseWorkSession(commerceData(api.workSession()))
        val old = mutable.value.availability
        commerceData(api.availability(buildJsonObject {
            put("area_id", areaId); put("online", online); put("expected_revision", old?.long("revision") ?: 0)
            put("expected_work_revision", work.revision)
            put("latitude", location?.latitude ?: (old?.get("latitude") as? JsonPrimitive)?.doubleOrNull ?: 0.0)
            put("longitude", location?.longitude ?: (old?.get("longitude") as? JsonPrimitive)?.doubleOrNull ?: 0.0)
        }))
        if(online) {
            try {CommerceLocationService.start(context)}
            catch(error:Exception) {locationStatus.failed("Location sharing could not start. Reopen this screen and resume it.")}
        } else CommerceLocationService.stop(context)
        if (online) "Availability saved. Location sharing keeps nearby delivery requests up to date." else "Commerce Delivery is offline."
    }
    fun resumeLocation() = mutate {
        val work=parseWorkSession(commerceData(api.workSession()))
        if(work.category != PartnerBusiness.COMMERCE_DELIVERY) throw CommerceWorkException("Commerce Delivery is not the active work category. Refresh availability first.")
        CommerceLocationService.start(context)
        "Checking your current location."
    }
    fun decide(offer: JsonObject, accept: Boolean) = mutate {
        if (commerceOfferSeconds(offer, Instant.now()) <= 0) throw CommerceWorkException("This request has expired. Refresh to see current requests.")
        commerceData(api.decide(offer.string("id"), if (accept) "accept" else "decline", buildJsonObject {}))
        if (accept) "Delivery accepted. Your confirmed job is shown below." else "Request declined."
    }
    fun action(job: JsonObject, action: String, input: String) = mutate {
        require(action in commerceJobActions(job.string("status")))
        val updated = commerceData(api.action(job.string("id"), action, buildJsonObject {
            put("expected_revision", job.long("revision")); put(if (action in setOf("pickup", "deliver")) "code" else "reason", input.trim())
        }))
        if (action != "support" && updated is JsonObject && updated.string("id") == job.string("id")) {
            mutable.update { old -> old.copy(focusedJob = old.focusedJob?.let { if (it.string("id") == updated.string("id")) updated else it }) }
        }
        if (action == "support") "Support request sent." else "Delivery updated."
    }
    fun contact(job: JsonObject) = mutate {
        val contact = commerceData(api.contact(job.string("id"))) as? JsonObject
            ?: throw CommerceWorkException("Contact details are temporarily unavailable.")
        mutable.update { it.copy(contact = contact) }; null
    }
    fun dismissContact() { mutable.update { it.copy(contact = null) } }
    fun openJob(id: String) = mutate {
        val job = commerceData(api.job(id)) as? JsonObject ?: throw CommerceWorkException("This delivery is no longer available to your account.")
        mutable.update { it.copy(focusedJob = job) }; null
    }
    private fun Exception.userMessage() = if (this is CommerceWorkException) message.orEmpty() else "Couldn't connect. Your saved details and job have not been changed. Please retry."
}

/** A fresh foreground fix only, never an area-centre substitute or a stale last-known location. */
@Suppress("MissingPermission")
internal suspend fun currentLocation(context: Context): Location {
    if (ContextCompat.checkSelfPermission(context, Manifest.permission.ACCESS_COARSE_LOCATION) != PackageManager.PERMISSION_GRANTED &&
        ContextCompat.checkSelfPermission(context, Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED)
        throw CommerceWorkException("Allow location to receive deliveries in your working area. You can still view your current job without it.")
    val manager = context.getSystemService(LocationManager::class.java)
    val provider = listOf(LocationManager.GPS_PROVIDER, LocationManager.NETWORK_PROVIDER).firstOrNull { manager.isProviderEnabled(it) }
        ?: throw CommerceWorkException("Turn on your phone's location, then try again.")
    return withTimeoutOrNull(20_000) { suspendCancellableCoroutine { continuation ->
        val signal = CancellationSignal()
        continuation.invokeOnCancellation { signal.cancel() }
        LocationManagerCompat.getCurrentLocation(manager, provider, signal, ContextCompat.getMainExecutor(context)) { location ->
            if (continuation.isActive) continuation.resume(location)
        }
    } } ?: throw CommerceWorkException("Couldn't find your current location. Move to an open area and try again.")
}

@Composable
internal fun CommerceWorkScreen(onApplication: () -> Unit, destination: CommerceDestination? = null, viewModel: CommerceWorkViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val locationState by viewModel.locationState.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) { viewModel.visible(true); onPauseOrDispose { viewModel.visible(false) } }
    val context = LocalContext.current
    var tab by rememberSaveable { mutableStateOf("Requests") }
    LaunchedEffect(destination) {
        destination?.let { tab = it.page; it.jobId?.let(viewModel::openJob) }
    }
    var areaId by rememberSaveable { mutableStateOf("") }
    var now by remember { mutableStateOf(Instant.now()) }
    var action by remember { mutableStateOf<Pair<JsonObject, String>?>(null) }
    var permissionMessage by remember { mutableStateOf<String?>(null) }
    var showLocationDisclosure by remember { mutableStateOf(false) }
    var resumeOnly by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { while (true) { now = Instant.now(); delay(1_000) } }
    LaunchedEffect(state.availability, state.coverage) {
        val saved = state.availability?.string("area_id").orEmpty()
        val online = (state.availability?.get("online") as? JsonPrimitive)?.booleanOrNull == true
        if (online || !commerceAreaSelected(state.coverage, areaId)) {
            areaId = saved.takeIf { online || commerceAreaSelected(state.coverage, it) }.orEmpty()
        }
    }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { result ->
        if (result.values.any { it }) {if(resumeOnly) viewModel.resumeLocation() else viewModel.availability(areaId, true)}
        else permissionMessage = "Location permission is required to receive nearby delivery requests, not to view existing work."
    }
    LazyColumn(Modifier.fillMaxSize().imePadding(), contentPadding = PaddingValues(UsTheme.spacing.pageHorizontal), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("Commerce Delivery", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                TextButton(onApplication) { Text("Documents") }
            }
            Text("Shopping deliveries · your confirmed payout is shown before you accept.", color = UsTheme.extended.textSecondary)
            if (state.loading || state.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            Row { TextButton({ tab = "Requests" }) { Text("Work") }; TextButton({ tab = "History" }) { Text("History") }; TextButton({ tab = "Earnings" }) { Text("Earnings") } }
        }
        (state.error ?: permissionMessage)?.let { message -> item {
            UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text(message, color = UsTheme.extended.statusWarning)
                Text("Refresh before making changes. Previously loaded work is kept below.", style = MaterialTheme.typography.bodySmall)
                UsSecondaryButton("Refresh", { permissionMessage = null; viewModel.refresh() }, enabled = !state.busy)
            }
        } }
        state.message?.let { item { Text(it, color = UsTheme.extended.statusSuccess) } }
        state.actionError?.let { item { Text(it, color = UsTheme.extended.statusWarning) } }
        state.locationError?.let { item { Text("Location update needed: $it", color = UsTheme.extended.statusWarning) } }
        if (tab == "Requests") {
            item {
                UsSurfaceCard(Modifier.fillMaxWidth()) {
                    val online = (state.availability?.get("online") as? JsonPrimitive)?.booleanOrNull == true
                    val locationMessage=commerceLocationMessage(online,locationState,now)
                    Text(if (online) "Commerce Delivery is on duty" else "Delivery availability", style = MaterialTheme.typography.titleMedium)
                    locationMessage?.let {Text(it,color=UsTheme.extended.statusWarning,style=MaterialTheme.typography.bodySmall)}
                    if(online && !locationState.running) UsSecondaryButton("Resume location sharing",{resumeOnly=true;showLocationDisclosure=true},enabled=!state.busy)
                    if (state.coverage.isEmpty() && !state.loading) Text("No local Commerce Delivery areas are enabled yet. Your approved account is unchanged.")
                    state.coverage.forEach { area ->
                        Row { RadioButton(areaId == area.string("id"), { areaId = area.string("id") }, enabled = !state.busy && !online)
                            Column(Modifier.weight(1f)) { Text(area.string("name")); Text("Delivery payout: ${commerceMoney(area.long("payout_paise"))}", style = MaterialTheme.typography.bodySmall) } }
                    }
                    UsButton(if (online) "Go offline" else "Use location & become available", {
                        if (online) viewModel.availability(areaId, false)
                        else {resumeOnly=false;showLocationDisclosure=true}
                    }, enabled = !state.busy && state.availabilityConfirmed && (online || commerceAreaSelected(state.coverage, areaId)))
                }
            }
            state.current?.let { job -> item { CommerceJobCard(job, state.busy || !state.jobsConfirmed, { action = job to it }, { viewModel.contact(job) }) } }
            state.focusedJob?.takeIf { it.string("id") != state.current?.string("id") }?.let { job -> item {
                CommerceJobCard(job, state.busy || !state.jobsConfirmed, { action = job to it }, { viewModel.contact(job) })
            } }
            item { Text("Requests", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold) }
            val live = state.offers.filter { commerceOfferSeconds(it, now) > 0 }
            if (live.isEmpty() && !state.loading) item { Text("No requests waiting. New offers appear here for 20 seconds.", color = UsTheme.extended.textSecondary) }
            items(live, key = { it.string("id") }) { offer ->
                UsSurfaceCard(Modifier.fillMaxWidth()) {
                    Text("${commerceMoney(offer.long("payout_paise"))} delivery payout", style = MaterialTheme.typography.titleMedium)
                    Text("Pickup: ${offer.string("pickup_label")}"); Text("Deliver to: ${offer.string("drop_label")}")
                    Text("Respond within ${commerceOfferSeconds(offer, now)} seconds", color = UsTheme.extended.statusWarning)
                    UsButton("Accept delivery", { viewModel.decide(offer, true) }, enabled = !state.busy && state.offersConfirmed)
                    UsSecondaryButton("Decline", { viewModel.decide(offer, false) }, enabled = !state.busy && state.offersConfirmed)
                }
            }
        } else if (tab == "History") {
            if (state.history.isEmpty() && !state.loading) item { Text("Your delivery history will appear here.") }
            items(state.history, key = { it.string("id") }) { job -> CommerceJobCard(job, state.busy || !state.jobsConfirmed, { action = job to it }, { viewModel.contact(job) }) }
        } else {
            item { Text("Credited earnings", style = MaterialTheme.typography.titleMedium); Text("Credits are added once after verified delivery. These are your payouts, not customer shipping charges.") }
            if (state.earnings.isEmpty() && !state.loading) item { Text("No delivery earnings yet.") }
            items(state.earnings, key = { it.string("job_id") }) { earning -> UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text(commerceMoney(earning.long("amount_paise")), style = MaterialTheme.typography.titleLarge)
                Text("Delivery ${earning.string("job_id").take(8)}"); Text(earning.string("created_at"), style = MaterialTheme.typography.bodySmall)
            } }
        }
    }
    if(showLocationDisclosure) AlertDialog(onDismissRequest={showLocationDisclosure=false},title={Text("Location while on duty")},text={Text("Commerce Delivery uses your location to match nearby requests while you are on duty, including when this app is not on screen. An ongoing notification shows when location sharing is running. Go offline to stop it. If sharing stops or your location is out of date for 30 minutes, new requests pause; your accepted delivery is kept.")},confirmButton={TextButton({showLocationDisclosure=false;permission.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION,Manifest.permission.ACCESS_COARSE_LOCATION))}){Text("Continue")}},dismissButton={TextButton({showLocationDisclosure=false}){Text("Not now")}})
    action?.let { (job, actionName) ->
        var input by rememberSaveable(job.string("id"), actionName) { mutableStateOf("") }
        val verify = actionName in setOf("pickup", "deliver")
        AlertDialog(onDismissRequest = { action = null }, title = { Text(when (actionName) { "pickup" -> "Verify pickup"; "deliver" -> "Verify delivery"; "failed" -> "Report a delivery problem"; else -> "Ask support" }) },
            text = { Column(Modifier.verticalScroll(rememberScrollState())) {
                Text(if (actionName == "pickup") "Ask the seller for the pickup code after collecting the shipment." else if (actionName == "deliver") "Ask the customer for the delivery code after handing over the shipment." else "Describe what happened. Do not include verification codes or bank details.")
                OutlinedTextField(input, { input = it.take(if (verify) 12 else 1000) }, label = { Text(if (verify) "Verification code" else "Reason") }, modifier = Modifier.fillMaxWidth(), singleLine = verify)
            } }, confirmButton = { TextButton({ viewModel.action(job, actionName, input); action = null }, enabled = input.trim().length >= if (verify) 4 else 5) { Text(if (verify) "Verify" else "Send") } },
            dismissButton = { TextButton({ action = null }) { Text("Cancel") } })
    }
    state.contact?.let { contact -> AlertDialog(onDismissRequest = viewModel::dismissContact, title = { Text("Delivery contact") }, text = {
        Column { listOf("pickup_phone" to "Call pickup contact", "customer_phone" to "Call customer").forEach { (key, label) ->
            val number = contact.string(key)
            if (number.isNotBlank()) TextButton({ runCatching { context.startActivity(Intent(Intent.ACTION_DIAL, Uri.parse("tel:${Uri.encode(number)}"))) } }) { Text(label) }
        } }
    }, confirmButton = { TextButton(viewModel::dismissContact) { Text("Close") } }) }
}

@Composable
private fun CommerceJobCard(job: JsonObject, disabled: Boolean, action: (String) -> Unit, contact: () -> Unit) {
    val context = LocalContext.current
    UsSurfaceCard(Modifier.fillMaxWidth()) {
        Text(commerceStatus(job.string("status")), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
        Text("Delivery ${job.string("id").take(8)} · ${commerceMoney(job.long("payout_paise"))}")
        Text("Pickup: ${job.string("pickup_label")}"); Text("Deliver to: ${job.string("drop_label")}")
        if (job.string("status") in setOf("assigned", "picked_up")) {
            val prefix = if (job.string("status") == "assigned") "pickup" else "drop"
            val lat = (job["${prefix}_latitude"] as? JsonPrimitive)?.doubleOrNull
            val lng = (job["${prefix}_longitude"] as? JsonPrimitive)?.doubleOrNull
            if (lat != null && lng != null && lat.isFinite() && lng.isFinite()) UsSecondaryButton("Navigate", {
                runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("geo:$lat,$lng?q=$lat,$lng"))) }
            })
            UsSecondaryButton("Contact", contact, enabled = !disabled)
        }
        commerceJobActions(job.string("status")).forEach { value -> UsSecondaryButton(when(value) { "pickup" -> "Verify pickup"; "deliver" -> "Verify delivery"; "failed" -> "Report failed delivery"; else -> "Ask support" }, { action(value) }, enabled = !disabled) }
    }
}
