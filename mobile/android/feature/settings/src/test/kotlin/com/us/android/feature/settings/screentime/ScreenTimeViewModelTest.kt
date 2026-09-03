package com.us.android.feature.settings.screentime

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.WellbeingApi
import com.us.android.core.profile.data.WellbeingGuardCache
import com.us.android.core.profile.data.WellbeingGuardSnapshot
import com.us.android.core.profile.data.WellbeingRepository
import com.us.android.core.profile.data.dto.ScreenTimeDayDto
import com.us.android.core.profile.data.dto.ScreenTimeReportRequest
import com.us.android.core.profile.data.dto.ScreenTimeWeekDto
import com.us.android.core.profile.data.dto.UpdateWellbeingRequest
import com.us.android.core.profile.data.dto.WellbeingDto
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Rule
import org.junit.Test

class ScreenTimeViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeApi : WellbeingApi {
        var loaded = WellbeingDto()
        var lastRequest: UpdateWellbeingRequest? = null

        override suspend fun wellbeing() = ApiEnvelope(loaded)

        override suspend fun updateWellbeing(body: UpdateWellbeingRequest): ApiEnvelope<WellbeingDto> {
            lastRequest = body
            loaded = loaded.copy(
                dailyLimitMins = body.dailyLimitMins.takeIf { it > 0 },
                bedtimeStart = (body.bedtimeStart as? JsonPrimitive)?.content,
                bedtimeEnd = (body.bedtimeEnd as? JsonPrimitive)?.content,
            )
            return ApiEnvelope(loaded)
        }

        override suspend fun reportScreenTime(body: ScreenTimeReportRequest): ApiEnvelope<ScreenTimeDayDto> {
            val minutes = (body.foregroundSecs / SECONDS_PER_MINUTE).toInt()
            return ApiEnvelope(ScreenTimeDayDto(body.date, minutes, body.sessions))
        }

        override suspend fun screenTime(range: String) = ApiEnvelope(
            ScreenTimeWeekDto(range, emptyList(), todayMinutes = TODAY_MINUTES, dailyLimitMins = loaded.dailyLimitMins),
        )

        private companion object {
            const val SECONDS_PER_MINUTE = 60
            const val TODAY_MINUTES = 12
        }
    }

    private class FakeGuardCache : WellbeingGuardCache {
        var stored: WellbeingGuardSnapshot? = null
        override suspend fun read() = stored
        override suspend fun write(snapshot: WellbeingGuardSnapshot) {
            stored = snapshot
        }
    }

    private fun TestScope.buildViewModel(api: FakeApi = FakeApi()) =
        ScreenTimeViewModel(WellbeingRepository(api, ErrorMapper(json), FakeGuardCache(), backgroundScope)) to api

    private fun ScreenTimeViewModel.editing() = state.value as ScreenTimeUiState.Editing

    @Test
    fun `loads settings and the week summary together`() = runTest {
        val api = FakeApi().apply { loaded = loaded.copy(dailyLimitMins = 60) }
        val (viewModel, _) = buildViewModel(api)

        val editing = viewModel.editing()
        assertThat(editing.value.dailyLimitMins).isEqualTo(60)
        assertThat(editing.week?.todayMinutes).isEqualTo(12)
    }

    @Test
    fun `setting the daily limit to Off sends zero, which the domain treats as no limit`() = runTest {
        // Starts from a non-null limit so turning it off is actually a
        // change from the server's original value — otherwise `save()`
        // correctly no-ops because nothing is dirty.
        val api = FakeApi().apply { loaded = loaded.copy(dailyLimitMins = 60) }
        val (viewModel, _) = buildViewModel(api)

        viewModel.setDailyLimit(null)
        viewModel.save()

        assertThat(api.lastRequest?.dailyLimitMins).isEqualTo(0)
        assertThat(viewModel.editing().value.dailyLimitMins).isNull()
    }

    @Test
    fun `both bedtime fields set together is valid`() = runTest {
        val (viewModel, _) = buildViewModel()

        viewModel.setSleepHours(start = "23:00", end = "07:00")

        assertThat(viewModel.editing().sleepHoursValid).isTrue()
    }

    @Test
    fun `an asymmetric bedtime pair — one set, one still null — is invalid`() = runTest {
        val (viewModel, _) = buildViewModel()
        val editing = viewModel.editing()

        val asymmetric = editing.copy(value = editing.value.copy(bedtimeStart = "23:00", bedtimeEnd = null))

        assertThat(asymmetric.sleepHoursValid).isFalse()
    }

    @Test
    fun `sleep hours off encodes as explicit nulls on save`() = runTest {
        // Starts with sleep hours already set on the server, so turning them
        // off is a real change from the original value.
        val api = FakeApi().apply { loaded = loaded.copy(bedtimeStart = "22:00", bedtimeEnd = "06:00") }
        val (viewModel, _) = buildViewModel(api)

        viewModel.turnOffSleepHours()
        viewModel.save()

        assertThat(api.lastRequest?.bedtimeStart).isEqualTo(JsonNull)
        assertThat(api.lastRequest?.bedtimeEnd).isEqualTo(JsonNull)
    }

    @Test
    fun `setting both bedtime fields together round-trips through save`() = runTest {
        val (viewModel, api) = buildViewModel()

        viewModel.setSleepHours(start = "23:00", end = "07:00")
        viewModel.save()

        assertThat((api.lastRequest?.bedtimeStart as? JsonPrimitive)?.content).isEqualTo("23:00")
        assertThat((api.lastRequest?.bedtimeEnd as? JsonPrimitive)?.content).isEqualTo("07:00")
        assertThat(viewModel.editing().dirty).isFalse()
    }

    @Test
    fun `save adopts the result into the repository's shared guard snapshot`() = runTest {
        // ScreenTimeGuardCoordinator reads WellbeingRepository.guardSnapshot
        // rather than polling the network itself, so a save here must be
        // visible there immediately — this is what makes that true.
        val api = FakeApi().apply { loaded = loaded.copy(dailyLimitMins = 60) }
        val cache = FakeGuardCache()
        val repository = WellbeingRepository(api, ErrorMapper(json), cache, backgroundScope)
        val viewModel = ScreenTimeViewModel(repository)

        viewModel.setDailyLimit(90)
        viewModel.save()

        assertThat(repository.guardSnapshot.value?.dailyLimitMins).isEqualTo(90)
        assertThat(cache.stored?.dailyLimitMins).isEqualTo(90)
    }
}
