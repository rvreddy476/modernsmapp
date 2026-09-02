package com.us.android.feature.settings.onboarding

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.AppModule
import com.us.android.core.profile.data.ModulePreferences
import com.us.android.core.profile.data.ModulePreferencesApi
import com.us.android.core.profile.data.ModulePreferencesCache
import com.us.android.core.profile.data.ModulePreferencesRepository
import com.us.android.core.profile.data.ModulePrefsState
import com.us.android.core.profile.data.dto.ModulePreferencesDto
import com.us.android.core.profile.data.dto.UpdateModulePreferencesRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

class OnboardingViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    /** Echoes the last PUT as the server would, stamping onboarding complete. */
    private class FakeApi(
        var current: ModulePreferencesDto = ModulePreferencesDto(modules = AppModule.selectable.map { it.id }),
        var failWrites: Boolean = false,
    ) : ModulePreferencesApi {
        val writes = mutableListOf<UpdateModulePreferencesRequest>()

        override suspend fun modules() = ApiEnvelope(current)

        override suspend fun updateModules(body: UpdateModulePreferencesRequest): ApiEnvelope<ModulePreferencesDto> {
            writes += body
            if (failWrites) return ApiEnvelope(error = ApiErrorBody(code = "INVALID_MODULE"))
            current = ModulePreferencesDto(
                modules = body.modules,
                homeModule = body.homeModule,
                onboardingCompletedAt = if (body.completeOnboarding) "2026-09-02T10:00:00Z" else null,
            )
            return ApiEnvelope(current)
        }
    }

    private class FakeCache : ModulePreferencesCache {
        var stored: ModulePreferences? = null
        override suspend fun read() = stored
        override suspend fun write(prefs: ModulePreferences) {
            stored = prefs
        }
        override suspend fun clear() {
            stored = null
        }
    }

    private suspend fun TestScope.repository(api: FakeApi, refreshed: Boolean = true): ModulePreferencesRepository {
        val repository = ModulePreferencesRepository(
            api,
            FakeCache(),
            ErrorMapper(Json { ignoreUnknownKeys = true }),
            backgroundScope,
        )
        if (refreshed) repository.refresh()
        return repository
    }

    private fun editing(viewModel: OnboardingViewModel) = viewModel.state.value as OnboardingUiState.Editing

    @Test
    fun `seeds from the loaded preferences with everything on and the feed first`() = runTest {
        val viewModel = OnboardingViewModel(repository(FakeApi()))

        val state = editing(viewModel)
        assertThat(state.value.modules).containsExactlyElementsIn(AppModule.selectable)
        assertThat(state.value.homeModule).isEqualTo(AppModule.FEED)
        assertThat(state.dirty).isFalse()
    }

    @Test
    fun `refreshes first when nothing is loaded yet`() = runTest {
        val api = FakeApi(current = ModulePreferencesDto(modules = listOf("chat"), homeModule = "chat"))

        val viewModel = OnboardingViewModel(repository(api, refreshed = false))

        assertThat(editing(viewModel).value.modules).containsExactly(AppModule.CHAT)
        assertThat(editing(viewModel).value.homeModule).isEqualTo(AppModule.CHAT)
    }

    @Test
    fun `switching off the home module sends the home back to the feed`() = runTest {
        val viewModel = OnboardingViewModel(repository(FakeApi()))
        viewModel.selectHome(AppModule.REELS)
        assertThat(editing(viewModel).value.homeModule).isEqualTo(AppModule.REELS)

        viewModel.toggleModule(AppModule.REELS, enabled = false)

        assertThat(editing(viewModel).value.homeModule).isEqualTo(AppModule.FEED)
        assertThat(editing(viewModel).value.homeCandidates).doesNotContain(AppModule.REELS)
    }

    @Test
    fun `a module that is off cannot become the home`() = runTest {
        val viewModel = OnboardingViewModel(repository(FakeApi()))
        viewModel.toggleModule(AppModule.CHAT, enabled = false)

        viewModel.selectHome(AppModule.CHAT)

        assertThat(editing(viewModel).value.homeModule).isEqualTo(AppModule.FEED)
    }

    @Test
    fun `save completes onboarding and adopts the server's echo`() = runTest {
        val api = FakeApi()
        val repository = repository(api)
        val viewModel = OnboardingViewModel(repository)
        viewModel.toggleModule(AppModule.DATING, enabled = false)
        viewModel.selectHome(AppModule.REELS)

        viewModel.save()

        val write = api.writes.single()
        assertThat(write.completeOnboarding).isTrue()
        assertThat(write.homeModule).isEqualTo("reels")
        assertThat(write.modules).doesNotContain("dating")
        assertThat(write.modules).doesNotContain("feed")

        val state = editing(viewModel)
        assertThat(state.saved).isTrue()
        assertThat(state.saving).isFalse()
        assertThat(state.dirty).isFalse()
        val loaded = repository.state.value as ModulePrefsState.Loaded
        assertThat(loaded.prefs.onboardingCompleted).isTrue()
        assertThat(loaded.prefs.homeModule).isEqualTo(AppModule.REELS)
    }

    @Test
    fun `a failed save keeps the draft and reports inline`() = runTest {
        val api = FakeApi(failWrites = true)
        val repository = repository(api)
        val viewModel = OnboardingViewModel(repository)
        viewModel.selectHome(AppModule.CHAT)

        viewModel.save()

        val state = editing(viewModel)
        assertThat(state.saved).isFalse()
        assertThat(state.saving).isFalse()
        assertThat(state.message).isNotNull()
        assertThat(state.value.homeModule).isEqualTo(AppModule.CHAT)
        val loaded = repository.state.value as ModulePrefsState.Loaded
        assertThat(loaded.prefs.onboardingCompleted).isFalse()
    }
}
