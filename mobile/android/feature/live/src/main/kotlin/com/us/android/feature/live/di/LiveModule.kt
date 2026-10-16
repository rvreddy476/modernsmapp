package com.us.android.feature.live.di

import android.content.Context
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.TokenProvider
import com.us.android.core.network.di.AuthenticatedClient
import com.us.android.feature.live.data.FollowGraphLiveFollows
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveFollows
import com.us.android.feature.live.data.LiveKitRoomSession
import com.us.android.feature.live.data.LiveRoomFactory
import com.us.android.feature.live.data.LiveRoomSocket
import com.us.android.feature.live.data.OkHttpLiveRoomSocket
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
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

    /**
     * The live room's frames over ws-gateway (T-30), on the AUTHENTICATED
     * client (same interceptors as every request) and the configured socket
     * origin. The token is read at connect time, so a reconnect after a
     * refresh presents the new one.
     */
    @Provides
    fun provideLiveRoomSocket(
        @AuthenticatedClient client: OkHttpClient,
        config: ApiConfig,
        tokens: TokenProvider,
        json: Json,
    ): LiveRoomSocket = OkHttpLiveRoomSocket(
        client = client,
        wsBaseUrl = config.wsBaseUrl,
        accessToken = tokens::currentAccessToken,
        json = json,
    )

    /** Follow in the live room writes through the app's shared follow graph. */
    @Provides
    fun provideLiveFollows(follows: FollowGraphLiveFollows): LiveFollows = follows
}
