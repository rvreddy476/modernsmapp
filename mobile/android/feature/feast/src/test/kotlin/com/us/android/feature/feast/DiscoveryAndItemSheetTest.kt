package com.us.android.feature.feast

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.model.toRupeeText
import com.us.android.core.food.network.CartAddonRequest
import com.us.android.core.food.network.FeastAddonDto
import com.us.android.core.food.network.FeastAddonGroupDto
import com.us.android.core.food.network.FeastMenuCategoryDto
import com.us.android.core.food.network.FeastMenuDto
import com.us.android.core.food.network.FeastMenuItemDto
import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.core.food.network.ItemsDto
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feast.home.FeastHomeViewModel
import com.us.android.feature.feast.home.RestaurantCardModel
import com.us.android.feature.feast.restaurant.ItemSelection
import com.us.android.feature.feast.restaurant.ItemSelectionRules
import com.us.android.feature.feast.restaurant.RestaurantViewModel
import com.us.android.feature.feast.restaurant.Serviceability
import com.us.android.feature.feast.restaurant.ServiceabilityRules
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.time.Instant

/** Restaurants near the picked address, the server's serviceability, and the item sheet (sizes, add-ons, address). */
@OptIn(ExperimentalCoroutinesApi::class)
class DiscoveryAndItemSheetTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    /** Noon in India on the fixtures' day. */
    private val noon = Instant.parse("2026-09-13T06:30:00Z")

    private val nearList = fixture("restaurants_get_200_near.json", ItemsDto.serializer(FeastRestaurantDto.serializer())).items
    private val goldenMenu = fixture("restaurant_menu_get_200.json", FeastMenuDto.serializer())
    private val paneer = goldenMenu.categories.single().items.first()

    private fun homeVm(api: FakeFeastApi, session: FeastSession = FeastSession()) = FeastHomeViewModel(api.repository(), session)

    private fun restaurantVm(api: FakeFeastApi, session: FeastSession) =
        RestaurantViewModel(SavedStateHandle(mapOf("restaurantId" to "r-1")), api.repository(), session)

    // ── The list asks for the picked address's point ────────────────────

    @Test
    fun `the list request carries lat and lng only when an address with coordinates is selected`() = runTest(dispatcher) {
        val pinned = FakeFeastApi().apply { addresses = listOf(pinnedAddress) }
        homeVm(pinned).refresh()
        advanceUntilIdle()
        assertThat(pinned.restaurantListRequests.single()).isEqualTo(RestaurantListRequest(null, null, 12.97, 77.59))

        val unpinned = FakeFeastApi().apply { addresses = listOf(pinnedAddress.copy(latitude = null, longitude = null)) }
        homeVm(unpinned).refresh()
        advanceUntilIdle()
        assertThat(unpinned.restaurantListRequests.single()).isEqualTo(RestaurantListRequest(null, "Bengaluru", null, null))

        val none = FakeFeastApi().apply { addresses = emptyList() }
        homeVm(none).refresh()
        advanceUntilIdle()
        assertThat(none.restaurantListRequests.single()).isEqualTo(RestaurantListRequest(null, null, null, null))
    }

    @Test
    fun `restaurant detail is read for the selected address's point`() = runTest(dispatcher) {
        val api = FakeFeastApi()
        val session = FeastSession().apply { selectAddress(pinnedAddress) }
        restaurantVm(api, session)
        advanceUntilIdle()
        assertThat(api.restaurantDetailPoints.single()).isEqualTo(12.97 to 77.59)

        val bare = FakeFeastApi()
        restaurantVm(bare, FeastSession())
        advanceUntilIdle()
        assertThat(bare.restaurantDetailPoints.single()).isEqualTo(null to null)
    }

    // ── Cards from restaurants_get_200_near ─────────────────────────────

    @Test
    fun `cards from the near fixture show the server's reasons, distance and opening time in the served order`() =
        runTest(dispatcher) {
            val api = FakeFeastApi().apply {
                addresses = listOf(pinnedAddress)
                restaurants = nearList
            }
            val model = homeVm(api)
            model.refresh()
            advanceUntilIdle()

            val rows = model.state.value.restaurants
            assertThat(rows.map { it.restaurant.name }).containsExactly("Test Kitchen", "Night Owl Biryani", "Highway Dhaba").inOrder()
            val (kitchen, owl, dhaba) = rows.map { RestaurantCardModel.from(it, noon) }

            assertThat(kitchen.enabled).isTrue()
            assertThat(kitchen.unavailableMessage).isNull()
            assertThat(kitchen.distance).isEqualTo("1.2 km")
            assertThat(kitchen.statusLabel).isEqualTo("Open")

            assertThat(owl.enabled).isFalse()
            assertThat(owl.statusLabel).isEqualTo("Closed")
            assertThat(owl.unavailableMessage).isEqualTo("restaurant is closed at this time")
            assertThat(owl.opensAt).isEqualTo("Opens at 6:00 PM")

            assertThat(dhaba.enabled).isFalse()
            assertThat(dhaba.statusLabel).isEqualTo("Doesn't deliver here")
            assertThat(dhaba.unavailableMessage).isEqualTo("delivery address is outside the restaurant's delivery range")
            assertThat(dhaba.distance).isEqualTo("14.8 km")
            assertThat(dhaba.opensAt).isNull()
        }

    @Test
    fun `the list keeps the server's order even when an unavailable restaurant comes first`() = runTest(dispatcher) {
        val api = FakeFeastApi().apply {
            addresses = listOf(pinnedAddress)
            restaurants = nearList.reversed()
        }
        val model = homeVm(api)
        model.refresh()
        advanceUntilIdle()

        assertThat(model.state.value.restaurants.map { it.restaurant.name })
            .containsExactly("Highway Dhaba", "Night Owl Biryani", "Test Kitchen").inOrder()
    }

    @Test
    fun `an opening on another day names the day`() {
        val owl = nearList[1]
        assertThat(RestaurantCardModel.from(row(owl), Instant.parse("2026-09-12T18:00:00Z")).opensAt)
            .isEqualTo("Opens tomorrow at 6:00 PM")
        assertThat(RestaurantCardModel.from(row(owl), Instant.parse("2026-09-10T06:00:00Z")).opensAt)
            .isEqualTo("Opens Sun at 6:00 PM")
    }

    private fun row(r: FeastRestaurantDto) = com.us.android.feature.feast.home.RestaurantRow(r, ServiceabilityRules.of(r))

    // ── ServiceabilityRules ─────────────────────────────────────────────

    @Test
    fun `serviceability prefers the server's fields over the guess from the flags`() {
        // The flags say closed, but the server judged this point serviceable.
        assertThat(ServiceabilityRules.of(restaurant(isOpen = false).copy(serviceable = true))).isEqualTo(Serviceability.Open)

        // The flags say open, but the server refused this point.
        val refused = restaurant().copy(
            serviceable = false,
            unserviceableReasonCode = "FOOD_ADDRESS_OUT_OF_RANGE",
            unserviceableMessage = "delivery address is outside the restaurant's delivery range",
        )
        assertThat(ServiceabilityRules.of(refused)).isEqualTo(
            Serviceability.Blocked("delivery address is outside the restaurant's delivery range", "FOOD_ADDRESS_OUT_OF_RANGE", fromServer = true),
        )

        // No point sent: only then the flags decide.
        assertThat(ServiceabilityRules.of(restaurant(isOpen = false))).isEqualTo(
            Serviceability.Blocked("Test Kitchen is closed right now.", null, fromServer = false),
        )
    }

    @Test
    fun `a fresh server answer supersedes a remembered range refusal but not a tax refusal`() {
        val outOfRange = Serviceability.Blocked("too far", "FOOD_ADDRESS_OUT_OF_RANGE", fromServer = true)
        val noTax = Serviceability.Blocked("no tax category", "FOOD_RESTAURANT_TAX_CATEGORY_MISSING", fromServer = true)
        val serviceable = restaurant().copy(serviceable = true)

        assertThat(ServiceabilityRules.resolve(serviceable, outOfRange)).isEqualTo(Serviceability.Open)
        assertThat(ServiceabilityRules.resolve(serviceable, noTax)).isEqualTo(noTax)
        assertThat(ServiceabilityRules.resolve(restaurant(), outOfRange)).isEqualTo(outOfRange)
    }

    // ── The item sheet ──────────────────────────────────────────────────

    private fun group(id: String, max: Int = 0, required: Boolean = false, vararg addons: FeastAddonDto) =
        FeastAddonGroupDto(id = id, name = "Group $id", maxSelect = max, isRequired = required, addons = addons.toList())

    private fun addon(id: String, paise: Long = 1_000) = FeastAddonDto(id = id, name = "Addon $id", pricePaise = Paise(paise))

    @Test
    fun `a required group with no available add-ons disables Add and says why`() = runTest(dispatcher) {
        // min_select 0 with is_required: required still means at least one.
        val item = FeastMenuItemDto(
            id = "m-9",
            name = "Dosa",
            isAvailable = true,
            addonGroups = listOf(group("chutney", max = 1, required = true)),
        )
        assertThat(ItemSelectionRules.problem(ItemSelection(item)))
            .isEqualTo("Group chutney isn't available right now, so Dosa can't be added")

        val api = FakeFeastApi().apply {
            this.menu = FeastMenuDto(listOf(FeastMenuCategoryDto(id = "c-1", name = "Mains", items = listOf(item))))
        }
        val model = restaurantVm(api, FeastSession())
        advanceUntilIdle()
        model.onAdd(item)
        model.confirmSheet()
        advanceUntilIdle()

        assertThat(api.addToCartRequests).isEmpty()
        assertThat(model.state.value.message?.text).isEqualTo("Group chutney isn't available right now, so Dosa can't be added")
    }

    @Test
    fun `max_select is enforced when picking add-ons`() {
        val item = FeastMenuItemDto(
            id = "m-8",
            name = "Thali",
            isAvailable = true,
            addonGroups = listOf(group("extras", max = 2, required = false, addon("a"), addon("b"), addon("c"))),
        )
        var selection = ItemSelection(item)
        listOf("a", "b", "c").forEach { selection = ItemSelectionRules.toggleAddon(selection, it) }

        assertThat(selection.addonIds).containsExactly("a", "b")
        assertThat(ItemSelectionRules.problem(selection)).isNull()
        assertThat(ItemSelectionRules.problem(selection.copy(addonIds = setOf("a", "b", "c")))).isEqualTo("Choose at most 2 in Group extras")

        // A one-choice group swaps rather than refusing.
        val single = item.copy(addonGroups = listOf(group("sauce", max = 1, required = false, addon("x"), addon("y"))))
        val swapped = ItemSelectionRules.toggleAddon(ItemSelectionRules.toggleAddon(ItemSelection(single), "x"), "y")
        assertThat(swapped.addonIds).containsExactly("y")
    }

    @Test
    fun `the total comes from price_paise - Half plus Extra cheese is 179 rupees 99`() {
        val half = paneer.variants.single()
        val cheese = paneer.addonGroups.single().addons.single()
        val selection = ItemSelection(paneer, variantId = half.id, addonIds = setOf(cheese.id))

        assertThat(ItemSelectionRules.unitPrice(selection)).isEqualTo(Paise(17_999))
        assertThat(ItemSelectionRules.total(selection).toRupeeText()).isEqualTo("₹179.99")
        assertThat(ItemSelectionRules.total(selection.copy(quantity = 3)).toRupeeText()).isEqualTo("₹539.97")
        // No size chosen: the discounted price, as the cart prices it.
        assertThat(ItemSelectionRules.unitPrice(ItemSelection(paneer))).isEqualTo(Paise(22_550))

        // 29.90 + 10.15 is 40.04999999999999 in doubles; paise keep it exact.
        val sized = paneer.copy(
            variants = listOf(half.copy(pricePaise = Paise(2_990))),
            addonGroups = listOf(paneer.addonGroups.single().copy(addons = listOf(cheese.copy(pricePaise = Paise(1_015))))),
        )
        assertThat(ItemSelectionRules.total(selection.copy(item = sized)).toRupeeText()).isEqualTo("₹40.05")
    }

    @Test
    fun `adding from the sheet sends the size, the add-ons and the delivery address`() = runTest(dispatcher) {
        val api = FakeFeastApi().apply { menu = goldenMenu }
        val session = FeastSession().apply { selectAddress(pinnedAddress) }
        val model = restaurantVm(api, session)
        advanceUntilIdle()

        model.onAdd(paneer)
        val cheese = paneer.addonGroups.single().addons.single().id
        model.toggleAddon(cheese)
        model.confirmSheet()
        advanceUntilIdle()

        val request = api.addToCartRequests.single()
        assertThat(request.menuItemId).isEqualTo(paneer.id)
        assertThat(request.variantId).isEqualTo(paneer.variants.single().id)
        assertThat(request.addons).containsExactly(CartAddonRequest(addonId = cheese, quantity = 1))
        assertThat(request.addressId).isEqualTo("addr-1")
    }

    // ── Add-to-cart refusals ────────────────────────────────────────────

    private val plainItem = FeastMenuItemDto(id = "m-1", restaurantId = "r-1", name = "Paneer Tikka", isAvailable = true)

    private fun apiWithPlainItem() = FakeFeastApi().apply {
        this.menu = FeastMenuDto(listOf(FeastMenuCategoryDto(id = "c-1", name = "Starters", items = listOf(plainItem))))
    }

    @Test
    fun `a 422 out-of-range add blocks with the server's message and sends nothing more`() = runTest(dispatcher) {
        val api = apiWithPlainItem()
        api.addToCartResponse = { refusedWithFixture(422, "cart_item_post_422_out_of_range.json") }
        val model = restaurantVm(api, FeastSession().apply { selectAddress(pinnedAddress) })
        advanceUntilIdle()

        model.onAdd(plainItem)
        advanceUntilIdle()
        model.onAdd(plainItem)
        advanceUntilIdle()

        assertThat(api.addToCartRequests).hasSize(1)
        val blocked = model.state.value.serviceability as Serviceability.Blocked
        assertThat(blocked.message).isEqualTo("delivery address is outside the restaurant's delivery range")
        assertThat(blocked.fromServer).isTrue()
        assertThat(model.state.value.message?.text).isEqualTo("delivery address is outside the restaurant's delivery range")
    }

    @Test
    fun `a 404 for the address asks the customer to choose it again`() = runTest(dispatcher) {
        val api = apiWithPlainItem()
        api.addToCartResponse = { refused(404, "FOOD_NOT_FOUND", "not found") }
        val model = restaurantVm(api, FeastSession().apply { selectAddress(pinnedAddress) })
        advanceUntilIdle()

        model.onAdd(plainItem)
        advanceUntilIdle()

        assertThat(model.state.value.message?.text).isEqualTo("Choose your delivery address again")
        assertThat(model.state.value.serviceability).isEqualTo(Serviceability.Open)
    }
}
