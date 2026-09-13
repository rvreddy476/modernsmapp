package com.us.android.feature.commerce.tryon

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Product
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.facear.FaceArGate
import com.us.android.core.facear.TryOnDescriptor
import com.us.android.core.facear.TryOnDiagnostics
import com.us.android.core.facear.TryOnEffect
import com.us.android.core.facear.TryOnEligibility
import com.us.android.core.facear.TryOnLook
import com.us.android.core.facear.TryOnVariant
import com.us.android.core.facear.effect.TryOnEffectResolution
import com.us.android.core.facear.effect.TryOnEffectSource
import com.us.android.core.facear.tryOnEligibility
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface TryOnUiState {
    data object Loading : TryOnUiState

    data class Content(
        val productTitle: String,
        val descriptor: TryOnDescriptor,
        /** The bundle on disk, or null — which today is most of the catalogue. */
        val effect: TryOnEffect?,
        /**
         * A licence or effect refusal, already worded for the viewer. Non-null
         * means the surface shows a sentence and opens NO camera.
         */
        val unavailable: String?,
        val selectedVariantId: String?,
        /** The finish and coverage the shopper chose. */
        val look: TryOnLook = TryOnLook.DEFAULT,
        /** Whether the worn shade can be bought, and at what price. */
        val bag: TryOnBagTarget = TryOnBagTarget.NoShade,
        val bagBusy: Boolean = false,
        /** Formatted for display, from the worn shade's own variant. */
        val priceLabel: String? = null,
        val message: String? = null,
        /** What the HOST knows about the try-on. The surface fills in the rest. */
        val diagnostics: TryOnDiagnostics = TryOnDiagnostics(),
    ) : TryOnUiState

    data class Failed(val message: String, val retryable: Boolean) : TryOnUiState
}

/**
 * One product's try-on.
 *
 * It re-reads the product rather than taking a descriptor through navigation:
 * a descriptor carried on a route is a descriptor from whenever the previous
 * screen loaded, and the effect slug is the join to a file on disk — stale is
 * the one thing it must not be. The same reasoning the seller's edit-price
 * route already uses for a price.
 *
 * The variant id DOES travel, because it is a choice the person made rather
 * than data: the camera opens on the shade they were looking at.
 *
 * ## THIS SCREEN CAN SELL
 *
 * The product is read here anyway, so its purchasable variants are here too,
 * and [tryOnBagTarget] joins the worn shade to one of them. That join is the
 * only reason this ViewModel holds the [Product] rather than just its title.
 *
 * ## THE DIAGNOSTIC HALF THAT LIVES HERE
 *
 * The licence, the licence's own SDK answer, the slug and the effect source's
 * verdict are all decided before a camera exists, so this is where they are
 * recorded. `FaceArSession` fills in the half that only a live effect knows.
 * Neither half guesses at the other's fields.
 */
@HiltViewModel
class TryOnViewModel @Inject constructor(
    private val repo: CommerceRepository,
    private val gate: FaceArGate,
    private val effects: TryOnEffectSource,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val productId: String = requireNotNull(savedState["productId"]) {
        "TryOnViewModel requires a productId argument"
    }
    private val initialVariantId: String? =
        savedState.get<String>("variantId")?.takeIf { it.isNotBlank() }

    private val _state = MutableStateFlow<TryOnUiState>(TryOnUiState.Loading)
    val state: StateFlow<TryOnUiState> = _state.asStateFlow()

    /** Held for the bag join and the price. Never rendered directly. */
    private var product: Product? = null

    init {
        // The destination cannot have been reached without the product page
        // deciding the gate was Ready, but `ensure` is idempotent and a deep
        // link straight here must not find an uninitialised SDK.
        gate.ensure()
        load()
    }

    fun retry() = load()

    private fun load() {
        _state.value = TryOnUiState.Loading
        viewModelScope.launch {
            when (val r = repo.product(productId)) {
                is CommerceResult.Failure ->
                    _state.value =
                        TryOnUiState.Failed(r.error.describe(), r.error.isRetryable())

                is CommerceResult.Success -> {
                    val loaded = r.value
                    product = loaded
                    val descriptor = loaded.tryOn
                    if (descriptor == null || !descriptor.isUsable) {
                        // Reachable by a deep link, or by a seller switching
                        // try-on off while the page was open. Not an error —
                        // a plain statement, and not retryable, because
                        // asking again will say the same thing.
                        _state.value = TryOnUiState.Failed(NOT_CAPABLE, retryable = false)
                        return@launch
                    }
                    // The product page's own choice, when it maps onto a
                    // try-on variant. It may not: a size is a purchase
                    // variant with no shade of its own.
                    val chosen = descriptor.variantById(initialVariantId)?.id
                        ?: descriptor.variants.firstOrNull()?.id
                    _state.value = TryOnUiState.Content(
                        productTitle = loaded.title,
                        descriptor = descriptor,
                        effect = null,
                        unavailable = licenceRefusal(descriptor),
                        selectedVariantId = chosen,
                        diagnostics = seedDiagnostics(descriptor),
                    ).withShade(chosen)
                    resolveEffect(descriptor)
                }
            }
        }
    }

    /**
     * The diagnostic facts that are known before any camera exists.
     *
     * Every one is read from something that ANSWERED — the gate's state, the
     * gate's record of what `isExpired()` said, the descriptor's own slug. The
     * effect-source fields stay at their "not checked" defaults until
     * [resolveEffect] has actually asked.
     */
    private fun seedDiagnostics(descriptor: TryOnDescriptor) = TryOnDiagnostics(
        gate = gate.state.value,
        licenceValid = gate.licenceValid.value,
        effectSlug = descriptor.effectSlug,
    )

    /**
     * The licence's own refusal for this product, or null when the licence is
     * fine. Reuses [tryOnEligibility] so this screen and the product page
     * cannot disagree about what a licence state means.
     */
    private fun licenceRefusal(descriptor: TryOnDescriptor): String? =
        when (val decision = tryOnEligibility(descriptor, gate.state.value)) {
            is TryOnEligibility.Unavailable -> decision.reason
            // Offer, and Hidden — which here means the licence has not settled
            // yet. Both leave this null: the surface's own "not ready" state
            // covers the wait, and a sentence invented here would contradict
            // the gate a moment later.
            TryOnEligibility.Offer, TryOnEligibility.Hidden -> null
        }

    private fun resolveEffect(descriptor: TryOnDescriptor) {
        viewModelScope.launch {
            val answer = effects.resolve(descriptor.effectSlug)
            val current = _state.value as? TryOnUiState.Content ?: return@launch
            _state.value = when (answer) {
                is TryOnEffectResolution.Available -> current.copy(
                    effect = answer.effect,
                    diagnostics = current.diagnostics.copy(
                        effect = answer.effect,
                        resolution = "${answer.effect.origin.label} → ${answer.effect.loadPath}",
                        // What "available" MEANS to BundledTryOnEffects is
                        // exactly "a readable config.json is in that
                        // directory". Reporting it as a separate fact would be
                        // inventing a second source of truth.
                        manifestFound = true,
                    ),
                )

                // The reason the SOURCE gave, not one invented here: it knows
                // whether the build carries no bundle or a download has not
                // arrived, and those are different things to tell someone.
                is TryOnEffectResolution.Missing -> current.copy(
                    effect = null,
                    unavailable = current.unavailable ?: answer.reason,
                    diagnostics = current.diagnostics.copy(
                        resolution = "no source resolved '${answer.slug}' — ${answer.reason}",
                        manifestFound = false,
                    ),
                )
            }
        }
    }

    fun selectVariant(variant: TryOnVariant) {
        val current = _state.value as? TryOnUiState.Content ?: return
        _state.value = current.copy(selectedVariantId = variant.id, message = null)
            .withShade(variant.id)
    }

    fun setLook(look: TryOnLook) {
        val current = _state.value as? TryOnUiState.Content ?: return
        _state.value = current.copy(look = look)
    }

    /**
     * Puts the worn shade in the bag.
     *
     * Refuses on anything but [TryOnBagTarget.Purchasable], with the reason
     * that target already carries — the button is disabled for those cases, so
     * reaching here is a race (stock going out while the camera was open) and
     * the shopper deserves the same sentence either way.
     */
    fun addToBag() {
        val current = _state.value as? TryOnUiState.Content ?: return
        val target = current.bag
        if (target !is TryOnBagTarget.Purchasable) {
            _state.value = current.copy(message = target.blockedReason() ?: BAG_FAILED)
            return
        }
        if (current.bagBusy) return
        _state.value = current.copy(bagBusy = true, message = null)
        viewModelScope.launch {
            val result = repo.addToCart(target.variantId, 1)
            val latest = _state.value as? TryOnUiState.Content ?: return@launch
            _state.value = latest.copy(
                bagBusy = false,
                message = when (result) {
                    is CommerceResult.Success -> BAG_ADDED
                    is CommerceResult.Failure -> result.error.describe()
                },
            )
        }
    }

    fun report(message: String) {
        val current = _state.value as? TryOnUiState.Content ?: return
        _state.value = current.copy(message = message)
    }

    fun dismissMessage() {
        val current = _state.value as? TryOnUiState.Content ?: return
        _state.value = current.copy(message = null)
    }

    /**
     * The bag target and price for [variantId], recomputed together.
     *
     * Together because they come from the same catalogue variant: a price from
     * one variant beside a bag target from another is exactly the bug that
     * charges someone for the wrong shade.
     */
    private fun TryOnUiState.Content.withShade(variantId: String?): TryOnUiState.Content {
        val target = tryOnBagTarget(variantId, product?.variants.orEmpty())
        return copy(
            bag = target,
            priceLabel = (target as? TryOnBagTarget.Purchasable)?.price?.formatWithSymbol(),
        )
    }

    private companion object {
        const val NOT_CAPABLE = "This product does not offer a try-on."
        const val BAG_ADDED = "Added to your bag."
        const val BAG_FAILED = "That shade could not be added."
    }
}
