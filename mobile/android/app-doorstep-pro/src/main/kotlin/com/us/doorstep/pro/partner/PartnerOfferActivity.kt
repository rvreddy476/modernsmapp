package com.us.doorstep.pro.partner

import android.content.Context
import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.viewModels
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.core.notifications.PartnerOfferCategory
import com.us.android.core.notifications.PartnerOfferInbox
import com.us.doorstep.pro.BuildConfig
import com.us.doorstep.pro.DoorstepProActivity
import dagger.hilt.android.AndroidEntryPoint
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.JsonObject
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

/** Shared across duplicate notification taps. Only acknowledged decisions are persisted. */
@Singleton
internal class PartnerOfferResponses @Inject constructor(@ApplicationContext context: Context) {
    val mutex = Mutex()
    private val prefs = context.getSharedPreferences("partner_offer_responses", Context.MODE_PRIVATE)
    fun get(request: PartnerOfferRequest) = prefs.getString(key(request), null)
    fun put(request: PartnerOfferRequest, message: String) {
        // Bounded history; no addresses, credentials or customer data.
        val edit = prefs.edit()
        if (prefs.all.size >= 100) edit.clear()
        edit.putString(key(request), message).commit()
    }
    private fun key(request: PartnerOfferRequest) = "${request.testOnly}:${request.category}:${request.id}"
}

internal data class PartnerOfferActionState(
    val request: PartnerOfferRequest? = null, val seconds: Long = 0, val busy: Boolean = true,
    val ready: Boolean = false, val details: JsonObject? = null, val result: String? = null, val error: String? = null,
)

@HiltViewModel
internal class PartnerOfferActionViewModel @Inject constructor(
    saved: SavedStateHandle, private val decision: PartnerOfferDecision,
    private val responses: PartnerOfferResponses, private val notifications: NotificationPresenter,
    private val inbox: PartnerOfferInbox,
) : ViewModel() {
    private val request = PartnerOfferRequest.parse(PartnerOfferCategory.EXTRA_KEYS.mapNotNull { key ->
        saved.get<String>(key)?.let { key to it }
    }.toMap())
    private val mutable = MutableStateFlow(PartnerOfferActionState(request = request))
    val state = mutable.asStateFlow()
    init {
        viewModelScope.launch {
            while (isActive) {
                mutable.update { it.copy(seconds = request?.secondsLeft() ?: 0) }
                delay(250)
            }
        }
        // SavedStateHandle retains this guard even after process recreation: do not auto-submit again.
        val action = if (saved.get<Boolean>("action_started") == true) "view" else saved.get<String>("decision") ?: "view"
        saved["action_started"] = true
        viewModelScope.launch {
            try {
                require(request != null) { "This request could not be opened." }
                require(!request.testOnly || BuildConfig.DEBUG) { "Practice requests are disabled in this build." }
                val previous = responses.get(request)
                if (previous != null) mutable.update { it.copy(result = previous) }
                else if (action in setOf("accept", "decline")) respondInternal(action == "accept")
                else {
                    check(request.secondsLeft() > 0) { "This request has expired." }
                    val details = if (request.testOnly) null else withTimeout(12_000) { decision.read(request) }
                    mutable.update { it.copy(ready = true, details = details) }
                }
            } catch (error: Exception) { failure(error) }
            finally { mutable.update { it.copy(busy = false) } }
        }
    }
    fun respond(accept: Boolean) {
        if (mutable.value.busy || mutable.value.result != null || !mutable.value.ready) return
        mutable.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try { respondInternal(accept) } catch (error: Exception) { failure(error) }
            finally { mutable.update { it.copy(busy = false) } }
        }
    }
    private suspend fun respondInternal(accept: Boolean) = responses.mutex.withLock {
        val req = requireNotNull(request)
        responses.get(req)?.let { prior -> mutable.update { it.copy(result = prior) }; return@withLock }
        check(req.secondsLeft() > 0) { "This request has expired." }
        check(!req.testOnly || BuildConfig.DEBUG) { "Practice requests are disabled in this build." }
        check(!inbox.isBlocked(req.testOnly)) { "You already accepted a request. Finish your current work before accepting another." }
        if (!req.testOnly) withTimeout(12_000) { decision.submit(req, accept) }
        if (accept) { inbox.accepted(req.testOnly); notifications.cancelPartnerOffers(req.testOnly) }
        val result = if (req.testOnly) {
            "TEST ${if (accept) "accepted" else "declined"}. No job, payment or earnings were created."
        } else if (accept) "Request accepted. Open Work to view your assignment." else "Request declined."
        responses.put(req, result)
        notifications.cancelForSubject(req.id)
        mutable.update { it.copy(result = result, ready = false) }
    }
    private fun failure(error: Exception) {
        if (error is CancellationException && error !is TimeoutCancellationException) throw error
        mutable.update { it.copy(ready = false, error = if (error is IllegalStateException || error is IllegalArgumentException)
            error.message else "Couldn't confirm your response. Open Work to check the latest status.") }
    }
}

/** Non-exported Activity PendingIntents let Android unlock first and avoid notification trampolines. */
@AndroidEntryPoint
class PartnerOfferActivity : ComponentActivity() {
    private val viewModel: PartnerOfferActionViewModel by viewModels()
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            UsTheme(darkTheme = false) {
                val state by viewModel.state.collectAsStateWithLifecycle()
                Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                    Column(Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState()).padding(24.dp),
                        verticalArrangement = Arrangement.spacedBy(20.dp)) {
                        Text(state.request?.category?.label ?: "Partner request", style = MaterialTheme.typography.headlineMedium)
                        if (state.request?.testOnly == true) Text("TEST ONLY · Practice request", color = MaterialTheme.colorScheme.primary)
                        Text(if (state.result != null) "Response recorded" else "New work request", style = MaterialTheme.typography.titleLarge)
                        if (state.result == null) {
                            Text(if (state.seconds > 0) "${state.seconds}s remaining" else "Request expired", style = MaterialTheme.typography.headlineLarge)
                            Text("Offers have a 20-second response window. Delivery and opening time count toward that window.")
                        }
                        if (state.request?.testOnly == true) Text("This tests notification actions and expiry only. It cannot assign work, contact a customer or take a payment.")
                        state.details?.let { details ->
                            listOf("service_name", "pickup_label", "drop_label", "pickup_address", "drop_address", "locality").forEach { key ->
                                details.string(key).takeIf { it.isNotBlank() }?.let { Text(it) }
                            }
                            listOf("payout_paise", "earning_estimate_paise", "estimated_fare_paise").firstOrNull { details.containsKey(it) }?.let {
                                Text("Estimated earnings: ${commerceMoney(details.long(it))}")
                            }
                        }
                        state.result?.let { Text(it, style = MaterialTheme.typography.titleMedium) }
                        state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                        if (state.busy) { CircularProgressIndicator(); Text("Checking the latest request status…") }
                        if (state.result == null && state.ready) {
                            UsButton("Accept", { viewModel.respond(true) }, enabled = !state.busy && state.seconds > 0, modifier = Modifier.fillMaxWidth())
                            UsSecondaryButton("Decline", { viewModel.respond(false) }, enabled = !state.busy && state.seconds > 0, modifier = Modifier.fillMaxWidth())
                        }
                        UsSecondaryButton(if (state.request?.testOnly == true) "Close test" else "Open Work", {
                            if (state.request?.testOnly != true) {
                                startActivity(Intent(this@PartnerOfferActivity, DoorstepProActivity::class.java)
                                    .putExtra("partner_work_category", state.request?.category?.name)
                                    .addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP))
                            }
                            finish()
                        }, modifier = Modifier.fillMaxWidth())
                    }
                }
            }
        }
    }
}
