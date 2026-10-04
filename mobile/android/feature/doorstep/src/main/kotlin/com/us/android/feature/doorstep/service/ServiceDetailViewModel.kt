package com.us.android.feature.doorstep.service

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.doorstep.DoorstepSession
import com.us.android.feature.doorstep.ServiceDraft
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.OutstandingDto
import com.us.android.feature.doorstep.data.ServiceDetailDto
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.domain.GenderRules
import com.us.android.feature.doorstep.domain.GroupViolation
import com.us.android.feature.doorstep.domain.OutstandingRules
import com.us.android.feature.doorstep.domain.SelectionRules
import com.us.android.feature.doorstep.domain.ServiceSelection
import com.us.android.feature.doorstep.model.Paise
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ServiceDetailUiState(
    val loading: Boolean = true,
    val service: ServiceDetailDto? = null,
    val selection: ServiceSelection = ServiceSelection(optionId = null),
    val outstanding: OutstandingDto? = null,
    val error: String? = null,
    /** Shown after Continue was pressed with a group still unfinished. */
    val showViolations: Boolean = false,
) {
    /** The lowest professional price for the pick (a lower bound); null while some item has none. */
    val fromEstimate: Paise? get() = service?.let { SelectionRules.fromEstimate(it, selection) }
    val durationMinutes: Int get() = service?.let { SelectionRules.durationMinutes(it, selection) } ?: 0
    val violations: List<GroupViolation> get() = service?.let { SelectionRules.violations(it, selection) }.orEmpty()
    val complete: Boolean get() = service?.let { SelectionRules.isComplete(it, selection) } ?: false
    val womanSwitchOffered: Boolean get() = service?.let { GenderRules.womanPreferenceOffered(it.category.genderRule) } ?: false
    val blocked: Boolean get() = OutstandingRules.blocksBooking(outstanding)
}

/**
 * A service page (the menu): options with their unit and the lowest
 * professional price, the quantity (jobs, hours or months), the add-on groups
 * with their min/max rules, and the "require a woman professional" switch
 * where the category allows it. Continue hands the pick to the address and
 * professionals steps; each professional prices it there (B1).
 */
@HiltViewModel
class ServiceDetailViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepRepository,
    private val session: DoorstepSession,
) : ViewModel() {

    private val serviceId: String =
        checkNotNull(savedStateHandle.get<String>("serviceId")) { "navigation argument 'serviceId' is missing" }

    private val _state = MutableStateFlow(ServiceDetailUiState())
    val state: StateFlow<ServiceDetailUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            when (val result = repository.service(serviceId, session.city.value)) {
                is DoorstepResult.Success -> {
                    val service = result.value.service
                    // Back from the slot step keeps the customer's pick.
                    val kept = session.draft.value?.takeIf { it.service.id == service.id }?.selection
                    _state.update {
                        it.copy(loading = false, service = service, selection = kept ?: SelectionRules.initial(service))
                    }
                }
                is DoorstepResult.Failure -> _state.update { it.copy(loading = false, error = result.error.userMessage()) }
            }
            (repository.outstanding() as? DoorstepResult.Success)?.value?.let { dues ->
                _state.update { it.copy(outstanding = dues) }
            }
        }
    }

    fun selectOption(optionId: String) = edit { service, s -> SelectionRules.selectOption(service, s, optionId) }

    fun setQuantity(quantity: Int) = edit { service, s -> SelectionRules.setQuantity(service, s, quantity) }

    fun toggleAddon(addonId: String) = edit { service, s -> SelectionRules.toggleAddon(service, s, addonId) }

    fun setRequireWoman(on: Boolean) = edit { _, s -> s.copy(requireWoman = on) }

    /**
     * Records the pick for the next steps; false when it is not complete yet
     * (the page then marks the unfinished groups) or dues block booking.
     */
    fun continueToBooking(): Boolean {
        val s = _state.value
        val service = s.service ?: return false
        if (s.blocked) return false
        if (!s.complete) {
            _state.update { it.copy(showViolations = true) }
            return false
        }
        val selection = s.selection.copy(
            requireWoman = GenderRules.requireFemalePro(service.category.genderRule, s.selection.requireWoman),
        )
        session.setDraft(ServiceDraft(service, selection))
        return true
    }

    private inline fun edit(crossinline change: (ServiceDetailDto, ServiceSelection) -> ServiceSelection) {
        _state.update { current ->
            val service = current.service ?: return@update current
            current.copy(selection = change(service, current.selection))
        }
    }
}
