package com.us.android.core.datastore

import kotlinx.coroutines.flow.Flow
import javax.inject.Inject

/**
 * The viewer's choice of sound in Reels, kept across restarts.
 *
 * founder, 2026-09-30: reels open MUTED; once the viewer turns the sound on
 * it stays on until they mute again. A seam rather than [SettingsDataStore]
 * itself so the Reels ViewModel can be tested on the JVM with an in-memory
 * store; the production one is [DataStoreReelsSoundStore].
 */
interface ReelsSoundStore {
    /** True once the viewer has turned the sound on; false until then, and after they mute. */
    val soundOn: Flow<Boolean>

    suspend fun setSoundOn(on: Boolean)
}

class DataStoreReelsSoundStore @Inject constructor(
    private val dataStore: SettingsDataStore,
) : ReelsSoundStore {
    override val soundOn: Flow<Boolean> get() = dataStore.reelsSoundOn

    override suspend fun setSoundOn(on: Boolean) = dataStore.setReelsSoundOn(on)
}
