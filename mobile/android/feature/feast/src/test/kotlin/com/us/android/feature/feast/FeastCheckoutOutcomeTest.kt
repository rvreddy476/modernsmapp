package com.us.android.feature.feast

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.FeastOrderMoneyDto
import com.us.android.core.food.network.FeastTotalsPaiseDto
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feast.checkout.FeastCheckoutState
import com.us.android.feature.feast.checkout.FeastCheckoutViewModel
import com.us.android.feature.feast.checkout.FeastPaymentMethod
import com.us.android.feature.feast.checkout.FeastTotalChange
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Place order with no definite answer keeps the order it may have made (food
 * audit M1); an unpaid order is paid with Pay now, never re-placed; and a
 * total the server re-priced is said before any sheet opens (L2).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class FeastCheckoutOutcomeTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val api = FakeFeastApi()
    private val session = FeastSession()

    private fun vm(handle: SavedStateHandle = SavedStateHandle()) =
        FeastCheckoutViewModel(api.repository(), session, PaymentHandoff(), confirmingOnlyCoordinator(), handle)

    private fun TestScope.settle() {
        advanceUntilIdle()
        runCurrent()
    }

    private fun TestScope.readyModel(handle: SavedStateHandle = SavedStateHandle()): FeastCheckoutViewModel =
        vm(handle).also {
            settle()
            assertThat(it.state.value).isInstanceOf(FeastCheckoutState.Ready::class.java)
        }

    private fun gatewayTimeout() = refused<FeastOrderDto>(504, "GATEWAY_TIMEOUT", "upstream timed out")

    @Test
    fun `a gateway timeout keeps the key and body, and Try again resends the same order`() = runTest(dispatcher) {
        val model = readyModel()
        api.placeOrderResponse = { gatewayTimeout() }
        model.placeOrder()
        settle()

        val held = model.state.value as FeastCheckoutState.Ready
        assertThat(held.awaitingOutcome).isTrue()
        assertThat(held.refusal).isEqualTo(FeastCheckoutViewModel.OUTCOME_UNKNOWN)

        // A different method would be a different body under the same key: refused.
        model.selectMethod(FeastPaymentMethod.CARD)
        assertThat((model.state.value as FeastCheckoutState.Ready).method).isEqualTo(FeastPaymentMethod.UPI)

        api.placeOrderResponse = { ok(api.order) }
        model.placeOrder()
        settle()

        assertThat(api.placeOrderKeys).hasSize(2)
        assertThat(api.placeOrderKeys.distinct()).hasSize(1)
        assertThat(api.placeOrderBodies.distinct()).hasSize(1)
        assertThat(api.placeOrderBodies.first().paymentMethod).isEqualTo("upi")
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.OpeningPayment::class.java)
    }

    @Test
    fun `the same key still in flight is not a refusal, and is resent`() = runTest(dispatcher) {
        val model = readyModel()
        api.placeOrderResponse = { refused(409, "FOOD_IDEMPOTENCY_IN_PROGRESS", "this key is in progress") }
        model.placeOrder()
        settle()
        assertThat((model.state.value as FeastCheckoutState.Ready).awaitingOutcome).isTrue()

        api.placeOrderResponse = { ok(api.order) }
        model.placeOrder()
        settle()

        assertThat(api.placeOrderKeys.distinct()).hasSize(1)
    }

    @Test
    fun `a definite refusal creates no order, so the next attempt is a new key`() = runTest(dispatcher) {
        val model = readyModel()
        api.placeOrderResponse = { refused(400, "FOOD_RESTAURANT_CLOSED", "restaurant is closed") }
        model.placeOrder()
        settle()
        val refused = model.state.value as FeastCheckoutState.Ready
        assertThat(refused.awaitingOutcome).isFalse()

        api.placeOrderResponse = { ok(api.order) }
        model.placeOrder()
        settle()

        assertThat(api.placeOrderKeys).hasSize(2)
        assertThat(api.placeOrderKeys.distinct()).hasSize(2)
    }

    @Test
    fun `after process death mid-timeout the order is resent under the same key, not re-read from the cart`() =
        runTest(dispatcher) {
            val handle = SavedStateHandle()
            val first = readyModel(handle)
            api.placeOrderResponse = { gatewayTimeout() }
            first.placeOrder()
            settle()

            // The order was made; the cart is gone on the server.
            api.cart = api.cart.copy(items = emptyList())
            api.placeOrderResponse = { ok(api.order) }
            val restored = SavedStateHandle().also { copy -> handle.keys().forEach { copy[it] = handle.get<Any?>(it) } }
            val second = vm(restored)
            settle()

            assertThat(api.placeOrderKeys.distinct()).hasSize(1)
            assertThat(second.state.value).isInstanceOf(FeastCheckoutState.OpeningPayment::class.java)
        }

    @Test
    fun `pay now pays the existing order with a new attempt and never places another`() = runTest(dispatcher) {
        api.paymentAnswers += { payment("confirming") }
        val model = vm(SavedStateHandle(mapOf("payOrderId" to "order-1", "payOrderNumber" to "FG1")))
        settle()

        val unpaid = model.state.value as FeastCheckoutState.Unpaid
        assertThat(unpaid.orderId).isEqualTo("order-1")
        assertThat(unpaid.amount).isEqualTo(Paise(64_912))
        assertThat(api.cartReads).isEqualTo(0)

        model.retryPayment(FeastPaymentMethod.CARD)
        val opening = model.state.value as FeastCheckoutState.OpeningPayment

        assertThat(opening.request.orderId).isEqualTo("order-1")
        assertThat(opening.request.method).isEqualTo(FeastPaymentMethod.CARD)
        assertThat(api.placeOrderKeys).isEmpty()
    }

    @Test
    fun `pay now for an order the server already shows paid says paid`() = runTest(dispatcher) {
        api.paymentAnswers += { payment("paid") }
        val model = vm(SavedStateHandle(mapOf("payOrderId" to "order-1", "payOrderNumber" to "FG1")))
        settle()

        assertThat(model.state.value).isEqualTo(FeastCheckoutState.Paid("order-1", "FG1"))
    }

    @Test
    fun `place order sends the total on screen, to be compared, never charged`() = runTest(dispatcher) {
        val model = readyModel()
        model.placeOrder()
        settle()

        assertThat(api.placeOrderBodies.single().expectedTotalPaise).isEqualTo(Paise(64_912))
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.OpeningPayment::class.java)
    }

    @Test
    fun `a changed total is said with the server's figure, and nothing opens until the customer places again`() =
        runTest(dispatcher) {
            val model = readyModel()
            api.placeOrderResponse = { refusedWithFixture(409, "order_place_409_total_changed.json") }
            model.placeOrder()
            settle()

            val asked = model.state.value as FeastCheckoutState.Ready
            assertThat(asked.totalChanged).isEqualTo(FeastTotalChange(was = Paise(64_912), now = Paise(47_200)))
            assertThat(asked.shownTotal).isEqualTo(Paise(47_200))
            assertThat(asked.awaitingOutcome).isFalse()
            assertThat(asked.placing).isFalse()
            assertThat(asked.canSubmit).isTrue()

            api.placeOrderResponse = { ok(api.order) }
            model.placeOrder()
            settle()

            assertThat(api.placeOrderBodies.map { it.expectedTotalPaise })
                .containsExactly(Paise(64_912), Paise(47_200)).inOrder()
            // The refused attempt left nothing under the key: the next decision reuses it.
            assertThat(api.placeOrderKeys.distinct()).hasSize(1)
            assertThat(model.state.value).isInstanceOf(FeastCheckoutState.OpeningPayment::class.java)
        }

    @Test
    fun `a changed total re-reads the bill, so its lines match the new figure`() = runTest(dispatcher) {
        val model = readyModel()
        api.cart = pricedCart().copy(totalsPaise = FeastTotalsPaiseDto(finalAmountPaise = Paise(47_200)))
        api.placeOrderResponse = { refusedWithFixture(409, "order_place_409_total_changed.json") }
        model.placeOrder()
        settle()

        assertThat((model.state.value as FeastCheckoutState.Ready).bill.total).isEqualTo(Paise(47_200))
        assertThat(api.cartReads).isEqualTo(2)
    }

    @Test
    fun `a changed total after process death is still said, never paid`() = runTest(dispatcher) {
        val handle = SavedStateHandle()
        val first = readyModel(handle)
        api.placeOrderResponse = { gatewayTimeout() }
        first.placeOrder()
        settle()

        api.placeOrderResponse = { refusedWithFixture(409, "order_place_409_total_changed.json") }
        val restored = SavedStateHandle().also { copy -> handle.keys().forEach { copy[it] = handle.get<Any?>(it) } }
        val second = vm(restored)
        settle()

        val asked = second.state.value as FeastCheckoutState.Ready
        assertThat(asked.totalChanged?.now).isEqualTo(Paise(47_200))
        // The resend after process death carried the figure the lost request did.
        assertThat(api.placeOrderBodies.map { it.expectedTotalPaise }).containsExactly(Paise(64_912), Paise(64_912))
    }

    @Test
    fun `a new address prices a new bill, and that bill is what is sent`() = runTest(dispatcher) {
        val model = readyModel()
        api.placeOrderResponse = { refusedWithFixture(409, "order_place_409_total_changed.json") }
        model.placeOrder()
        settle()
        val office = pinnedAddress.copy(id = "addr-2", label = "Office", isDefault = false)
        api.addresses = listOf(pinnedAddress, office)
        api.cart = pricedCart().copy(totalsPaise = FeastTotalsPaiseDto(finalAmountPaise = Paise(68_000)))
        session.selectAddress(office)
        model.refreshAddress()
        settle()

        val ready = model.state.value as FeastCheckoutState.Ready
        assertThat(ready.totalChanged).isNull()
        assertThat(ready.shownTotal).isEqualTo(Paise(68_000))
    }

    @Test
    fun `the same total opens payment straight away`() = runTest(dispatcher) {
        val model = readyModel()
        api.order = api.order.copy(money = FeastOrderMoneyDto(FeastTotalsPaiseDto(finalAmountPaise = Paise(64_912))))
        model.placeOrder()
        settle()

        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.OpeningPayment::class.java)
    }

    @Test
    fun `a new address re-reads the cart so the bill is priced for it`() = runTest(dispatcher) {
        val model = readyModel()
        val office = pinnedAddress.copy(id = "addr-2", label = "Office", isDefault = false)
        api.addresses = listOf(pinnedAddress, office)
        api.cart = pricedCart().copy(totalsPaise = FeastTotalsPaiseDto(finalAmountPaise = Paise(68_000)))
        session.selectAddress(office)

        model.refreshAddress()
        settle()

        val ready = model.state.value as FeastCheckoutState.Ready
        assertThat(ready.address?.id).isEqualTo("addr-2")
        assertThat(ready.bill.total).isEqualTo(Paise(68_000))
        assertThat(api.cartReads).isEqualTo(2)
    }

    @Test
    fun `while the outcome is unknown the address is held too`() = runTest(dispatcher) {
        val model = readyModel()
        api.placeOrderResponse = { gatewayTimeout() }
        model.placeOrder()
        settle()
        // A real second address the customer picked: it exists, so reconciling keeps it.
        val office = pinnedAddress.copy(id = "addr-2", isDefault = false)
        api.addresses = listOf(pinnedAddress, office)
        session.selectAddress(office)

        model.refreshAddress()
        settle()

        assertThat((model.state.value as FeastCheckoutState.Ready).address?.id).isEqualTo("addr-1")
        api.placeOrderResponse = { ok(api.order) }
        model.placeOrder()
        settle()
        assertThat(api.placeOrderBodies.map { it.addressId }).containsExactly("addr-1", "addr-1")
    }
}
