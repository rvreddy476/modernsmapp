package com.us.android.navigation

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.call.CallSessionManager
import com.us.android.core.call.CallState
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.model.SessionState
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
import com.us.android.push.PushDestinations
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * The shell-state matrix, driven through the real repository with a fake
 * endpoint and cache: what the graph shows for each (session, preferences)
 * pair, and that the session drives the refresh and the clear.
 */
class MainViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val signedIn = SessionState.Authenticated(userId = "u1", sessionId = "s1")
    private val completed = ModulePreferencesDto(
        modules = listOf("reels", "chat"),
        homeModule = "reels",
        onboardingCompletedAt = "2026-09-02T10:00:00Z",
    )
    private val fresh = ModulePreferencesDto(modules = AppModule.selectable.map { it.id })

    private class FakeSession(initial: SessionState) : SessionStateProvider {
        override val sessionState = MutableStateFlow(initial)
    }

    private class FakeApi(var answer: ApiEnvelope<ModulePreferencesDto>) : ModulePreferencesApi {
        var gets = 0
        override suspend fun modules() = answer.also { gets++ }
        override suspend fun updateModules(body: UpdateModulePreferencesRequest) = answer
    }

    private class FakeCache(var stored: ModulePreferences? = null) : ModulePreferencesCache {
        var cleared = 0
        override suspend fun read() = stored
        override suspend fun write(prefs: ModulePreferences) {
            stored = prefs
        }
        override suspend fun clear() {
            stored = null
            cleared++
        }
    }

    private val failure = ApiEnvelope<ModulePreferencesDto>(error = ApiErrorBody(code = "UNAVAILABLE"))

    @Test
    fun `signed out is Unauthenticated regardless of preferences`() = runTest {
        val (viewModel, _) = build(FakeSession(SessionState.Unauthenticated), FakeApi(ApiEnvelope(completed)))

        assertThat(viewModel.shellState.value).isEqualTo(ShellState.Unauthenticated)
    }

    @Test
    fun `a pending second factor is not a session`() = runTest {
        val (viewModel, _) = build(FakeSession(SessionState.PendingTwoFactor("t")), FakeApi(ApiEnvelope(completed)))

        assertThat(viewModel.shellState.value).isEqualTo(ShellState.Unauthenticated)
    }

    @Test
    fun `signed in with completed onboarding is Ready with the server's choices`() = runTest {
        val (viewModel, _) = build(FakeSession(signedIn), FakeApi(ApiEnvelope(completed)))

        val ready = viewModel.shellState.value as ShellState.Ready
        assertThat(ready.prefs.modules).containsExactly(AppModule.REELS, AppModule.CHAT)
        assertThat(ready.prefs.homeModule).isEqualTo(AppModule.REELS)
    }

    @Test
    fun `signed in without onboarding needs onboarding`() = runTest {
        val (viewModel, _) = build(FakeSession(signedIn), FakeApi(ApiEnvelope(fresh)))

        assertThat(viewModel.shellState.value).isEqualTo(ShellState.NeedsOnboarding)
    }

    @Test
    fun `an unreachable endpoint with no cache is Ready with defaults`() = runTest {
        val (viewModel, _) = build(FakeSession(signedIn), FakeApi(failure))

        assertThat(viewModel.shellState.value).isEqualTo(ShellState.Ready(ModulePreferences.DEFAULT))
    }

    @Test
    fun `a cached answer survives an unreachable endpoint`() = runTest {
        val cached = ModulePreferences(setOf(AppModule.CHAT), AppModule.CHAT, onboardingCompleted = true)

        val (viewModel, _) = build(FakeSession(signedIn), FakeApi(failure), FakeCache(cached))

        assertThat(viewModel.shellState.value).isEqualTo(ShellState.Ready(cached))
    }

    @Test
    fun `signing in refreshes and signing out clears`() = runTest {
        val session = FakeSession(SessionState.Unauthenticated)
        val api = FakeApi(ApiEnvelope(completed))
        val cache = FakeCache()
        val (viewModel, repository) = build(session, api, cache)
        assertThat(api.gets).isEqualTo(0)

        session.sessionState.value = signedIn
        assertThat(api.gets).isEqualTo(1)
        assertThat(viewModel.shellState.value).isInstanceOf(ShellState.Ready::class.java)
        assertThat(cache.stored).isNotNull()

        session.sessionState.value = SessionState.Unauthenticated
        assertThat(viewModel.shellState.value).isEqualTo(ShellState.Unauthenticated)
        assertThat(repository.state.value).isEqualTo(ModulePrefsState.Unknown)
        assertThat(cache.stored).isNull()
    }

    @Test
    fun `the pure gate answers Loading only while nothing is known`() {
        assertThat(deriveShellState(signedIn, ModulePrefsState.Unknown)).isEqualTo(ShellState.Loading)
        assertThat(deriveShellState(SessionState.Unauthenticated, ModulePrefsState.Unknown))
            .isEqualTo(ShellState.Unauthenticated)
        assertThat(deriveShellState(signedIn, ModulePrefsState.Unavailable))
            .isEqualTo(ShellState.Ready(ModulePreferences.DEFAULT))
    }

    private fun TestScope.build(
        session: FakeSession,
        api: FakeApi,
        cache: FakeCache = FakeCache(),
    ): Pair<MainViewModel, ModulePreferencesRepository> {
        val repository = ModulePreferencesRepository(
            api,
            cache,
            ErrorMapper(Json { ignoreUnknownKeys = true }),
            backgroundScope,
        )
        val callState: StateFlow<CallState> = MutableStateFlow(CallState.Idle)
        val viewModel = MainViewModel(
            sessionStateProvider = session,
            engagementStore = mockk<EngagementStore>(relaxed = true),
            pushDestinations = PushDestinations(),
            callSessionManager = mockk<CallSessionManager> { every { state } returns callState },
            modulePreferences = repository,
        )
        return viewModel to repository
    }
}
