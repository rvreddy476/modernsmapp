package com.us.android.feature.commerce.profile

import androidx.compose.ui.graphics.vector.ImageVector
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.feature.commerce.ui.CommerceBrand
import com.us.android.feature.commerce.ui.CommercePerson
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Whether this person has a shop.
 *
 * Three states, not two, and the third is the point. Only a server that SAID
 * "no such seller" turns the menu's last row into "Start selling"; while the
 * answer is unknown — still loading, or the lookup failed — the row reads
 * "Seller dashboard" and MSeller's hub shows the real state with a Retry.
 * A menu must not invent a shop's absence from a network blip, and inviting
 * an existing seller to "start selling" is the version of that mistake they
 * would actually notice.
 */
enum class SellerPresence { UNKNOWN, NONE, EXISTS }

/**
 * The rows of MStore's profile menu.
 *
 * Every row goes somewhere real. There is no row here for a page this build
 * does not have — that is the same rule Tube's More sheet follows, and the
 * reason "Payments" is a record of what was charged rather than a wallet the
 * app cannot yet hold.
 */
enum class StoreMenuRow(val label: String, val icon: ImageVector) {
    ORDERS("My orders", UsIcons.Package),
    FAVOURITES("Favourites", UsIcons.HeartOutline),
    ADDRESSES("Addresses", UsIcons.MapPin),
    PAYMENTS("Payments", UsIcons.CreditCard),
    PURCHASE_HISTORY("Purchase history", UsIcons.Clock),
    SETTINGS("Settings", UsIcons.Settings),

    /** The switch into MSeller for someone who has no shop yet. */
    START_SELLING("Start selling", UsIcons.Store),

    /** The switch into MSeller for someone who has one. */
    SELLER_DASHBOARD("Seller dashboard", UsIcons.Store),
    ;

    /** Both selling rows open MSeller. One person can be both, so this is a switch. */
    val opensSeller: Boolean get() = this == START_SELLING || this == SELLER_DASHBOARD
}

/**
 * Which rows the menu shows. Pure, so it is a table test rather than
 * something only a screenshot can check.
 */
fun storeMenuRows(seller: SellerPresence): List<StoreMenuRow> = listOf(
    StoreMenuRow.ORDERS,
    StoreMenuRow.FAVOURITES,
    StoreMenuRow.ADDRESSES,
    StoreMenuRow.PAYMENTS,
    StoreMenuRow.PURCHASE_HISTORY,
    StoreMenuRow.SETTINGS,
    if (seller == SellerPresence.NONE) StoreMenuRow.START_SELLING else StoreMenuRow.SELLER_DASHBOARD,
)

/** The selling row's own line of copy — what the switch actually offers. */
fun sellingRowDetail(seller: SellerPresence): String = when (seller) {
    SellerPresence.NONE -> "Open a shop on ${CommerceBrand.Buyer} and list your first product."
    else -> "Your shop, your listings and your stock."
}

data class StoreProfileState(
    val person: CommercePerson = CommercePerson(),
    val seller: SellerPresence = SellerPresence.UNKNOWN,
) {
    val rows: List<StoreMenuRow> get() = storeMenuRows(seller)
}

/**
 * Who is signed in, and whether they also sell.
 *
 * The person comes from the same own-profile read the Me tab uses, so MStore
 * cannot show a different name from the rest of the app. The avatar arrives
 * as a media id and is resolved through media-service the way every other
 * surface resolves one — never by inventing a URL from the id.
 */
@HiltViewModel
class StoreProfileViewModel @Inject constructor(
    private val profiles: ProfileRepository,
    private val media: MediaRepository,
    private val commerce: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(StoreProfileState())
    val state: StateFlow<StoreProfileState> = _state.asStateFlow()

    init {
        refresh()
    }

    /**
     * Re-read on every open of the menu.
     *
     * Someone can open a shop, or have one approved, while the app is running.
     * A menu rendered once at first composition would keep offering "Start
     * selling" to a seller for the rest of the process.
     */
    fun refresh() {
        viewModelScope.launch { loadPerson() }
        viewModelScope.launch { loadSeller() }
    }

    private suspend fun loadPerson() {
        val profile = (profiles.getOwnProfile() as? AppResult.Success)?.data ?: return
        _state.value = _state.value.copy(
            person = CommercePerson(
                name = profile.displayName.ifBlank { profile.username.ifBlank { "You" } },
                seed = profile.userId,
                avatarUrl = resolveAvatar(profile.avatarMediaId),
            ),
        )
    }

    private suspend fun resolveAvatar(mediaId: String?): String? {
        val id = mediaId?.takeIf { it.isNotBlank() } ?: return null
        return (media.delivery(id) as? AppResult.Success)?.data?.posterUrl
    }

    private suspend fun loadSeller() {
        val presence = when (val r = commerce.sellerProfile()) {
            is CommerceResult.Success -> SellerPresence.EXISTS
            is CommerceResult.Failure ->
                if (r.error.saysNoSeller()) SellerPresence.NONE else SellerPresence.UNKNOWN
        }
        _state.value = _state.value.copy(seller = presence)
    }
}

/**
 * Whether the server definitively said this caller has no shop.
 *
 * A 404 with no code, the mapped `ORDER_NOT_FOUND`, or the explicit
 * `NO_SELLER` — all three are the server answering the question. Anything
 * else, a timeout most of all, is the question going unanswered.
 */
private fun CommerceError.saysNoSeller(): Boolean =
    this is CommerceError.NotAvailable ||
        this is CommerceError.OrderNotFound ||
        (this is CommerceError.Unexpected && code == "NO_SELLER")
