package com.us.android.feature.commerce.seller

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.StockLevel
import com.us.android.core.commerce.model.StockReason
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.commerce.repository.outcomeUnknown
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
        /** A neutral line: a resent change the server had already saved. */
        val notice: String? = null,
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
    private val savedState: SavedStateHandle,
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
        it.copy(amount = raw.filter(Char::isDigit).take(MAX_AMOUNT_DIGITS), error = null, notice = null)
    }

    fun setRemoving(removing: Boolean) = edit { it.copy(removing = removing, error = null) }

    fun setReason(reason: StockReason) = edit { it.copy(reason = reason, error = null) }

    fun setNotes(notes: String) = edit { it.copy(notes = notes.take(MAX_NOTE_CHARS)) }

    /**
     * Sends the adjustment under the key for THIS edit (audit M3).
     *
     * The same edit (amount, direction, reason, note) keeps one key across
     * retries and process death, so a save whose answer was lost and is sent
     * again is a replay the server recognises, not a second +10 on the shelf.
     * A different edit gets a different key. The key is spent on any definite
     * answer, so the next edit, even an identical one, is new.
     */
    fun submit() {
        val current = _state.value as? StockUiState.Content ?: return
        val delta = current.delta ?: return
        if (!current.canSubmit) return

        val key = keyFor(editFingerprint(delta, current))
        _state.value = current.copy(saving = true, error = null, notice = null)
        viewModelScope.launch {
            val r = repo.adjustStock(
                variantId = variantId,
                delta = delta,
                reason = current.reason,
                notes = current.notes,
                idempotencyKey = key,
            )
            when (r) {
                is CommerceResult.Success -> {
                    spendKey()
                    // The server's resulting level, not a locally computed
                    // one. Adding the delta to the old figure here would drift
                    // from the truth the moment anything else moved the stock.
                    _state.value = current.copy(
                        level = r.value.level,
                        amount = "",
                        notes = "",
                        saving = false,
                        lastChange = if (r.value.replayed) null else delta,
                        notice = if (r.value.replayed) ALREADY_SAVED else null,
                    )
                    // A replay answers with the level as it was when the
                    // change was first saved; read what it is now.
                    if (r.value.replayed) reread()
                }

                is CommerceResult.Failure -> onSaveFailed(current, r.error)
            }
        }
    }

    private suspend fun onSaveFailed(current: StockUiState.Content, error: CommerceError) {
        when {
            // No answer: the change may be saved. Keep the key and the amount,
            // so pressing the button again replays rather than repeats.
            error.outcomeUnknown() -> {
                _state.value = current.copy(saving = false, error = OUTCOME_UNKNOWN)
                reread()
            }

            // The key stood for a different edit. Start this one afresh, from
            // the level as it is now.
            error == CommerceError.IdempotencyConflict -> {
                spendKey()
                _state.value = current.copy(saving = false, error = KEY_CONFLICT)
                reread()
            }

            else -> {
                spendKey()
                _state.value = current.copy(saving = false, error = error.describe())
            }
        }
    }

    /** Refreshes the level shown, keeping what the seller typed. */
    private suspend fun reread() {
        val level = (repo.stock(variantId) as? CommerceResult.Success)?.value ?: return
        val now = _state.value as? StockUiState.Content ?: return
        _state.value = now.copy(level = level)
    }

    private fun editFingerprint(delta: Int, edit: StockUiState.Content): String =
        listOf(variantId, delta.toString(), edit.reason.name, edit.notes).joinToString(FINGERPRINT_SEPARATOR)

    /** The key for [fingerprint]: the saved one if it is the same edit, else a new one. */
    private fun keyFor(fingerprint: String): String {
        val savedKey = savedState.get<String>(KEY_STOCK_KEY)
        if (savedKey != null && savedState.get<String>(KEY_STOCK_EDIT) == fingerprint) return savedKey
        val fresh = repo.newStockAdjustmentKey()
        savedState[KEY_STOCK_EDIT] = fingerprint
        savedState[KEY_STOCK_KEY] = fresh
        return fresh
    }

    private fun spendKey() {
        savedState.remove<String>(KEY_STOCK_EDIT)
        savedState.remove<String>(KEY_STOCK_KEY)
    }

    private fun edit(transform: (StockUiState.Content) -> StockUiState.Content) {
        val current = _state.value as? StockUiState.Content ?: return
        _state.value = transform(current)
    }

    internal companion object {
        const val MAX_AMOUNT_DIGITS = 6
        const val MAX_NOTE_CHARS = 200
        const val KEY_STOCK_EDIT = "stock_adjustment_edit"
        const val KEY_STOCK_KEY = "stock_adjustment_key"
        private const val FINGERPRINT_SEPARATOR = "\u0000"
        const val ALREADY_SAVED = "That change was already saved."
        const val OUTCOME_UNKNOWN =
            "We couldn't confirm that change. Check the stock below, or try again: it won't be counted twice."
        const val KEY_CONFLICT = "That change clashed with an earlier one. Check the stock and try again."
    }
}
