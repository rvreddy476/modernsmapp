package com.us.android.feature.commerce.seller

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
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
        /** The seller's own switch is off (`inactive`). */
        val paused: Boolean,
        /** The variant is `out_of_stock`: on, but with nothing to sell. */
        val outOfStock: Boolean = false,
        /** The variant's SKU as the server holds it. */
        val currentSku: String = "",
        /**
         * Whether the SKU may change: only while the listing is unapproved
         * (see [skuEditable]). False when the listing's state is unknown.
         */
        val skuEditable: Boolean = false,
        /** As typed. Empty means "leave this alone". */
        val price: String = "",
        val mrp: String = "",
        val sku: String = "",
        val saving: Boolean = false,
        val error: String? = null,
        val saved: Boolean = false,
    ) : EditPriceUiState {
        val newPrice: Paise? get() = parseRupees(price)
        val newMrp: Paise? get() = parseRupees(mrp)

        /**
         * The SKU to send, or null to leave it alone: blank, unchanged, or a
         * listing whose SKU is locked sends nothing — a locked SKU is never
         * put on the wire to be refused.
         */
        val newSku: String?
            get() = sku.trim().takeIf { skuEditable && it.isNotEmpty() && it != currentSku }

        /** A field with text in it that does not parse is a mistake, not a skip. */
        val priceMalformed: Boolean get() = price.isNotBlank() && newPrice == null
        val mrpMalformed: Boolean get() = mrp.isNotBlank() && newMrp == null

        /**
         * Whether the struck-through price would end up below the selling
         * price, taking into account whichever of the two is being changed.
         *
         * Both have to be considered together: editing only the price can put
         * it above an MRP that was fine before. The server refuses the same
         * pair with 400 PRICE_ABOVE_MRP; this is so the button says so first.
         */
        val mrpBelowPrice: Boolean
            get() {
                val price = newPrice ?: currentPrice
                val mrp = newMrp ?: currentMrp
                return mrp < price
            }

        val changed: Boolean get() = newPrice != null || newMrp != null || newSku != null

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
 *
 * 2026-10-24 (commerce-service f6dce2470): a price change goes live at once,
 * with no review, so nothing here says "under review" after one. The pause
 * switch sends `inactive` — `paused` is a PRODUCT status and a variant PATCH
 * carrying it was refused with VARIANT_STATUS_INVALID. The SKU is editable
 * only while the listing is unapproved.
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
     *
     * The listing's approval state is read the same way, from the seller's
     * catalogue, because it decides whether the SKU may change. A catalogue
     * that cannot be read leaves the SKU locked rather than failing the
     * screen: the price is what the seller came for.
     */
    fun refresh() {
        _state.value = EditPriceUiState.Loading
        viewModelScope.launch {
            when (val r = repo.variant(variantId)) {
                is CommerceResult.Failure ->
                    _state.value = EditPriceUiState.Failed(
                        r.error.describeForSeller(),
                        r.error.isRetryable(),
                    )

                is CommerceResult.Success -> {
                    val approvalStatus = approvalStatusOf(r.value.productId)
                    _state.value = EditPriceUiState.Content(
                        currentPrice = r.value.sellingPrice,
                        currentMrp = r.value.mrp,
                        paused = isVariantPaused(r.value.status),
                        outOfStock = r.value.status == VARIANT_OUT_OF_STOCK,
                        currentSku = r.value.sku,
                        skuEditable = skuEditable(approvalStatus),
                    )
                }
            }
        }
    }

    private suspend fun approvalStatusOf(productId: String): String? {
        if (productId.isBlank()) return null
        return when (val r = repo.sellerProducts()) {
            is CommerceResult.Success -> r.value.firstOrNull { it.id == productId }?.approvalStatus
            is CommerceResult.Failure -> null
        }
    }

    fun setPrice(raw: String) = edit { it.copy(price = raw, error = null) }

    fun setMrp(raw: String) = edit { it.copy(mrp = raw, error = null) }

    fun setSku(raw: String) = edit { it.copy(sku = raw, error = null) }

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
            when (val r = repo.updateVariant(variantId, status = variantStatusFor(paused))) {
                is CommerceResult.Failure ->
                    _state.value = current.copy(saving = false, error = r.error.describeForSeller())

                is CommerceResult.Success ->
                    _state.value = current.copy(
                        saving = false,
                        paused = paused,
                        // Either way the variant is now `inactive` or
                        // `active`, never `out_of_stock`.
                        outOfStock = false,
                        saved = true,
                    )
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
                sku = current.newSku,
            )
            when (r) {
                is CommerceResult.Failure -> _state.value = afterRefusal(current, r.error)

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

/**
 * The form after the server refused a save. The seller's typing is kept; a
 * SKU_LOCKED (the listing was approved since this screen loaded) also locks
 * the field and drops the SKU they typed, so the next Save sends the price
 * alone instead of meeting the same refusal again.
 */
internal fun afterRefusal(current: EditPriceUiState.Content, error: CommerceError): EditPriceUiState.Content {
    val locked = error is CommerceError.Unexpected && error.code == "SKU_LOCKED"
    return current.copy(
        saving = false,
        error = error.describeForSeller(),
        skuEditable = current.skuEditable && !locked,
        sku = if (locked) "" else current.sku,
    )
}
