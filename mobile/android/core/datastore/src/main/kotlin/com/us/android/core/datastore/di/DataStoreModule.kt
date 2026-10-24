package com.us.android.core.datastore.di

import com.us.android.core.datastore.AppearanceStore
import com.us.android.core.datastore.AudioLanguageStore
import com.us.android.core.datastore.DataSaverStore
import com.us.android.core.datastore.DataStoreAppearanceStore
import com.us.android.core.datastore.DataStoreDataSaverStore
import com.us.android.core.datastore.DataStoreAudioLanguageStore
import com.us.android.core.datastore.DataStoreLeavingDomainsStore
import com.us.android.core.datastore.LeavingDomainsStore
import com.us.android.core.datastore.DataStoreOfflinePrefs
import com.us.android.core.datastore.DataStoreReelsAutoScrollStore
import com.us.android.core.datastore.DataStoreReelsSoundStore
import com.us.android.core.datastore.DataStoreUsageStore
import com.us.android.core.datastore.DataStoreWatchHistoryPauseStore
import com.us.android.core.datastore.OfflinePrefs
import com.us.android.core.datastore.ReelsAutoScrollStore
import com.us.android.core.datastore.ReelsSoundStore
import com.us.android.core.datastore.UsageStore
import com.us.android.core.datastore.WatchHistoryPauseStore
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

@Module
@InstallIn(SingletonComponent::class)
abstract class DataStoreModule {
    @Binds
    abstract fun bindUsageStore(store: DataStoreUsageStore): UsageStore

    @Binds
    abstract fun bindReelsSoundStore(store: DataStoreReelsSoundStore): ReelsSoundStore

    @Binds
    abstract fun bindReelsAutoScrollStore(store: DataStoreReelsAutoScrollStore): ReelsAutoScrollStore

    @Binds
    abstract fun bindAppearanceStore(store: DataStoreAppearanceStore): AppearanceStore

    @Binds
    abstract fun bindDataSaverStore(store: DataStoreDataSaverStore): DataSaverStore

    @Binds
    abstract fun bindOfflinePrefs(store: DataStoreOfflinePrefs): OfflinePrefs

    @Binds
    abstract fun bindAudioLanguageStore(store: DataStoreAudioLanguageStore): AudioLanguageStore

    /** Tube's "Pause recording" (2026-10-22): read by the progress writer and the History page. */
    @Binds
    abstract fun bindWatchHistoryPauseStore(store: DataStoreWatchHistoryPauseStore): WatchHistoryPauseStore

    /** The Action button's leaving page (2026-10-23): once per site per person. */
    @Binds
    abstract fun bindLeavingDomainsStore(store: DataStoreLeavingDomainsStore): LeavingDomainsStore
}
