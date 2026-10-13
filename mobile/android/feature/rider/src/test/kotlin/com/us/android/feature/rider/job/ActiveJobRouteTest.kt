package com.us.android.feature.rider.job

import androidx.lifecycle.ViewModelStore
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.BatchRoutePreviewDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderAssignmentStep
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.rider.location.RiderDuty
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class ActiveJobRouteTest {
    private val dispatcher = StandardTestDispatcher()
    @get:Rule val main = MainDispatcherRule(dispatcher)
    private val rider = mockk<RiderRepository>()
    private val assignment = routeAssignment()
    private val batch = routeBatch(assignment)

    private fun model(store: ViewModelStore): ActiveJobViewModel {
        coEvery { rider.currentAssignment() } returns FoodResult.Success(assignment)
        coEvery { rider.batch(assignment.orderId) } returns FoodResult.Success(batch)
        coEvery { rider.assignments() } returns FoodResult.Success(listOf(assignment, sibling()))
        return ActiveJobViewModel(rider, RiderDuty()).also { store.put("job", it) }
    }

    private fun sibling() = assignment.copy(id = "sibling-assignment", orderId = "sibling-order", orderNumber = "FG-sibling")

    @Test
    fun `remaining batch orders require an explicit confirm tap before suggestions`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            coEvery { rider.assignments() } returns FoodResult.Success(listOf(assignment, sibling().copy(status = "ASSIGNED", drop = null)))
            runCurrent()
            vm.suggestRoute()
            runCurrent()
            coVerify(exactly = 0) { rider.previewBatchRoute(any()) }
            coVerify(exactly = 0) { rider.step(any(), any(), any()) }
            coEvery { rider.step("sibling-assignment", RiderAssignmentStep.ACCEPT, any()) } returns FoodResult.Success(sibling())
            coEvery { rider.assignments() } returns FoodResult.Success(listOf(assignment, sibling()))
            vm.confirmBatchOrder("sibling-order")
            runCurrent()
            coVerify(exactly = 1) { rider.step("sibling-assignment", RiderAssignmentStep.ACCEPT, any()) }
            assertThat(BatchRouteRules.ready(vm.state.value.assignment, vm.state.value.batch, vm.state.value.batchJobs)).isTrue()
            assertThat(vm.state.value.assignment).isEqualTo(assignment)
        } finally { store.clear() }
    }

    @Test
    fun `confirm refuses orders outside the callers batch`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            runCurrent()
            vm.confirmBatchOrder("unknown")
            runCurrent()
            coVerify(exactly = 0) { rider.step(any(), any(), any()) }
        } finally { store.clear() }
    }

    @Test
    fun `network retry of explicit confirmation reuses its idempotency key`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            coEvery { rider.assignments() } returns FoodResult.Success(listOf(assignment, sibling().copy(status = "ASSIGNED", drop = null)))
            val keys = mutableListOf<String>()
            coEvery { rider.step("sibling-assignment", RiderAssignmentStep.ACCEPT, capture(keys)) } returns FoodResult.Failure(FoodError.Network(null))
            runCurrent()
            repeat(2) { vm.confirmBatchOrder("sibling-order"); runCurrent() }
            assertThat(keys).hasSize(2)
            assertThat(keys[0]).isEqualTo(keys[1])
        } finally { store.clear() }
    }

    @Test
    fun `solo orders never request an optimization`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            coEvery { rider.batch(assignment.orderId) } returns FoodResult.Success(null)
            runCurrent()
            vm.suggestRoute()
            runCurrent()
            assertThat(vm.state.value.batch).isNull()
            coVerify(exactly = 0) { rider.previewBatchRoute(any()) }
            coVerify(exactly = 0) { rider.assignments() }
        } finally { store.clear() }
    }

    @Test
    fun `invalid or incomplete Google result is never rendered`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            coEvery { rider.previewBatchRoute(assignment.orderId) } returns FoodResult.Success(routeSuggestion(batch).copy(orderIds = listOf("unknown")))
            runCurrent()
            vm.suggestRoute()
            runCurrent()
            assertThat(vm.state.value.routePreview).isNull()
            assertThat(vm.state.value.routeError).contains("couldn't verify")
        } finally { store.clear() }
    }

    @Test
    fun `batch changing while preview is pending discards the result`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            val reply = CompletableDeferred<FoodResult<BatchRoutePreviewDto>>()
            coEvery { rider.previewBatchRoute(assignment.orderId) } coAnswers { reply.await() }
            runCurrent()
            vm.suggestRoute()
            runCurrent()
            coEvery { rider.batch(assignment.orderId) } returns FoodResult.Success(batch.copy(id = "replacement-batch"))
            advanceTimeBy(10_000)
            runCurrent()
            reply.complete(FoodResult.Success(routeSuggestion(batch)))
            runCurrent()
            assertThat(vm.state.value.routePreview).isNull()
            assertThat(vm.state.value.routeLoading).isFalse()
            assertThat(vm.state.value.batch?.id).isEqualTo("replacement-batch")
        } finally { store.clear() }
    }

    @Test
    fun `tap sends one request displays server order and never mutates assignments`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            val reply = CompletableDeferred<FoodResult<BatchRoutePreviewDto>>()
            coEvery { rider.previewBatchRoute(assignment.orderId) } coAnswers { reply.await() }
            runCurrent()
            vm.suggestRoute()
            vm.suggestRoute()
            runCurrent()
            assertThat(vm.state.value.routeLoading).isTrue()
            reply.complete(FoodResult.Success(routeSuggestion(batch)))
            runCurrent()
            assertThat(vm.state.value.routePreview?.orderIds).containsExactlyElementsIn(routeSuggestion(batch).orderIds).inOrder()
            assertThat(vm.state.value.batch).isEqualTo(batch)
            assertThat(vm.state.value.assignment).isEqualTo(assignment)
            coVerify(exactly = 1) { rider.previewBatchRoute(assignment.orderId) }
            coVerify(exactly = 0) { rider.step(any(), any(), any()) }
        } finally { store.clear() }
    }

    @Test
    fun `pickup while Google is pending discards the result and hides the button`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            val reply = CompletableDeferred<FoodResult<BatchRoutePreviewDto>>()
            coEvery { rider.previewBatchRoute(assignment.orderId) } coAnswers { reply.await() }
            runCurrent()
            vm.suggestRoute()
            runCurrent()
            coEvery { rider.currentAssignment() } returns FoodResult.Success(assignment.copy(status = "PICKED_UP", orderStatus = "OUT_FOR_DELIVERY"))
            advanceTimeBy(10_000)
            runCurrent()
            reply.complete(FoodResult.Success(routeSuggestion(batch)))
            runCurrent()
            assertThat(vm.state.value.routePreview).isNull()
            assertThat(vm.state.value.batch).isNull()
            assertThat(vm.state.value.routeLoading).isFalse()
        } finally { store.clear() }
    }

    @Test
    fun `revoked batch access on reread discards the suggestion`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            runCurrent()
            coEvery { rider.previewBatchRoute(assignment.orderId) } returns FoodResult.Success(routeSuggestion(batch))
            coEvery { rider.batch(assignment.orderId) } returns FoodResult.Success(null)
            vm.suggestRoute()
            runCurrent()
            assertThat(vm.state.value.routePreview).isNull()
            assertThat(vm.state.value.batch).isNull()
        } finally { store.clear() }
    }

    @Test
    fun `unavailable response preserves saved sequence and permits retry`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            coEvery { rider.previewBatchRoute(assignment.orderId) } returns FoodResult.Failure(
                FoodError.Unexpected(503, "BATCH_ROUTE_UNAVAILABLE", "private internals"),
            )
            runCurrent()
            vm.suggestRoute()
            runCurrent()
            assertThat(vm.state.value.routeLoading).isFalse()
            assertThat(vm.state.value.routeError).contains("saved stop order")
            assertThat(vm.state.value.batch).isEqualTo(batch)
            coEvery { rider.previewBatchRoute(assignment.orderId) } returns FoodResult.Success(routeSuggestion(batch))
            vm.suggestRoute()
            runCurrent()
            assertThat(vm.state.value.routePreview).isNotNull()
            assertThat(vm.state.value.routeError).isNull()
        } finally { store.clear() }
    }

    @Test
    fun `suggestion expires after five minutes even when the batch is unchanged`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store)
            coEvery { rider.previewBatchRoute(assignment.orderId) } returns FoodResult.Success(routeSuggestion(batch))
            runCurrent()
            vm.suggestRoute()
            runCurrent()
            advanceTimeBy(300_000)
            runCurrent()
            assertThat(vm.state.value.routePreview).isNull()
            assertThat(vm.state.value.routeError).contains("expired")
        } finally { store.clear() }
    }
}
