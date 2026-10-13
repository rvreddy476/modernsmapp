package com.us.android.feature.commerce.delivery

import android.content.Context
import android.location.Geocoder
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalInspectionMode
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.maps.LocationMap
import com.us.android.core.maps.MapPin
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.*
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.http.*
import javax.inject.Inject

internal interface LocalDeliveryApi {
    @GET("v1/commerce/delivery/orders/{id}/jobs") suspend fun jobs(@Path("id") id: String): Response<JsonObject>
    @GET("v1/commerce/delivery/customer/jobs/{id}/code") suspend fun code(@Path("id") id: String): Response<JsonObject>
    @GET("v1/commerce/orders/{id}/shipments") suspend fun shipments(@Path("id") id: String): Response<JsonObject>
    @POST("v1/commerce/delivery/seller/jobs/{id}/ready") suspend fun ready(@Path("id") id: String, @Body body: JsonObject): Response<JsonObject>
    @PUT("v1/commerce/delivery/orders/{id}/location") suspend fun location(@Path("id") id: String, @Body body: JsonObject): Response<JsonObject>
}

internal fun JsonObject.text(key: String) = (get(key) as? JsonPrimitive)?.contentOrNull.orEmpty()
internal fun localData(response: Response<JsonObject>): JsonElement? {
    if (!response.isSuccessful) {
        val message = response.errorBody()?.string()?.let { runCatching { Json.parseToJsonElement(it).jsonObject["error"]?.jsonObject?.text("message") }.getOrNull() }
        throw IllegalStateException(message?.takeIf(String::isNotBlank) ?: "Couldn't check local delivery. Please refresh.")
    }
    val body = response.body() ?: error("Delivery information is incomplete. Please refresh.")
    check(body.containsKey("data")) { "Delivery information is incomplete. Please refresh." }
    return body["data"].takeUnless { it is JsonNull }
}
internal fun canRevealDeliveryCode(status: String) = status == "assigned" || status == "picked_up"
internal fun localDeliveryStatus(status: String): String = when(status) {
    "awaiting_ready" -> "Seller preparing your order"
    "unassigned" -> "Ready · finding an available delivery partner"
    "assigned" -> "Delivery partner assigned · awaiting pickup"
    "picked_up" -> "Picked up · on the way"
    "delivered" -> "Delivery verified"
    "failed" -> "Delivery needs assistance"
    "cancelled" -> "Delivery cancelled"
    else -> "Check delivery status"
}
data class LocalDeliveryState(
    val jobs: List<JsonObject> = emptyList(), val loading: Boolean = true,
    val busy: Boolean = false, val error: String? = null, val codes: Map<String,String> = emptyMap(),
)
@HiltViewModel
class LocalDeliveryViewModel @Inject constructor(retrofit: Retrofit) : ViewModel() {
    private val api = retrofit.create(LocalDeliveryApi::class.java)
    private val mutable = MutableStateFlow(LocalDeliveryState())
    val state = mutable.asStateFlow()
    fun hideCodes() { mutable.update { it.copy(codes = emptyMap()) } }
    private var request: Job? = null
    fun refresh(orderId: String, seller: Boolean) {
        if (request?.isActive == true || mutable.value.busy) return
        request = viewModelScope.launch {
            try {
                val data = localData(api.jobs(orderId))
                val jobs = (data as? JsonArray)?.map { it.jsonObject } ?: if(data == null) emptyList() else error("Delivery information is incomplete.")
                // Do not fetch seller-only pickup codes until explicitly requested.
                mutable.update { old -> old.copy(jobs = jobs, loading = false, error = null,
                    codes = old.codes.filterKeys { id -> jobs.any { it.text("id") == id && if(seller) it.text("status") in setOf("unassigned","assigned") else canRevealDeliveryCode(it.text("status")) } }) }
            } catch(cancel: CancellationException) { throw cancel }
            catch(error: Exception) { mutable.update { it.copy(loading = false, error = error.message ?: "Couldn't check delivery. Please refresh.", codes = emptyMap()) } }
        }
    }
    fun reveal(orderId: String, jobId: String, seller: Boolean) = action(orderId, seller) {
        val code = if(seller) {
            val data = localData(api.shipments(orderId))?.jsonObject
            (data?.get("local_pickup_codes") as? JsonObject)?.text(jobId).orEmpty()
        } else localData(api.code(jobId))?.jsonObject?.text("delivery_code").orEmpty()
        check(code.isNotBlank()) { "This handover code is not available now. Refresh the delivery status." }
        mutable.update { it.copy(codes = it.codes + (jobId to code)) }
    }
    fun ready(orderId: String, jobId: String) = action(orderId, true) {
        localData(api.ready(jobId, buildJsonObject {}))
    }
    private fun action(orderId: String, seller: Boolean, block: suspend () -> Unit) {
        if(mutable.value.busy) return
        mutable.update { it.copy(busy = true,error = null) }
        viewModelScope.launch {
            try { block() }
            catch(cancel: CancellationException) { throw cancel }
            catch(error: Exception) { mutable.update { it.copy(error = error.message ?: "Couldn't complete that request.",codes=emptyMap()) } }
            finally { mutable.update { it.copy(busy=false) }; if(mutable.value.error == null) refresh(orderId,seller) }
        }
    }
}

/** Backend ownership, not this flag, decides which handover codes the account may read. */
@Composable
fun LocalOrderDeliveries(orderId: String, seller: Boolean = false, onLocalJobs: (Boolean) -> Unit = {}) {
    if(LocalInspectionMode.current) return
    val vm: LocalDeliveryViewModel = hiltViewModel(key = "local-delivery-$seller-$orderId")
    val state by vm.state.collectAsStateWithLifecycle()
    val now by produceState(System.currentTimeMillis()) {
        while (true) { value = System.currentTimeMillis(); delay(5_000) }
    }
    val reportLocalJobs by rememberUpdatedState(onLocalJobs)
    LaunchedEffect(state.jobs) { reportLocalJobs(state.jobs.isNotEmpty()) }
    val scope = rememberCoroutineScope()
    LifecycleResumeEffect(orderId, seller) {
        val poll = scope.launch { while(isActive) { vm.refresh(orderId,seller);delay(if (seller) 15_000 else 5_000) } }
        onPauseOrDispose { poll.cancel();vm.hideCodes() }
    }
    if(state.jobs.isEmpty() && state.error == null) return
    Column(verticalArrangement=Arrangement.spacedBy(UsTheme.spacing.m)) {
        Text("Local deliveries",style=MaterialTheme.typography.titleMedium)
        state.error?.let { Text(it,color=UsTheme.extended.statusWarning); UsSecondaryButton("Refresh deliveries",{vm.refresh(orderId,seller)},enabled=!state.busy) }
        state.jobs.forEach { job ->
            val id = job.text("id");val status = job.text("status")
            UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text("Shipment ${job.text("shipment_id").take(8)}",style=MaterialTheme.typography.titleSmall)
                Text(localDeliveryStatus(status))
                if (!seller && status == "picked_up") {
                    val fix = (job["customer_location"] as? JsonObject)
                    val lat = (fix?.get("lat") as? JsonPrimitive)?.doubleOrNull
                    val lng = (fix?.get("lng") as? JsonPrimitive)?.doubleOrNull
                    val at = runCatching { java.time.Instant.parse(fix?.text("recorded_at")).toEpochMilli() }.getOrNull()
                    val pin = if (lat != null && lng != null) MapPin(lat, lng, "Delivery partner") else null
                    if (state.error == null && pin?.valid == true && at != null && now - at in 0..120_000) {
                        LocationMap(listOf(pin), Modifier.fillMaxWidth().height(220.dp))
                    } else Text("Waiting for a recent delivery partner location.", style = MaterialTheme.typography.bodySmall)
                }
                if(seller && status == "awaiting_ready") {
                    Text("Mark ready only when this shipment is packed and ready to hand over. Assignment does not mark it shipped.")
                    UsButton("Ready for local pickup",{vm.ready(orderId,id)},enabled=!state.busy && state.error == null)
                }
                if(if(seller) status in setOf("unassigned","assigned") else canRevealDeliveryCode(status)) {
                    Text(if(seller) "Share the pickup code with the assigned partner only when handing over this shipment." else "Share the delivery code only after receiving this shipment. Never share it in advance.",style=MaterialTheme.typography.bodySmall)
                    state.codes[id]?.let { Text(it,style=MaterialTheme.typography.headlineMedium) }
                        ?: UsSecondaryButton(if(seller) "Show pickup code" else "Show delivery code",{vm.reveal(orderId,id,seller)},enabled=!state.busy && state.error == null)
                }
            }
        }
    }
}

data class DeliveryPinState(val results:List<MapPin> = emptyList(),val selected:MapPin?=null,val busy:Boolean=false,val error:String?=null)
@HiltViewModel
class DeliveryPinViewModel @Inject constructor(retrofit:Retrofit,@ApplicationContext private val context:Context):ViewModel() {
    private val api=retrofit.create(LocalDeliveryApi::class.java)
    private val mutable=MutableStateFlow(DeliveryPinState())
    val state=mutable.asStateFlow()
    fun select(pin:MapPin) { if(pin.valid) mutable.update { it.copy(selected=pin,error=null) } }
    fun search(query:String) {
        if(mutable.value.busy || query.trim().length<3) return
        mutable.update { it.copy(busy=true,error=null,results=emptyList(),selected=null) }
        viewModelScope.launch {
            try {
                val results=withContext(Dispatchers.IO) {
                    @Suppress("DEPRECATION")
                    Geocoder(context).getFromLocationName(query.trim(),5).orEmpty().map { MapPin(it.latitude,it.longitude,it.getAddressLine(0).orEmpty()) }.filter { it.valid }
                }
                mutable.update { it.copy(results=results,error=if(results.isEmpty()) "No matching address. Refine the address or continue without a map pin." else null) }
            } catch(cancel:CancellationException) {throw cancel}
            catch(error:Exception) { mutable.update { it.copy(error="Address search is unavailable. You can continue without a map pin.") } }
            finally { mutable.update {it.copy(busy=false)} }
        }
    }
    fun save(orderId:String,label:String,onSaved:()->Unit) {
        val pin=mutable.value.selected?.takeIf { it.valid } ?: return
        if(mutable.value.busy) return
        mutable.update {it.copy(busy=true,error=null)}
        viewModelScope.launch {
            try { localData(api.location(orderId,buildJsonObject {put("latitude",pin.latitude);put("longitude",pin.longitude);put("label",label.take(500))}));onSaved() }
            catch(cancel:CancellationException){throw cancel}
            catch(error:Exception){mutable.update {it.copy(error=error.message ?: "Couldn't save your confirmed pin.")}}
            finally {mutable.update {it.copy(busy=false)}}
        }
    }
}

/** Optional map pin; never reads device location or converts an area centre into a delivery address. */
@Composable
fun ConfirmDeliveryPin(orderId:String,addressSummary:String,onContinue:()->Unit) {
    val vm:DeliveryPinViewModel=hiltViewModel(key="delivery-pin-$orderId")
    val state by vm.state.collectAsStateWithLifecycle()
    var query by remember(orderId){mutableStateOf(addressSummary)}
    var confirmed by remember(state.selected){mutableStateOf(false)}
    Column(verticalArrangement=Arrangement.spacedBy(UsTheme.spacing.m)) {
        Text("Confirm your delivery location",style=MaterialTheme.typography.titleLarge)
        Text("Optional: add a precise pin for eligible local delivery. This does not change your saved address or shipping price.")
        Text(addressSummary,style=MaterialTheme.typography.bodyMedium)
        UsTextField(value=query,onValueChange={query=it},label="Search your delivery address",enabled=!state.busy)
        Text("Search uses your device's address lookup service. Choose a result, adjust the pin if needed, and confirm it matches the address above.",style=MaterialTheme.typography.bodySmall)
        UsSecondaryButton("Search address",{vm.search(query)},enabled=!state.busy && query.trim().length>=3)
        state.results.forEach { pin -> TextButton({vm.select(pin)}){Text(pin.label)} }
        state.selected?.let { pin ->
            LocationMap(listOf(pin),Modifier.fillMaxWidth().height(230.dp),onPin=vm::select)
            Text(pin.label)
            Row { Checkbox(confirmed,{confirmed=it});Text("This pin matches my delivery address.",Modifier.weight(1f)) }
        }
        state.error?.let {Text(it,color=UsTheme.extended.statusWarning)}
        UsButton("Save pin & continue to payment",{vm.save(orderId,addressSummary,onContinue)},enabled=confirmed && state.selected!=null && !state.busy)
        UsSecondaryButton("Continue without a map pin",onContinue,enabled=!state.busy)
    }
}
