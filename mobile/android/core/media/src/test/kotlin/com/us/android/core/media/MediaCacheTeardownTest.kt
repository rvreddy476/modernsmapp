package com.us.android.core.media

import androidx.media3.database.StandaloneDatabaseProvider
import androidx.media3.datasource.cache.NoOpCacheEvictor
import androidx.media3.datasource.cache.SimpleCache
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * reels-20 (was V4-8): the streaming video cache is emptied at sign-out, so
 * the next account on the device inherits none of the last one's segments.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
class MediaCacheTeardownTest {

    @get:Rule
    val folder = TemporaryFolder()

    private val cache by lazy {
        SimpleCache(
            folder.newFolder("media"),
            NoOpCacheEvictor(),
            StandaloneDatabaseProvider(ApplicationProvider.getApplicationContext()),
        )
    }

    @After
    fun release() = cache.release()

    private fun put(key: String, bytes: ByteArray) {
        val hole = cache.startReadWrite(key, 0, bytes.size.toLong())
        val file = cache.startFile(key, 0, bytes.size.toLong())
        file.writeBytes(bytes)
        cache.commitFile(file, bytes.size.toLong())
        cache.releaseHoleSpan(hole)
    }

    @Test
    fun `after the sign-out task the cache holds no keys`() = runTest {
        put("segment-a", ByteArray(64) { 1 })
        put("segment-b", ByteArray(32) { 2 })
        assertThat(cache.keys).hasSize(2)

        MediaCacheTeardown(cache, Dispatchers.Unconfined).onSignOut()

        assertThat(cache.keys).isEmpty()
        assertThat(cache.cacheSpace).isEqualTo(0L)
    }

    @Test
    fun `an empty cache is fine, and running twice is fine`() = runTest {
        val teardown = MediaCacheTeardown(cache, Dispatchers.Unconfined)

        teardown.onSignOut()
        teardown.onSignOut()

        assertThat(cache.keys).isEmpty()
    }
}
