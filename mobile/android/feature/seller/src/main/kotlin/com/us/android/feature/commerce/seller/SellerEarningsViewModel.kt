package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.SellerEarning
import com.us.android.core.commerce.network.EARNINGS_PAGE_SIZE
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The figures over the earnings list.
 *
 * Summed HERE, over the rows on screen, because the server has no earnings
 * summary route: `GET /seller/earnings` is the rows and nothing else. That
 * makes this the one place the app adds money up, and the screen says what
 * it is: a sum of the rows loaded, not a statement of account. A payout
 * statement is the server's to issue.
 */
data class EarningsSummary(
    val rows: Int,
    val gross: Paise,
    val net: Paise,
) {
    companion object {
        fun of(rows: List<SellerEarning>) = EarningsSummary(
            rows = rows.size,
            gross = rows.fold(Paise.ZERO) { acc, e -> acc + e.gross },
            net = rows.fold(Paise.ZERO) { acc, e -> acc + e.net },
        )
    }
}

sealed interface SellerEarningsUiState {
    data object Loading : SellerEarningsUiState

    data class Content(
        val rows: List<SellerEarning>,
        val loadingMore: Boolean = false,
        val exhausted: Boolean = false,
        val message: String? = null,
    ) : SellerEarningsUiState {
        val summary: EarningsSummary get() = EarningsSummary.of(rows)
        val canLoadMore: Boolean get() = !exhausted && !loadingMore
    }

    data class Failed(val message: String, val retryable: Boolean) : SellerEarningsUiState
}

@HiltViewModel
class SellerEarningsViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<SellerEarningsUiState>(SellerEarningsUiState.Loading)
    val state: StateFlow<SellerEarningsUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = SellerEarningsUiState.Loading
        viewModelScope.launch {
            when (val r = repo.sellerEarnings(offset = 0)) {
                is CommerceResult.Failure ->
                    _state.value = SellerEarningsUiState.Failed(r.error.describe(), r.error.isRetryable())

                is CommerceResult.Success -> _state.value = SellerEarningsUiState.Content(
                    rows = r.value,
                    exhausted = r.value.size < EARNINGS_PAGE_SIZE,
                )
            }
        }
    }

    fun loadMore() {
        val current = _state.value as? SellerEarningsUiState.Content ?: return
        if (!current.canLoadMore) return

        _state.value = current.copy(loadingMore = true, message = null)
        viewModelScope.launch {
            when (val r = repo.sellerEarnings(offset = current.rows.size)) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    loadingMore = false,
                    message = r.error.describe(),
                )

                is CommerceResult.Success -> _state.value = current.copy(
                    rows = (current.rows + r.value).distinctBy { it.orderItemId },
                    loadingMore = false,
                    exhausted = r.value.size < EARNINGS_PAGE_SIZE,
                )
            }
        }
    }
}
