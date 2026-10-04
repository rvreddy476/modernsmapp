package com.us.android.feature.doorstep.bookings

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.data.BookingPhotoDto
import com.us.android.feature.doorstep.data.CancelPreviewDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.ExtraDto
import com.us.android.feature.doorstep.data.ExtrasBillDto
import com.us.android.feature.doorstep.data.ReworkRequestDto
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.domain.BookingRules
import com.us.android.feature.doorstep.domain.BookingStatus
import com.us.android.feature.doorstep.domain.ExtrasSummary
import com.us.android.feature.doorstep.domain.TimelineStep
import com.us.android.feature.doorstep.payment.BillPayment
import com.us.android.feature.doorstep.realtime.BookingEventStream
import com.us.android.feature.doorstep.realtime.DoorstepTopics
import com.us.android.feature.doorstep.ui.DoorstepMediaUrls
import com.us.android.feature.doorstep.ui.errorMessage
import com.us.android.feature.doorstep.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class BookingDetailUiState(
    val loading: Boolean = true,
    val booking: BookingDto? = null,
    val extras: List<ExtraDto> = emptyList(),
    val bill: ExtrasBillDto? = null,
    val rework: List<ReworkRequestDto> = emptyList(),
    val reworkSlots: List<com.us.android.feature.doorstep.data.SlotDto> = emptyList(),
    /** The SSE stream is connected. When false the screen is kept fresh by polling. */
    val live: Boolean = false,
    val error: String? = null,
    val message: UsMessage? = null,
    val decidingExtraId: String? = null,
    /** Non-null while the cancel dialog shows the server's fee preview. */
    val cancelPreview: CancelPreviewDto? = null,
    val cancelling: Boolean = false,
    val rated: Boolean = false,
    val reworkRequested: Boolean = false,
    val submitting: Boolean = false,
    /** A share link to hand to the system share sheet, once. */
    val shareUrl: String? = null,
    val conversation: com.us.android.feature.doorstep.data.MessagePageDto? = null,
    val chatBusy: Boolean = false,
    val chatLoading: Boolean = false,
    val chatError: String? = null,
    val trustedContact: com.us.android.feature.doorstep.data.TrustedContactDto? = null,
    val contactBusy: Boolean = false,
    val contactError: String? = null,
    val tickets: List<com.us.android.feature.doorstep.data.TicketDto> = emptyList(),
    val ticketBusy: Boolean = false,
    val ticketError: String? = null,
) {
    val status: BookingStatus get() = BookingStatus.of(booking?.status)

    /** The start OTP, only in the window the professional may ask for it. */
    val startOtp: String? get() = booking?.let(BookingRules::visibleStartOtp)

    /** The finish OTP, only while the job is in progress. */
    val endOtp: String? get() = booking?.let(BookingRules::visibleEndOtp)

    /** Drawn from the booking's `status_history`; empty before the booking loads. */
    val timeline: List<TimelineStep> get() = booking?.let(BookingRules::timeline).orEmpty()

    /** Before then After, each oldest first. */
    val photos: List<Pair<String, List<BookingPhotoDto>>> get() = booking?.let(BookingRules::visitPhotos).orEmpty()

    val extrasSummary: ExtrasSummary get() = ExtrasSummary.of(extras)
}

/**
 * One booking, live: the timeline, the professional once assigned, the start
 * OTP only while it may be asked for, extras to approve or decline and the
 * extras bill to pay, then rating and rework; cancel (fee previewed first),
 * reschedule, SOS and the share link while someone is coming or in the home.
 *
 * Realtime is the fast path; a poll keeps it honest (SSE reconnects silently)
 * and stands in when the stream is refused. Any frame on the booking's topic
 * re-reads the booking rather than patching it, so a missed event can never
 * leave a wrong status on screen.
 */
@HiltViewModel
class BookingDetailViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepRepository,
    private val stream: BookingEventStream,
    handoff: PaymentHandoff,
    payments: PaymentCoordinator,
    /** Where visit photos and extras evidence load from. */
    val mediaUrls: DoorstepMediaUrls,
) : ViewModel() {

    private val bookingId: String =
        checkNotNull(savedStateHandle.get<String>("bookingId")) { "navigation argument 'bookingId' is missing" }

    private val _state = MutableStateFlow(BookingDetailUiState())
    val state: StateFlow<BookingDetailUiState> = _state.asStateFlow()

    /** The extras bill's payment: same rules as checkout, confirmed by the server only. */
    val billPayment = BillPayment(viewModelScope, repository, handoff, payments, savedStateHandle, onSettled = { refreshNow() })

    private var streamJob: Job? = null
    private var pollJob: Job? = null
    private var chatJob: Job? = null

    fun openChat() {
        chatJob?.cancel()
        chatJob = viewModelScope.launch {
            while (true) {
                loadChat()
                delay(15_000)
            }
        }
    }

    fun closeChat() { chatJob?.cancel(); chatJob = null }

    private suspend fun loadChat(cursor: String? = null) {
        if (_state.value.chatLoading) return
        _state.update { it.copy(chatLoading = true) }
        // Refresh the loaded window without dropping pages the user opened.
        val target = _state.value.conversation?.items?.size ?: 0
        val items = if (cursor == null) mutableListOf() else _state.value.conversation?.items.orEmpty().toMutableList()
        var next = cursor
        do {
            when (val result = repository.messages(bookingId, next)) {
                is DoorstepResult.Success -> {
                    items.addAll(result.value.items)
                    next = result.value.nextCursor
                    _state.update { it.copy(conversation = result.value.copy(items = items.distinctBy { message -> message.id }), chatError = null) }
                }
                is DoorstepResult.Failure -> { _state.update { it.copy(chatLoading = false, chatError = result.error.userMessage()) }; return }
            }
        } while (cursor == null && next != null && items.size < target)
        _state.update { it.copy(chatLoading = false) }
        _state.value.conversation?.items.orEmpty().filter { it.senderKind == "pro" && it.readAt == null }.forEach {
            repository.readMessage(bookingId, it.id)
        }
    }

    fun loadMoreMessages() = viewModelScope.launch { _state.value.conversation?.nextCursor?.let { loadChat(it) } }

    fun sendMessage(body: String, onSent: () -> Unit) {
        val text = body.trim()
        if (text.isBlank() || text.length > 1000 || _state.value.chatBusy) return
        _state.update { it.copy(chatBusy = true, chatError = null) }
        viewModelScope.launch {
            when (val result = repository.sendMessage(bookingId, text)) {
                is DoorstepResult.Success -> { onSent(); loadChat() }
                is DoorstepResult.Failure -> _state.update { it.copy(chatError = result.error.userMessage()) }
            }
            _state.update { it.copy(chatBusy = false) }
        }
    }

    fun loadTrustedContact() = viewModelScope.launch {
        when (val result = repository.trustedContact()) {
            is DoorstepResult.Success -> _state.update { it.copy(trustedContact = result.value) }
            is DoorstepResult.Failure -> _state.update { it.copy(message = errorMessage(result.error.userMessage())) }
        }
    }

    fun saveTrustedContact(name: String, phone: String, onSaved: () -> Unit) = viewModelScope.launch {
        if (_state.value.contactBusy) return@launch
        _state.update { it.copy(contactBusy = true, contactError = null) }
        when (val result = repository.saveTrustedContact(name.trim(), phone.trim())) {
            is DoorstepResult.Success -> { _state.update { it.copy(trustedContact = result.value, message = successMessage("Trusted contact saved")) }; onSaved() }
            is DoorstepResult.Failure -> _state.update { it.copy(contactError = result.error.userMessage()) }
        }
        _state.update { it.copy(contactBusy = false) }
    }

    fun revokeShare() = viewModelScope.launch {
        when (val result = repository.revokeShare(bookingId)) {
            is DoorstepResult.Success -> _state.update { it.copy(message = successMessage("Shared link revoked")) }
            is DoorstepResult.Failure -> _state.update { it.copy(message = errorMessage(result.error.userMessage())) }
        }
    }

    fun loadTickets() = viewModelScope.launch {
        when (val result = repository.tickets()) {
            is DoorstepResult.Success -> _state.update { it.copy(tickets = result.value.filter { ticket -> ticket.bookingId == bookingId }, ticketError = null) }
            is DoorstepResult.Failure -> _state.update { it.copy(ticketError = result.error.userMessage()) }
        }
    }

    fun openTicket(category: String, subject: String, body: String, onSaved: () -> Unit) = viewModelScope.launch {
        if (_state.value.ticketBusy || subject.isBlank() || body.isBlank()) return@launch
        _state.update { it.copy(ticketBusy = true, ticketError = null) }
        when (val result = repository.openTicket(com.us.android.feature.doorstep.data.TicketInputDto(bookingId, category, subject.trim(), body.trim()))) {
            is DoorstepResult.Success -> { _state.update { it.copy(tickets = listOf(result.value) + it.tickets, message = successMessage("Support request sent")) }; onSaved() }
            is DoorstepResult.Failure -> _state.update { it.copy(ticketError = result.error.userMessage()) }
        }
        _state.update { it.copy(ticketBusy = false) }
    }

    init {
        viewModelScope.launch {
            refresh()
            if (BookingRules.isLive(_state.value.status)) {
                subscribe()
                poll()
            }
        }
    }

    fun refreshNow() {
        viewModelScope.launch { refresh() }
    }

    fun approveExtra(extraId: String) = decide(extraId, approve = true)

    fun declineExtra(extraId: String) = decide(extraId, approve = false)

    fun payBill() {
        val bill = _state.value.bill ?: return
        billPayment.pay(bookingId, bill.id)
    }

    fun payProfessionalDifference() {
        val intent = com.us.android.feature.doorstep.domain.ProChangeRules.payableIntent(_state.value.booking?.pendingChange) ?: return
        billPayment.payIntent(bookingId, intent)
    }

    fun previewCancel() {
        if (_state.value.cancelling) return
        _state.update { it.copy(cancelling = true) }
        viewModelScope.launch {
            when (val result = repository.cancelPreview(bookingId)) {
                is DoorstepResult.Success -> _state.update { it.copy(cancelling = false, cancelPreview = result.value) }
                is DoorstepResult.Failure -> _state.update {
                    it.copy(cancelling = false, message = errorMessage(result.error.userMessage()))
                }
            }
        }
    }

    fun dismissCancel() = _state.update { it.copy(cancelPreview = null) }

    fun confirmCancel(reason: String) {
        val preview = _state.value.cancelPreview ?: return
        if (!preview.allowed || _state.value.cancelling) return
        _state.update { it.copy(cancelling = true) }
        viewModelScope.launch {
            when (val result = repository.cancel(bookingId, reason.ifBlank { DEFAULT_CANCEL_REASON })) {
                is DoorstepResult.Success -> {
                    _state.update {
                        it.copy(booking = result.value, cancelling = false, cancelPreview = null, message = successMessage("Booking cancelled"))
                    }
                    stopLive()
                }
                is DoorstepResult.Failure -> _state.update {
                    it.copy(cancelling = false, cancelPreview = null, message = errorMessage(result.error.cancelMessage()))
                }
            }
        }
    }

    fun rate(stars: Int, comment: String) {
        if (stars !in 1..MAX_STARS || _state.value.submitting) return
        _state.update { it.copy(submitting = true) }
        viewModelScope.launch {
            when (val result = repository.rate(bookingId, stars, comment)) {
                is DoorstepResult.Success -> _state.update { it.copy(submitting = false, rated = true, message = successMessage("Thanks for rating")) }
                is DoorstepResult.Failure -> _state.update {
                    // Rated already (another device): the form is done either way.
                    val already = result.error.code == DoorstepCodes.RATING_EXISTS
                    it.copy(submitting = false, rated = already || it.rated, message = errorMessage(result.error.userMessage()))
                }
            }
        }
    }

    fun loadReworkSlots() {
        viewModelScope.launch {
            when(val result=repository.slots(null,bookingId,null,false)) {
                is DoorstepResult.Success -> _state.update { it.copy(reworkSlots=result.value.days.flatMap { day -> day.slots }.filter { slot -> slot.available }) }
                is DoorstepResult.Failure -> _state.update { it.copy(message=errorMessage(result.error.userMessage())) }
            }
        }
    }

    fun requestRework(reason: String, slotStart: String) {
        if (reason.isBlank() || _state.value.submitting) return
        _state.update { it.copy(submitting = true) }
        viewModelScope.launch {
            when (val result = repository.requestRework(bookingId, reason.trim(), slotStart)) {
                is DoorstepResult.Success -> _state.update {
                    it.copy(submitting = false, reworkRequested = true, rework = it.rework + result.value, message = successMessage("Rework requested"))
                }
                is DoorstepResult.Failure -> _state.update { it.copy(submitting = false, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    fun sos(note: String) {
        viewModelScope.launch {
            when (val result = repository.sos(bookingId, note)) {
                is DoorstepResult.Success -> _state.update {
                    it.copy(message = successMessage("Safety alert recorded. If you are in danger, call 112 now; do not wait for a response."))
                }
                is DoorstepResult.Failure -> _state.update {
                    it.copy(message = errorMessage("Couldn't reach us. If you're in danger, call 112."))
                }
            }
        }
    }

    fun share() {
        viewModelScope.launch {
            when (val result = repository.share(bookingId)) {
                is DoorstepResult.Success -> _state.update { it.copy(shareUrl = result.value.url) }
                is DoorstepResult.Failure -> _state.update { it.copy(message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    fun consumeShareUrl() = _state.update { it.copy(shareUrl = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    private fun decide(extraId: String, approve: Boolean) {
        if (_state.value.decidingExtraId != null) return
        _state.update { it.copy(decidingExtraId = extraId) }
        viewModelScope.launch {
            val result = if (approve) repository.approveExtra(bookingId, extraId) else repository.declineExtra(bookingId, extraId)
            when (result) {
                is DoorstepResult.Success -> {
                    _state.update { s -> s.copy(decidingExtraId = null, extras = s.extras.map { if (it.id == extraId) result.value else it }) }
                    // An approval can open a bill (charge-now threshold): read it back.
                    refresh()
                }
                is DoorstepResult.Failure -> _state.update { it.copy(decidingExtraId = null, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    private suspend fun refresh() {
        when (val result = repository.booking(bookingId)) {
            is DoorstepResult.Failure -> _state.update {
                it.copy(loading = false, error = if (it.booking == null) result.error.detailMessage() else null)
            }
            is DoorstepResult.Success -> {
                val booking = result.value
                val status = BookingStatus.of(booking.status)
                _state.update { it.copy(loading = false, booking = booking, error = null) }
                if (BookingRules.extrasVisible(status)) loadVisit()
                if (status == BookingStatus.COMPLETED) loadRework()
                if (!BookingRules.isLive(status)) stopLive()
            }
        }
    }

    private suspend fun loadVisit() {
        val extras = viewModelScope.async { repository.extras(bookingId) }
        val bill = viewModelScope.async { repository.extrasBill(bookingId) }
        (extras.await() as? DoorstepResult.Success)?.value?.let { list -> _state.update { it.copy(extras = list) } }
        // No bill yet is a 404; that is not an error on this screen.
        _state.update { it.copy(bill = (bill.await() as? DoorstepResult.Success)?.value ?: it.bill) }
    }

    private suspend fun loadRework() {
        (repository.rework(bookingId) as? DoorstepResult.Success)?.value?.let { list -> _state.update { it.copy(rework = list) } }
    }

    @Suppress("TooGenericExceptionCaught")
    private fun subscribe() {
        streamJob?.cancel()
        streamJob = viewModelScope.launch {
            try {
                stream.events(bookingId).collect(::onEvent)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // Refused or ended: the poll keeps the screen fresh.
                _state.update { it.copy(live = false) }
            }
        }
    }

    private fun onEvent(event: RealtimeEvent) {
        if (event is RealtimeEvent.Connected) {
            _state.update { it.copy(live = true) }
            return
        }
        if (DoorstepTopics.isBookingChange(bookingId, event)) refreshNow()
    }

    private fun poll() {
        pollJob?.cancel()
        pollJob = viewModelScope.launch {
            while (true) {
                delay(if (_state.value.live) RECONCILE_MILLIS else POLL_MILLIS)
                refresh()
                if (!BookingRules.isLive(_state.value.status)) break
            }
        }
    }

    private fun stopLive() {
        streamJob?.cancel()
        pollJob?.cancel()
        _state.update { it.copy(live = false) }
    }

    private fun DoorstepError.detailMessage(): String = when {
        this == DoorstepError.NotFound || code == DoorstepCodes.BOOKING_NOT_FOUND -> "We couldn't find that booking."
        else -> userMessage()
    }

    private fun DoorstepError.cancelMessage(): String = when (code) {
        DoorstepCodes.CANCEL_NOT_ALLOWED -> "The job has started, so it can't be cancelled now. Use Help if something is wrong."
        else -> userMessage()
    }

    internal companion object {
        const val POLL_MILLIS = 10_000L
        const val RECONCILE_MILLIS = 30_000L
        const val MAX_STARS = 5
        const val DEFAULT_CANCEL_REASON = "Cancelled by customer"
    }
}
