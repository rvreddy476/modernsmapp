package com.us.android.feature.commerce.seller

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.StockLevel
import com.us.android.core.commerce.model.StockReason
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

sealed interface StockUiState {
    data object Loading : StockUiState

    data class Content(
        val level: StockLevel,
        /**
         * The number of units being added or removed, as typed.
         *
         * A STRING, not an Int, because a half-typed "1" on the way to "12"
         * and an empty field are different states and an Int cannot hold the
         * second one. Parsing on submit keeps the field from fighting the
         * keyboard.
         */
        val amount: String = "",
        val reason: StockReason = StockReason.PURCHASE,
        val removing: Boolean = false,
        val notes: String = "",
        val saving: Boolean = false,
        val error: String? = null,
        /** Set after a successful adjustment, for the confirmation line. */
        val lastChange: Int? = null,
    ) : StockUiState {
        val parsedAmount: Int? get() = amount.trim().toIntOrNull()?.takeIf { it > 0 }

        /**
         * The signed delta the server is sent.
         *
         * The screen asks "how many did you add or remove", never "what is the
         * new total". A new-total field is a lost-update generator: the screen
         * renders 42, two units sell while the seller types, they submit 52
         * meaning "I added ten", and the two sold units are put back on the
         * shelf.
         */
        val delta: Int? get() = parsedAmount?.let { if (removing) -it else it }

        /**
         * Whether the removal would eat into units already promised to live
         * orders.
         *
         * Checked here only to say so BEFORE the round trip — the server holds
         * the real floor under a row lock and refuses with STOCK_RESERVED
         * regardless. Two units can sell between this check and that one, which
         * is precisely why the client's answer is advice and not permission.
         */
        val wouldBreachReserved: Boolean
            get() = delta?.let { level.total + it < level.reserved } ?: false

        val canSubmit: Boolean
            get() = parsedAmount != null && !saving && !wouldBreachReserved
    }

    data class Failed(val message: String, val retryable: Boolean) : StockUiState
}

/**
 * Stock for one variant.
 *
 * Before this screen existed, stock was set once at product creation and never
 * again: the only other writer, bulk import, is behind the launch fence. A
 * seller who sold out stayed sold out.
 */
@HiltViewModel
class StockViewModel @Inject constructor(
    private val repo: CommerceRepository,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val variantId: String = checkNotNull(savedState["variantId"]) {
        "StockViewModel requires a variantId"
    }

    private val _state = MutableStateFlow<StockUiState>(StockUiState.Loading)
    val state: StateFlow<StockUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = StockUiState.Loading
        viewModelScope.launch {
            when (val r = repo.stock(variantId)) {
                is CommerceResult.Failure ->
                    _state.value = StockUiState.Failed(
                        r.error.describe(),
                        r.error.isRetryable(),
                    )

                is CommerceResult.Success ->
                    _state.value = StockUiState.Content(level = r.value)
            }
        }
    }

    fun setAmount(raw: String) = edit {
        // Digits only. A minus sign here would give the seller two ways to
        // express a removal — the toggle and the sign — which can disagree.
        it.copy(amount = raw.filter(Char::isDigit).take(MAX_AMOUNT_DIGITS), error = null)
    }

    fun setRemoving(removing: Boolean) = edit { it.copy(removing = removing, error = null) }

    fun setReason(reason: StockReason) = edit { it.copy(reason = reason, error = null) }

    fun setNotes(notes: String) = edit { it.copy(notes = notes.take(MAX_NOTE_CHARS)) }

    fun submit() {
        val current = _state.value as? StockUiState.Content ?: return
        val delta = current.delta ?: return
        if (!current.canSubmit) return

        _state.value = current.copy(saving = true, error = null)
        viewModelScope.launch {
            val r = repo.adjustStock(
                variantId = variantId,
                delta = delta,
                reason = current.reason,
                notes = current.notes,
            )
            _state.value = when (r) {
                is CommerceResult.Failure ->
                    current.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success ->
                    // The server's resulting level, not a locally computed
                    // one. Adding the delta to the old figure here would drift
                    // from the truth the moment anything else moved the stock.
                    current.copy(
                        level = r.value,
                        amount = "",
                        notes = "",
                        saving = false,
                        lastChange = delta,
                    )
            }
        }
    }

    private fun edit(transform: (StockUiState.Content) -> StockUiState.Content) {
        val current = _state.value as? StockUiState.Content ?: return
        _state.value = transform(current)
    }

    private companion object {
        const val MAX_AMOUNT_DIGITS = 6
        const val MAX_NOTE_CHARS = 200
    }
}
