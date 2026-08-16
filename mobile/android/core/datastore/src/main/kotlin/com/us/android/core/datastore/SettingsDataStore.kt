package com.us.android.core.datastore

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.map
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

private val Context.dataStore: DataStore<Preferences> by preferencesDataStore(name = "us_settings")

/**
 * Non-secret user and client preferences.
 *
 * Explicitly NOT the place for tokens. DataStore is not encrypted; the
 * refresh token lives in [com.us.android.core.auth.TokenStore] behind an
 * Android Keystore key. Putting credentials here would be a security
 * downgrade dressed up as convenience.
 */
@Singleton
class SettingsDataStore @Inject constructor(
    @ApplicationContext private val context: Context,
) {

    private val store = context.dataStore

    /**
     * Cursor for the notification SSE stream (`Last-Event-ID`).
     *
     * Persisted because the server replays missed events from it. Losing it
     * across a restart means silently dropping notifications that arrived
     * while the app was closed.
     */
    val lastNotificationEventId: Flow<String?> = store.data
        .safe()
        .map { it[KEY_LAST_EVENT_ID] }

    suspend fun setLastNotificationEventId(id: String) {
        store.edit { it[KEY_LAST_EVENT_ID] = id }
    }

    /**
     * Data saver. Read by the media layer to cap the ABR ladder rather than
     * to pick a different URL — one stream, one ceiling (parent plan §5.4).
     */
    val dataSaverEnabled: Flow<Boolean> = store.data
        .safe()
        .map { it[KEY_DATA_SAVER] ?: false }

    suspend fun setDataSaverEnabled(enabled: Boolean) {
        store.edit { it[KEY_DATA_SAVER] = enabled }
    }

    suspend fun clear() {
        store.edit { it.clear() }
    }

    /**
     * A corrupt or unreadable preferences file must not take the app down.
     * Falling back to defaults is always recoverable; crashing on launch is not.
     */
    private fun Flow<Preferences>.safe(): Flow<Preferences> = catch { cause ->
        if (cause is IOException) emit(emptyPreferences()) else throw cause
    }

    private companion object {
        val KEY_LAST_EVENT_ID = stringPreferencesKey("last_notification_event_id")
        val KEY_DATA_SAVER = booleanPreferencesKey("data_saver_enabled")
    }
}
