package com.us.android.feature.doorstep.bookings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.doorstep.data.BookingSummaryDto
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The list's two tabs — the server's `status` filter values. */
enum class BookingsFilter(val wire: String, val label: String) {
    UPCOMING("upcoming", "Upcoming"),
    PAST("past", "Past"),
}

data class BookingsUiState(
    val filter: BookingsFilter = BookingsFilter.UPCOMING,
    val loading: Boolean = true,
    val items: List<BookingSummaryDto> = emptyList(),
    val nextCursor: String? = null,
    val loadingMore: Boolean = false,
    val error: String? = null,
)

/** My bookings, upcoming and past, newest first, cursor-paged. */
@HiltViewModel
class BookingsViewModel @Inject constructor(
    private val repository: DoorstepRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(BookingsUiState())
    val state: StateFlow<BookingsUiState> = _state.asStateFlow()

    fun select(filter: BookingsFilter) {
        if (filter == _state.value.filter && !_state.value.loading) return
        _state.value = BookingsUiState(filter = filter)
        refresh()
    }

    fun refresh() {
        val filter = _state.value.filter
        viewModelScope.launch {
            when (val result = repository.bookings(filter.wire)) {
                is DoorstepResult.Success -> _state.update {
                    if (it.filter != filter) it else it.copy(loading = false, items = result.value.items, nextCursor = result.value.nextCursor, error = null)
                }
                is DoorstepResult.Failure -> _state.update {
                    if (it.filter != filter) it else it.copy(loading = false, error = result.error.userMessage())
                }
            }
        }
    }

    fun loadMore() {
        val s = _state.value
        val cursor = s.nextCursor ?: return
        if (s.loadingMore) return
        _state.update { it.copy(loadingMore = true) }
        viewModelScope.launch {
            when (val result = repository.bookings(s.filter.wire, cursor)) {
                is DoorstepResult.Success -> _state.update {
                    if (it.filter != s.filter) {
                        it
                    } else {
                        val known = it.items.map { b -> b.id }.toSet()
                        it.copy(
                            loadingMore = false,
                            items = it.items + result.value.items.filterNot { b -> b.id in known },
                            nextCursor = result.value.nextCursor,
                        )
                    }
                }
                is DoorstepResult.Failure -> _state.update { it.copy(loadingMore = false) }
            }
        }
    }
}
