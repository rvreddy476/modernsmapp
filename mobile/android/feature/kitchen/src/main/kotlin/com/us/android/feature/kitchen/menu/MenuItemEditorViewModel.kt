package com.us.android.feature.kitchen.menu

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.MenuItemDto
import com.us.android.core.food.network.MenuItemRequest
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.feature.kitchen.money.RupeeFormat
import com.us.android.feature.kitchen.navigation.requireArg
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.math.BigDecimal
import javax.inject.Inject

/** The dish form's client checks. Prices are parsed straight to paise; tax is a decimal, never a Double. */
object MenuItemRules {
    const val FIELD_NAME = "name"
    const val FIELD_PRICE = "price"
    const val FIELD_OFFER = "discount"
    const val FIELD_PREP = "prep"
    const val FIELD_TAX = "tax"
    private const val MAX_PREP_MINUTES = 240
    private val MAX_TAX = BigDecimal(28)

    data class Parsed(val basePrice: Paise, val offerPrice: Paise?, val prepMinutes: Int, val taxPercent: BigDecimal)

    fun validate(name: String, price: String, offer: String, prepMinutes: String, taxPercent: String): Pair<Map<String, String>, Parsed?> {
        val base = RupeeFormat.parse(price)
        val offerPrice = offer.takeIf { it.isNotBlank() }?.let(RupeeFormat::parse)
        val prep = prepMinutes.trim().toIntOrNull()
        val tax = taxPercent.trim().toBigDecimalOrNull()
        val errors = buildMap {
            if (name.isBlank()) put(FIELD_NAME, "Give the dish a name")
            if (base == null || base.value <= 0) put(FIELD_PRICE, "Enter a price like 249 or 249.50")
            when {
                offer.isNotBlank() && offerPrice == null -> put(FIELD_OFFER, "Enter an offer price like 199")
                offerPrice != null && base != null && offerPrice >= base -> put(FIELD_OFFER, "The offer price must be lower than the price")
            }
            if (prep == null || prep !in 1..MAX_PREP_MINUTES) put(FIELD_PREP, "Between 1 and 240 minutes")
            if (tax == null || tax < BigDecimal.ZERO || tax > MAX_TAX) put(FIELD_TAX, "A GST rate from 0 to 28")
        }
        val parsed = if (errors.isEmpty() && base != null && prep != null && tax != null) {
            Parsed(base, offerPrice, prep, tax)
        } else {
            null
        }
        return errors to parsed
    }
}

data class MenuItemEditorUiState(
    val isNew: Boolean = true,
    val loading: Boolean = false,
    val name: String = "",
    val description: String = "",
    val foodType: String = FOOD_VEG,
    val price: String = "",
    val offer: String = "",
    val prepMinutes: String = "15",
    val taxPercent: String = "5",
    val recommended: Boolean = false,
    /** Kept and sent back unchanged: UPDATE replaces every field. */
    val imageUrl: String = "",
    val errors: Map<String, String> = emptyMap(),
    val saving: Boolean = false,
    val confirmingDelete: Boolean = false,
    val deleting: Boolean = false,
    /** Set when the editor is done and should close. */
    val finished: Boolean = false,
    val extrasNote: String = EXTRAS_UNAVAILABLE,
    val message: UsMessage? = null,
)

/**
 * Create or edit one dish → `POST …/menu/items` or `PATCH /menu/items/:id`.
 *
 * ROUTE GAPS: there is no partner `GET /menu/items/:id` (the dish is found in the
 * category list); menu items take an `image_url`, not a media id, so a photo
 * cannot be attached through the upload pipeline; variants and add-on groups
 * have no partner routes ([MenuExtrasRepository]).
 */
@HiltViewModel
class MenuItemEditorViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val kitchen: KitchenRepository,
    private val extras: MenuExtrasRepository,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()
    private val categoryId = savedStateHandle.requireArg("categoryId")
    private val itemId: String? = savedStateHandle.get<String>("itemId")

    private val _state = MutableStateFlow(MenuItemEditorUiState(isNew = itemId == null, loading = itemId != null))
    val state: StateFlow<MenuItemEditorUiState> = _state.asStateFlow()

    init {
        itemId?.let { id ->
            load(id)
            viewModelScope.launch {
                if (extras.extras(id) is FoodResult.Success) _state.update { it.copy(extrasNote = "") }
            }
        }
    }

    fun onName(value: String) = edit(MenuItemRules.FIELD_NAME) { it.copy(name = value.take(MAX_NAME)) }

    fun onDescription(value: String) = edit("description") { it.copy(description = value.take(MAX_DESCRIPTION)) }

    fun onFoodType(value: String) = edit("food_type") { it.copy(foodType = value) }

    fun onPrice(value: String) = edit(MenuItemRules.FIELD_PRICE) { it.copy(price = priceInput(value)) }

    fun onOffer(value: String) = edit(MenuItemRules.FIELD_OFFER) { it.copy(offer = priceInput(value)) }

    fun onPrepMinutes(value: String) = edit(MenuItemRules.FIELD_PREP) { it.copy(prepMinutes = value.filter(Char::isDigit).take(3)) }

    fun onTaxPercent(value: String) = edit(MenuItemRules.FIELD_TAX) { it.copy(taxPercent = priceInput(value).take(5)) }

    fun onRecommended(value: Boolean) = edit("recommended") { it.copy(recommended = value) }

    fun askDelete() {
        _state.update { it.copy(confirmingDelete = true) }
    }

    fun cancelDelete() {
        _state.update { it.copy(confirmingDelete = false) }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun save() {
        val s = _state.value
        val (errors, parsed) = MenuItemRules.validate(s.name, s.price, s.offer, s.prepMinutes, s.taxPercent)
        if (parsed == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        val request = MenuItemRequest(
            categoryId = categoryId.takeIf { itemId == null },
            name = s.name.trim(),
            description = s.description.trim(),
            foodType = s.foodType,
            basePrice = parsed.basePrice,
            discountPrice = parsed.offerPrice,
            imageUrl = s.imageUrl,
            preparationMinutes = parsed.prepMinutes,
            isRecommended = s.recommended,
            taxPercentage = parsed.taxPercent,
        )
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            val result = if (itemId == null) {
                kitchen.createMenuItem(restaurantId, request)
            } else {
                kitchen.updateMenuItem(itemId, request)
            }
            when (result) {
                is FoodResult.Success -> _state.update { it.copy(saving = false, finished = true) }
                is FoodResult.Failure -> _state.update { it.copy(saving = false, message = result.error.asMessage()) }
            }
        }
    }

    fun delete() {
        val id = itemId ?: return
        _state.update { it.copy(confirmingDelete = false, deleting = true) }
        viewModelScope.launch {
            when (val result = kitchen.deleteMenuItem(id)) {
                is FoodResult.Success -> _state.update { it.copy(deleting = false, finished = true) }
                is FoodResult.Failure -> _state.update { it.copy(deleting = false, message = result.error.asMessage()) }
            }
        }
    }

    private fun load(id: String) {
        viewModelScope.launch {
            when (val result = kitchen.menuCategories(restaurantId)) {
                is FoodResult.Success -> {
                    val item = result.value.flatMap { it.items }.firstOrNull { it.id == id }
                    _state.update {
                        if (item == null) {
                            it.copy(loading = false, message = UsMessage("This dish is no longer on your menu."))
                        } else {
                            fill(it, item)
                        }
                    }
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(loading = false, message = UsMessage(result.error.userMessage()))
                }
            }
        }
    }

    private fun fill(state: MenuItemEditorUiState, item: MenuItemDto): MenuItemEditorUiState = state.copy(
        loading = false,
        name = item.name,
        description = item.description.orEmpty(),
        foodType = item.foodType.ifBlank { FOOD_VEG },
        price = RupeeFormat.toEntryText(item.basePrice),
        offer = item.discountPrice?.takeIf { it.value > 0 }?.let(RupeeFormat::toEntryText).orEmpty(),
        prepMinutes = item.preparationMinutes.takeIf { it > 0 }?.toString() ?: state.prepMinutes,
        taxPercent = item.taxPercentage.stripTrailingZeros().toPlainString(),
        recommended = item.isRecommended,
        imageUrl = item.imageUrl.orEmpty(),
    )

    private fun priceInput(value: String): String = value.filter { it.isDigit() || it == '.' }.take(MAX_PRICE_INPUT)

    private inline fun edit(field: String, crossinline change: (MenuItemEditorUiState) -> MenuItemEditorUiState) {
        _state.update { change(it).copy(errors = it.errors - field) }
    }

    private companion object {
        const val MAX_NAME = 120
        const val MAX_DESCRIPTION = 500
        const val MAX_PRICE_INPUT = 12
    }
}

internal const val EXTRAS_UNAVAILABLE =
    "Not available yet: Feast's server has no partner routes for sizes, variants or add-on groups. Customers order this dish at its price."
