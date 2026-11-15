package com.us.android.feature.premium.ui

import android.app.Activity
import android.content.Context
import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentStateStore
import com.us.android.feature.premium.PremiumPurchaseSwitch
import com.us.android.feature.premium.data.PremiumInvoice
import com.us.android.feature.premium.data.PremiumPlan
import com.us.android.feature.premium.data.PremiumRepository
import com.us.android.feature.premium.data.PremiumStatus
import com.us.android.feature.premium.data.PremiumWords
import com.us.android.feature.premium.data.invoiceErrorMessage
import com.us.android.feature.premium.data.isPremiumOff
import com.us.android.feature.premium.data.premiumErrorMessage
import com.us.android.feature.premium.payment.PremiumPaymentOpener
import com.us.android.feature.premium.payment.PremiumPaymentRequest
import com.us.android.feature.premium.payment.PremiumPurchaseFlow
import com.us.android.feature.premium.payment.PurchaseState
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.OutputStream
import javax.inject.Inject

/** Which document is downloading, which one failed and why, and the last saved note. */
data class InvoiceDownloadState(
    val pendingId: String? = null,
    val failedId: String? = null,
    val message: String? = null,
    val saved: Boolean = false,
)

data class PremiumUiState(
    /** The server answered PREMIUM_NOT_ENABLED: "Premium is not available yet". */
    val off: Boolean = false,
    /**
     * The app may sell Premium ([PremiumPurchaseSwitch] on). False: the passes and
     * the purchase are not drawn; status and invoices still are (15 Nov 2026).
     */
    val canBuy: Boolean = false,
    val loading: Boolean = true,
    val plans: List<PremiumPlan> = emptyList(),
    val plansError: String? = null,
    val status: PremiumStatus? = null,
    val statusError: String? = null,
    /** The buyer's documents, newest first; empty hides the section. */
    val invoices: List<PremiumInvoice> = emptyList(),
    val purchase: PurchaseState = PurchaseState.Idle,
    val download: InvoiceDownloadState = InvoiceDownloadState(),
)

/** Where a picked document is written. A seam, so the ViewModel tests against a fake. */
fun interface InvoiceFileTarget {
    fun open(uri: Uri): OutputStream?
}

/** The production [InvoiceFileTarget]: the document the buyer picked in "Save to", opened for writing. */
class ContentResolverInvoiceTarget @Inject constructor(
    @ApplicationContext private val context: Context,
) : InvoiceFileTarget {
    override fun open(uri: Uri): OutputStream? = context.contentResolver.openOutputStream(uri, "wt")
}

/**
 * Momentum Premium (2026-11-13): the viewer's status, the passes with the
 * server's GST breakdown, the purchase ([PremiumPurchaseFlow]), and the tax
 * invoices and credit notes.
 *
 * With the app's [PremiumPurchaseSwitch] off (15 Nov 2026) it shows the
 * viewer's status and invoices only: no plans or checkout are ever asked,
 * and no pass can be bought — a web buyer still reaches their documents.
 */
// Constructor injection of the screen's collaborators; a wrapper would add indirection, not clarity.
@Suppress("LongParameterList", "TooManyFunctions")
@HiltViewModel
class PremiumViewModel @Inject constructor(
    private val switch: PremiumPurchaseSwitch,
    private val repository: PremiumRepository,
    private val opener: PremiumPaymentOpener,
    private val files: InvoiceFileTarget,
    handoff: PaymentHandoff,
    payments: PaymentCoordinator,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val flow: PremiumPurchaseFlow? = if (switch.enabled) {
        PremiumPurchaseFlow(
            scope = viewModelScope,
            store = object : PaymentStateStore {
                override fun get(key: String): String? = savedState[key]
                override fun set(key: String, value: String?) {
                    savedState[key] = value
                }
            },
            repository = repository,
            handoff = handoff,
            payments = payments,
        )
    } else {
        null
    }

    private val _state = MutableStateFlow(PremiumUiState(canBuy = switch.enabled))
    val state: StateFlow<PremiumUiState> = _state.asStateFlow()

    /** The document the buyer is choosing a place for, between Download and "Save to". */
    private var downloadTarget: String? = null

    init {
        flow?.let { f ->
            viewModelScope.launch {
                var previous: PurchaseState = PurchaseState.Idle
                f.state.collect { purchase ->
                    _state.update {
                        it.copy(purchase = purchase, off = it.off || purchase == PurchaseState.Unavailable)
                    }
                    if (purchase is PurchaseState.Paid && paidNews(previous, purchase)) refreshAfterPaid()
                    previous = purchase
                }
            }
            viewModelScope.launch {
                load()
                f.resume()
            }
        }
        if (flow == null) viewModelScope.launch { load() }
    }

    fun refresh() {
        _state.update { it.copy(loading = true) }
        viewModelScope.launch { load() }
    }

    fun buy(plan: PremiumPlan) = flow?.buy(plan.code)

    fun pay() = flow?.pay()

    fun checkAgain() = flow?.checkAgain()

    fun retry() = flow?.retry()

    fun resetPurchase() = flow?.reset()

    fun openSheet(activity: Activity, request: PremiumPaymentRequest) {
        val f = flow ?: return
        opener.start(activity, request)
        f.onSheetOpened(request)
    }

    fun abandon() {
        val f = flow ?: return
        val opening = _state.value.purchase as? PurchaseState.OpeningPayment ?: return
        if (opening.request.attempt == f.activeAttempt) opener.abandon(opening.request)
    }

    /** Download pressed on [invoiceId]: the screen opens "Save to"; [saveTo] gets the answer. */
    fun startDownload(invoiceId: String): Boolean {
        if (_state.value.off || _state.value.download.pendingId != null) return false
        downloadTarget = invoiceId
        return true
    }

    /** The picked file, or null when the buyer backed out of "Save to". */
    fun saveTo(uri: Uri?) = saveWith(uri?.let { picked -> { files.open(picked) } })

    internal fun saveWith(open: (() -> OutputStream?)?) {
        val id = downloadTarget ?: return
        downloadTarget = null
        if (open == null) return
        _state.update { it.copy(download = InvoiceDownloadState(pendingId = id)) }
        viewModelScope.launch {
            val stream = withContext(Dispatchers.IO) { runCatching { open() }.getOrNull() }
            if (stream == null) {
                val failed = InvoiceDownloadState(failedId = id, message = PremiumWords.INVOICE_NO_FILE)
                _state.update { it.copy(download = failed) }
                return@launch
            }
            val result = stream.use { repository.saveInvoiceDocument(id, it) }
            _state.update {
                it.copy(
                    download = when (result) {
                        is AppResult.Success -> InvoiceDownloadState(message = PremiumWords.INVOICE_SAVED, saved = true)
                        is AppResult.Failure ->
                            InvoiceDownloadState(failedId = id, message = invoiceErrorMessage(result.error))
                    },
                )
            }
        }
    }

    fun dismissDownloadMessage() = _state.update { it.copy(download = InvoiceDownloadState()) }

    private suspend fun load() {
        val plans = if (switch.enabled) repository.plans() else AppResult.Success(emptyList())
        val me = repository.me()
        val off = plans.offError() || me.offError()
        _state.update {
            it.copy(
                off = off || it.purchase == PurchaseState.Unavailable,
                loading = false,
                plans = (plans as? AppResult.Success)?.data ?: it.plans,
                plansError = (plans as? AppResult.Failure)?.let { f -> premiumErrorMessage(f.error) },
                status = (me as? AppResult.Success)?.data ?: it.status,
                statusError = (me as? AppResult.Failure)?.let { f -> premiumErrorMessage(f.error) },
            )
        }
        if (!off) loadInvoices()
    }

    private suspend fun loadInvoices() {
        val rows = (repository.invoices() as? AppResult.Success)?.data ?: return
        _state.update { it.copy(invoices = rows) }
    }

    /** Newly paid, or the invoice just landed. */
    private fun paidNews(previous: PurchaseState, now: PurchaseState.Paid): Boolean =
        previous !is PurchaseState.Paid || (previous.invoiceId == null && now.invoiceId != null)

    /** A paid pass: the status card and the Invoices list read the server again (once more when the invoice lands). */
    private fun refreshAfterPaid() {
        viewModelScope.launch {
            val status = (repository.me() as? AppResult.Success)?.data
            if (status != null) _state.update { it.copy(status = status, statusError = null) }
            loadInvoices()
        }
    }

    private fun AppResult<*>.offError(): Boolean = this is AppResult.Failure && error.isPremiumOff()
}
