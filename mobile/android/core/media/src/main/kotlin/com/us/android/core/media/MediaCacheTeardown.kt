package com.us.android.core.media

import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.cache.Cache
import androidx.media3.datasource.cache.SimpleCache
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.session.SessionTeardownTask
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.IntoSet
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import javax.inject.Inject

/**
 * Empties the STREAMING video cache (`cacheDir/media`, 256 MiB) at sign-out
 * (reels-20, was V4-8).
 *
 * Every segment this account watched — members-only reels and videos
 * included — sat in that cache across a sign-out, so the next account on the
 * handset held the previous one's viewing on disk. Image caches were already
 * cleared (`ImageCacheTeardown`); this is the video half.
 *
 * Offline copies are NOT touched: they live in their own cache
 * (`OfflineMediaModule`, `@OfflineCache`) and are held hidden for 48 hours at
 * sign-out by the founder's rule (`OfflineTeardown`).
 *
 * Local only and idempotent, as [SessionTeardownTask] asks; a key that cannot
 * be removed (a player still holds a span) is skipped, never thrown.
 */
@OptIn(UnstableApi::class)
class MediaCacheTeardown @Inject constructor(
    private val cache: SimpleCache,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : SessionTeardownTask {
    override suspend fun onSignOut() = withContext(io) { clearMediaCache(cache) }
}

/** Removes every resource in [cache]. Pure over the [Cache] interface, so it is tested on a real one. */
@OptIn(UnstableApi::class)
internal fun clearMediaCache(cache: Cache) {
    for (key in cache.keys.toList()) {
        runCatching { cache.removeResource(key) }
    }
}

/** Contributes [MediaCacheTeardown] to sign-out. */
@Module
@InstallIn(SingletonComponent::class)
abstract class MediaCacheTeardownModule {
    @Binds
    @IntoSet
    abstract fun bindMediaCacheTeardown(impl: MediaCacheTeardown): SessionTeardownTask
}
