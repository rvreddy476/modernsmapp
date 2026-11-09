package com.us.android.feature.kitchen.queue

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.notifications.NotificationUpdates
import com.us.android.core.testing.MainDispatcherRule
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test
import java.io.File

/**
 * The kitchen's order detail (food audit H2, L8): the order is read by id, so
 * the cook sees what to make BEFORE accepting, and a pickup-code lockout is
 * said plainly and stops the field.
 */
class OrderDetailViewModelTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val golden: PartnerOrderDto = Json { ignoreUnknownKeys = true }
        .decodeFromString(
            ApiEnvelope.serializer(PartnerOrderDto.serializer()),
            File("../../core/food/src/test/resources/contracts/partner_order_get_200.json").readText(),
        ).data!!

    private val kitchen = mockk<KitchenRepository>()
    private val args = SavedStateHandle(mapOf("restaurantId" to "rst_1", "orderId" to golden.id))

    private fun viewModel() = OrderDetailViewModel(args, kitchen, NotificationUpdates())

    @Test
    fun `the order is read by id and shows its items before it is accepted`() {
        coEvery { kitchen.order(golden.id) } returns FoodResult.Success(golden)

        val state = viewModel().state.value

        coVerify(exactly = 0) { kitchen.ordersIn(any(), any(), any()) }
        assertThat(state.loadError).isNull()
        assertThat(state.order?.status).isEqualTo("CONFIRMED")
        assertThat(state.order?.items?.map { it.name }).containsExactly("Paneer Tikka")
    }

    @Test
    fun `an order that is not this restaurant's says so`() {
        coEvery { kitchen.order(golden.id) } returns FoodResult.Failure(FoodError.NotFound)

        val state = viewModel().state.value

        assertThat(state.order).isNull()
        assertThat(state.loadError).contains("isn't one of this restaurant's")
    }

    @Test
    fun `a failed refresh keeps the order on screen`() {
        coEvery { kitchen.order(golden.id) } returnsMany listOf(
            FoodResult.Success(golden),
            FoodResult.Failure(FoodError.Network(null)),
        )
        val vm = viewModel()

        vm.load()

        assertThat(vm.state.value.order).isEqualTo(golden)
        assertThat(vm.state.value.loadError).isNull()
    }

    @Test
    fun `too many wrong pickup codes locks the field and points to support`() {
        coEvery { kitchen.order(golden.id) } returns FoodResult.Success(golden)
        coEvery { kitchen.verifyPickup(golden.id, "1234") } returns FoodResult.Failure(
            FoodError.Unexpected(429, "FOOD_PICKUP_CODE_ATTEMPTS_EXCEEDED", "too many wrong pickup codes"),
        )
        val vm = viewModel()
        vm.onPickupCode("1234")

        vm.verifyPickup()
        vm.verifyPickup()

        assertThat(vm.state.value.pickupLocked).isTrue()
        assertThat(vm.state.value.pickupError).contains("Contact Feast support")
        coVerify(exactly = 1) { kitchen.verifyPickup(golden.id, "1234") }
    }

    @Test
    fun `a wrong code is still just a wrong code`() {
        coEvery { kitchen.order(golden.id) } returns FoodResult.Success(golden)
        coEvery { kitchen.verifyPickup(golden.id, "9999") } returns FoodResult.Failure(
            FoodError.Unexpected(400, "PICKUP_VERIFY_FAILED", "pickup code mismatch"),
        )
        val vm = viewModel()
        vm.onPickupCode("9999")

        vm.verifyPickup()

        assertThat(vm.state.value.pickupLocked).isFalse()
        assertThat(vm.state.value.pickupError).contains("doesn't match")
    }
}
