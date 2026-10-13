package com.us.android.feature.doorsteppro.root

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.AuthRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.doorsteppro.data.*
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinkBus
import com.us.android.feature.doorsteppro.location.ProDuty
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import io.mockk.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.*
import kotlinx.serialization.json.Json
import org.junit.After
import org.junit.Before
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class PartnerApprovalTest {
    private val dispatcher = StandardTestDispatcher()
    private val repository = mockk<DoorstepProRepository>()
    private val fixture: ProfessionalDto by lazy {
        val raw = checkNotNull(javaClass.classLoader?.getResource("contracts/pro_application_post_201.json")).readText()
        checkNotNull(Json { ignoreUnknownKeys = true }.decodeFromString<ApiEnvelope<ProfessionalDto>>(raw).data)
    }
    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }
    private fun viewModel() = ProRootViewModel(repository, mockk<AuthRepository>(relaxed = true), mockk<ProDuty>(relaxed = true),
        mockk<OnboardingMemory>(relaxed = true), ProDeepLinkBus(), ProSession())

    @Test fun approvedApplicationOpensWorkNotOldReceipt() {
        assertThat(fixture.application).isNotNull()
        val state = rootStateFor(fixture.copy(status = "approved"))
        assertThat(state).isInstanceOf(ProRootState.Ready::class.java)
        assertThat((state as ProRootState.Ready).onboarded).isTrue()
    }
    @Test fun pendingRemainsPendingAndNeverAutoApproves() {
        assertThat(rootStateFor(fixture.copy(status = "pending_verification"))).isInstanceOf(ProRootState.Submitted::class.java)
        val restricted = rootStateFor(fixture.copy(status = "suspended")) as ProRootState.Ready
        assertThat(restricted.professional.status).isEqualTo("suspended")
    }
    @Test fun returningToAppRefreshesPendingToApproved() = runTest(dispatcher) {
        coEvery { repository.me() } returns ProResult.Success(fixture.copy(status = "pending_verification"))
        val vm = viewModel()
        runCurrent()
        assertThat(vm.state.value).isInstanceOf(ProRootState.Submitted::class.java)
        coEvery { repository.me() } returns ProResult.Success(fixture.copy(status = "approved"))
        vm.refreshStatus()
        // Keep the content mounted while the request is in flight.
        assertThat(vm.state.value).isInstanceOf(ProRootState.Submitted::class.java)
        runCurrent()
        assertThat((vm.state.value as ProRootState.Ready).professional.status).isEqualTo("approved")
        assertThat(vm.refreshing.value).isFalse()
    }
    @Test fun refreshNeverDiscardsAnApplicationBeingEdited() = runTest(dispatcher) {
        coEvery { repository.me() } returns ProResult.Success(fixture.copy(status = "draft", application = null))
        val vm = viewModel()
        runCurrent()
        vm.refreshStatus()
        runCurrent()
        assertThat(vm.state.value).isEqualTo(ProRootState.Applying)
        coVerify(exactly = 1) { repository.me() }
    }

    @Test fun networkFailureDoesNotTurnApprovedAccountIntoPending() = runTest(dispatcher) {
        coEvery { repository.me() } returns ProResult.Success(fixture.copy(status = "approved"))
        val vm = viewModel()
        runCurrent()
        coEvery { repository.me() } returns ProResult.Failure(ProError.Network(null))
        vm.refreshStatus()
        runCurrent()
        assertThat((vm.state.value as ProRootState.Ready).professional.status).isEqualTo("approved")
        assertThat(vm.refreshError.value).isNotNull()
        assertThat(vm.refreshing.value).isFalse()
    }
}
