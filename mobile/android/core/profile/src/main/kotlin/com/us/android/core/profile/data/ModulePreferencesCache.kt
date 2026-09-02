package com.us.android.core.profile.data

import com.us.android.core.datastore.SettingsDataStore
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import javax.inject.Inject

/**
 * The last server-confirmed [ModulePreferences], kept across launches.
 *
 * An interface so the repository is testable without a DataStore (which
 * needs an Android context). The only implementation writes to
 * [SettingsDataStore]; tests use an in-memory one.
 */
interface ModulePreferencesCache {
    suspend fun read(): ModulePreferences?
    suspend fun write(prefs: ModulePreferences)
    suspend fun clear()
}

class DataStoreModulePreferencesCache @Inject constructor(
    private val dataStore: SettingsDataStore,
) : ModulePreferencesCache {

    override suspend fun read(): ModulePreferences? = combine(
        dataStore.cachedModuleIds,
        dataStore.cachedHomeModuleId,
        dataStore.cachedModuleOnboardingCompleted,
    ) { ids, home, completed ->
        // A null id list means nothing was ever cached; an empty string is a
        // real answer (no optional modules chosen).
        ids?.let {
            ModulePreferences(
                modules = it.split(SEPARATOR).mapNotNull(AppModule::fromId).toSet() - AppModule.FEED,
                homeModule = home?.let(AppModule::fromId) ?: AppModule.FEED,
                onboardingCompleted = completed,
            )
        }
    }.first()

    override suspend fun write(prefs: ModulePreferences) {
        dataStore.setCachedModulePreferences(
            moduleIds = prefs.modules.joinToString(SEPARATOR) { it.id },
            homeModuleId = prefs.homeModule.id,
            onboardingCompleted = prefs.onboardingCompleted,
        )
    }

    override suspend fun clear() = dataStore.clearCachedModulePreferences()

    private companion object {
        const val SEPARATOR = ","
    }
}
