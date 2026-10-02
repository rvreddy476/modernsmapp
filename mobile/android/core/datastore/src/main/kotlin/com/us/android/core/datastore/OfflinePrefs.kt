package com.us.android.core.datastore

import kotlinx.coroutines.flow.Flow
import javax.inject.Inject

/**
 * What offline copies keep in preferences (2026-10-02): the Wi-Fi-only
 * switch and this install's device id. A seam rather than
 * [SettingsDataStore] itself, for the reason [ReelsSoundStore] is one: the
 * copy state machine is tested on the JVM against an in-memory store.
 */
interface OfflinePrefs {
    /** "Save on Wi-Fi only". True until the viewer says otherwise. */
    val wifiOnly: Flow<Boolean>

    suspend fun setWifiOnly(enabled: Boolean)

    /** This install's `device_id`: made on first use, the same on every later call. */
    suspend fun deviceId(): String
}

class DataStoreOfflinePrefs @Inject constructor(
    private val dataStore: SettingsDataStore,
) : OfflinePrefs {
    override val wifiOnly: Flow<Boolean> get() = dataStore.offlineWifiOnly

    override suspend fun setWifiOnly(enabled: Boolean) = dataStore.setOfflineWifiOnly(enabled)

    override suspend fun deviceId(): String = dataStore.offlineDeviceId()
}

/**
 * The stored id when it is usable, else a new one from [generate].
 *
 * post-service takes an opaque id of at most [MAX_DEVICE_ID_LENGTH]
 * characters. A stored value that is blank or too long is replaced rather
 * than sent: the server would refuse it on every call, and the copies made
 * under it could never be checked.
 */
fun offlineDeviceIdOr(stored: String?, generate: () -> String): String {
    val kept = stored?.trim().orEmpty()
    if (kept.isNotEmpty() && kept.length <= MAX_DEVICE_ID_LENGTH) return kept
    return generate().trim().take(MAX_DEVICE_ID_LENGTH)
}

/** post-service's bound on `device_id`. */
const val MAX_DEVICE_ID_LENGTH = 64
