package com.us.android

import android.content.Context
import coil3.SingletonImageLoader
import coil3.disk.DiskCache
import coil3.memory.MemoryCache
import com.us.android.core.common.session.SessionTeardownTask
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.IntoSet
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import javax.inject.Inject

/**
 * Empties the image loader's memory and disk caches during sign-out (chat
 * audit L2).
 *
 * The disk cache (`cacheDir/images`, see [buildImageLoader]) keeps every
 * photo this account viewed — chat photos included — and outlives the
 * session: `ChatTeardown` wipes the Room cache, not this. Without it, the next
 * account on this handset inherits the previous one's chat photos on disk,
 * and a bitmap still in memory can paint before its own request is checked.
 *
 * Local only: no network and no session state, so it cannot fail sign-out.
 */
class ImageCacheTeardown @Inject constructor(
    @ApplicationContext private val context: Context,
) : SessionTeardownTask {

    override suspend fun onSignOut() {
        val loader = SingletonImageLoader.get(context)
        clearImageCaches(loader.memoryCache, loader.diskCache)
    }
}

/** Clears both caches; the disk walk runs off the caller's thread. */
internal suspend fun clearImageCaches(memory: MemoryCache?, disk: DiskCache?) {
    memory?.clear()
    if (disk != null) withContext(Dispatchers.IO) { disk.clear() }
}

/** Contributes [ImageCacheTeardown] to sign-out. */
@Module
@InstallIn(SingletonComponent::class)
abstract class ImageCacheTeardownModule {
    @Binds
    @IntoSet
    abstract fun bindImageCacheTeardown(impl: ImageCacheTeardown): SessionTeardownTask
}
