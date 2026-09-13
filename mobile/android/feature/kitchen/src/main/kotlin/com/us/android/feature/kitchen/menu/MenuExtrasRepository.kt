package com.us.android.feature.kitchen.menu

import com.us.android.core.food.model.Paise
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import javax.inject.Inject

data class MenuVariant(val id: String, val name: String, val price: Paise, val isAvailable: Boolean)

data class AddOn(val id: String, val name: String, val price: Paise, val isAvailable: Boolean)

data class AddOnGroup(
    val id: String,
    val name: String,
    val minSelect: Int,
    val maxSelect: Int,
    val addOns: List<AddOn>,
)

data class MenuItemExtras(val variants: List<MenuVariant>, val addOnGroups: List<AddOnGroup>)

/**
 * ============================================================================
 *  ROUTE GAP — variants and add-on groups.
 * ============================================================================
 *
 * food-service prices add-ons at checkout (store/postgres/addons.go) and carts
 * accept a `variant_id`, but there are NO partner routes to list, create, edit
 * or delete a menu item's variants or add-on groups. The editor is built
 * against this interface so the screens need no change when they arrive:
 *
 *     GET    /v1/food/partner/menu/items/:itemId/variants
 *     PUT    /v1/food/partner/menu/items/:itemId/variants/:variantId
 *     GET    /v1/food/partner/menu/items/:itemId/addon-groups
 *     PUT    /v1/food/partner/menu/items/:itemId/addon-groups/:groupId
 *
 * (proposed shapes, to be agreed with the backend lane). Until then every call
 * is [FoodError.NotAvailable] and the editor says so.
 */
interface MenuExtrasRepository {
    suspend fun extras(itemId: String): FoodResult<MenuItemExtras>

    suspend fun saveVariant(itemId: String, variant: MenuVariant): FoodResult<MenuVariant>

    suspend fun saveAddOnGroup(itemId: String, group: AddOnGroup): FoodResult<AddOnGroup>
}

class UnavailableMenuExtrasRepository @Inject constructor() : MenuExtrasRepository {
    override suspend fun extras(itemId: String): FoodResult<MenuItemExtras> = FoodResult.Failure(FoodError.NotAvailable)

    override suspend fun saveVariant(itemId: String, variant: MenuVariant): FoodResult<MenuVariant> =
        FoodResult.Failure(FoodError.NotAvailable)

    override suspend fun saveAddOnGroup(itemId: String, group: AddOnGroup): FoodResult<AddOnGroup> =
        FoodResult.Failure(FoodError.NotAvailable)
}
