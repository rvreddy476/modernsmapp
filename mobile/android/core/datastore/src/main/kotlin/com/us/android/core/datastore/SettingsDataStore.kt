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
import java.util.UUID
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

    /**
     * Whether the notification permission has ever been REQUESTED — Slice D.
     *
     * Persisted because the platform cannot tell us.
     * `shouldShowRequestPermissionRationale` is false both BEFORE the first ask
     * and AFTER a permanent denial, so without this flag the app cannot
     * distinguish "never asked" from "asked and shut the door" — and would
     * either never prompt, or prompt forever into a dialog the system no
     * longer shows.
     */
    val notificationPermissionAsked: Flow<Boolean> = store.data
        .safe()
        .map { it[KEY_NOTIFICATION_PERMISSION_ASKED] ?: false }

    suspend fun setNotificationPermissionAsked() {
        store.edit { it[KEY_NOTIFICATION_PERMISSION_ASKED] = true }
    }

    /**
     * The last server-confirmed module preferences, so a relaunch renders the
     * user's chosen tabs on the first resolved frame instead of a splash that
     * waits on the network. `null` module ids means "never cached" — an empty
     * string is a real value (a user who chose no optional modules).
     *
     * Raw strings rather than a typed model: this module cannot see
     * `:core:profile`, which owns the [com.us.android.core.profile] types.
     */
    val cachedModuleIds: Flow<String?> = store.data
        .safe()
        .map { it[KEY_MODULE_IDS] }

    val cachedHomeModuleId: Flow<String?> = store.data
        .safe()
        .map { it[KEY_HOME_MODULE_ID] }

    val cachedModuleOnboardingCompleted: Flow<Boolean> = store.data
        .safe()
        .map { it[KEY_MODULE_ONBOARDING_COMPLETED] ?: false }

    suspend fun setCachedModulePreferences(
        moduleIds: String,
        homeModuleId: String,
        onboardingCompleted: Boolean,
    ) {
        store.edit {
            it[KEY_MODULE_IDS] = moduleIds
            it[KEY_HOME_MODULE_ID] = homeModuleId
            it[KEY_MODULE_ONBOARDING_COMPLETED] = onboardingCompleted
        }
    }

    /** Sign-out: the next account must not inherit this one's module choices. */
    suspend fun clearCachedModulePreferences() {
        store.edit {
            it.remove(KEY_MODULE_IDS)
            it.remove(KEY_HOME_MODULE_ID)
            it.remove(KEY_MODULE_ONBOARDING_COMPLETED)
        }
    }

    /**
     * The last server-confirmed muted keywords, already normalised (lower
     * case, no `#`). The feed reads this directly so a keyword the server has
     * accepted is hidden on the very next page even before the ranking
     * service catches up. Comma-separated: a keyword cannot contain a comma
     * after normalisation.
     */
    val keywordFilters: Flow<List<String>> = store.data
        .safe()
        .map { prefs ->
            prefs[KEY_KEYWORD_FILTERS]
                ?.split(KEYWORD_SEPARATOR)
                ?.filter { it.isNotBlank() }
                .orEmpty()
        }

    suspend fun setKeywordFilters(keywords: List<String>) {
        store.edit { it[KEY_KEYWORD_FILTERS] = keywords.joinToString(KEYWORD_SEPARATOR) }
    }

    /**
     * The screen-time ledger: foreground time per local date that has not yet
     * been confirmed by the server. Opaque here — [UsageAccumulator] owns the
     * encoding — because this class stores strings, not domain types.
     */
    val usageLedger: Flow<String?> = store.data
        .safe()
        .map { it[KEY_USAGE_LEDGER] }

    suspend fun setUsageLedger(encoded: String) {
        store.edit { it[KEY_USAGE_LEDGER] = encoded }
    }

    /**
     * The last server-confirmed daily-limit / sleep-window pair the
     * screen-time guard resolves against — not the full wellbeing settings
     * object, just what [com.us.android.screentime.ScreenTimeGuardCoordinator]
     * needs. Lets a cold start show a correct nudge in the ~60 seconds before
     * the first per-session fetch answers, instead of nudging blind. Opaque
     * here for the same reason [usageLedger] is: this module cannot see
     * `:core:profile`'s types.
     */
    val wellbeingGuardCache: Flow<String?> = store.data
        .safe()
        .map { it[KEY_WELLBEING_GUARD_CACHE] }

    suspend fun setWellbeingGuardCache(encoded: String) {
        store.edit { it[KEY_WELLBEING_GUARD_CACHE] = encoded }
    }

    /**
     * "Autoplay next episode" on the Tube watch screen (founder, 2026-09-12):
     * when a video in a series ends, the next episode plays after a ten
     * second countdown. On by default because a series is watched in order
     * and the countdown itself is the viewer's chance to say no; the switch
     * is for the viewer who never wants to be moved along. Series-only: a
     * video outside a series ends on its end screen whatever this says.
     */
    val autoplayNextEpisode: Flow<Boolean> = store.data
        .safe()
        .map { it[KEY_AUTOPLAY_NEXT_EPISODE] ?: true }

    suspend fun setAutoplayNextEpisode(enabled: Boolean) {
        store.edit { it[KEY_AUTOPLAY_NEXT_EPISODE] = enabled }
    }

    /**
     * Whether Reels plays with SOUND.
     *
     * founder, 2026-09-30: reels open MUTED, like the web; once the viewer
     * turns the sound on it STAYS on — across reels, across leaving and
     * re-entering Reels, and across app restarts — until they mute again
     * ("Once user makes it on on the sound keep it on"). It replaces the
     * 2026-09-05 decision that Reels open with the sound on and keep a mute
     * for the session only. Off until the viewer says otherwise, and the
     * choice drives the video and its added sound together.
     */
    val reelsSoundOn: Flow<Boolean> = store.data
        .safe()
        .map { it[KEY_REELS_SOUND_ON] ?: false }

    suspend fun setReelsSoundOn(on: Boolean) {
        store.edit { it[KEY_REELS_SOUND_ON] = on }
    }

    /**
     * "Save on Wi-Fi only" for offline copies (2026-10-02). On by default: a
     * long video is hundreds of megabytes, and nobody should find that on a
     * mobile bill because they tapped Save offline on the bus.
     */
    val offlineWifiOnly: Flow<Boolean> = store.data
        .safe()
        .map { it[KEY_OFFLINE_WIFI_ONLY] ?: true }

    suspend fun setOfflineWifiOnly(enabled: Boolean) {
        store.edit { it[KEY_OFFLINE_WIFI_ONLY] = enabled }
    }

    /**
     * This install's id for offline copies (2026-10-02): random, made once,
     * and sent as `device_id` so post-service can tell this phone's copies
     * from the same account's copies on another. It names no hardware and no
     * person; see [offlineDeviceIdOr].
     */
    suspend fun offlineDeviceId(): String {
        var id = ""
        store.edit { prefs ->
            id = offlineDeviceIdOr(prefs[KEY_OFFLINE_DEVICE_ID]) { UUID.randomUUID().toString() }
            prefs[KEY_OFFLINE_DEVICE_ID] = id
        }
        return id
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
        val KEY_NOTIFICATION_PERMISSION_ASKED = booleanPreferencesKey("notification_permission_asked")
        val KEY_MODULE_IDS = stringPreferencesKey("module_ids")
        val KEY_HOME_MODULE_ID = stringPreferencesKey("home_module_id")
        val KEY_MODULE_ONBOARDING_COMPLETED = booleanPreferencesKey("module_onboarding_completed")
        val KEY_KEYWORD_FILTERS = stringPreferencesKey("keyword_filters")
        val KEY_USAGE_LEDGER = stringPreferencesKey("usage_ledger")
        val KEY_WELLBEING_GUARD_CACHE = stringPreferencesKey("wellbeing_guard_cache")
        val KEY_AUTOPLAY_NEXT_EPISODE = booleanPreferencesKey("autoplay_next_episode")
        val KEY_REELS_SOUND_ON = booleanPreferencesKey("reels_sound_on")
        val KEY_OFFLINE_WIFI_ONLY = booleanPreferencesKey("offline_wifi_only")
        val KEY_OFFLINE_DEVICE_ID = stringPreferencesKey("offline_device_id")
        const val KEYWORD_SEPARATOR = ","
    }
}
