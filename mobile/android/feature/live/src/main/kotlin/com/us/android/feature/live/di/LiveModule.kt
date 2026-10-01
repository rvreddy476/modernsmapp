package com.us.android.feature.live.di

import android.content.Context
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveKitRoomSession
import com.us.android.feature.live.data.LiveRoomFactory
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Creates the live endpoints from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object LiveModule {

    @Provides
    @Singleton
    fun provideLiveApi(retrofit: Retrofit): LiveApi = retrofit.create(LiveApi::class.java)

    /** Each call is a NEW room; the ViewModel that asked owns and disconnects it. */
    @Provides
    fun provideLiveRoomFactory(@ApplicationContext context: Context): LiveRoomFactory =
        LiveRoomFactory { LiveKitRoomSession(context) }
}
