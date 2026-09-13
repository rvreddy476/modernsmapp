package com.us.android.feature.commerce.product

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Product
import com.us.android.core.commerce.model.Variant
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.facear.FaceArGate
import com.us.android.core.facear.TryOnEligibility
import com.us.android.core.facear.tryOnEligibility
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface ProductUiState {
    data object Loading : ProductUiState

    data class Content(
        val product: Product,
        /**
         * Null until the buyer picks one, unless the product has exactly one
         * variant. A product ALWAYS has variants server-side; "no selection"
         * is a UI state, not a data state.
         */
        val selectedVariant: Variant?,
        val quantity: Int = 1,
        val adding: Boolean = false,
        /** Set after a successful add, so the screen can offer "Go to bag". */
        val addedToCart: Boolean = false,
        val message: String? = null,
        /**
         * Whether to show the "Try on" action, and what to say instead.
         *
         * Decided by [tryOnEligibility] in `:core:facear` rather than here:
         * the rule is a licence state crossed with a product capability, it
         * has to be the same answer on every surface that asks, and it is the
         * kind of thing that gets decided differently on each screen the
         * moment it lives on a screen. Starts [TryOnEligibility.Hidden], so a
         * product page that has not heard from the licence yet shows nothing.
         */
        val tryOn: TryOnEligibility = TryOnEligibility.Hidden,
    ) : ProductUiState {

        /**
         * The buyer may add only when a variant is chosen AND that variant is
         * actually purchasable. Availability comes from the server; the app
         * must not infer it from a price being present, which an earlier
         * revision did and which showed sold-out items as buyable.
         */
        val canAddToCart: Boolean
            get() = selectedVariant?.inStock == true && !adding

        /** Cap the stepper at what the server says is available. */
        val maxQuantity: Int
            get() = (selectedVariant?.availableQty ?: 0).coerceAtMost(MAX_LINE_QUANTITY)
    }

    data class Failed(val message: String, val retryable: Boolean) : ProductUiState
}

@HiltViewModel
class ProductViewModel @Inject constructor(
    private val repo: CommerceRepository,
    private val faceAr: FaceArGate,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val productId: String = requireNotNull(savedState["productId"]) {
        "ProductViewModel requires a productId argument"
    }

    private val _state = MutableStateFlow<ProductUiState>(ProductUiState.Loading)
    val state: StateFlow<ProductUiState> = _state.asStateFlow()

    init {
        load()
        watchFaceArLicence()
    }

    /**
     * The licence answer can arrive after the product has rendered — it is a
     * native start and a licence check — so the action appears when it lands
     * rather than the page waiting for it.
     */
    private fun watchFaceArLicence() {
        viewModelScope.launch {
            faceAr.state.collect { licence ->
                val current = _state.value as? ProductUiState.Content ?: return@collect
                _state.value = current.copy(
                    tryOn = tryOnEligibility(current.product.tryOn, licence),
                )
            }
        }
    }

    fun retry() = load()

    private fun load() {
        _state.value = ProductUiState.Loading
        viewModelScope.launch {
            when (val r = repo.product(productId)) {
                is CommerceResult.Failure ->
                    _state.value =
                        ProductUiState.Failed(r.error.describe(), r.error.isRetryable())

                is CommerceResult.Success -> {
                    val product = r.value
                    _state.value = ProductUiState.Content(
                        product = product,
                        // Preselect only when there is genuinely no choice to
                        // make. Preselecting the first of several would put a
                        // size or colour the buyer never chose into the cart.
                        selectedVariant = product.variants.singleOrNull(),
                        tryOn = tryOnEligibility(product.tryOn, faceAr.state.value),
                    )
                    // Only now, and only for a product that actually offers a
                    // try-on. `ensure` loads native libraries and asks a
                    // licence server; paying that on every product page —
                    // almost none of which can be tried on — would be a cost
                    // with no return. Idempotent, so a second try-on-capable
                    // product costs nothing.
                    if (product.tryOn?.isUsable == true) faceAr.ensure()
                }
            }
        }
    }

    fun selectVariant(variant: Variant) {
        val current = _state.value as? ProductUiState.Content ?: return
        _state.value = current.copy(
            selectedVariant = variant,
            // Reset the stepper: the previous quantity may exceed what this
            // variant has in stock.
            quantity = 1,
            addedToCart = false,
            message = null,
        )
    }

    fun setQuantity(value: Int) {
        val current = _state.value as? ProductUiState.Content ?: return
        val capped = value.coerceIn(1, current.maxQuantity.coerceAtLeast(1))
        _state.value = current.copy(quantity = capped)
    }

    fun addToCart() {
        val current = _state.value as? ProductUiState.Content ?: return
        val variant = current.selectedVariant ?: return
        if (!current.canAddToCart) return

        _state.value = current.copy(adding = true, message = null)
        viewModelScope.launch {
            when (val r = repo.addToCart(variant.id, current.quantity)) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    adding = false,
                    message = r.error.describe(),
                )

                is CommerceResult.Success -> _state.value = current.copy(
                    adding = false,
                    addedToCart = true,
                    message = null,
                )
            }
        }
    }

    fun dismissMessage() {
        val current = _state.value as? ProductUiState.Content ?: return
        _state.value = current.copy(message = null)
    }
}

/**
 * A per-line cap.
 *
 * Not a business rule the server enforces — it is a guard against a stepper
 * held down. The server's stock check remains authoritative; this only keeps
 * the client from sending an obviously absurd quantity.
 */
const val MAX_LINE_QUANTITY = 10
