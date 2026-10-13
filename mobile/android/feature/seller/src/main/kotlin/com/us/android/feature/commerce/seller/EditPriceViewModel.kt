package com.us.android.feature.commerce.seller

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Paise
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

sealed interface EditPriceUiState {
    data object Loading : EditPriceUiState

    data class Content(
        /** What the listing sells for right now, as the server reports it. */
        val currentPrice: Paise,
        val currentMrp: Paise,
        val paused: Boolean,
        /** As typed. Empty means "leave this alone". */
        val price: String = "",
        val mrp: String = "",
        val saving: Boolean = false,
        val error: String? = null,
        val saved: Boolean = false,
    ) : EditPriceUiState {
        val newPrice: Paise? get() = parseRupees(price)
        val newMrp: Paise? get() = parseRupees(mrp)

        /** A field with text in it that does not parse is a mistake, not a skip. */
        val priceMalformed: Boolean get() = price.isNotBlank() && newPrice == null
        val mrpMalformed: Boolean get() = mrp.isNotBlank() && newMrp == null

        /**
         * Whether the struck-through price would end up below the selling
         * price, taking into account whichever of the two is being changed.
         *
         * Both have to be considered together: editing only the price can put
         * it above an MRP that was fine before.
         */
        val mrpBelowPrice: Boolean
            get() {
                val price = newPrice ?: currentPrice
                val mrp = newMrp ?: currentMrp
                return mrp < price
            }

        val changed: Boolean get() = newPrice != null || newMrp != null

        val canSave: Boolean
            get() = changed && !saving && !priceMalformed && !mrpMalformed && !mrpBelowPrice
    }

    data class Failed(val message: String, val retryable: Boolean) : EditPriceUiState
}

/**
 * Repricing a listing that is already selling.
 *
 * The screen sends PAISE. A price entered exactly as ₹1,299.99 stays 129999
 * through an edit rather than round-tripping through 1299.9899999999998, and
 * the server moves both money columns together — the NUMERIC mirror and the
 * `_minor` column checkout actually reads. Moving only the mirror is the
 * original defect: the seller sees the new price everywhere and the buyer is
 * charged the old one.
 *
 * Only what the seller typed is sent. An untouched field is omitted rather
 * than re-asserted, so a price edit cannot silently un-pause a listing.
 */
@HiltViewModel
class EditPriceViewModel @Inject constructor(
    private val repo: CommerceRepository,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val variantId: String = checkNotNull(savedState["variantId"]) {
        "EditPriceViewModel requires a variantId"
    }

    private val _state = MutableStateFlow<EditPriceUiState>(EditPriceUiState.Loading)
    val state: StateFlow<EditPriceUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    /**
     * Loads the current price from the catalogue, not from a route argument.
     *
     * A price carried in navigation is a price from whenever the previous
     * screen loaded, and repricing against a stale figure is how a seller
     * undoes a change they made a minute ago on another device.
     */
    fun refresh() {
        _state.value = EditPriceUiState.Loading
        viewModelScope.launch {
            when (val r = repo.variant(variantId)) {
                is CommerceResult.Failure ->
                    _state.value = EditPriceUiState.Failed(
                        r.error.describe(),
                        r.error.isRetryable(),
                    )

                is CommerceResult.Success -> _state.value = EditPriceUiState.Content(
                    currentPrice = r.value.sellingPrice,
                    currentMrp = r.value.mrp,
                    paused = r.value.status != "active",
                )
            }
        }
    }

    fun setPrice(raw: String) = edit { it.copy(price = raw, error = null) }

    fun setMrp(raw: String) = edit { it.copy(mrp = raw, error = null) }

    /**
     * Pauses or resumes the listing.
     *
     * Applied immediately rather than batched with a price edit: a seller
     * pausing something has usually just run out of it, and making them press
     * Save first is how stock gets sold that does not exist.
     */
    fun setPaused(paused: Boolean) {
        val current = _state.value as? EditPriceUiState.Content ?: return
        if (current.saving) return

        _state.value = current.copy(saving = true, error = null)
        viewModelScope.launch {
            val status = if (paused) "paused" else "active"
            when (val r = repo.updateVariant(variantId, status = status)) {
                is CommerceResult.Failure ->
                    _state.value = current.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success ->
                    _state.value = current.copy(saving = false, paused = paused, saved = true)
            }
        }
    }

    fun save(onSaved: () -> Unit) {
        val current = _state.value as? EditPriceUiState.Content ?: return
        if (!current.canSave) return

        _state.value = current.copy(saving = true, error = null, saved = false)
        viewModelScope.launch {
            val r = repo.updateVariant(
                variantId = variantId,
                sellingPrice = current.newPrice,
                mrp = current.newMrp,
            )
            when (r) {
                is CommerceResult.Failure ->
                    _state.value = current.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    // Re-read rather than assume. The server is the authority
                    // on what the listing now costs, and a locally applied
                    // figure would hide a refusal the edge turned into a
                    // partial success.
                    refresh()
                    onSaved()
                }
            }
        }
    }

    private fun edit(transform: (EditPriceUiState.Content) -> EditPriceUiState.Content) {
        val current = _state.value as? EditPriceUiState.Content ?: return
        _state.value = transform(current).copy(saved = false)
    }
}
