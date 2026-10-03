package com.us.android.core.datastore.di

import com.us.android.core.datastore.DataStoreOfflinePrefs
import com.us.android.core.datastore.DataStoreReelsSoundStore
import com.us.android.core.datastore.DataStoreUsageStore
import com.us.android.core.datastore.OfflinePrefs
import com.us.android.core.datastore.ReelsSoundStore
import com.us.android.core.datastore.UsageStore
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
    abstract fun bindOfflinePrefs(store: DataStoreOfflinePrefs): OfflinePrefs
}
