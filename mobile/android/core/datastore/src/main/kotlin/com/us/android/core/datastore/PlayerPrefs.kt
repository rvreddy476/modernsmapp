package com.us.android.core.datastore

import com.us.android.core.model.AppearanceMode
import kotlinx.coroutines.flow.Flow
import javax.inject.Inject

/**
 * "Auto scroll" in Reels (TikTok parity T-32, 2026-10-15), kept across
 * restarts. A seam rather than [SettingsDataStore] itself for the reason
 * [ReelsSoundStore] is one: the Reels ViewModel is tested on the JVM against
 * an in-memory store. The production one is [DataStoreReelsAutoScrollStore].
 */
interface ReelsAutoScrollStore {
    /** False until the viewer turns it on: a reel loops until they swipe. */
    val autoScroll: Flow<Boolean>

    suspend fun setAutoScroll(on: Boolean)
}

class DataStoreReelsAutoScrollStore @Inject constructor(
    private val dataStore: SettingsDataStore,
) : ReelsAutoScrollStore {
    override val autoScroll: Flow<Boolean> get() = dataStore.reelsAutoScroll

    override suspend fun setAutoScroll(on: Boolean) = dataStore.setReelsAutoScroll(on)
}

/**
 * The viewer's audio language (2026-10-21), ONE preference for reels and
 * long videos alike, as the web keeps `audioLanguage`. A seam for the same
 * reason [ReelsAutoScrollStore] is one: both video ViewModels are tested on
 * the JVM against an in-memory store.
 */
interface AudioLanguageStore {
    /** A lowercase language tag, or null for the original audio, which is where every viewer starts. */
    val language: Flow<String?>

    suspend fun setLanguage(language: String?)
}

class DataStoreAudioLanguageStore @Inject constructor(
    private val dataStore: SettingsDataStore,
) : AudioLanguageStore {
    override val language: Flow<String?> get() = dataStore.audioLanguage

    override suspend fun setLanguage(language: String?) = dataStore.setAudioLanguage(language)
}

/**
 * The Appearance setting (T-32, 2026-10-15): System, Light or Dark. Read by
 * the app's root, which hands the answer to `UsTheme(darkTheme =)`, and
 * written by the settings screen.
 */
interface AppearanceStore {
    /** [AppearanceMode.SYSTEM] until the viewer picks. */
    val mode: Flow<AppearanceMode>

    suspend fun setMode(mode: AppearanceMode)
}

class DataStoreAppearanceStore @Inject constructor(
    private val dataStore: SettingsDataStore,
) : AppearanceStore {
    override val mode: Flow<AppearanceMode> get() = dataStore.appearanceMode

    override suspend fun setMode(mode: AppearanceMode) = dataStore.setAppearanceMode(mode)
}
