package com.us.android.core.creator.engine

import android.content.Context
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import java.io.File
import java.io.InputStream
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * CS-A-LB-3 — quota admission is atomic across DIFFERENT asset ids, and low
 * storage refuses cleanly.
 *
 * ## THE RACE THE REVIEW EXECUTED, MADE PERMANENT
 *
 * The vault used to lock per asset id. Two imports of different assets ran
 * concurrently, each snapshotted `totalBytes()` while the vault was still
 * empty, each passed its own check against that stale snapshot, and together
 * they committed 12 KiB into an 8 KiB cap. The review proved it with a
 * barrier; this test keeps that barrier permanently.
 *
 * ## HOW THE BARRIER WORKS WITHOUT DEADLOCKING THE FIX
 *
 * Each source stream counts down a shared latch when it is OPENED and then
 * waits for the latch — with a timeout. Under the broken per-asset locking,
 * both streams open concurrently, the latch reaches zero immediately, and both
 * proceed into the race. Under the fixed vault-wide lock, the second import
 * cannot open its stream until the first finishes, so the first times out of
 * the latch and proceeds alone — serialised, exactly as designed. The timeout
 * is what lets one test distinguish the two implementations instead of hanging
 * on the correct one.
 *
 * These tests use REAL threads ([Dispatchers.IO]), not a test scheduler: a
 * single-threaded virtual clock cannot race anything, and a race that cannot
 * happen is not being tested.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SourceVaultQuotaAtomicityTest {

    private lateinit var context: Context

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        File(context.filesDir, "creator").deleteRecursively()
    }

    private fun vault(
        maxBytes: Long = CAP,
        freeBytes: () -> Long = { PLENTY_FREE },
        headroom: Long = 0L,
    ) = SourceVault(context, Dispatchers.IO, maxBytes, freeBytes, headroom)

    /** A stream that parks at open until both imports have reached their streams. */
    private class BarrierStream(
        private val payload: ByteArray,
        private val barrier: CountDownLatch,
    ) : InputStream() {
        private var position = 0
        private var awaited = false

        override fun read(): Int {
            awaitBarrierOnce()
            return if (position < payload.size) payload[position++].toInt() and BYTE_MASK else -1
        }

        override fun read(target: ByteArray, offset: Int, length: Int): Int {
            awaitBarrierOnce()
            if (position >= payload.size) return -1
            val count = minOf(length, payload.size - position)
            System.arraycopy(payload, position, target, offset, count)
            position += count
            return count
        }

        private fun awaitBarrierOnce() {
            if (awaited) return
            awaited = true
            barrier.countDown()
            // Times out rather than waits forever: under the FIXED vault the
            // second stream never opens while the first holds the write lock,
            // and this import must proceed alone rather than deadlock.
            barrier.await(BARRIER_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        }

        private companion object {
            const val BYTE_MASK = 0xFF
        }
    }

    private fun barrierUri(id: String, payload: ByteArray, barrier: CountDownLatch): Uri {
        val uri = Uri.parse("content://media/external/images/media/$id")
        Shadows.shadowOf(context.contentResolver)
            .registerInputStream(uri, BarrierStream(payload, barrier))
        return uri
    }

    // ------------------------------------------------------------------
    // THE permanent review sequence
    // ------------------------------------------------------------------

    /**
     * Two different assets, together over the cap, genuinely concurrent.
     *
     * Exactly one may commit. The committed total may never exceed the cap, no
     * temp file may survive, and the winner's bytes must verify against its
     * recorded hash.
     */
    @Test
    fun `concurrent imports of different assets cannot exceed the quota together`() {
        val vault = vault(maxBytes = CAP)
        val barrier = CountDownLatch(2)
        val payloadA = ByteArray(SIX_KIB) { 1 }
        val payloadB = ByteArray(SIX_KIB) { 2 }

        val results = runBlocking {
            listOf(
                async(Dispatchers.IO) {
                    vault.importSource(barrierUri("a", payloadA, barrier), assetId = "assetA")
                },
                async(Dispatchers.IO) {
                    vault.importSource(barrierUri("b", payloadB, barrier), assetId = "assetB")
                },
            ).awaitAll()
        }

        val committed = results.filterNotNull()

        // The invariant CS-A-LB-3 is about: the promise holds under concurrency.
        assertThat(runBlocking { vault.totalBytes() }).isAtMost(CAP)
        // 6 KiB + 6 KiB > 8 KiB, so both cannot have won.
        assertThat(committed.size).isAtMost(1)
        // Whoever won is intact and verifiable.
        committed.forEach { entry ->
            assertThat(runBlocking { vault.verify(entry.relativePath, entry.sha256) }).isTrue()
        }
        // And the loser left nothing behind.
        val leftoverTemps = File(context.filesDir, "creator/sources")
            .listFiles().orEmpty().filter { it.name.contains(".tmp") }
        assertThat(leftoverTemps).isEmpty()
    }

    /** Sequential imports under the same cap behave identically — no concurrency tax. */
    @Test
    fun `sequential imports enforce the same admission`() = runBlocking {
        val vault = vault(maxBytes = CAP)
        val first = vault.importSource(plainUri("s1", ByteArray(SIX_KIB) { 1 }), "seqA")
        val second = vault.importSource(plainUri("s2", ByteArray(SIX_KIB) { 2 }), "seqB")

        assertThat(first).isNotNull()
        assertThat(second).isNull()
        assertThat(vault.totalBytes()).isAtMost(CAP)
    }

    // ------------------------------------------------------------------
    // Low storage
    // ------------------------------------------------------------------

    /** A device already under the headroom floor is refused before any byte is written. */
    @Test
    fun `an import on a low-storage device is refused with nothing written`() = runBlocking {
        val vault = vault(
            maxBytes = VaultQuota.MAX_BYTES,
            freeBytes = { LOW_FREE },
            headroom = VaultQuota.HEADROOM_BYTES,
        )

        val entry = vault.importSource(plainUri("l1", ByteArray(SIX_KIB) { 1 }), "lowspace")

        assertThat(entry).isNull()
        val sources = File(context.filesDir, "creator/sources").listFiles().orEmpty()
        assertThat(sources).isEmpty()
    }

    /** A stream that would eat into the headroom is cut off mid-write, temp removed. */
    @Test
    fun `a stream that would breach headroom is refused mid-write`() = runBlocking {
        // 4 KiB free above the floor; the 6 KiB payload must be refused partway.
        val vault = vault(
            maxBytes = VaultQuota.MAX_BYTES,
            freeBytes = { VaultQuota.HEADROOM_BYTES + FOUR_KIB },
            headroom = VaultQuota.HEADROOM_BYTES,
        )

        val entry = vault.importSource(plainUri("h1", ByteArray(SIX_KIB) { 1 }), "headroom")

        assertThat(entry).isNull()
        assertThat(File(context.filesDir, "creator/sources").listFiles().orEmpty()).isEmpty()
    }

    /** A low-space refusal deletes nothing that already exists. */
    @Test
    fun `a low-storage refusal leaves existing entries untouched`() = runBlocking {
        var free = PLENTY_FREE
        val vault = vault(
            maxBytes = VaultQuota.MAX_BYTES,
            freeBytes = { free },
            headroom = VaultQuota.HEADROOM_BYTES,
        )
        val existing = vault.importSource(plainUri("e1", ByteArray(SIX_KIB) { 9 }), "existing")!!

        free = LOW_FREE
        val refused = vault.importSource(plainUri("e2", ByteArray(SIX_KIB) { 1 }), "newcomer")

        assertThat(refused).isNull()
        assertThat(vault.verify(existing.relativePath, existing.sha256)).isTrue()
    }

    private fun plainUri(id: String, payload: ByteArray): Uri {
        val uri = Uri.parse("content://media/external/images/media/$id")
        Shadows.shadowOf(context.contentResolver).registerInputStream(uri, payload.inputStream())
        return uri
    }

    private companion object {
        const val CAP = 8L * 1024
        const val SIX_KIB = 6 * 1024
        const val FOUR_KIB = 4L * 1024
        const val PLENTY_FREE = Long.MAX_VALUE / 2
        const val LOW_FREE = 1L * 1024 * 1024
        const val BARRIER_TIMEOUT_SECONDS = 2L
    }
}
