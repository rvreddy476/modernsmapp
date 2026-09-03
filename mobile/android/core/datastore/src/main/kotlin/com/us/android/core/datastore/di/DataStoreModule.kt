package com.us.android.core.datastore.di

import com.us.android.core.datastore.DataStoreUsageStore
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
}
