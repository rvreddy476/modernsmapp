package com.us.android.core.chat.di

import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.di.AuthenticatedClient
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Creates the chat endpoints and the socket from the app-wide instances.
 *
 * No client, no base URL, no converter of its own. A module that assembles its
 * own client forks token refresh, and two refreshers racing a rotating refresh
 * token sign the user out.
 */
@Module
@InstallIn(SingletonComponent::class)
object ChatModule {

    @Provides
    @Singleton
    fun provideChatApi(retrofit: Retrofit): ChatApi = retrofit.create(ChatApi::class.java)

    /**
     * The socket shares the AUTHENTICATED OkHttp client.
     *
     * Same client as every other request, so the socket inherits single-flight
     * token refresh and the origin-scoped bearer. A second stack would hold
     * its own stale token and race the rotation.
     *
     * The URL comes from [ApiConfig.wsBaseUrl] rather than being derived from
     * the HTTP base: the two are separately configurable per flavor, and
     * guessing one from the other silently breaks whichever environment does
     * not follow the pattern.
     */
    @Provides
    @Singleton
    fun provideChatSocket(
        @AuthenticatedClient client: OkHttpClient,
        config: ApiConfig,
    ): ChatSocket = ChatSocket(client = client, wsBaseUrl = config.wsBaseUrl)
}
