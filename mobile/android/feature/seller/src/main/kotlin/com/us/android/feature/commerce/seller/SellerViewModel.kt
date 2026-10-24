package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.SellScope
import com.us.android.core.commerce.model.SellScopeView
import com.us.android.core.commerce.model.SellerAddress
import com.us.android.core.commerce.model.SellerAddressKind
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.model.SellerProfile
import com.us.android.core.commerce.model.SellerStatus
import com.us.android.core.commerce.model.Variant
import com.us.android.core.commerce.model.gstinProblem
import com.us.android.core.commerce.model.normaliseTaxId
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
 * The seller hub.
 *
 * Two things load together because the screen is meaningless without both: the
 * profile says whether this seller may sell at all, and the catalogue says
 * what they have. Loading them separately would let the screen render a
 * product list under a status header that has not arrived, which is exactly
 * the moment a seller draws the wrong conclusion about why nothing is selling.
 */
sealed interface SellerUiState {
    data object Loading : SellerUiState

    data class Content(
        val profile: SellerProfile,
        val products: List<SellerProduct>,
        /** The product whose variants are being read before its editor opens. */
        val resolvingProductId: String? = null,
        /** Set when a product has several variants and the seller must pick one. */
        val chooser: VariantChooser? = null,
        /** One line under the header: a resolution that found nothing, or failed. */
        val message: String? = null,
        /** The listing whose submit is in flight; its button shows progress, the others wait. */
        val submittingProductId: String? = null,
        /** The server's refusal of the last submit, shown under that listing's row. */
        val submitProblem: SubmitProblem? = null,
        /**
         * A books-only shop's scope (migration 045), read only when the profile
         * says books only; null otherwise. Drives "Sell more categories".
         */
        val sellScope: SellScopeView? = null,
        val upgrade: UpgradeForm = UpgradeForm(),
    ) : SellerUiState

    /**
     * The caller has no seller account.
     *
     * A first-class state, not an error. Most people using this app are
     * buyers, and "seller account not found" is the correct, unalarming
     * answer to a buyer who tapped the wrong thing.
     */
    data object NotASeller : SellerUiState

    data class Failed(val message: String, val retryable: Boolean) : SellerUiState
}

/**
 * "Sell more categories": a books-only shop adds a GSTIN registered to its
 * PAN (`POST /seller/sell-scope/upgrade`). The local check shows only after
 * a first try.
 */
data class UpgradeForm(
    val open: Boolean = false,
    val gstin: String = "",
    val attempted: Boolean = false,
    val saving: Boolean = false,
    val serverError: String? = null,
) {
    val error: String? get() = serverError ?: if (attempted) gstinProblem(gstin) else null
}

/** Whether the hub offers "Sell more categories": a books-only shop the server says may upgrade. */
fun offersUpgrade(view: SellScopeView?): Boolean = view != null && view.booksOnly && view.canUpgrade

/** A product with several variants, and the variants to choose from. */
data class VariantChooser(
    val product: SellerProduct,
    val variants: List<Variant>,
)

/**
 * Where a tap on a product row goes, given the variants it turned out to
 * have. Pure, so the three-way rule is a table test: exactly one opens the
 * editor for it, several open a chooser, none is a message — and in no case
 * is the PRODUCT id what opens an editor.
 */
sealed interface VariantTarget {
    data class One(val variantId: String) : VariantTarget
    data class Several(val variants: List<Variant>) : VariantTarget
    data object None : VariantTarget
}

fun variantTarget(variants: List<Variant>): VariantTarget = when (variants.size) {
    0 -> VariantTarget.None
    1 -> VariantTarget.One(variants.single().id)
    else -> VariantTarget.Several(variants)
}

/** The one line for a product whose variants could not be found. */
const val NO_VARIANT_TO_EDIT = "This product has no variant to edit yet."

private const val GSTIN_LENGTH = 15

/** The one line after a listing was sent for review. */
const val SUBMITTED_FOR_REVIEW = "Sent for review. It goes on sale once it's approved."

@HiltViewModel
class SellerViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<SellerUiState>(SellerUiState.Loading)
    val state: StateFlow<SellerUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = SellerUiState.Loading
        viewModelScope.launch { load() }
    }

    /**
     * Opens the stock (or price) editor for [product] — for its VARIANT.
     *
     * 2026-09-30. The row used to pass the product id as the variant id, on
     * the comment "the P0 catalogue is single-variant"; the server looks up
     * `product_variants.id`, so every stock and price edit answered
     * variant-not-found. The variants come from the row when the server sent
     * them and from `GET /products/:id/variants` otherwise; one opens
     * straight away, several open a chooser, none is said as one line.
     * [open] receives a variant id, never a product id.
     */
    fun openStock(product: SellerProduct, open: (variantId: String, title: String) -> Unit) {
        val current = _state.value as? SellerUiState.Content ?: return
        if (current.resolvingProductId != null) return
        _state.value = current.copy(resolvingProductId = product.id, message = null)
        viewModelScope.launch {
            val result = repo.variantsForEdit(product)
            val latest = _state.value as? SellerUiState.Content ?: return@launch
            when (result) {
                is CommerceResult.Failure ->
                    _state.value = latest.copy(resolvingProductId = null, message = result.error.describe())

                is CommerceResult.Success -> when (val target = variantTarget(result.value)) {
                    is VariantTarget.One -> {
                        _state.value = latest.copy(resolvingProductId = null)
                        open(target.variantId, product.title)
                    }

                    is VariantTarget.Several ->
                        _state.value = latest.copy(
                            resolvingProductId = null,
                            chooser = VariantChooser(product, target.variants),
                        )

                    VariantTarget.None ->
                        _state.value = latest.copy(resolvingProductId = null, message = NO_VARIANT_TO_EDIT)
                }
            }
        }
    }

    /** The seller picked one of several variants. */
    fun chooseVariant(variant: Variant, open: (variantId: String, title: String) -> Unit) {
        val current = _state.value as? SellerUiState.Content ?: return
        val chooser = current.chooser ?: return
        _state.value = current.copy(chooser = null)
        open(variant.id, chooser.product.title)
    }

    fun dismissChooser() {
        val current = _state.value as? SellerUiState.Content ?: return
        _state.value = current.copy(chooser = null)
    }

    fun dismissMessage() {
        val current = _state.value as? SellerUiState.Content ?: return
        _state.value = current.copy(message = null)
    }

    /**
     * Sends [productId] for review — first time, or again after a reviewer
     * rejected it or asked for changes (the server allows both, see
     * [submitOffer]).
     *
     * 2026-10-24. This used to be a separate ViewModel whose progress and
     * error nobody collected, called with an empty callback: a refused
     * submit — a listing with no photo, no stock, a missing category field —
     * looked exactly like a successful one, and the row never changed. Now
     * the row shows progress, a refusal lands under it with every gap the
     * server named, and a success re-reads the catalogue so the row says
     * "Awaiting review".
     */
    fun submitProduct(productId: String) {
        val current = _state.value as? SellerUiState.Content ?: return
        if (current.submittingProductId != null) return
        _state.value = current.copy(submittingProductId = productId, submitProblem = null, message = null)
        viewModelScope.launch {
            val result = repo.submitProduct(productId)
            val latest = _state.value as? SellerUiState.Content ?: return@launch
            when (result) {
                is CommerceResult.Failure -> _state.value = latest.copy(
                    submittingProductId = null,
                    submitProblem = submitProblem(productId, result.error),
                )

                is CommerceResult.Success -> {
                    val products = when (val p = repo.sellerProducts()) {
                        is CommerceResult.Success -> p.value
                        // The submit went through; only the re-read failed.
                        // Said locally so the row does not keep offering a
                        // submit the server would now refuse.
                        is CommerceResult.Failure -> latest.products.map {
                            if (it.id == productId) it.copy(approvalStatus = "submitted") else it
                        }
                    }
                    val after = _state.value as? SellerUiState.Content ?: return@launch
                    _state.value = after.copy(
                        products = products,
                        submittingProductId = null,
                        message = SUBMITTED_FOR_REVIEW,
                    )
                }
            }
        }
    }

    fun dismissSubmitProblem() {
        val current = _state.value as? SellerUiState.Content ?: return
        _state.value = current.copy(submitProblem = null)
    }

    fun openUpgrade() = editUpgrade { it.copy(open = true) }

    fun cancelUpgrade() = editUpgrade { UpgradeForm() }

    fun setUpgradeGstin(raw: String) =
        editUpgrade { it.copy(gstin = normaliseTaxId(raw).take(GSTIN_LENGTH), serverError = null) }

    /** Adds the GSTIN; on success the shop sells in every category and the panel goes. */
    fun upgrade() {
        val current = _state.value as? SellerUiState.Content ?: return
        val form = current.upgrade
        if (form.saving) return
        if (gstinProblem(form.gstin) != null) {
            _state.value = current.copy(upgrade = form.copy(attempted = true))
            return
        }
        _state.value = current.copy(upgrade = form.copy(attempted = true, saving = true, serverError = null))
        viewModelScope.launch {
            val result = repo.upgradeSellScope(form.gstin)
            val latest = _state.value as? SellerUiState.Content ?: return@launch
            _state.value = when (result) {
                is CommerceResult.Failure -> latest.copy(
                    upgrade = latest.upgrade.copy(saving = false, serverError = sellScopeRefusal(result.error).message),
                )

                is CommerceResult.Success -> latest.copy(
                    profile = latest.profile.copy(sellScope = result.value.scope),
                    sellScope = result.value,
                    upgrade = UpgradeForm(),
                    message = UPGRADE_DONE_COPY,
                )
            }
        }
    }

    private fun editUpgrade(transform: (UpgradeForm) -> UpgradeForm) {
        val current = _state.value as? SellerUiState.Content ?: return
        _state.value = current.copy(upgrade = transform(current.upgrade))
    }

    private suspend fun load() {
        when (val profile = repo.sellerProfile()) {
            is CommerceResult.Failure -> {
                // NO_SELLER and ORDER_NOT_FOUND both surface here as
                // OrderNotFound — the repository maps the server's 404/403 for
                // "you have no seller account" onto it. Treating that as a
                // failure would show a buyer a red error screen for opening a
                // tab they are simply not enrolled in.
                _state.value = if (profile.error.isNoSuchThing()) {
                    SellerUiState.NotASeller
                } else {
                    SellerUiState.Failed(
                        profile.error.describe(),
                        profile.error.isRetryable(),
                    )
                }
            }

            is CommerceResult.Success -> {
                // The catalogue is allowed to fail without taking the screen
                // down: the status header is the more important half, and a
                // seller who cannot see their products still needs to be told
                // their application is still under review.
                val products = when (val p = repo.sellerProducts()) {
                    is CommerceResult.Success -> p.value
                    is CommerceResult.Failure -> emptyList()
                }
                // Only a books-only shop has a scope to show (migration 045);
                // a read that fails hides the upgrade rather than the hub.
                val scope = if (profile.value.sellScope == SellScope.BOOKS_ONLY) {
                    (repo.sellScope() as? CommerceResult.Success)?.value
                } else {
                    null
                }
                _state.value = SellerUiState.Content(profile.value, products, sellScope = scope)
            }
        }
    }
}

/**
 * Whether an error means "this does not exist for you" rather than "something
 * went wrong".
 *
 * The distinction matters because the two want completely different screens: a
 * buyer with no seller account should see an invitation to start selling, not
 * a retry button over a red message.
 */
private fun com.us.android.core.commerce.repository.CommerceError.isNoSuchThing(): Boolean =
    this is com.us.android.core.commerce.repository.CommerceError.OrderNotFound ||
        (
            this is com.us.android.core.commerce.repository.CommerceError.Unexpected &&
                code == "NO_SELLER"
            )

/** Seller-facing status copy. */
fun SellerStatus.label(): String = when (this) {
    SellerStatus.DRAFT -> "Not submitted yet"
    SellerStatus.SUBMITTED -> "Submitted for review"
    SellerStatus.UNDER_REVIEW -> "Under review"
    SellerStatus.CHANGES_REQUIRED -> "Changes needed"
    SellerStatus.APPROVED -> "Approved"
    SellerStatus.REJECTED -> "Not approved"
    SellerStatus.SUSPENDED -> "Suspended"
    SellerStatus.DISABLED -> "Closed"
    // Never "Approved". A status this build does not recognise must fail
    // toward caution, because the alternative is telling a seller they can
    // sell when the server has decided otherwise.
    SellerStatus.UNKNOWN -> "Status unavailable"
}

/**
 * What the seller should do next, in their own terms.
 *
 * Null when there is nothing to say — an approved seller does not need a
 * banner explaining that they are approved.
 */
fun SellerStatus.guidance(): String? = when (this) {
    SellerStatus.DRAFT ->
        "Finish your details and submit them before you can start selling."
    SellerStatus.SUBMITTED, SellerStatus.UNDER_REVIEW ->
        "We are checking your details. You can add products now — they will go " +
            "live once your shop is approved."
    SellerStatus.CHANGES_REQUIRED ->
        "We need something changed before we can approve your shop."
    SellerStatus.REJECTED ->
        "Your application was not approved. Contact support if you think this is wrong."
    SellerStatus.SUSPENDED ->
        "Your shop is suspended, so nothing is on sale right now."
    SellerStatus.DISABLED ->
        "This shop is closed."
    SellerStatus.UNKNOWN ->
        "We could not read your shop's status. Pull to refresh, or try again shortly."
    SellerStatus.APPROVED -> null
}

/**
 * Why a product is not on sale, or null when it is.
 *
 * Both columns are consulted because they answer different questions and a
 * seller needs the right one: `status` is whether they have switched it on,
 * `approvalStatus` is whether moderation let it through. Reporting only one
 * leaves a seller toggling a switch that was never the problem.
 *
 * `status` here is the PRODUCT's column (draft, active, paused, archived) —
 * `paused` is a real value of it. A VARIANT's switch is a different column
 * whose "paused" is `inactive`; see [variantStatusFor].
 *
 * 2026-10-24: the states a reviewer sends a listing back in are named, and
 * the reviewer's own words are on the row beneath ([reviewRemark]), next to
 * the Resubmit button the server now honours.
 */
fun SellerProduct.notLiveReason(): String? = when {
    approvalStatus == "rejected" -> "Not approved by moderation"
    approvalStatus == "changes_requested" -> "Changes requested"
    approvalStatus == "flagged" -> "Flagged by moderation"
    approvalStatus == "hidden" -> "Hidden by moderation"
    approvalStatus == "draft" || approvalStatus == "pending" -> "Not submitted for review"
    approvalStatus == "submitted" || approvalStatus == "under_review" -> "Awaiting review"
    status == "draft" -> "Draft"
    status == "paused" -> "Paused"
    status == "archived" -> "Archived"
    status == "active" && (approvalStatus == "approved" || approvalStatus == "live") -> null
    else -> "Not on sale"
}

/** A pickup-address form. Validation is local; the server re-validates. */
data class PickupAddressForm(
    val contactName: String = "",
    val phone: String = "",
    val line1: String = "",
    val line2: String = "",
    val city: String = "",
    val state: String = "",
    val postalCode: String = "",
    val saving: Boolean = false,
    val error: String? = null,
    val saved: Boolean = false,
) {
    /**
     * Completeness only — not a claim that a courier will collect from here.
     *
     * State and PIN are checked because both decide money and the server
     * refuses without them: the PIN is the courier's origin, and the state is
     * the seller half of the GST place-of-supply comparison. Letting the form
     * submit without either just turns a local check into a round trip.
     */
    val isComplete: Boolean
        get() = contactName.isNotBlank() &&
            phone.trim().length >= MIN_PHONE_DIGITS &&
            line1.isNotBlank() &&
            city.isNotBlank() &&
            state.isNotBlank() &&
            postalCode.trim().length == PIN_LENGTH &&
            postalCode.all { it.isDigit() }

    fun toAddress() = SellerAddress(
        contactName = contactName.trim(),
        phone = phone.trim(),
        line1 = line1.trim(),
        line2 = line2.trim().takeIf { it.isNotBlank() },
        city = city.trim(),
        state = state.trim(),
        postalCode = postalCode.trim(),
    )
}

private const val MIN_PHONE_DIGITS = 10
private const val PIN_LENGTH = 6

@HiltViewModel
class PickupAddressViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _form = MutableStateFlow(PickupAddressForm())
    val form: StateFlow<PickupAddressForm> = _form.asStateFlow()

    init {
        // Prefill the state and PIN from the seller's registered details.
        // Those are the two fields that decide money, and a seller who has
        // already given them once should not be asked to retype them and get
        // one of them subtly wrong.
        viewModelScope.launch {
            when (val r = repo.sellerProfile()) {
                is CommerceResult.Success -> _form.value = _form.value.copy(
                    city = r.value.city.orEmpty(),
                    state = r.value.state.orEmpty(),
                    postalCode = r.value.postalCode.orEmpty(),
                )

                is CommerceResult.Failure -> Unit // the form simply starts empty
            }
        }
    }

    fun update(transform: (PickupAddressForm) -> PickupAddressForm) {
        _form.value = transform(_form.value).copy(error = null, saved = false)
    }

    /**
     * Saves the form as the [kind] of address: the pickup point, or the
     * business address a books-only seller gives (migration 045).
     */
    fun save(kind: SellerAddressKind = SellerAddressKind.PICKUP, onSaved: () -> Unit) {
        val form = _form.value
        if (!form.isComplete || form.saving) return

        _form.value = form.copy(saving = true, error = null)
        viewModelScope.launch {
            when (val r = repo.saveSellerAddress(form.toAddress(), kind)) {
                is CommerceResult.Failure ->
                    _form.value = form.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    _form.value = form.copy(saving = false, saved = true)
                    onSaved()
                }
            }
        }
    }
}

/**
 * What the seller hub can do, as one value.
 *
 * Grouped rather than passed as five separate callbacks: the hub is a menu,
 * and a menu's entries belong together. Every action is still named, so
 * nothing is hidden by the grouping.
 */
data class SellerHubActions(
    val openStock: (variantId: String, title: String) -> Unit,
    /**
     * A listing's photos.
     *
     * The thing MSeller had no way to do: a product could be created, priced
     * and stocked and never given a picture, so every listing reached buyers
     * as a grey box.
     */
    val openImages: (productId: String, title: String) -> Unit,
    val openPickupAddress: () -> Unit,
    val listProduct: () -> Unit,
    val submitShop: () -> Unit,
    // A listing's submit is not here: it is the hub's own work
    // (SellerViewModel.submitProduct), because its progress and refusal
    // belong on the row that started it, not in a callback nobody watched.
    /**
     * The order surface: what to pack and ship, what came back, what was
     * earned. MSeller had catalogue, stock, price and payout and no way to
     * see an order, so a shop could be approved and sell and its owner
     * could not ship what sold.
     */
    val openOrders: () -> Unit,
    val openReturns: () -> Unit,
    val openEarnings: () -> Unit,
)
