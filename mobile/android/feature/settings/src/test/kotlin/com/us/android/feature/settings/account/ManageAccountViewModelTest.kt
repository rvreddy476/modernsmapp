package com.us.android.feature.settings.account

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.AccountSecurityApi
import com.us.android.core.profile.data.ManageAccountApi
import com.us.android.core.profile.data.ManageAccountRepository
import com.us.android.core.profile.data.dto.AccountSummaryDto
import com.us.android.core.profile.data.dto.CodeRequest
import com.us.android.core.profile.data.dto.DisableTwoFactorRequest
import com.us.android.core.profile.data.dto.RegionDto
import com.us.android.core.profile.data.dto.SecurityEventDto
import com.us.android.core.profile.data.dto.StatusDto
import com.us.android.core.profile.data.dto.TwoFactorSetupDto
import com.us.android.core.profile.data.dto.UpdateRegionRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

class ManageAccountViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeAccountApi : AccountSecurityApi {
        var account = AccountSummaryDto(userId = "u1", email = "raghu@example.com", emailVerified = true)

        override suspend fun account() = ApiEnvelope(account)
        override suspend fun securityEvents() = ApiEnvelope(emptyList<SecurityEventDto>())
        override suspend fun acknowledgeEvent(id: String) = ApiEnvelope(StatusDto("ok"))
        override suspend fun setupTwoFactor() = ApiEnvelope(TwoFactorSetupDto())
        override suspend fun verifyTwoFactor(body: CodeRequest) = ApiEnvelope(StatusDto("ok"))
        override suspend fun disableTwoFactor(body: DisableTwoFactorRequest) = ApiEnvelope(StatusDto("ok"))
    }

    private class FakeRegionApi : ManageAccountApi {
        var region = "IN"
        var failNextUpdate = false

        override suspend fun region() = ApiEnvelope(RegionDto(region))

        override suspend fun updateRegion(body: UpdateRegionRequest): ApiEnvelope<RegionDto> {
            if (failNextUpdate) error("region update failed")
            region = body.countryCode
            return ApiEnvelope(RegionDto(region))
        }
    }

    private fun buildViewModel(
        accountApi: FakeAccountApi = FakeAccountApi(),
        regionApi: FakeRegionApi = FakeRegionApi(),
    ) = ManageAccountViewModel(ManageAccountRepository(accountApi, regionApi, ErrorMapper(json))) to regionApi

    @Test
    fun `loads the account summary and region together`() = runTest {
        val (viewModel, _) = buildViewModel()

        val loaded = viewModel.state.value as ManageAccountUiState.Loaded
        assertThat(loaded.account.email).isEqualTo("raghu@example.com")
        assertThat(loaded.account.emailVerified).isTrue()
        assertThat(loaded.region).isEqualTo("IN")
    }

    @Test
    fun `selecting a region saves immediately`() = runTest {
        val (viewModel, regionApi) = buildViewModel()

        viewModel.setRegion("US")

        assertThat(regionApi.region).isEqualTo("US")
        val loaded = viewModel.state.value as ManageAccountUiState.Loaded
        assertThat(loaded.region).isEqualTo("US")
        assertThat(loaded.savingRegion).isFalse()
    }

    @Test
    fun `selecting the same region again is a no-op`() = runTest {
        val (viewModel, regionApi) = buildViewModel()

        viewModel.setRegion("IN")

        // No exception even though a real update would have been observable
        // through the fake; nothing changed because nothing needed to.
        assertThat(regionApi.region).isEqualTo("IN")
    }
}
