package com.us.doorstep.pro.partner

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
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

internal interface FoodDeliverySupportApi {
    @GET("v1/food/support/tickets/me") suspend fun list(): Response<JsonObject>
    @POST("v1/food/support/tickets") suspend fun create(@Body body: JsonObject): Response<JsonObject>
    @GET("v1/food/support/tickets/{id}") suspend fun detail(@Path("id") id: String): Response<JsonObject>
    @POST("v1/food/support/tickets/{id}/messages") suspend fun reply(@Path("id") id: String, @Body body: JsonObject): Response<JsonObject>
}
data class FoodSupportState(val busy: Boolean = false, val error: String? = null, val tickets: List<JsonObject> = emptyList(), val selected: JsonObject? = null, val messages: List<JsonObject> = emptyList())
@HiltViewModel
class FoodDeliverySupportViewModel @Inject constructor(retrofit: Retrofit) : ViewModel() {
    private val api = retrofit.create(FoodDeliverySupportApi::class.java)
    private val mutable = MutableStateFlow(FoodSupportState())
    val state = mutable.asStateFlow()
    private fun run(block: suspend () -> Unit) {
        if (mutable.value.busy) return
        mutable.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try { block() }
            catch (cancel: CancellationException) { throw cancel }
            catch (error: Exception) { mutable.update { it.copy(error = (error as? CommerceWorkException)?.message ?: "Couldn't reach support. Your draft is kept; please retry.") } }
            finally { mutable.update { it.copy(busy = false) } }
        }
    }
    private suspend fun load() {
        val data = commerceData(api.list()) as? JsonObject ?: error("Missing tickets")
        mutable.update { it.copy(tickets = data["tickets"].commerceRows().filter { ticket -> ticket.string("category") == "delivery_partner" }) }
    }
    private suspend fun loadDetail(id: String) {
        val data = commerceData(api.detail(id)) as? JsonObject ?: error("Missing ticket")
        val ticket = data["ticket"] as? JsonObject ?: error("Missing ticket")
        require(ticket.string("category") == "delivery_partner")
        mutable.update { it.copy(selected = ticket, messages = data["messages"].commerceRows()) }
    }
    fun refresh() = run { mutable.value.selected?.string("id")?.let { loadDetail(it) } ?: load() }
    fun open(id: String) = run { loadDetail(id) }
    fun back() { mutable.update { it.copy(selected = null, messages = emptyList()) }; refresh() }
    fun create(subject: String, detail: String) = run {
        val ticket = commerceData(api.create(buildJsonObject {
            put("category", "delivery_partner"); put("subject", "Food Delivery · ${subject.trim()}"); put("detail", detail.trim())
        })) as? JsonObject ?: error("Missing ticket")
        loadDetail(ticket.string("id")); load()
    }
    fun reply(body: String) = run {
        val id = mutable.value.selected?.string("id") ?: return@run
        commerceData(api.reply(id, buildJsonObject { put("body", body.trim()) })); loadDetail(id)
    }
}

@Composable
internal fun FoodDeliverySupport(onBack: () -> Unit, viewModel: FoodDeliverySupportViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var subject by rememberSaveable { mutableStateOf("") }
    var detail by rememberSaveable { mutableStateOf("") }
    var reply by rememberSaveable { mutableStateOf("") }
    LifecycleResumeEffect(viewModel) { viewModel.refresh(); onPauseOrDispose {} }
    LazyColumn(Modifier.fillMaxSize().imePadding(), contentPadding = PaddingValues(UsTheme.spacing.pageHorizontal), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item {
            TextButton(if (state.selected != null) viewModel::back else onBack) { Text(if (state.selected != null) "All support requests" else "Back to Food Delivery") }
            Text("Food Delivery support", style = MaterialTheme.typography.titleLarge)
            Text("For immediate danger, contact emergency services. This inbox is for delivery and account support.", style = MaterialTheme.typography.bodySmall)
            if (state.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            state.error?.let { Text(it, color = UsTheme.extended.statusWarning) }
            TextButton(viewModel::refresh, enabled = !state.busy) { Text("Refresh") }
        }
        val ticket = state.selected
        if (ticket != null) {
            item { UsSurfaceCard(Modifier.fillMaxWidth()) { Text(ticket.string("subject"), style = MaterialTheme.typography.titleMedium); Text(ticket.string("status")); Text(ticket.string("detail")) } }
            items(state.messages, key = { it.string("id") }) { message -> UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text(if ((message["is_admin"] as? JsonPrimitive)?.booleanOrNull == true) "Support team" else "You", style = MaterialTheme.typography.labelLarge)
                Text(message.string("body"))
            } }
            item {
                OutlinedTextField(reply, { reply = it.take(2000) }, label = { Text("Reply") }, modifier = Modifier.fillMaxWidth(), minLines = 2)
                UsButton("Send reply", { viewModel.reply(reply) }, enabled = !state.busy && reply.trim().isNotEmpty())
            }
        } else {
            item { UsSurfaceCard(Modifier.fillMaxWidth()) {
                Text("New request", style = MaterialTheme.typography.titleMedium)
                OutlinedTextField(subject, { subject = it.take(120) }, label = { Text("Topic") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(detail, { detail = it.take(2000) }, label = { Text("How can we help?") }, minLines = 3, modifier = Modifier.fillMaxWidth())
                Text("You can include a delivery reference. Do not send verification codes or bank details.", style = MaterialTheme.typography.bodySmall)
                UsButton("Send request", { viewModel.create(subject, detail) }, enabled = !state.busy && subject.trim().length >= 3 && detail.trim().length >= 10)
            } }
            items(state.tickets, key = { it.string("id") }) { row -> UsSurfaceCard(Modifier.fillMaxWidth(), onClick = { viewModel.open(row.string("id")) }) {
                Text(row.string("subject"), style = MaterialTheme.typography.titleMedium); Text(row.string("status"), color = UsTheme.extended.textSecondary)
            } }
        }
    }
}
