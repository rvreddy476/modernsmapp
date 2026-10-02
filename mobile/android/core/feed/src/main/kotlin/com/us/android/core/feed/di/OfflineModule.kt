package com.us.android.core.feed.di

import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.core.feed.offline.AndroidOfflineConnectivity
import com.us.android.core.feed.offline.OfflineApi
import com.us.android.core.feed.offline.OfflineCheckScheduler
import com.us.android.core.feed.offline.OfflineConnectivity
import com.us.android.core.feed.offline.OfflineCopies
import com.us.android.core.feed.offline.OfflineLibrary
import com.us.android.core.feed.offline.OfflineTeardown
import com.us.android.core.feed.offline.WorkManagerOfflineCheckScheduler
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.IntoSet
import retrofit2.Retrofit
import javax.inject.Singleton

/** Offline copies (2026-10-02): the routes, from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object OfflineModule {
    @Provides
    @Singleton
    fun provideOfflineApi(retrofit: Retrofit): OfflineApi = retrofit.create(OfflineApi::class.java)
}

@Module
@InstallIn(SingletonComponent::class)
abstract class OfflineBindings {

    /** What the screens read; tests hand them a fake. */
    @Binds
    abstract fun bindOfflineLibrary(impl: OfflineCopies): OfflineLibrary

    @Binds
    abstract fun bindOfflineConnectivity(impl: AndroidOfflineConnectivity): OfflineConnectivity

    @Binds
    abstract fun bindOfflineCheckScheduler(impl: WorkManagerOfflineCheckScheduler): OfflineCheckScheduler

    /** Sign-out keeps this device's copies for 48 hours, hidden; then they are deleted. */
    @Binds
    @IntoSet
    abstract fun bindOfflineTeardown(impl: OfflineTeardown): SessionTeardownTask
}
