package com.us.android.feature.doorstep.booking

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.doorstep.DoorstepSession
import com.us.android.feature.doorstep.data.AddressDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.QuoteAddonDto
import com.us.android.feature.doorstep.data.QuoteDto
import com.us.android.feature.doorstep.data.QuoteRequestDto
import com.us.android.feature.doorstep.data.SlotDayDto
import com.us.android.feature.doorstep.data.SlotDaysDto
import com.us.android.feature.doorstep.data.SlotDto
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.ui.errorMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where the slot step goes next. */
sealed interface SlotOutcome {
    /** A new booking: checkout for this quote, address and slot. */
    data class Checkout(val quoteId: String, val addressId: String, val slotStart: String, val requireFemalePro: Boolean) : SlotOutcome

    /** A reschedule the server accepted. */
    data object Rescheduled : SlotOutcome
}

data class SlotPickerUiState(
    val loading: Boolean = true,
    val rescheduling: Boolean = false,
    val quote: QuoteDto? = null,
    val address: AddressDto? = null,
    val days: List<SlotDayDto> = emptyList(),
    val selectedDate: String? = null,
    val selectedSlot: SlotDto? = null,
    val submitting: Boolean = false,
    /** Unpaid extras: DOORSTEP_OUTSTANDING_DUE. */
    val blockedByDues: Boolean = false,
    /** The service pick was lost (process death): the customer starts from the service again. */
    val draftLost: Boolean = false,
    val error: String? = null,
    val message: UsMessage? = null,
    val outcome: SlotOutcome? = null,
) {
    val slotsOfDay: List<SlotDto> get() = days.firstOrNull { it.date == selectedDate }?.slots.orEmpty()
}

/**
 * The slot step. For a new booking it asks the server to price the pick for
 * this address (`POST /quotes`, GST-inclusive, 15 minutes) and then for the
 * calendar-derived slots of that quote; for a reschedule it asks for the
 * booking's own slots. A slot is only offered when a qualified professional
 * is free — the server decides; the grid only shows its answer.
 */
@HiltViewModel
class SlotPickerViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepRepository,
    private val session: DoorstepSession,
) : ViewModel() {

    private val addressId: String? = savedStateHandle.get<String>("addressId")
    private val rescheduleBookingId: String? = savedStateHandle.get<String>("rescheduleBookingId")

    private val _state = MutableStateFlow(SlotPickerUiState(rescheduling = rescheduleBookingId != null))
    val state: StateFlow<SlotPickerUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            if (rescheduleBookingId != null) loadReschedule(rescheduleBookingId) else loadNew()
        }
    }

    fun selectDate(date: String) = _state.update { if (it.selectedDate == date) it else it.copy(selectedDate = date, selectedSlot = null) }

    fun selectSlot(slot: SlotDto) {
        if (!slot.available) return
        _state.update { it.copy(selectedSlot = slot) }
    }

    fun confirm() {
        val s = _state.value
        val slot = s.selectedSlot ?: return
        if (s.submitting) return
        val bookingId = rescheduleBookingId
        if (bookingId == null) {
            val quote = s.quote ?: return
            val address = s.address ?: return
            val requireWoman = session.draft.value?.selection?.requireWoman ?: false
            _state.update { it.copy(outcome = SlotOutcome.Checkout(quote.id, address.id, slot.start, requireWoman)) }
            return
        }
        _state.update { it.copy(submitting = true) }
        viewModelScope.launch {
            when (val result = repository.reschedule(bookingId, slot.start)) {
                is DoorstepResult.Success -> _state.update { it.copy(submitting = false, outcome = SlotOutcome.Rescheduled) }
                is DoorstepResult.Failure -> {
                    _state.update { it.copy(submitting = false, message = errorMessage(result.error.userMessage())) }
                    if (result.error.code == DoorstepCodes.SLOT_TAKEN) loadReschedule(bookingId)
                }
            }
        }
    }

    fun consumeOutcome() = _state.update { it.copy(outcome = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

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
        val request = QuoteRequestDto(
            serviceId = draft.service.id,
            optionId = draft.selection.optionId.orEmpty(),
            quantity = draft.selection.quantity,
            addons = draft.selection.addonIds.sorted().map(::QuoteAddonDto),
            lat = address.lat,
            lng = address.lng,
        )
        val quote = when (val result = repository.createQuote(request)) {
            is DoorstepResult.Success -> result.value
            is DoorstepResult.Failure -> {
                _state.update { it.copy(loading = false, address = address, error = result.error.userMessage()) }
                return
            }
        }
        _state.update { it.copy(quote = quote, address = address) }
        applySlots(repository.slots(quote.id, null, address.id, draft.selection.requireWoman))
    }

    private suspend fun loadReschedule(bookingId: String) {
        applySlots(repository.slots(null, bookingId, null, false))
    }

    private fun applySlots(result: DoorstepResult<SlotDaysDto>) {
        when (result) {
            is DoorstepResult.Success -> {
                val days = result.value.days
                _state.update { current ->
                    val keepDate = current.selectedDate?.takeIf { d -> days.any { it.date == d } }
                    val firstOpen = days.firstOrNull { day -> day.slots.any { it.available } }?.date ?: days.firstOrNull()?.date
                    current.copy(
                        loading = false,
                        days = days,
                        selectedDate = keepDate ?: firstOpen,
                        selectedSlot = current.selectedSlot?.takeIf { sel -> days.any { d -> d.slots.any { it == sel && it.available } } },
                        error = null,
                    )
                }
            }
            is DoorstepResult.Failure -> _state.update {
                it.copy(
                    loading = false,
                    blockedByDues = result.error.code == DoorstepCodes.OUTSTANDING_DUE,
                    error = result.error.slotMessage(),
                )
            }
        }
    }

    private suspend fun resolveAddress(): AddressDto? {
        session.address.value?.takeIf { addressId == null || it.id == addressId }?.let { return it }
        val all = (repository.addresses() as? DoorstepResult.Success)?.value ?: return null
        return all.firstOrNull { it.id == addressId } ?: all.firstOrNull { it.isDefault }
    }

    private fun DoorstepError.slotMessage(): String = when (code) {
        DoorstepCodes.QUOTE_EXPIRED -> "Prices were refreshed. Tap Try again for fresh slots."
        else -> userMessage()
    }
}
