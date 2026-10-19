package com.us.android.feature.post.studio

import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.PostDetails
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The photo publish record (2026-10-19): what Post handed to the worker, and
 * whether it gave up, read back by a second store the way a process restart
 * reads it — the failed tile and its Retry depend on exactly that.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class PhotoPublishRecordsTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun records() =
        PhotoPublishRecords(ApplicationProvider.getApplicationContext(), json, Dispatchers.Unconfined)

    private val details = PostDetails(title = "Camera fresh project", visibility = "friends")

    @Test
    fun `a failure keeps the details and survives a restart`() = runTest {
        records().save(PendingPhotoPublish("p1", details))
        records().fail("p1", PendingPhotoFailure("That didn't finish uploading. Try again.", retryable = true), PostDetails())

        val restored = records().load("p1")
        assertThat(restored?.details).isEqualTo(details)
        assertThat(restored?.failure?.retryable).isTrue()
    }

    @Test
    fun `a failure with no record creates one with the worker's details`() = runTest {
        records().fail("p2", PendingPhotoFailure("This post couldn't be published.", retryable = false), details)

        assertThat(records().load("p2")).isEqualTo(
            PendingPhotoPublish("p2", details, PendingPhotoFailure("This post couldn't be published.", retryable = false)),
        )
    }

    @Test
    fun `saving again replaces in place and remove forgets it`() = runTest {
        val store = records()
        store.save(PendingPhotoPublish("a", details))
        store.save(PendingPhotoPublish("b"))
        store.save(PendingPhotoPublish("a", details, PendingPhotoFailure("x", retryable = true)).copy(failure = null))

        assertThat(store.loadAll().map { it.projectId }).containsExactly("a", "b").inOrder()
        store.remove("a")
        store.remove("b")
        assertThat(records().loadAll()).isEmpty()
    }
}
