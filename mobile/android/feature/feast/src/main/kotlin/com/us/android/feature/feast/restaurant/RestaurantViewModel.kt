package com.us.android.feature.feast.restaurant

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.AddCartItemRequest
import com.us.android.core.food.network.CartAddonRequest
import com.us.android.core.food.network.FeastAddonGroupDto
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastMenuCategoryDto
import com.us.android.core.food.network.FeastMenuItemDto
import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.core.food.network.deliveryPoint
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.code
import com.us.android.core.food.repository.serverMessage
import com.us.android.feature.feast.FeastSession
import com.us.android.feature.feast.ui.errorMessage
import com.us.android.feature.feast.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the customer chose on the item sheet. */
data class ItemSelection(
    val item: FeastMenuItemDto,
    val variantId: String? = null,
    val addonIds: Set<String> = emptySet(),
    val quantity: Int = 1,
)

/**
 * The item sheet's rules: sizes, add-on limits, the estimate and the add body.
 *
 * These are for the customer's convenience only. The SERVER validates every
 * choice and prices the cart; the cart screen renders its figures.
 */
object ItemSelectionRules {

    fun needsSheet(item: FeastMenuItemDto): Boolean = item.variants.isNotEmpty() || item.addonGroups.isNotEmpty()

    /** How many add-ons [group] needs: a required group needs at least one. */
    fun minimum(group: FeastAddonGroupDto): Int =
        if (group.isRequired) maxOf(group.minSelect, 1) else group.minSelect.coerceAtLeast(0)

    /** The first unmet requirement, as a sentence, or null when [selection] can be added. */
    fun problem(selection: ItemSelection): String? {
        val item = selection.item
        if (item.variants.isNotEmpty()) {
            val variants = item.variants.filter { it.isAvailable }
            if (variants.isEmpty()) return "No sizes of ${item.name} are available right now"
            if (variants.none { it.id == selection.variantId }) return "Choose a size"
        }
        for (group in item.addonGroups) {
            val available = group.addons.filter { it.isAvailable }
            val min = minimum(group)
            // The menu lists only available add-ons, so a required group can be left with too few.
            if (available.size < min) return "${group.name} isn't available right now, so ${item.name} can't be added"
            val chosen = available.count { it.id in selection.addonIds }
            if (chosen < min) return "Choose at least $min in ${group.name}"
            if (group.maxSelect > 0 && chosen > group.maxSelect) return "Choose at most ${group.maxSelect} in ${group.name}"
        }
        if (selection.quantity !in 1..MAX_QUANTITY) return "Choose a quantity"
        return null
    }

    fun selectVariant(selection: ItemSelection, variantId: String): ItemSelection =
        if (selection.item.variants.any { it.id == variantId && it.isAvailable }) selection.copy(variantId = variantId) else selection

    /**
     * Ticks or unticks [addonId]. A pick beyond the group's `max_select` is
     * refused, except in a one-choice group, where it replaces the current pick.
     */
    fun toggleAddon(selection: ItemSelection, addonId: String): ItemSelection {
        if (addonId in selection.addonIds) return selection.copy(addonIds = selection.addonIds - addonId)
        val group = selection.item.addonGroups.firstOrNull { g -> g.addons.any { it.id == addonId } } ?: return selection
        if (group.addons.none { it.id == addonId && it.isAvailable }) return selection
        val inGroup = group.addons.map { it.id }.toSet()
        val chosen = selection.addonIds.count { it in inGroup }
        return when {
            group.maxSelect == 1 -> selection.copy(addonIds = selection.addonIds - inGroup + addonId)
            group.maxSelect > 0 && chosen >= group.maxSelect -> selection
            else -> selection.copy(addonIds = selection.addonIds + addonId)
        }
    }

    /**
     * One unit priced the way the cart prices it: the size's price, else the
     * discounted price, else the base price, plus each chosen add-on. Integer
     * paise from the `*_paise` fields only.
     */
    fun unitPrice(selection: ItemSelection): Paise {
        val item = selection.item
        val base = item.variants.firstOrNull { it.id == selection.variantId }?.pricePaise
            ?: item.discountPricePaise?.takeIf { it > Paise.ZERO }
            ?: item.basePricePaise
        return item.addonGroups
            .flatMap { it.addons }
            .filter { it.id in selection.addonIds }
            .distinctBy { it.id }
            .fold(base) { sum, addon -> sum + addon.pricePaise }
    }

    /** [unitPrice] times the quantity. An estimate; the cart shows the server's total. */
    fun total(selection: ItemSelection): Paise =
        Paise(Math.multiplyExact(unitPrice(selection).value, selection.quantity.toLong()))

    /** The add-to-cart body, with the delivery address so the server checks range at add time. */
    fun request(selection: ItemSelection, addressId: String?, clearExisting: Boolean = false): AddCartItemRequest {
        val item = selection.item
        val knownAddons = item.addonGroups.flatMap { group -> group.addons.map { it.id } }.toSet()
        return AddCartItemRequest(
            menuItemId = item.id,
            quantity = selection.quantity,
            variantId = selection.variantId?.takeIf { id -> item.variants.any { it.id == id } },
            clearExisting = clearExisting,
            addons = selection.addonIds.filter { it in knownAddons }.sorted().map { CartAddonRequest(addonId = it, quantity = 1) },
            addressId = addressId,
        )
    }

    const val MAX_QUANTITY = 20
}

data class RestaurantUiState(
    val loading: Boolean = true,
    val restaurant: FeastRestaurantDto? = null,
    val categories: List<FeastMenuCategoryDto> = emptyList(),
    val serviceability: Serviceability = Serviceability.Open,
    val cart: FeastCartDto? = null,
    val sheet: ItemSelection? = null,
    val addingItemId: String? = null,
    /** An add waiting for "start a new cart?" because the cart holds another restaurant. */
    val pendingReplace: ItemSelection? = null,
    val loadError: String? = null,
    val message: UsMessage? = null,
) {
    val cartItemCount: Int get() = cart?.items?.sumOf { it.quantity } ?: 0
}

/**
 * A restaurant's menu and the add-to-cart path.
 *
 * The restaurant is read for the picked address's pin, so the server's own
 * serviceability applies. Serviceability is checked BEFORE a request: an
 * unserviceable restaurant, or one the server already refused for this
 * address, blocks the add locally with that message and sends nothing. The
 * menu stays browsable either way.
 */
@HiltViewModel
class RestaurantViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: FeastRepository,
    private val session: FeastSession,
) : ViewModel() {

    private val restaurantId: String = checkNotNull(savedStateHandle.get<String>("restaurantId")) {
        "navigation argument 'restaurantId' is missing"
    }

    private val _state = MutableStateFlow(RestaurantUiState())
    val state: StateFlow<RestaurantUiState> = _state.asStateFlow()

    /** The address the restaurant was last read for. */
    private var readForAddressId: String? = null

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, loadError = null) }
        val address = session.address.value
        readForAddressId = address?.id
        viewModelScope.launch {
            val restaurant = async { repository.restaurant(restaurantId, address?.deliveryPoint()) }
            val menu = async { repository.menu(restaurantId) }
            val cart = async { repository.cart() }
            val r = restaurant.await()
            val m = menu.await()
            val c = (cart.await() as? FoodResult.Success)?.value
            if (r is FoodResult.Failure) {
                _state.update { it.copy(loading = false, loadError = describe(r.error)) }
                return@launch
            }
            val detail = (r as FoodResult.Success).value
            _state.update {
                it.copy(
                    loading = false,
                    restaurant = detail,
                    categories = (m as? FoodResult.Success)?.value?.categories.orEmpty().filter { cat -> cat.items.isNotEmpty() },
                    serviceability = currentServiceability(detail),
                    cart = c,
                    loadError = (m as? FoodResult.Failure)?.let { failure -> describe(failure.error) },
                )
            }
        }
    }

    /** The customer tapped Add on [item]: a sheet when it has choices, straight to the cart otherwise. */
    fun onAdd(item: FeastMenuItemDto) {
        if (blockIfUnserviceable()) return
        if (!item.isAvailable) {
            _state.update { it.copy(message = errorMessage("${item.name} is unavailable right now.")) }
            return
        }
        if (ItemSelectionRules.needsSheet(item)) {
            val firstVariant = item.variants.sortedBy { it.sortOrder }.firstOrNull { it.isAvailable }?.id
            _state.update { it.copy(sheet = ItemSelection(item, variantId = firstVariant)) }
        } else {
            add(ItemSelection(item))
        }
    }

    fun updateSheet(selection: ItemSelection) {
        _state.update { it.copy(sheet = selection) }
    }

    fun selectVariant(variantId: String) {
        _state.update { s -> s.copy(sheet = s.sheet?.let { ItemSelectionRules.selectVariant(it, variantId) }) }
    }

    fun toggleAddon(addonId: String) {
        _state.update { s -> s.copy(sheet = s.sheet?.let { ItemSelectionRules.toggleAddon(it, addonId) }) }
    }

    fun dismissSheet() {
        _state.update { it.copy(sheet = null) }
    }

    fun confirmSheet() {
        val selection = _state.value.sheet ?: return
        ItemSelectionRules.problem(selection)?.let { problem ->
            _state.update { it.copy(message = errorMessage(problem)) }
            return
        }
        _state.update { it.copy(sheet = null) }
        add(selection)
    }

    fun confirmReplaceCart() {
        val pending = _state.value.pendingReplace ?: return
        _state.update { it.copy(pendingReplace = null) }
        add(pending, clearExisting = true)
    }

    fun dismissReplaceCart() {
        _state.update { it.copy(pendingReplace = null) }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    /** Re-evaluates serviceability, re-reading the restaurant when the customer picked another address. */
    fun refreshServiceability() {
        val restaurant = _state.value.restaurant ?: return
        val address = session.address.value
        if (address?.id == readForAddressId) {
            _state.update { it.copy(serviceability = currentServiceability(restaurant)) }
            return
        }
        readForAddressId = address?.id
        viewModelScope.launch {
            // The old read judged the previous address; without a fresh one, fall back to the flags.
            val fresh = (repository.restaurant(restaurantId, address?.deliveryPoint()) as? FoodResult.Success)?.value
                ?: restaurant.withoutPointAnswer()
            _state.update { it.copy(restaurant = fresh, serviceability = currentServiceability(fresh)) }
        }
    }

    private fun add(selection: ItemSelection, clearExisting: Boolean = false) {
        if (blockIfUnserviceable()) return
        val addressId = session.address.value?.id
        _state.update { it.copy(addingItemId = selection.item.id) }
        viewModelScope.launch {
            val request = ItemSelectionRules.request(selection, addressId, clearExisting)
            when (val result = repository.addToCart(request)) {
                is FoodResult.Success -> _state.update {
                    it.copy(addingItemId = null, cart = result.value, message = successMessage("Added ${selection.item.name}"))
                }
                is FoodResult.Failure -> onAddFailed(selection, result.error, addressId)
            }
        }
    }

    private fun onAddFailed(selection: ItemSelection, error: FoodError, addressId: String?) {
        if (error.code == CART_CONFLICT) {
            _state.update { it.copy(addingItemId = null, pendingReplace = selection) }
            return
        }
        if (error == FoodError.NotFound && addressId != null) {
            // 404 FOOD_NOT_FOUND for the address: deleted, or not this customer's.
            _state.update { it.copy(addingItemId = null, message = errorMessage(CHOOSE_ADDRESS_AGAIN)) }
            return
        }
        val refusal = ServiceabilityRules.fromRefusal(error)
        if (refusal != null) {
            session.recordRefusal(restaurantId, addressId, refusal)
            _state.update { it.copy(addingItemId = null, serviceability = refusal, message = errorMessage(refusal.message)) }
            return
        }
        _state.update { it.copy(addingItemId = null, message = errorMessage(describe(error))) }
    }

    /** True, with the block's message shown, when adding must not be attempted. */
    private fun blockIfUnserviceable(): Boolean {
        val restaurant = _state.value.restaurant ?: return true
        val serviceability = currentServiceability(restaurant)
        if (serviceability is Serviceability.Blocked) {
            _state.update { it.copy(serviceability = serviceability, message = errorMessage(serviceability.message)) }
            return true
        }
        return false
    }

    /** The server's answer for this address, weighed against a refusal it returned earlier. */
    private fun currentServiceability(restaurant: FeastRestaurantDto): Serviceability =
        ServiceabilityRules.resolve(restaurant, session.refusalFor(restaurantId))

    private fun describe(error: FoodError): String = when (error) {
        is FoodError.Network -> "Check your connection and try again."
        FoodError.NotFound, FoodError.NotAvailable -> "This restaurant isn't available."
        else -> error.serverMessage ?: "Something went wrong. Please try again."
    }

    private fun FeastRestaurantDto.withoutPointAnswer() =
        copy(distanceMeters = null, serviceable = null, unserviceableReasonCode = null, unserviceableMessage = null)

    companion object {
        const val CHOOSE_ADDRESS_AGAIN = "Choose your delivery address again"
        private const val CART_CONFLICT = "FOOD_CART_RESTAURANT_CONFLICT"
    }
}
