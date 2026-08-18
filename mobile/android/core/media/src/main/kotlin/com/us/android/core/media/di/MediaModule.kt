package com.us.android.core.media.di

import android.content.Context
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.database.StandaloneDatabaseProvider
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.cache.CacheDataSource
import androidx.media3.datasource.cache.LeastRecentlyUsedCacheEvictor
import androidx.media3.datasource.cache.SimpleCache
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import com.us.android.core.media.MEDIA_CACHE_BYTES
import com.us.android.core.media.PlayerFactory
import com.us.android.core.media.PlaylistAwareDataSourceFactory
import com.us.android.core.media.data.MediaApi
import com.us.android.core.media.reelsLoadControl
import com.us.android.core.network.di.AuthenticatedClient
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import java.io.File
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
@OptIn(UnstableApi::class)
object MediaModule {

    /**
     * One cache for the whole process.
     *
     * [SimpleCache] takes an exclusive lock on its directory — a second
     * instance over the same folder throws at construction. Singleton scope is
     * therefore a correctness requirement, not an optimisation.
     */
    @Provides
    @Singleton
    fun provideMediaCache(@ApplicationContext context: Context): SimpleCache = SimpleCache(
        File(context.cacheDir, "media"),
        LeastRecentlyUsedCacheEvictor(MEDIA_CACHE_BYTES),
        StandaloneDatabaseProvider(context),
    )

    /**
     * The data source chain every player reads through.
     *
     * Built on the app's AUTHENTICATED OkHttp client, which is the load-bearing
     * detail. HLS master and child playlists are served from the gateway at
     * `/v1/media/:id/hls/...` and are authorized, so a player using its own
     * default HTTP stack gets 401 on the playlist and never reaches a segment.
     * Sharing the client also means media requests inherit single-flight token
     * refresh — two stacks would each hold a stale token and race the rotation.
     *
     * Segment URLs are pre-signed and absolute, and the bearer must NOT travel
     * with them. Verified on a device on 2026-08-18: attaching it made the
     * object store answer 400 "request has multiple authentication types", and
     * more seriously it handed a credential that authenticates as the user to
     * a host that is not ours. AuthInterceptor now scopes the token to the API
     * origin; see AuthOriginTest.
     *
     * Segments are cached, playlists are not — see the factory below.
     */
    @Provides
    @Singleton
    fun provideCacheDataSourceFactory(
        @ApplicationContext context: Context,
        @AuthenticatedClient client: OkHttpClient,
        cache: SimpleCache,
    ): DataSource.Factory {
        val upstream = DefaultDataSource.Factory(
            context,
            OkHttpDataSource.Factory(client),
        )
        val cached = CacheDataSource.Factory()
            .setCache(cache)
            .setUpstreamDataSourceFactory(upstream)
            // A corrupt or truncated cache entry must not break playback; fall
            // through to the network instead of surfacing an error the user
            // cannot act on.
            .setFlags(CacheDataSource.FLAG_IGNORE_CACHE_ON_ERROR)

        // Segments cached, playlists never. An .m3u8 body embeds signed URLs
        // that expire in five minutes, so caching one leaves the player
        // replaying dead links AND stops it asking for a fresh playlist that
        // would have healed it. See PlaylistAwareDataSourceFactory.
        return PlaylistAwareDataSourceFactory(cacheFactory = cached, upstreamFactory = upstream)
    }

    /**
     * Takes no data source, deliberately.
     *
     * The cached, authenticated chain is attached where the media source is
     * built — in [com.us.android.core.media.PlayerPool] — not to the player
     * itself. Setting a default here as well would give a player two sources
     * of truth for how bytes are fetched, and whichever the media source
     * carried would silently win.
     */
    @Provides
    @Singleton
    fun providePlayerFactory(
        @ApplicationContext context: Context,
    ): PlayerFactory = object : PlayerFactory {
        override fun create(): ExoPlayer = ExoPlayer.Builder(context)
            .setLoadControl(reelsLoadControl())
            .build()
            .apply {
                repeatMode = ExoPlayer.REPEAT_MODE_ONE
                // Reels start muted. Autoplaying sound in a scrolling feed is
                // hostile in public, and every platform that ships it also
                // ships an unmute affordance.
                volume = 0f
            }
    }
}

/**
 * The media endpoints, created from the app-wide [retrofit2.Retrofit].
 *
 * No client, no base URL, no converter here. A module that assembles its own
 * client forks token refresh, and two refreshers racing a rotating refresh
 * token sign the user out.
 */
@Module
@InstallIn(SingletonComponent::class)
object MediaApiModule {

    @Provides
    @Singleton
    fun provideMediaApi(retrofit: retrofit2.Retrofit): MediaApi =
        retrofit.create(MediaApi::class.java)
}
