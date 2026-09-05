package com.us.android.feature.post.createhub

import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File

/**
 * The persisted queue (2026-09-05): records come back in the order they
 * were saved — which is the order the worker runs them — a known key is
 * updated in place, a removed one is gone, and the file survives a
 * second store reading it, the way a process restart does.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class FileReelPublishStoreTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun store() =
        FileReelPublishStore(ApplicationProvider.getApplicationContext(), json, Dispatchers.Unconfined)

    private fun pending(key: String, caption: String = "") =
        PendingReelPublish(creationKey = key, videoUri = "content://v/$key", caption = caption)

    @Test
    fun `two pending publishes keep their order and the first is what a worker takes first`() = runTest {
        val store = store()
        store.save(pending("key-1", "first"))
        store.save(pending("key-2", "second"))

        assertThat(store.loadAll().map { it.creationKey }).containsExactly("key-1", "key-2").inOrder()
        assertThat(store.loadAll().first().caption).isEqualTo("first")
        assertThat(store.load("key-2")?.caption).isEqualTo("second")
        assertThat(store.load("key-9")).isNull()

        store.remove("key-1")
        store.remove("key-2")
    }

    @Test
    fun `a checkpoint updates the record in place without moving it`() = runTest {
        val store = store()
        store.save(pending("key-1"))
        store.save(pending("key-2"))

        store.save(pending("key-1").copy(confirmedVideoId = "video-1"))

        assertThat(store.loadAll().map { it.creationKey }).containsExactly("key-1", "key-2").inOrder()
        assertThat(store.load("key-1")?.confirmedVideoId).isEqualTo("video-1")

        store.remove("key-1")
        store.remove("key-2")
    }

    @Test
    fun `the queue survives a restart and empties cleanly`() = runTest {
        val first = store()
        first.save(pending("key-1").copy(hashtags = listOf("a", "b"), publishAt = "2026-09-06T13:00:00Z"))

        val second = store()
        val restored = second.load("key-1")
        assertThat(restored?.hashtags).containsExactly("a", "b").inOrder()
        assertThat(restored?.publishAt).isEqualTo("2026-09-06T13:00:00Z")

        second.remove("key-1")
        assertThat(second.loadAll()).isEmpty()
        val dir = File(ApplicationProvider.getApplicationContext<android.content.Context>().filesDir, "reel_publish")
        assertThat(File(dir, "queue.json").exists()).isFalse()
    }
}
