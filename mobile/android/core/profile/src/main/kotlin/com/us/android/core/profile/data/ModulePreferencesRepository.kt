package com.us.android.core.profile.data

import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.common.result.onSuccess
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.profile.data.dto.ModulePreferencesDto
import com.us.android.core.profile.data.dto.UpdateModulePreferencesRequest
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The one holder of "which modules did this user choose".
 *
 * The state is what the shell gates on, so its transitions are the contract:
 *
 *  - It starts [ModulePrefsState.Unknown] and becomes [ModulePrefsState.Loaded]
 *    from the cache as soon as the cache answers — a signed-in relaunch
 *    renders the user's own tabs without waiting on the network.
 *  - [refresh] replaces whatever is held with the server's answer. A failed
 *    refresh keeps a cached value (stale beats blank) and only becomes
 *    [ModulePrefsState.Unavailable] when there is nothing to keep.
 *  - [save] is a full-snapshot write; the server's echo is what the state
 *    becomes, never the client's optimistic copy.
 *  - [clear] is sign-out: the next account starts Unknown with an empty cache.
 */
@Singleton
class ModulePreferencesRepository @Inject constructor(
    private val api: ModulePreferencesApi,
    private val cache: ModulePreferencesCache,
    private val errorMapper: ErrorMapper,
    @ApplicationScope scope: CoroutineScope,
) {
    private val _state = MutableStateFlow<ModulePrefsState>(ModulePrefsState.Unknown)
    val state: StateFlow<ModulePrefsState> = _state.asStateFlow()

    init {
        scope.launch { seedFromCache() }
    }

    suspend fun refresh() {
        when (val result = apiCall(errorMapper) { api.modules() }.map { it.toDomain() }) {
            is AppResult.Success -> adopt(result.data)
            // Offline: the cache is the answer. Read it HERE rather than
            // trusting the init seed, because a fast failure (airplane mode)
            // can land before the DataStore read does — and the seed must not
            // overwrite a Loaded state, so it cannot be the one to recover.
            is AppResult.Failure -> seedFromCache(orElse = ModulePrefsState.Unavailable)
        }
    }

    suspend fun save(prefs: ModulePreferences, completeOnboarding: Boolean): AppResult<ModulePreferences> =
        apiCall(errorMapper) { api.updateModules(prefs.toRequest(completeOnboarding)) }
            .map { it.toDomain() }
            .onSuccess { adopt(it) }

    suspend fun clear() {
        _state.value = ModulePrefsState.Unknown
        cache.clear()
    }

    private suspend fun adopt(prefs: ModulePreferences) {
        _state.value = ModulePrefsState.Loaded(prefs)
        cache.write(prefs)
    }

    /**
     * Promotes the cached answer, if any, over Unknown or Unavailable — never
     * over a Loaded state, which is at least as fresh. With no cache, the
     * state becomes [orElse] when given (a failed refresh passes Unavailable
     * so the shell stops waiting) and is otherwise left alone (the init seed,
     * which has no verdict yet).
     */
    private suspend fun seedFromCache(orElse: ModulePrefsState? = null) {
        val cached = cache.read()
        _state.update { current ->
            when {
                current is ModulePrefsState.Loaded -> current
                cached != null -> ModulePrefsState.Loaded(cached)
                else -> orElse ?: current
            }
        }
    }
}

private fun ModulePreferencesDto.toDomain(): ModulePreferences {
    val chosen = modules.mapNotNull(AppModule::fromId).toSet() - AppModule.FEED
    // An id this build does not know cannot be a home; the feed is the one
    // home every build has.
    val home = AppModule.fromId(homeModule)?.takeIf { it == AppModule.FEED || it in chosen } ?: AppModule.FEED
    return ModulePreferences(
        modules = chosen,
        homeModule = home,
        onboardingCompleted = !onboardingCompletedAt.isNullOrBlank(),
    )
}

private fun ModulePreferences.toRequest(completeOnboarding: Boolean) = UpdateModulePreferencesRequest(
    modules = AppModule.selectable.filter { it in modules }.map { it.id },
    homeModule = homeModule.id,
    completeOnboarding = completeOnboarding,
)
