package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.SellerAddress
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.model.SellerProfile
import com.us.android.core.commerce.model.SellerStatus
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
                _state.value = SellerUiState.Content(profile.value, products)
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
 */
fun SellerProduct.notLiveReason(): String? = when {
    approvalStatus == "rejected" ->
        rejectionReason?.takeIf { it.isNotBlank() } ?: "Not approved by moderation"

    approvalStatus == "draft" -> "Not submitted for review"
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

    fun save(onSaved: () -> Unit) {
        val form = _form.value
        if (!form.isComplete || form.saving) return

        _form.value = form.copy(saving = true, error = null)
        viewModelScope.launch {
            when (val r = repo.saveSellerAddress(form.toAddress())) {
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
 * Sending a listing for review.
 *
 * `POST /products/:id/submit` was declared in the API layer and called by
 * nothing. A seller listed a product that stayed `draft` forever, so it never
 * appeared in search, never reached a cart, and the seller had no way to find
 * out why — the catalogue filter hides everything that is not approved.
 */
@HiltViewModel
class SubmitProductViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _submitting = MutableStateFlow(false)
    val submitting: StateFlow<Boolean> = _submitting.asStateFlow()

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error.asStateFlow()

    fun submit(productId: String, onSubmitted: () -> Unit) {
        if (_submitting.value) return
        _submitting.value = true
        _error.value = null
        viewModelScope.launch {
            when (val r = repo.submitProduct(productId)) {
                is CommerceResult.Failure -> {
                    _submitting.value = false
                    _error.value = r.error.describe()
                }

                is CommerceResult.Success -> {
                    _submitting.value = false
                    onSubmitted()
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
    val submitProduct: (productId: String) -> Unit,
)
