package com.us.android.feature.feast

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastMenuCategoryDto
import com.us.android.core.food.network.FeastMenuDto
import com.us.android.core.food.network.FeastMenuItemDto
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feast.cart.BillLine
import com.us.android.feature.feast.cart.CartBill
import com.us.android.feature.feast.checkout.FeastCheckoutState
import com.us.android.feature.feast.checkout.FeastCheckoutViewModel
import com.us.android.feature.feast.restaurant.RestaurantViewModel
import com.us.android.feature.feast.restaurant.Serviceability
import com.us.android.core.payments.PaymentHandoff
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class CartAndServiceabilityTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    // ── The bill is the server's ────────────────────────────────────────

    @Test
    fun `the bill shows the server's charges, taxes and total from the golden cart`() {
        val bill = CartBill.from(fixture("cart_get_200_section_9_5.json", FeastCartDto.serializer()))

        assertThat(bill.itemTotal).isEqualTo(Paise(50_000))
        assertThat(bill.addonTotal).isEqualTo(Paise(6_000))
        assertThat(bill.charges).containsExactly(
            BillLine("Packaging charges", Paise(2_000)),
            BillLine("Platform fee", Paise(500)),
            BillLine("Delivery fee", Paise(2_900)),
        ).inOrder()
        assertThat(bill.taxes).containsExactly(
            BillLine("GST on platform and delivery fees", Paise(612)),
            BillLine("GST on food, paid by the platform under section 9(5)", Paise(2_900)),
        ).inOrder()
        assertThat(bill.total).isEqualTo(Paise(64_912))
        assertThat(bill.adviserNotice).isEqualTo("Tax rates pending adviser confirmation")
        assertThat(bill.payable).isTrue()
    }

    @Test
    fun `the total is final_amount_paise even when the lines do not add up to it`() {
        val golden = fixture("cart_get_200_supplier_liable.json", FeastCartDto.serializer())
        // The server's total is authoritative, including rules the client does
        // not know. A client that re-added its own lines would show 72,452 here.
        val tampered = golden.copy(totalsPaise = golden.totalsPaise!!.copy(finalAmountPaise = Paise(70_001)))

        val bill = CartBill.from(tampered)

        assertThat(bill.total).isEqualTo(Paise(70_001))
    }

    @Test
    fun `a cart the server cannot price has no total and cannot be paid, with the server's words`() {
        val bill = CartBill.from(fixture("cart_get_200_pricing_blocked.json", FeastCartDto.serializer()))

        assertThat(bill.total).isNull()
        assertThat(bill.payable).isFalse()
        assertThat(bill.blockedReason).isEqualTo("restaurant has no GST tax category and cannot take orders")
    }

    // ── Serviceability blocks adding ────────────────────────────────────

    private val item = FeastMenuItemDto(id = "m-1", restaurantId = "r-1", name = "Paneer Tikka", isAvailable = true)

    private fun restaurantVm(api: FakeFeastApi, session: FeastSession) =
        RestaurantViewModel(SavedStateHandle(mapOf("restaurantId" to "r-1")), api.repository(), session)

    private fun apiWithMenu() = FakeFeastApi().apply {
        menu = FeastMenuDto(listOf(FeastMenuCategoryDto(id = "c-1", name = "Starters", items = listOf(item))))
    }

    @Test
    fun `a closed restaurant blocks adding to the cart and sends nothing`() = runTest(dispatcher) {
        val api = apiWithMenu().apply { restaurant = restaurant(isOpen = false) }
        val model = restaurantVm(api, FeastSession())
        advanceUntilIdle()

        model.onAdd(item)
        advanceUntilIdle()

        assertThat(api.addToCartRequests).isEmpty()
        assertThat(model.state.value.serviceability).isInstanceOf(Serviceability.Blocked::class.java)
        assertThat(model.state.value.message?.text).isEqualTo("Test Kitchen is closed right now.")
    }

    @Test
    fun `an out-of-range refusal at checkout blocks adding from the menu with the server's message`() =
        runTest(dispatcher) {
            val api = apiWithMenu()
            val session = FeastSession()
            api.placeOrderResponse = {
                refused(422, "FOOD_ADDRESS_OUT_OF_RANGE", "delivery address is outside the restaurant's delivery range")
            }

            val checkout = FeastCheckoutViewModel(api.repository(), session, PaymentHandoff(), confirmingOnlyCoordinator(), SavedStateHandle())
            advanceUntilIdle()
            checkout.placeOrder()
            advanceUntilIdle()
            assertThat((checkout.state.value as FeastCheckoutState.Ready).refusal)
                .isEqualTo("delivery address is outside the restaurant's delivery range")

            val menu = restaurantVm(api, session)
            advanceUntilIdle()
            menu.onAdd(item)
            advanceUntilIdle()

            assertThat(api.addToCartRequests).isEmpty()
            val blocked = menu.state.value.serviceability as Serviceability.Blocked
            assertThat(blocked.fromServer).isTrue()
            assertThat(blocked.message).isEqualTo("delivery address is outside the restaurant's delivery range")
            assertThat(menu.state.value.message?.text).isEqualTo("delivery address is outside the restaurant's delivery range")
        }

    @Test
    fun `a restaurant the server says is not accepting blocks every later add with its message`() = runTest(dispatcher) {
        val api = apiWithMenu()
        api.addToCartResponse = { refused(422, "FOOD_RESTAURANT_NOT_ACCEPTING", "restaurant is not accepting orders") }
        val model = restaurantVm(api, FeastSession())
        advanceUntilIdle()

        model.onAdd(item)
        advanceUntilIdle()
        model.onAdd(item)
        advanceUntilIdle()

        assertThat(api.addToCartRequests).hasSize(1)
        assertThat((model.state.value.serviceability as Serviceability.Blocked).message).isEqualTo("restaurant is not accepting orders")
    }

    @Test
    fun `an open restaurant adds straight to the server cart`() = runTest(dispatcher) {
        val api = apiWithMenu()
        val model = restaurantVm(api, FeastSession())
        advanceUntilIdle()

        model.onAdd(item)
        advanceUntilIdle()

        assertThat(api.addToCartRequests.single().menuItemId).isEqualTo("m-1")
        assertThat(model.state.value.serviceability).isEqualTo(Serviceability.Open)
    }
}
