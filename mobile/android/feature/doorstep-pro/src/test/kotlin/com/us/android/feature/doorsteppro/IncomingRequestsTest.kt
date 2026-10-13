package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.DoorstepOfferAlertCoordinator
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.doorsteppro.data.OfferDto
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.di.ProEventStream
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.offers.IncomingRequestsViewModel
import com.us.android.feature.doorsteppro.offers.openRequests
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.time.Instant

class IncomingRequestsTest {
    @get:Rule val main = MainDispatcherRule()
    private var now = Instant.parse("2026-10-10T10:00:00Z")
    private val repo = FakeProRepository()
    private fun offer(id: String = "offer", seconds: Long = 30) = OfferDto(
        id = id, bookingId = "booking", serviceName = "AC service", categorySlug = "ac",
        locality = "Uppal", distanceM = 1000, slotStart = now.plusSeconds(3600).toString(),
        slotEnd = now.plusSeconds(7200).toString(), earningEstimatePaise = 50000,
        expiresAt = now.plusSeconds(seconds).toString(),
    )
    private fun vm() = IncomingRequestsViewModel(repo, ProEventStream { _, _ -> emptyFlow() }, ProClock { now }, DoorstepOfferAlertCoordinator())

    @Test fun `only live unique requests can ring in earliest expiry order`() {
        val first = offer("first", 10)
        val later = offer("later", 30)
        assertThat(openRequests(listOf(later, first, first, offer("expired", 0), offer("closed").copy(status = "accepted"), offer("bad").copy(expiresAt = "bad")), now))
            .containsExactly(first, later).inOrder()
    }

    @Test fun `refresh never restarts deadline and expired requests cannot be accepted`() = runTest {
        repo.offersResult = ProResult.Success(listOf(offer()))
        val vm = vm()
        vm.refresh()
        now = now.plusSeconds(30)
        vm.respond(true)
        assertThat(repo.calls).isEmpty()
        vm.refresh()
        assertThat(vm.state.value.incoming).isNull()
    }

    @Test fun `duplicate server delivery does not re-ring answered or muted request`() = runTest {
        repo.offersResult = ProResult.Success(listOf(offer()))
        val vm = vm()
        vm.refresh()
        vm.dismiss()
        vm.refresh()
        assertThat(vm.state.value.incoming).isNull()
        assertThat(vm.state.value.offers).hasSize(1)
        vm.reset()
        vm.refresh()
        vm.respond(false)
        assertThat(repo.calls).containsExactly("decline:offer")
        assertThat(vm.state.value.offers).isEmpty()
        vm.refresh()
        assertThat(vm.state.value.incoming).isNull()
    }

    @Test fun `successful acceptance opens job only after server confirms`() = runTest {
        repo.offersResult = ProResult.Success(listOf(offer()))
        repo.acceptOfferResult = ProResult.Success(Jobs.job(bookingId = "booking"))
        val vm = vm()
        vm.refresh()
        vm.respond(true)
        assertThat(vm.state.value.acceptedBookingId).isEqualTo("booking")
        assertThat(vm.state.value.offers).isEmpty()
        vm.reset()
        assertThat(vm.state.value.acceptedBookingId).isNull()
    }

    @Test fun `failed acceptance stays unassigned and shows error`() = runTest {
        repo.offersResult = ProResult.Success(listOf(offer()))
        repo.acceptOfferResult = ProResult.Failure(ProError.NotFound)
        val vm = vm()
        vm.refresh()
        vm.respond(true)
        assertThat(vm.state.value.acceptedBookingId).isNull()
        assertThat(vm.state.value.error).isNotNull()
        assertThat(vm.state.value.responding).isFalse()
    }
}
