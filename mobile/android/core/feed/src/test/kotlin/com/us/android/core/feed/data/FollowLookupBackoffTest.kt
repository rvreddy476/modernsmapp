package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FollowStatus
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * A relationship lookup that fails is not repeated on every page (audit
 * 2026-10-30, A4): a deleted account's 404 or a failing graph used to cost
 * one request per author on every append for the whole session. The author
 * waits out a back-off, doubling per failure, and a success clears it.
 */
class FollowLookupBackoffTest {

    private var clock = 1_000_000L

    private fun graph(api: RecordingGraphApi) = followGraph(api).also { it.now = { clock } }

    @Test
    fun `a failed lookup is not asked again before its back-off has passed`() = runTest {
        val api = RecordingGraphApi().apply { failingRelationships += "gone" }
        val graph = graph(api)

        graph.ensureKnown(listOf("gone", "ada"))
        graph.ensureKnown(listOf("gone", "ada"))
        clock += followLookupBackoffMillis(1) - 1
        graph.ensureKnown(listOf("gone"))

        assertThat(api.relationshipRequests).containsExactly("me" to "gone", "me" to "ada")
        assertThat(graph.edges.value).containsExactly("ada", FollowStatus.NONE)
    }

    @Test
    fun `once the back-off has passed the author is asked again, and each failure waits longer`() = runTest {
        val api = RecordingGraphApi().apply { failingRelationships += "gone" }
        val graph = graph(api)

        graph.ensureKnown(listOf("gone"))
        clock += followLookupBackoffMillis(1)
        graph.ensureKnown(listOf("gone"))
        assertThat(api.relationshipRequests).hasSize(2)

        // Second failure: the first back-off is no longer enough.
        clock += followLookupBackoffMillis(1)
        graph.ensureKnown(listOf("gone"))
        assertThat(api.relationshipRequests).hasSize(2)

        clock += followLookupBackoffMillis(2) - followLookupBackoffMillis(1)
        graph.ensureKnown(listOf("gone"))
        assertThat(api.relationshipRequests).hasSize(3)
    }

    @Test
    fun `a lookup that recovers records the edge and is never asked again`() = runTest {
        val api = RecordingGraphApi().apply { failingRelationships += "blip" }
        val graph = graph(api)

        graph.ensureKnown(listOf("blip"))
        api.failingRelationships.clear()
        clock += followLookupBackoffMillis(1)
        graph.ensureKnown(listOf("blip"))
        clock += followLookupBackoffMillis(FAR_FUTURE_FAILURES)
        graph.ensureKnown(listOf("blip"))

        assertThat(api.relationshipRequests).hasSize(2)
        assertThat(graph.edges.value).containsExactly("blip", FollowStatus.NONE)
    }

    @Test
    fun `the back-off starts at thirty seconds, doubles, and stops at ten minutes`() {
        assertThat(followLookupBackoffMillis(1)).isEqualTo(30_000L)
        assertThat(followLookupBackoffMillis(2)).isEqualTo(60_000L)
        assertThat(followLookupBackoffMillis(3)).isEqualTo(120_000L)
        assertThat(followLookupBackoffMillis(5)).isEqualTo(480_000L)
        assertThat(followLookupBackoffMillis(6)).isEqualTo(600_000L)
        assertThat(followLookupBackoffMillis(FAR_FUTURE_FAILURES)).isEqualTo(600_000L)
    }

    private companion object {
        const val FAR_FUTURE_FAILURES = 1_000
    }
}
