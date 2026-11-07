package com.us.android

import android.app.Application
import android.graphics.Bitmap
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.annotation.DelicateCoilApi
import coil3.asImage
import coil3.memory.MemoryCache
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.test.runTest
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

/**
 * Chat audit L2: the image caches hold the photos the signed-in account saw,
 * chat photos included, and outlive the session. Sign-out empties both, so
 * the next account on this handset inherits none of them.
 *
 * The app's own loader ([buildImageLoader]) with its real caches: a fake
 * would only prove the fake was cleared.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], application = Application::class)
class ImageCacheTeardownTest {

    private val context: Application = RuntimeEnvironment.getApplication()
    private val loader: ImageLoader = buildImageLoader(context, OkHttpClient())

    @After
    fun tearDown() {
        loader.diskCache?.clear()
        loader.shutdown()
    }

    @Test
    fun `clearing empties the memory and the disk cache`() = runTest {
        cacheChatPhoto()

        clearImageCaches(loader.memoryCache, loader.diskCache)

        assertCachesEmpty()
    }

    @OptIn(DelicateCoilApi::class)
    @Test
    fun `sign-out clears the app-wide loader's caches`() = runTest {
        SingletonImageLoader.setUnsafe(loader)
        cacheChatPhoto()

        ImageCacheTeardown(context).onSignOut()

        assertCachesEmpty()
    }

    @Test
    fun `a loader without caches is a no-op`() = runTest {
        clearImageCaches(memory = null, disk = null)
    }

    private fun cacheChatPhoto() {
        val memory = checkNotNull(loader.memoryCache)
        val disk = checkNotNull(loader.diskCache)
        memory[MemoryCache.Key(PHOTO_KEY)] =
            MemoryCache.Value(Bitmap.createBitmap(4, 4, Bitmap.Config.ARGB_8888).asImage())
        val editor = checkNotNull(disk.openEditor(PHOTO_KEY))
        disk.fileSystem.write(editor.data) { writeUtf8("jpeg bytes") }
        disk.fileSystem.write(editor.metadata) { writeUtf8("headers") }
        editor.commit()
        // Precondition: there is something to clear.
        assertThat(memory[MemoryCache.Key(PHOTO_KEY)]).isNotNull()
        disk.openSnapshot(PHOTO_KEY).use { assertThat(it).isNotNull() }
    }

    private fun assertCachesEmpty() {
        val memory = checkNotNull(loader.memoryCache)
        val disk = checkNotNull(loader.diskCache)
        assertThat(memory[MemoryCache.Key(PHOTO_KEY)]).isNull()
        assertThat(memory.size).isEqualTo(0)
        disk.openSnapshot(PHOTO_KEY).use { assertThat(it).isNull() }
        assertThat(disk.size).isEqualTo(0)
    }

    private companion object {
        const val PHOTO_KEY = "https://api.example/v1/media/chat-photo/serve"
    }
}
