package com.us.android.feature.doorstep.professionals

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.doorstep.DoorstepSession
import com.us.android.feature.doorstep.ServiceDraft
import com.us.android.feature.doorstep.data.AddressDto
import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.ProChangeRequestDto
import com.us.android.feature.doorstep.data.ProfessionalCardDto
import com.us.android.feature.doorstep.data.ProfessionalListDto
import com.us.android.feature.doorstep.data.ProfessionalQuery
import com.us.android.feature.doorstep.data.QuoteAddonDto
import com.us.android.feature.doorstep.data.QuoteDto
import com.us.android.feature.doorstep.data.QuoteRequestDto
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.domain.BookingMode
import com.us.android.feature.doorstep.domain.BookingStatus
import com.us.android.feature.doorstep.domain.ChangeOutcome
import com.us.android.feature.doorstep.domain.ProChangeRules
import com.us.android.feature.doorstep.domain.ProfessionalListView
import com.us.android.feature.doorstep.domain.ProfessionalPick
import com.us.android.feature.doorstep.domain.ProfessionalRules
import com.us.android.feature.doorstep.domain.ProfessionalSort
import com.us.android.feature.doorstep.ui.errorMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where the professionals step goes next. */
sealed interface ProfessionalsOutcome {
    /** A new booking: checkout for this quote (priced with the picked professional) at a time or ASAP. */
    data class Checkout(
        val quoteId: String,
        val addressId: String,
        val slotStart: String?,
        val asap: Boolean,
        val requireFemalePro: Boolean,
        val proFirstName: String,
        val etaMinutes: Int?,
    ) : ProfessionalsOutcome

    /** The picked professional's full calendar (the slot picker on their quote). */
    data class MoreTimes(val quoteId: String, val addressId: String, val proFirstName: String) : ProfessionalsOutcome

    /** A change for a pro_unavailable booking is waiting for its difference: the booking screen pays it. */
    data class BackToBooking(val bookingId: String) : ProfessionalsOutcome
}

data class ProfessionalsUiState(
    /** True when listing alternatives for a pro_unavailable booking. */
    val forBooking: Boolean,
    val loading: Boolean = true,
    val mode: BookingMode = BookingMode.SCHEDULED,
    /** The chosen day (YYYY-MM-DD, India time); null = the next free starts from now. */
    val date: String? = null,
    val sort: ProfessionalSort = ProfessionalSort.PRICE,
    val list: ProfessionalListDto? = null,
    val serviceName: String? = null,
    val address: AddressDto? = null,
    /** The pro_unavailable booking (change mode), for its cause and choice deadline. */
    val booking: BookingDto? = null,
    /** The pick being turned into a quote or a change. */
    val picking: ProfessionalPick? = null,
    /** Change mode: a cheaper (or equal) professional was confirmed at once. */
    val applied: ChangeOutcome.Applied? = null,
    /** Change mode: the booking no longer takes a pick (window closed, or it moved on). */
    val choiceClosed: String? = null,
    val blockedByDues: Boolean = false,
    /** New booking: the service pick was lost (process death). */
    val draftLost: Boolean = false,
    val error: String? = null,
    val message: UsMessage? = null,
    val outcome: ProfessionalsOutcome? = null,
) {
    val view: ProfessionalListView? get() = list?.let(ProfessionalRules::view)
}

/**
 * B1: the professionals step. For a NEW booking it lists the approved
 * professionals for the service pick (kept in [DoorstepSession]) at the
 * address — price for that pick, rating, a distance band, next free times,
 * sorted by price, rating or soonest — scheduled, or "as soon as possible"
 * with an ETA. ASAP with nobody shows the scheduled alternatives the server
 * sent for the SAME pick and address. A pick prices a quote with that
 * professional (`pro_id`) and goes to checkout.
 *
 * For a pro_unavailable BOOKING it lists the alternatives (each with the
 * difference against what was paid) and a pick is a change of professional:
 * one Idempotency-Key per decision, saved BEFORE the request leaves and reused
 * on a resend; cheaper or equal is confirmed at once with the refund shown;
 * dearer goes back to the booking, where the difference is paid through the
 * extras-bill path and confirmed only by the payment status.
 */
@HiltViewModel
class ProfessionalsViewModel @Inject constructor(
    private val savedStateHandle: SavedStateHandle,
    private val repository: DoorstepRepository,
    private val session: DoorstepSession,
) : ViewModel() {

    private val addressId: String? = savedStateHandle.get<String>("addressId")
    private val bookingId: String? = savedStateHandle.get<String>("bookingId")

    private val _state = MutableStateFlow(ProfessionalsUiState(forBooking = bookingId != null))
    val state: StateFlow<ProfessionalsUiState> = _state.asStateFlow()

    private var loadJob: Job? = null

    init {
        load()
    }

    fun load() {
        loadJob?.cancel()
        _state.update { it.copy(loading = true, error = null) }
        loadJob = viewModelScope.launch { if (bookingId != null) loadForBooking(bookingId) else loadNew() }
    }

    fun setMode(mode: BookingMode) {
        if (_state.value.mode == mode) return
        _state.update { it.copy(mode = mode, date = if (mode == BookingMode.ASAP) null else it.date) }
        load()
    }

    /** A day for the scheduled list (null: the next free starts); never with ASAP. */
    fun setDate(date: String?) {
        if (_state.value.date == date && _state.value.mode == BookingMode.SCHEDULED) return
        _state.update { it.copy(mode = BookingMode.SCHEDULED, date = date) }
        load()
    }

    fun setSort(sort: ProfessionalSort) {
        if (_state.value.sort == sort) return
        _state.update { it.copy(sort = sort) }
        load()
    }

    /** Book [card] with [pick]: a quote (new booking) or a change of professional (pro_unavailable booking). */
    fun pick(card: ProfessionalCardDto, pick: ProfessionalPick) {
        if (_state.value.picking != null) return
        _state.update { it.copy(picking = pick, message = null) }
        viewModelScope.launch {
            if (bookingId != null) change(bookingId, card, pick) else quoteAndCheckout(card, pick)
        }
    }

    /** The full calendar of [card]'s professional: a quote with them, then the slot picker on it. */
    fun moreTimes(card: ProfessionalCardDto) {
        if (_state.value.picking != null || bookingId != null) return
        val first = card.nextSlots.firstOrNull() ?: return
        _state.update { it.copy(picking = ProfessionalPick.At(card.proId, first.start, first.end)) }
        viewModelScope.launch {
            val quote = quoteFor(card) ?: return@launch
            val address = _state.value.address ?: return@launch
            _state.update { it.copy(picking = null, outcome = ProfessionalsOutcome.MoreTimes(quote.id, address.id, card.firstName)) }
        }
    }

    fun consumeOutcome() = _state.update { it.copy(outcome = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    // ── New booking ──

    private suspend fun loadNew() {
        val draft = session.draft.value
        if (draft == null) {
            _state.update { it.copy(loading = false, draftLost = true) }
            return
        }
        val address = resolveAddress() ?: run {
            _state.update { it.copy(loading = false, error = "Pick an address for the visit first.") }
            return
        }
        _state.update { it.copy(serviceName = draft.service.name, address = address) }
        val s = _state.value
        val query = ProfessionalQuery(
            serviceId = draft.service.id,
            optionId = draft.selection.optionId.orEmpty(),
            quantity = draft.selection.quantity,
            addonIds = draft.selection.addonIds,
            addressId = address.id,
            date = s.date.takeIf { s.mode == BookingMode.SCHEDULED },
            asap = s.mode == BookingMode.ASAP,
            sort = s.sort.wire,
            requireFemalePro = draft.selection.requireWoman,
        )
        applyList(repository.serviceProfessionals(query))
    }

    private suspend fun quoteAndCheckout(card: ProfessionalCardDto, pick: ProfessionalPick) {
        val quote = quoteFor(card) ?: return
        val address = _state.value.address ?: return
        val requireWoman = session.draft.value?.selection?.requireWoman ?: false
        val outcome = ProfessionalsOutcome.Checkout(
            quoteId = quote.id,
            addressId = address.id,
            slotStart = (pick as? ProfessionalPick.At)?.slotStart,
            asap = pick is ProfessionalPick.Asap,
            requireFemalePro = requireWoman,
            proFirstName = card.firstName,
            etaMinutes = (pick as? ProfessionalPick.Asap)?.etaMinutes,
        )
        _state.update { it.copy(picking = null, outcome = outcome) }
    }

    /** `POST /quotes` with the picked professional: THEIR approved prices for the kept pick. */
    private suspend fun quoteFor(card: ProfessionalCardDto): QuoteDto? {
        val draft: ServiceDraft = session.draft.value ?: run {
            _state.update { it.copy(picking = null, draftLost = true) }
            return null
        }
        val address = _state.value.address ?: return null
        val request = QuoteRequestDto(
            serviceId = draft.service.id,
            proId = card.proId,
            optionId = draft.selection.optionId.orEmpty(),
            quantity = draft.selection.quantity,
            addons = draft.selection.addonIds.sorted().map(::QuoteAddonDto),
            lat = address.lat,
            lng = address.lng,
        )
        return when (val result = repository.createQuote(request)) {
            is DoorstepResult.Success -> result.value
            is DoorstepResult.Failure -> {
                refused(result.error)
                null
            }
        }
    }

    private suspend fun resolveAddress(): AddressDto? {
        session.address.value?.takeIf { addressId == null || it.id == addressId }?.let { return it }
        val all = (repository.addresses() as? DoorstepResult.Success)?.value ?: return null
        return all.firstOrNull { it.id == addressId } ?: all.firstOrNull { it.isDefault }
    }

    // ── A pro_unavailable booking ──

    private suspend fun loadForBooking(bookingId: String) {
        when (val result = repository.booking(bookingId)) {
            is DoorstepResult.Success -> {
                val booking = result.value
                _state.update { it.copy(booking = booking, serviceName = booking.serviceName, address = booking.address) }
                if (BookingStatus.of(booking.status) != BookingStatus.PRO_UNAVAILABLE) {
                    _state.update { it.copy(loading = false, choiceClosed = NOT_WAITING) }
                    return
                }
            }
            // The list below answers for itself; the header just goes without the deadline.
            is DoorstepResult.Failure -> Unit
        }
        val s = _state.value
        applyList(
            repository.bookingProfessionals(
                bookingId,
                date = s.date.takeIf { s.mode == BookingMode.SCHEDULED },
                asap = s.mode == BookingMode.ASAP,
                sort = s.sort.wire,
            ),
        )
    }

    private suspend fun change(bookingId: String, card: ProfessionalCardDto, pick: ProfessionalPick) {
        val request = when (pick) {
            is ProfessionalPick.At -> ProChangeRequestDto(proId = card.proId, slotStart = pick.slotStart)
            is ProfessionalPick.Asap -> ProChangeRequestDto(proId = card.proId, asap = true)
        }
        // One key per decision, in saved state BEFORE the request leaves: a
        // resend of the same pick (lost response, process death) answers the
        // same change instead of making a second one.
        val key = changeKey(signature(request))
        when (val result = repository.changeProfessional(key, bookingId, request)) {
            is DoorstepResult.Success -> {
                clearChangeKey()
                when (val outcome = ProChangeRules.outcome(result.value.change)) {
                    is ChangeOutcome.Applied -> _state.update { it.copy(picking = null, applied = outcome, booking = result.value.booking) }
                    is ChangeOutcome.AwaitingPayment ->
                        _state.update { it.copy(picking = null, outcome = ProfessionalsOutcome.BackToBooking(bookingId)) }
                    is ChangeOutcome.NotUsable -> {
                        _state.update { it.copy(picking = null, message = errorMessage(outcome.why)) }
                        load()
                    }
                }
            }
            is DoorstepResult.Failure -> {
                // A definite refusal made no change: the next pick is a new decision.
                if (result.error !is DoorstepError.Network) clearChangeKey()
                refused(result.error)
            }
        }
    }

    private fun signature(request: ProChangeRequestDto): String = "${request.proId}|${request.slotStart ?: "asap"}"

    private fun changeKey(signature: String): String {
        val saved = savedStateHandle.get<String>(KEY_CHANGE_KEY)
        if (saved != null && savedStateHandle.get<String>(KEY_CHANGE_SIGNATURE) == signature) return saved
        val fresh = repository.newIdempotencyKey()
        savedStateHandle[KEY_CHANGE_SIGNATURE] = signature
        savedStateHandle[KEY_CHANGE_KEY] = fresh
        return fresh
    }

    private fun clearChangeKey() {
        savedStateHandle[KEY_CHANGE_KEY] = null
        savedStateHandle[KEY_CHANGE_SIGNATURE] = null
    }

    // ── Shared ──

    private fun applyList(result: DoorstepResult<ProfessionalListDto>) {
        when (result) {
            is DoorstepResult.Success -> _state.update { it.copy(loading = false, list = result.value, error = null) }
            is DoorstepResult.Failure -> when {
                result.error.code == DoorstepCodes.OUTSTANDING_DUE -> _state.update { it.copy(loading = false, blockedByDues = true) }
                // GET /bookings/{id}/professionals in any status but pro_unavailable.
                bookingId != null && result.error.code == DoorstepCodes.INVALID_TRANSITION ->
                    _state.update { it.copy(loading = false, choiceClosed = NOT_WAITING) }
                else -> _state.update { it.copy(loading = false, error = result.error.userMessage()) }
            }
        }
    }

    /** A refused pick: say why, and list again when what was on screen is stale. */
    private fun refused(error: DoorstepError) {
        _state.update { it.copy(picking = null) }
        when (error.code) {
            DoorstepCodes.CHOICE_WINDOW_CLOSED -> _state.update { it.copy(choiceClosed = error.userMessage()) }
            DoorstepCodes.OUTSTANDING_DUE -> _state.update { it.copy(blockedByDues = true) }
            DoorstepCodes.INVALID_TRANSITION -> _state.update { it.copy(choiceClosed = NOT_WAITING) }
            in STALE_LIST -> {
                _state.update { it.copy(message = errorMessage(error.userMessage())) }
                load()
            }
            else -> _state.update { it.copy(message = errorMessage(error.userMessage())) }
        }
    }

    internal companion object {
        const val KEY_CHANGE_KEY = "doorstep.change.key"
        const val KEY_CHANGE_SIGNATURE = "doorstep.change.signature"
        const val NOT_WAITING = "This booking doesn't need a new professional any more."

        /** Refusals that mean the list on screen is out of date. */
        val STALE_LIST = setOf(
            DoorstepCodes.SLOT_TAKEN,
            DoorstepCodes.SLOT_UNAVAILABLE,
            DoorstepCodes.PRICE_UNAVAILABLE,
            DoorstepCodes.QUOTE_EXPIRED,
        )
    }
}
