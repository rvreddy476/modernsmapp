package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.ResumableUploader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * One worker run, minus WorkManager (2026-11-07).
 *
 * What this protects: an exception from a step — the lapsed Photo Picker
 * grant's `SecurityException` (audit 2026-10-30) — is RECORDED as a
 * retryable failure and never escapes, so the queue behind it is not
 * cancelled and the tile offers Retry instead of "Preparing" forever; and a
 * transient failure is retried by the worker a bounded number of times
 * before the user is asked.
 */
class ReelPublishRunTest {

    private class Store : ReelPublishStore {
        val records = mutableListOf<PendingReelPublish>()
        override suspend fun loadAll(): List<PendingReelPublish> = records.toList()
        override suspend fun save(pending: PendingReelPublish) {
            val index = records.indexOfFirst { it.creationKey == pending.creationKey }
            if (index < 0) records += pending else records[index] = pending
        }
        override suspend fun remove(creationKey: String) {
            records.removeAll { it.creationKey == creationKey }
        }
    }

    private class Files : ReelPublishFiles {
        val deleted = mutableListOf<String?>()
        override suspend fun stashVideo(uri: String, creationKey: String): StashResult = StashResult.Unreadable
        override fun exportTarget(creationKey: String): String = "/cache/$creationKey.video"
        override suspend fun writeCover(bytes: ByteArray, creationKey: String): String? = null
        override fun openVideo(path: String, mimeType: String): PickedMedia? = null
        override suspend fun readBytes(path: String): ByteArray? = null
        override suspend fun delete(paths: List<String?>) {
            deleted += paths
        }
    }

    private val store = Store()
    private val files = Files()
    private val tracker = ReelPublishTracker()
    private val discards = ReelPublishDiscards()
    private val pending = PendingReelPublish(creationKey = "key-1", videoUri = "content://media/picker/0/1")

    private fun runner(step: suspend (PendingReelPublish) -> ReelPublishPipeline.Outcome) =
        ReelPublishRun({ record, _ -> step(record) }, store, files, tracker, discards)

    @Test
    fun `a step that throws is recorded as a retryable failure, and the run still finishes`() = runTest {
        store.save(pending)

        val end = runner { throw SecurityException("Permission Denial: no grant") }.run(pending, attempt = 0)

        assertThat(end).isEqualTo(ReelPublishRun.End.Finished(failure = ReelPublishRun.UNEXPECTED_FAILURE))
        assertThat(store.records.single().failure)
            .isEqualTo(PendingReelFailure(ReelPublishRun.UNEXPECTED_FAILURE, retryable = true))
        assertThat(tracker.stateOf("key-1"))
            .isEqualTo(ReelPublishState.Failed(ReelPublishRun.UNEXPECTED_FAILURE, retryable = true))
    }

    @Test
    fun `a throw after the user discarded writes nothing back`() = runTest {
        discards.discard("key-1")

        val end = runner { throw IllegalStateException("boom") }.run(pending, attempt = 0)

        assertThat(end).isEqualTo(ReelPublishRun.End.Finished())
        assertThat(store.records).isEmpty()
    }

    @Test
    fun `WorkManager stopping the run is not a failure`() = runTest {
        store.save(pending)

        val thrown = runCatching {
            runner { throw CancellationException("stopped") }.run(pending, attempt = 0)
        }.exceptionOrNull()

        assertThat(thrown).isInstanceOf(CancellationException::class.java)
        assertThat(store.records.single().failure).isNull()
    }

    @Test
    fun `a transient failure is retried by the worker with the record left unfailed`() = runTest {
        val failed = ReelPublishPipeline.Outcome.Failed("The upload didn't finish. Try again.", true, transient = true)
        store.save(pending.copy(failure = PendingReelFailure(failed.message, retryable = true)))

        val end = runner { failed }.run(pending, attempt = 0)

        assertThat(end).isEqualTo(ReelPublishRun.End.RetryLater)
        assertThat(store.records.single().failure).isNull()
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Preparing)
    }

    @Test
    fun `the retries run out, and then the user is asked`() = runTest {
        val failed = ReelPublishPipeline.Outcome.Failed("The upload didn't finish. Try again.", true, transient = true)
        store.save(pending.copy(failure = PendingReelFailure(failed.message, retryable = true)))

        val end = runner { failed }.run(pending, attempt = ReelPublishRun.AUTO_RETRIES)

        assertThat(end).isEqualTo(ReelPublishRun.End.Finished(failure = failed.message))
        assertThat(store.records.single().failure).isNotNull()
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Failed(failed.message, retryable = true))
    }

    @Test
    fun `a long upload that moved on before losing the network starts a fresh run, however many came before`() =
        runTest {
            val lost = ReelPublishPipeline.Outcome.Failed(
                ResumableUploader.PART_FAILED,
                retryable = true,
                transient = true,
                progressed = true,
            )
            store.save(pending.copy(failure = PendingReelFailure(lost.message, retryable = true)))

            // Past the counted retries: an upload that keeps landing parts is not given up on (M4).
            val end = runner { lost }.run(pending, attempt = ReelPublishRun.AUTO_RETRIES + 5)

            assertThat(end).isEqualTo(ReelPublishRun.End.Continue)
            assertThat(store.records.single().failure).isNull()
            assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Preparing)
        }

    @Test
    fun `a lost network that landed nothing is counted, and a refusal never continues`() {
        val stuck = ReelPublishPipeline.Outcome.Failed("x", retryable = true, transient = true, progressed = false)
        val refusedAfterParts = ReelPublishPipeline.Outcome.Failed("x", retryable = true, progressed = true)

        assertThat(ReelPublishRun.continuesAfterProgress(stuck)).isFalse()
        assertThat(ReelPublishRun.retriesAutomatically(stuck, attempt = ReelPublishRun.AUTO_RETRIES)).isFalse()
        assertThat(ReelPublishRun.continuesAfterProgress(refusedAfterParts)).isFalse()
    }

    @Test
    fun `only the network's failures are retried by themselves`() {
        val transient = ReelPublishPipeline.Outcome.Failed("x", retryable = true, transient = true)
        val usersCall = ReelPublishPipeline.Outcome.Failed("x", retryable = true)
        val final = ReelPublishPipeline.Outcome.Failed("x", retryable = false)

        assertThat(ReelPublishRun.retriesAutomatically(transient, attempt = 0)).isTrue()
        assertThat(ReelPublishRun.retriesAutomatically(transient, attempt = ReelPublishRun.AUTO_RETRIES - 1)).isTrue()
        assertThat(ReelPublishRun.retriesAutomatically(transient, attempt = ReelPublishRun.AUTO_RETRIES)).isFalse()
        assertThat(ReelPublishRun.retriesAutomatically(usersCall, attempt = 0)).isFalse()
        assertThat(ReelPublishRun.retriesAutomatically(final, attempt = 0)).isFalse()
    }

    @Test
    fun `a published run clears the record and its files`() = runTest {
        val stashed = pending.copy(videoPath = "/files/key-1.video", coverPath = "/cache/key-1.jpg")
        store.save(stashed)

        val end = runner { ReelPublishPipeline.Outcome.Published("post-9") }.run(pending, attempt = 0)

        assertThat(end).isEqualTo(ReelPublishRun.End.Finished(postId = "post-9"))
        assertThat(store.records).isEmpty()
        assertThat(files.deleted).containsExactly("/files/key-1.video", "/cache/key-1.jpg")
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Published("post-9"))
    }
}
