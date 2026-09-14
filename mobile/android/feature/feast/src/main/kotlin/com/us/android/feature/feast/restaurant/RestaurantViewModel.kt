package com.us.android.feature.feast.restaurant

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.AddCartItemRequest
import com.us.android.core.food.network.CartAddonRequest
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastMenuCategoryDto
import com.us.android.core.food.network.FeastMenuItemDto
import com.us.android.core.food.network.FeastRestaurantDto
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

/** The choices an item requires, checked before anything is sent. The SERVER still validates. */
object ItemSelectionRules {

    fun needsSheet(item: FeastMenuItemDto): Boolean = item.variants.isNotEmpty() || item.addonGroups.isNotEmpty()

    /** The first unmet requirement, as a sentence, or null when [selection] can be added. */
    fun problem(selection: ItemSelection): String? {
        val item = selection.item
        val variants = item.variants.filter { it.isAvailable }
        if (item.variants.isNotEmpty() && variants.none { it.id == selection.variantId }) return "Choose a size"
        for (group in item.addonGroups) {
            val chosen = group.addons.count { it.id in selection.addonIds }
            val min = if (group.isRequired) maxOf(group.minSelect, 1) else group.minSelect
            if (chosen < min) return "Choose at least $min in ${group.name}"
            if (group.maxSelect > 0 && chosen > group.maxSelect) return "Choose at most ${group.maxSelect} in ${group.name}"
        }
        if (selection.quantity !in 1..MAX_QUANTITY) return "Choose a quantity"
        return null
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
 * Serviceability is checked BEFORE a request: a closed or not-accepting
 * restaurant, or one the server already refused for this address, blocks the
 * add locally with that message and sends nothing.
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

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            val restaurant = async { repository.restaurant(restaurantId) }
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
            val firstVariant = item.variants.firstOrNull { it.isAvailable }?.id
            _state.update { it.copy(sheet = ItemSelection(item, variantId = firstVariant)) }
        } else {
            add(ItemSelection(item))
        }
    }

    fun updateSheet(selection: ItemSelection) {
        _state.update { it.copy(sheet = selection) }
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

    /** Re-evaluates serviceability, e.g. after the customer picked another address. */
    fun refreshServiceability() {
        val restaurant = _state.value.restaurant ?: return
        _state.update { it.copy(serviceability = currentServiceability(restaurant)) }
    }

    private fun add(selection: ItemSelection, clearExisting: Boolean = false) {
        if (blockIfUnserviceable()) return
        _state.update { it.copy(addingItemId = selection.item.id) }
        viewModelScope.launch {
            val request = AddCartItemRequest(
                menuItemId = selection.item.id,
                quantity = selection.quantity,
                variantId = selection.variantId,
                clearExisting = clearExisting,
                addons = selection.addonIds.sorted().map { CartAddonRequest(addonId = it, quantity = 1) },
            )
            when (val result = repository.addToCart(request)) {
                is FoodResult.Success -> _state.update {
                    it.copy(addingItemId = null, cart = result.value, message = successMessage("Added ${selection.item.name}"))
                }
                is FoodResult.Failure -> onAddFailed(selection, result.error)
            }
        }
    }

    private fun onAddFailed(selection: ItemSelection, error: FoodError) {
        if (error.code == CART_CONFLICT) {
            _state.update { it.copy(addingItemId = null, pendingReplace = selection) }
            return
        }
        val refusal = ServiceabilityRules.fromRefusal(error)
        if (refusal != null) {
            session.recordRefusal(restaurantId, session.address.value?.id, refusal)
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

    /** The server's standing refusal first (its own words), then the restaurant's flags. */
    private fun currentServiceability(restaurant: FeastRestaurantDto): Serviceability =
        session.refusalFor(restaurantId) ?: ServiceabilityRules.of(restaurant)

    private fun describe(error: FoodError): String = when (error) {
        is FoodError.Network -> "Check your connection and try again."
        FoodError.NotFound, FoodError.NotAvailable -> "This restaurant isn't available."
        else -> error.serverMessage ?: "Something went wrong. Please try again."
    }

    private companion object {
        const val CART_CONFLICT = "FOOD_CART_RESTAURANT_CONFLICT"
    }
}
