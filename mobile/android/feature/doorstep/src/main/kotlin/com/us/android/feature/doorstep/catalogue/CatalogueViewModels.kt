package com.us.android.feature.doorstep.catalogue

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.doorstep.DoorstepSession
import com.us.android.feature.doorstep.data.CategorySummaryDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.OutstandingDto
import com.us.android.feature.doorstep.data.ServiceSummaryDto
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.domain.OutstandingRules
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class CatalogueUiState(
    val loading: Boolean = true,
    val cityName: String = "",
    val categories: List<CategorySummaryDto> = emptyList(),
    val outstanding: OutstandingDto? = null,
    val error: String? = null,
    /** Doorstep is not open to this account or city (the gateway's pilot gate, an unknown city). */
    val unavailable: Boolean = false,
) {
    /** Unpaid extras block a new booking; the banner says so before the customer builds a basket. */
    val blocked: Boolean get() = OutstandingRules.blocksBooking(outstanding)
}

/** The category grid of the customer's city, and the outstanding-dues banner. */
@HiltViewModel
class CatalogueViewModel @Inject constructor(
    private val repository: DoorstepRepository,
    private val session: DoorstepSession,
) : ViewModel() {

    private val _state = MutableStateFlow(CatalogueUiState())
    val state: StateFlow<CatalogueUiState> = _state.asStateFlow()

    fun load() {
        viewModelScope.launch {
            val outstanding = async { repository.outstanding() }
            when (val result = repository.catalogue(session.city.value)) {
                is DoorstepResult.Success -> _state.update {
                    it.copy(
                        loading = false,
                        cityName = result.value.city.name,
                        categories = result.value.categories.sortedBy { c -> c.sortOrder },
                        error = null,
                        unavailable = false,
                    )
                }
                is DoorstepResult.Failure -> _state.update {
                    it.copy(
                        loading = false,
                        error = result.error.userMessage(),
                        unavailable = result.error.isUnavailable(),
                    )
                }
            }
            // A dues read that fails leaves the banner as it was; the server
            // still refuses the booking (DOORSTEP_OUTSTANDING_DUE) and says why.
            (outstanding.await() as? DoorstepResult.Success)?.value?.let { dues ->
                _state.update { it.copy(outstanding = dues) }
            }
        }
    }
}

internal fun DoorstepError.isUnavailable(): Boolean =
    this == DoorstepError.NotFound || code == DoorstepCodes.CITY_NOT_FOUND

data class CategoryUiState(
    val loading: Boolean = true,
    val category: CategorySummaryDto? = null,
    val services: List<ServiceSummaryDto> = emptyList(),
    val error: String? = null,
)

/** One category's services. */
@HiltViewModel
class CategoryViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepRepository,
    private val session: DoorstepSession,
) : ViewModel() {

    private val slug: String = checkNotNull(savedStateHandle.get<String>("slug")) { "navigation argument 'slug' is missing" }

    private val _state = MutableStateFlow(CategoryUiState())
    val state: StateFlow<CategoryUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            _state.value = when (val result = repository.category(slug, session.city.value)) {
                is DoorstepResult.Success -> CategoryUiState(
                    loading = false,
                    category = result.value.category,
                    services = result.value.services,
                )
                is DoorstepResult.Failure -> CategoryUiState(loading = false, error = result.error.userMessage())
            }
        }
    }
}
