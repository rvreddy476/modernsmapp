package com.us.android.feature.rider.job

import androidx.lifecycle.ViewModelStore
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.food.network.VerifyDeliveryDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.rider.location.RiderDuty
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/**
 * The end of a delivery when answers are lost (food audit L6), and the job
 * screen's poll running only while it is shown (L5).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ActiveJobDeliveryTest {
    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val rider = mockk<RiderRepository>()
    private val job = routeAssignment().copy(status = "PICKED_UP", orderStatus = "OUT_FOR_DELIVERY")
    private val lost = FoodResult.Failure(FoodError.Network(IOException("read timed out")))

    private fun model(store: ViewModelStore): ActiveJobViewModel {
        coEvery { rider.currentAssignment() } returns FoodResult.Success(job)
        coEvery { rider.batch(any()) } returns FoodResult.Success(null)
        coEvery { rider.assignments() } returns FoodResult.Success(listOf(job))
        return ActiveJobViewModel(rider, RiderDuty()).also { store.put("job", it) }
    }

    @Test
    fun `a delivery code whose answer is lost is resent with the same code`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            coEvery { rider.verifyDelivery(job.id, "7390") } returnsMany listOf(
                lost,
                FoodResult.Success(
                    VerifyDeliveryDto(assignmentId = job.id, orderId = job.orderId, status = "delivered"),
                ),
            )
            vm.onCode("7390")

            vm.verifyCode()
            advanceTimeBy(2_000)
            runCurrent()

            coVerify(exactly = 2) { rider.verifyDelivery(job.id, "7390") }
            assertThat(vm.state.value.message?.text).isEqualTo("Delivered. Nice work.")
            assertThat(vm.finishedEvents.first()).isEqualTo(Unit)
        } finally { store.clear() }
    }

    @Test
    fun `a definite refusal is not resent`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            coEvery { rider.verifyDelivery(job.id, "1111") } returns FoodResult.Failure(
                FoodError.InvalidField("FOOD_DELIVERY_CODE_INVALID", "code", "delivery code does not match"),
            )
            vm.onCode("1111")

            vm.verifyCode()
            advanceTimeBy(5_000)
            runCurrent()

            coVerify(exactly = 1) { rider.verifyDelivery(job.id, "1111") }
            assertThat(vm.state.value.codeError).contains("doesn't match")
        } finally { store.clear() }
    }

    @Test
    fun `a job that vanishes after a lost answer is found delivered in history`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            coEvery { rider.currentAssignment() } returns FoodResult.Success(null)
            coEvery { rider.history() } returns FoodResult.Success(listOf(job.copy(status = "DELIVERED")))

            advanceTimeBy(10_001)
            runCurrent()

            assertThat(vm.state.value.message?.text).isEqualTo("Delivered. Nice work.")
            assertThat(vm.state.value.message?.type).isEqualTo(UsMessageType.Success)
            assertThat(vm.finishedEvents.first()).isEqualTo(Unit)
        } finally { store.clear() }
    }

    @Test
    fun `a job that vanishes without being delivered says it is no longer assigned`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            coEvery { rider.currentAssignment() } returns FoodResult.Success(null)
            coEvery { rider.history() } returns FoodResult.Success(listOf(job.copy(status = "CANCELLED")))

            advanceTimeBy(10_001)
            runCurrent()

            assertThat(vm.state.value.message?.text).isEqualTo("This job is no longer assigned to you.")
        } finally { store.clear() }
    }

    @Test
    fun `nothing polls while the job screen is not shown`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            model(store)
            runCurrent()

            advanceTimeBy(60_000)
            runCurrent()

            coVerify(exactly = 1) { rider.currentAssignment() }
        } finally { store.clear() }
    }
}
