package com.us.doorstep.pro.partner

import android.app.NotificationManager
import android.media.AudioManager
import android.media.RingtoneManager
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.content.getSystemService
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.notifications.*
import com.us.android.feature.rider.offers.ServerTime
import com.us.doorstep.pro.BuildConfig
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import java.time.Instant
import javax.inject.Inject

internal data class PartnerRequestCard(
    val request: PartnerOfferRequest, val service: String, val location: String,
    val amount: Long?, val confirmed: Boolean = true,
) {
    val key get() = "${request.category}:${request.id}"
    val amountLabel get() = if (request.category == PartnerOfferCategory.MOPED) "Estimated fare" else "Your earnings"
}

internal fun partnerRequestCard(category: PartnerOfferCategory, row: JsonObject, test: Boolean = false): PartnerRequestCard? {
    val request = PartnerOfferRequest.parse(mapOf("type" to category.wireType, "offer_id" to row.string("id"),
        "expires_at" to row.string("expires_at"), "test_only" to test.toString())) ?: return null
    if (request.secondsLeft() <= 0 || row.string("status") !in setOf("", "open", "pending", "offered", "sent")) return null
    val pickup = row.string("pickup_label").ifBlank { row.string("pickup_address") }
        .ifBlank { (row["restaurant"] as? JsonObject)?.string("name").orEmpty() }
    val drop = row.string("drop_label").ifBlank { row.string("drop_address") }
        .ifBlank { (row["drop_area"] as? JsonObject)?.string("locality").orEmpty() }
    val location = listOf(pickup, drop).filter { it.isNotBlank() }.joinToString(" → ")
        .ifBlank { row.string("locality") }.ifBlank { "Location not available" }
    val amount = listOf("payout_paise", "earning_estimate_paise", "estimated_fare_paise")
        .firstNotNullOfOrNull { (row[it] as? JsonPrimitive)?.longOrNull }?.takeIf { it >= 0 }
    return PartnerRequestCard(request, row.string("service_name").ifBlank { when(category) {
        PartnerOfferCategory.MOPED -> "Passenger ride"; PartnerOfferCategory.HOME -> "Home service request"
        PartnerOfferCategory.FOOD -> "Food pickup & delivery"; PartnerOfferCategory.COMMERCE -> "Shopping order delivery"
    } }, location, amount)
}

internal data class PartnerRequestsState(
    val cards: List<PartnerRequestCard> = emptyList(), val now: Instant = Instant.now(),
    val responding: String? = null, val message: String? = null, val error: String? = null,
)

@HiltViewModel
internal class PartnerRequestsViewModel @Inject constructor(
    val inbox: PartnerOfferInbox, private val decisions: PartnerOfferDecision,
    private val responses: PartnerOfferResponses, private val notifications: NotificationPresenter,
    val sounds: DoorstepOfferAlertCoordinator,
    private val updates: NotificationUpdates,
) : ViewModel() {
    private val mutable = MutableStateFlow(PartnerRequestsState())
    val state = mutable.asStateFlow()
    private val refreshLock = Mutex()
    private var categories: Set<PartnerOfferCategory> = emptySet()

    suspend fun listen(allowed: Set<PartnerOfferCategory>) = coroutineScope {
        categories = allowed
        launch { updates.events.collect { refresh(allowed) } }
        launch { inbox.busy.collect { (live, practice) ->
            mutable.update { old -> old.copy(cards=old.cards.filterNot { if(it.request.testOnly) practice else live }) }
            if(live) notifications.cancelPartnerOffers(false)
            if(practice) notifications.cancelPartnerOffers(true)
        } }
        launch { inbox.offers.collect { hints ->
            val practice = if (BuildConfig.DEBUG) hints.mapNotNull { data ->
                if (data["test_only"] != "true" || inbox.isBlocked(true)) return@mapNotNull null
                val category = PartnerOfferCategory.forType(data["type"]) ?: return@mapNotNull null
                partnerRequestCard(category, buildJsonObject {
                    put("id", data["offer_id"] ?: data["entity_id"] ?: "")
                    put("expires_at", data["expires_at"].orEmpty())
                    put("service_name", data["test_service"] ?: "Practice request")
                    put("locality", data["test_location"] ?: "Example location")
                    data["test_amount_paise"]?.toLongOrNull()?.let { put("payout_paise", it) }
                }, test = true)?.takeIf { responses.get(it.request) == null }
            } else emptyList()
            val checking = hints.filter { it["test_only"] != "true" }.mapNotNull { data ->
                PartnerOfferRequest.parse(data)?.takeIf { !inbox.isBlocked(false) && it.secondsLeft() > 0 && responses.get(it) == null }?.let {
                    PartnerRequestCard(it, "Checking request details…", "Fetching location and amount", null, confirmed = false)
                }
            }
            mutable.update { old ->
                val live = old.cards.filterNot { card -> card.request.testOnly }
                old.copy(cards = live + checking.filter { hint -> live.none { it.key == hint.key } } + practice)
            }
            val hinted = hints.filter { it["test_only"] != "true" }.mapNotNull { PartnerOfferCategory.forType(it["type"]) }.toSet()
            refresh(allowed + hinted)
        } }
        launch { while (isActive) { refresh(allowed); delay(3_000) } }
        launch { while (isActive) {
            val now = Instant.now()
            mutable.update { it.copy(now = now, cards = it.cards.filter { card -> card.request.secondsLeft(now) > 0 && responses.get(card.request) == null }) }
            delay(250)
        } }
    }

    private suspend fun refresh(allowed: Set<PartnerOfferCategory>) = refreshLock.withLock {
        if (mutable.value.responding != null) return@withLock
        val checkpoint = inbox.checkpoint()
        try {
            val busy = withTimeout(6_000) { decisions.isBusy() }
            inbox.reconcileLive(busy, checkpoint)
        } catch(cancel: CancellationException) { if(cancel !is TimeoutCancellationException) throw cancel
            allowed.forEach(::markUnconfirmed); return@withLock
        } catch(_: Exception) { allowed.forEach(::markUnconfirmed); return@withLock } // Never infer availability from a network failure.
        if(inbox.isBlocked(false)) return@withLock
        coroutineScope {
            allowed.map { category -> async {
                try {
                    val cards = withTimeout(6_000) { decisions.list(category) }.mapNotNull { partnerRequestCard(category, it) }
                        .filter { !inbox.isBlocked(false) && responses.get(it.request) == null }
                    mutable.update { old -> old.copy(cards = old.cards.filter { it.request.testOnly || it.request.category != category } + cards) }
                    cards.forEach { notifications.cancelForSubject(it.request.id) }
                } catch (cancel: CancellationException) { if(cancel !is TimeoutCancellationException) throw cancel
                    markUnconfirmed(category)
                } catch (_: Exception) { markUnconfirmed(category) }
            } }.awaitAll()
        }
    }
    private fun markUnconfirmed(category: PartnerOfferCategory) {
        mutable.update { old -> old.copy(cards = old.cards.map { if (!it.request.testOnly && it.request.category == category) it.copy(confirmed = false) else it }) }
    }
    fun respond(card: PartnerRequestCard, accept: Boolean) {
        if (mutable.value.responding != null || !card.confirmed || card.request.secondsLeft() <= 0) return
        mutable.update { it.copy(responding = card.key, message = null, error = null) }
        viewModelScope.launch {
            try {
                responses.mutex.withLock {
                    val req = card.request
                    check(req.secondsLeft() > 0) { "This request has expired." }
                    if (responses.get(req) != null) return@withLock
                    check(!inbox.isBlocked(req.testOnly)) { "You already accepted a request. Finish your current work first." }
                    if (req.testOnly) check(BuildConfig.DEBUG) else withTimeout(12_000) { decisions.submit(req, accept) }
                    if(accept) { inbox.accepted(req.testOnly); notifications.cancelPartnerOffers(req.testOnly) }
                    val result = if (req.testOnly) "TEST ${if(accept) "accepted" else "declined"} · No real work assigned."
                        else if (accept) "${req.category.label} request accepted. Your assignment is in Work." else "Request declined."
                    responses.put(req, result)
                    notifications.cancelForSubject(req.id)
                    inbox.remove(req.id)
                    mutable.update { old -> old.copy(message = result, cards = old.cards.filter { it.key != card.key && !(accept && it.request.testOnly == req.testOnly) }) }
                }
            } catch (cancel: CancellationException) { if(cancel !is TimeoutCancellationException) throw cancel
                mutable.update { it.copy(error = "Couldn't confirm your response. Check Work before trying again.") }
            } catch (error: Exception) { mutable.update { it.copy(error = error.message ?: "Couldn't respond. Please check Work.") } }
            finally { mutable.update { it.copy(responding = null) }; refresh(categories) }
        }
    }
    fun clearMessage() { mutable.update { it.copy(message = null, error = null) } }
    fun resetPractice() { inbox.resetPractice(); clearMessage() }
    fun reset() { inbox.clear(); mutable.value = PartnerRequestsState() }
}

internal fun PartnerBusiness.offerCategory() = when (this) {
    PartnerBusiness.HOME -> PartnerOfferCategory.HOME; PartnerBusiness.MOPED -> PartnerOfferCategory.MOPED
    PartnerBusiness.FOOD_DELIVERY -> PartnerOfferCategory.FOOD; PartnerBusiness.COMMERCE_DELIVERY -> PartnerOfferCategory.COMMERCE
}

@Composable
internal fun PartnerRequestsHost(userId: String, summaries: List<BusinessSummary>,
    vm: PartnerRequestsViewModel = hiltViewModel(key = "requests-$userId")) {
    val state by vm.state.collectAsStateWithLifecycle()
    val busy by vm.inbox.busy.collectAsStateWithLifecycle()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val allowed = summaries.filter { it.registered && it.status == BusinessStatus.APPROVED }.map { it.business.offerCategory() }.toSet()
    LaunchedEffect(vm, lifecycle, allowed) {
        vm.inbox.bindAccount(userId)
        lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            vm.inbox.visibleHost = true
            try { vm.listen(allowed) } finally { vm.inbox.visibleHost = false }
        }
    }
    DisposableEffect(vm) { onDispose { vm.inbox.visibleHost = false; vm.reset() } }
    if(busy.second && state.message == null) {
        Box(Modifier.fillMaxSize(),contentAlignment=Alignment.TopCenter) {
            FilledTonalButton(vm::resetPractice,Modifier.padding(8.dp)) { Text("Practice job accepted · End practice") }
        }
    }
    var hidden by remember { mutableStateOf(false) }
    val keys = state.cards.map { it.key }.toSet()
    var previous by remember { mutableStateOf(emptySet<String>()) }
    LaunchedEffect(keys) { if ((keys - previous).isNotEmpty()) hidden = false; previous = keys }
    if (state.cards.isNotEmpty() && hidden) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.TopCenter) {
            FilledTonalButton({ hidden = false }, Modifier.padding(8.dp)) { Text("${state.cards.size} incoming requests") }
        }
    }
    if (!hidden && (state.cards.isNotEmpty() || state.message != null || state.error != null)) {
        state.cards.firstOrNull()?.let { PartnerRequestSound(it, vm.sounds) }
        Dialog(onDismissRequest = { if(state.responding == null) { hidden = true; vm.clearMessage() } },
            properties = DialogProperties(usePlatformDefaultWidth = false)) {
            PartnerRequestsPanel(state, vm::respond, { hidden = true; vm.clearMessage() })
        }
    }
}

@Composable
internal fun PartnerRequestsPanel(state: PartnerRequestsState, respond: (PartnerRequestCard, Boolean) -> Unit, close: () -> Unit) {
    Surface(Modifier.fillMaxWidth().padding(12.dp).heightIn(max = 720.dp), shape = RoundedCornerShape(24.dp),
        color = MaterialTheme.colorScheme.surface) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Incoming requests", Modifier.weight(1f), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                TextButton(close, enabled = state.responding == null) { Text("Hide") }
            }
            Text("Compare location and amount. Choose the request that suits you.", style = MaterialTheme.typography.bodySmall)
            state.message?.let { Text(it, color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.bodyMedium) }
            state.error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }
            LazyColumn(Modifier.weight(1f, fill = false), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                items(state.cards, key = { it.key }) { card ->
                    Surface(shape = RoundedCornerShape(16.dp), color = MaterialTheme.colorScheme.surfaceContainerLow) {
                        Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text("${if(card.request.testOnly) "TEST · " else ""}${card.request.category.label}", Modifier.weight(1f),
                                    style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
                                Text("${card.request.secondsLeft(state.now)}s", style = MaterialTheme.typography.labelLarge)
                            }
                            Text(card.service, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                            Text(card.location, style = MaterialTheme.typography.bodySmall)
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Text(card.amountLabel, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
                                Text(card.amount?.let(::commerceMoney) ?: "Not available", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                            }
                            if (!card.confirmed) Text("Reconnecting to check this request…", style = MaterialTheme.typography.bodySmall)
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                val enabled = state.responding == null && card.confirmed && card.request.secondsLeft(state.now) > 0
                                UsSecondaryButton("Decline", { respond(card, false) }, enabled = enabled, modifier = Modifier.weight(1f))
                                UsButton(if(state.responding == card.key) "Please wait…" else "Accept", { respond(card, true) },
                                    enabled = enabled && card.amount != null, modifier = Modifier.weight(1f))
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun PartnerRequestSound(card: PartnerRequestCard, sounds: DoorstepOfferAlertCoordinator) {
    val context = LocalContext.current
    LaunchedEffect(card.key) {
        val manager = context.getSystemService<NotificationManager>() ?: return@LaunchedEffect
        val audio = context.getSystemService<AudioManager>() ?: return@LaunchedEffect
        val channel = manager.getNotificationChannel(NotificationChannelSpec.forType(card.request.category.wireType).id)
        if (audio.ringerMode != AudioManager.RINGER_MODE_NORMAL || manager.currentInterruptionFilter != NotificationManager.INTERRUPTION_FILTER_ALL ||
            !manager.areNotificationsEnabled() || (channel?.importance ?: 0) < NotificationManager.IMPORTANCE_DEFAULT) return@LaunchedEffect
        if (!sounds.claimSound(card.request.id, card.request.secondsLeft() * 1000)) return@LaunchedEffect
        val tone = channel?.sound?.let { RingtoneManager.getRingtone(context, it) } ?: return@LaunchedEffect
        try { tone.play(); delay(1_500) } finally { tone.stop() }
    }
}
