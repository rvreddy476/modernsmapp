package com.us.android.core.feed.offline

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/**
 * The life of an offline copy on this device, against a scripted server and
 * scripted bytes:
 *
 *   idle → requesting → saving(progress) ⇄ waiting → stored → removed
 *
 * What this protects, in the order a viewer (or a creator) would notice it
 * breaking: a save that reaches "stored" only when every byte that was
 * promised is on the device; a refused or impossible save leaving nothing
 * behind; a cancel and a remove taking the bytes and telling the server; a
 * save surviving the process; the copy going when it expires or the server
 * says so, and NOT going because the phone was offline; one account never
 * seeing another's copies; and sign-out leaving the device empty.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class OfflineCopiesTest {

    @get:Rule
    val folder = TemporaryFolder()

    private val remote = FakeRemote()
    private val store = FakeMediaStore()
    private val prefs = FakePrefs()
    private val scheduler = FakeScheduler()
    private var now = 1_000_000L
    private var viewer = VIEWER
    private var connection = OfflineConnection.UNMETERED

    private val indexFile: File get() = File(folder.root, "copies.json")

    private fun TestScope.machine() = OfflineCopies(
        remote = remote,
        store = store,
        index = OfflineIndex(file = { indexFile }),
        prefs = prefs,
        viewerId = { viewer },
        connection = { connection },
        now = { now },
        scheduler = scheduler,
        scope = backgroundScope,
        io = UnconfinedTestDispatcher(testScheduler),
    )

    private fun test(block: suspend TestScope.() -> Unit) = runTest(UnconfinedTestDispatcher(), testBody = block)

    private fun OfflineCopies.phase(postId: String = "p1"): OfflinePhase? = state.value.phaseOf(postId)

    /** p1 saved and whole on the device. */
    private suspend fun TestScope.stored(copies: OfflineCopies, postId: String = "p1", bytes: Long = 1_000L) {
        copies.save(videoPost(postId))
        store.done("$postId/video", bytes)
        runCurrent()
    }

    // ── Save ────────────────────────────────────────────────────────────

    @Test
    fun `nothing is on the device until something is saved`() = test {
        val copies = machine()
        copies.ensureLoaded()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(copies.state.value.loaded).isTrue()
        assertThat(copies.playable("p1")).isNull()
    }

    @Test
    fun `a save asks the server with this device's id, then fetches the granted stream under the post's key`() = test {
        val copies = machine()

        val result = copies.save(videoPost("p1"))

        assertThat(result).isEqualTo(OfflineSaveResult.Started)
        assertThat(remote.calls).containsExactly("grant:p1:$DEVICE")
        assertThat(store.asked.map { it.key to it.url })
            .containsExactly("p1/video" to "https://api.test/v1/media/m-p1/serve/720p")
        assertThat(copies.phase()).isEqualTo(OfflinePhase.SAVING)
        assertThat(scheduler.scheduled).isEqualTo(1)
    }

    @Test
    fun `while the grant is on the wire the copy is requesting`() = test {
        val copies = machine()
        var seen: OfflinePhase? = null
        remote.onGrant = { seen = copies.phase() }

        copies.save(videoPost("p1"))

        assertThat(seen).isEqualTo(OfflinePhase.REQUESTING)
    }

    @Test
    fun `progress is the bytes that arrived over the bytes that were granted`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))

        store.running("p1/video", bytes = 250L, total = 1_000L)
        runCurrent()

        val entry = copies.state.value.copies.getValue("p1")
        assertThat(entry.phase).isEqualTo(OfflinePhase.SAVING)
        assertThat(entry.progress).isWithin(0.001f).of(0.25f)
        assertThat(copies.playable("p1")).isNull()
    }

    @Test
    fun `a copy is stored once every granted byte is on the device, and only then can it be played`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))

        store.done("p1/video", 1_000L)
        runCurrent()

        assertThat(copies.phase()).isEqualTo(OfflinePhase.STORED)
        assertThat(copies.playable("p1")!!.sizeBytes).isEqualTo(1_000L)
        assertThat(copies.state.value.usedBytes).isEqualTo(1_000L)
        // And it is in the index: a new process finds it.
        assertThat(OfflineIndex({ indexFile }).read().copies.single().stored).isTrue()
    }

    @Test
    fun `saving twice asks the server once`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))

        val again = copies.save(videoPost("p1"))

        assertThat(again).isEqualTo(OfflineSaveResult.Started)
        assertThat(remote.calls.count { it.startsWith("grant:") }).isEqualTo(1)
        assertThat(store.asked).hasSize(1)
    }

    @Test
    fun `a reel's added sound is fetched beside its video and both must arrive`() = test {
        val copies = machine()
        remote.grants["p1"] = AppResult.Success(
            grant(
                "p1",
                now,
                kind = OfflineKind.REEL,
                sound = OfflineGrantSound(
                    stream = OfflineGrantStream("https://api.test/v1/audio/s1/serve", "audio/mp4", 0L),
                    startMs = 1_500L,
                    originalVolume = 0.0,
                    overlayVolume = 1.0,
                ),
            ),
        )
        copies.save(videoPost("p1", contentType = "flick", sound = reelSound()))

        store.done("p1/video", 1_000L)
        runCurrent()
        assertThat(copies.phase()).isEqualTo(OfflinePhase.SAVING)

        store.done("p1/sound", 300L)
        runCurrent()

        assertThat(store.asked.map { it.key }).containsExactly("p1/video", "p1/sound")
        assertThat(copies.phase()).isEqualTo(OfflinePhase.STORED)
        val copy = copies.playable("p1")!!
        assertThat(copy.kind).isEqualTo(OfflineKind.REEL)
        assertThat(copy.sizeBytes).isEqualTo(1_300L)
        assertThat(copy.sound!!.originalVolume).isEqualTo(0.0)
    }

    @Test
    fun `the poster and the caption tracks are stored beside the copy`() = test {
        val copies = machine()
        remote.grants["p1"] = AppResult.Success(
            grant(
                "p1",
                now,
                captions = listOf(OfflineGrantCaption("en", "English", "https://api.test/v1/subtitles/m/en.vtt")),
            ),
        )

        stored(copies)

        assertThat(store.files).containsExactly("p1/poster", "p1/captions_en.vtt")
        val copy = copies.playable("p1")!!
        assertThat(copy.posterFile).endsWith("poster")
        assertThat(copy.captions.single().file).endsWith("captions_en.vtt")
        assertThat(copy.playback().captions.single().language).isEqualTo("en")
    }

    // ── Refused ─────────────────────────────────────────────────────────

    @Test
    fun `a save the server refuses leaves nothing behind and says why`() = test {
        val copies = machine()
        remote.grants["p1"] = AppResult.Failure(AppError.Forbidden(code = "OFFLINE_NOT_ALLOWED"))

        val result = copies.save(videoPost("p1"))

        assertThat(result).isEqualTo(OfflineSaveResult.Refused("The creator hasn't allowed saving this offline."))
        assertThat(copies.phase()).isNull()
        assertThat(store.asked).isEmpty()
        assertThat(indexFile.exists()).isFalse()
    }

    @Test
    fun `a signed-out viewer saves nothing and the server is not asked`() = test {
        viewer = ""
        val copies = machine()

        val result = copies.save(videoPost("p1"))

        assertThat(result).isInstanceOf(OfflineSaveResult.Refused::class.java)
        assertThat(remote.calls).isEmpty()
    }

    @Test
    fun `with no room for the copy nothing is fetched and the grant is given back`() = test {
        val copies = machine()
        store.usable = STORAGE_RESERVE_BYTES + 999L

        val result = copies.save(videoPost("p1"))

        assertThat(result).isInstanceOf(OfflineSaveResult.Refused::class.java)
        assertThat((result as OfflineSaveResult.Refused).message).contains("space")
        assertThat(store.asked).isEmpty()
        assertThat(copies.phase()).isNull()
        assertThat(remote.calls).containsExactly("grant:p1:$DEVICE", "remove:p1:$DEVICE").inOrder()
    }

    @Test
    fun `storage that cannot be opened is a save that could not be made, never a crash`() = test {
        val copies = machine()
        store.broken = true

        val result = copies.save(videoPost("p1"))

        assertThat(result).isEqualTo(OfflineSaveResult.Refused(COULD_NOT_SAVE_OFFLINE))
    }

    // ── Wi-Fi only ──────────────────────────────────────────────────────

    @Test
    fun `wifi only is handed to the fetcher before anything is fetched, and a metered save says it waits`() = test {
        val copies = machine()
        connection = OfflineConnection.METERED

        val result = copies.save(videoPost("p1"))

        assertThat(result).isEqualTo(OfflineSaveResult.WaitingForWifi)
        assertThat(store.wifiOnly).isTrue()

        store.waiting("p1/video")
        runCurrent()

        assertThat(copies.phase()).isEqualTo(OfflinePhase.WAITING)
    }

    @Test
    fun `with the switch off a metered save starts`() = test {
        val copies = machine()
        connection = OfflineConnection.METERED
        prefs.setWifiOnly(false)

        val result = copies.save(videoPost("p1"))

        assertThat(result).isEqualTo(OfflineSaveResult.Started)
        assertThat(store.wifiOnly).isFalse()
    }

    @Test
    fun `flipping the switch reaches the fetcher`() = test {
        val copies = machine()
        copies.ensureLoaded()

        copies.setWifiOnly(false)
        runCurrent()

        assertThat(store.wifiOnly).isFalse()
    }

    // ── What arrived ────────────────────────────────────────────────────

    @Test
    fun `a stream that is not the granted length is discarded, never stored`() = test {
        val copies = machine()
        val notices = mutableListOf<String>()
        backgroundScope.launch { copies.notices.collect { notices += it } }
        copies.save(videoPost("p1"))

        // The fetch says it finished, but 400 of the 1000 granted bytes are there.
        store.done("p1/video", 400L)
        runCurrent()

        assertThat(copies.phase()).isNull()
        assertThat(copies.playable("p1")).isNull()
        assertThat(store.removed).contains("p1/video")
        assertThat(store.stored).isEmpty()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
        assertThat(notices).containsExactly(COULD_NOT_SAVE_OFFLINE)
        assertThat(OfflineIndex({ indexFile }).read().copies).isEmpty()
    }

    @Test
    fun `a fetch that was given up on is discarded and the server is told`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))

        store.failed("p1/video")
        runCurrent()

        assertThat(copies.phase()).isNull()
        assertThat(store.removed).contains("p1/video")
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
    }

    // ── Cancel and remove ───────────────────────────────────────────────

    @Test
    fun `cancelling a save takes the partial bytes and tells the server`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))
        store.running("p1/video", 250L, 1_000L)
        runCurrent()

        copies.remove("p1")

        assertThat(copies.phase()).isNull()
        assertThat(store.removed).containsExactly("p1/video")
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
        assertThat(scheduler.cancelled).isEqualTo(1)
    }

    @Test
    fun `removing a stored copy takes its bytes and files and tells the server`() = test {
        val copies = machine()
        stored(copies)

        copies.remove("p1")

        assertThat(copies.playable("p1")).isNull()
        assertThat(store.stored).isEmpty()
        assertThat(store.files).isEmpty()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
        assertThat(OfflineIndex({ indexFile }).read().copies).isEmpty()
    }

    @Test
    fun `a remove with no network still removes the copy from the device`() = test {
        val copies = machine()
        stored(copies)
        remote.removeFails = true

        copies.remove("p1")

        assertThat(copies.phase()).isNull()
        assertThat(store.stored).isEmpty()
    }

    @Test
    fun `remove all empties the device and tells the server for each copy`() = test {
        val copies = machine()
        stored(copies, "p1")
        stored(copies, "p2")

        copies.removeAll()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(store.removedAll).isEqualTo(1)
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE", "remove:p2:$DEVICE")
    }

    // ── Resume ──────────────────────────────────────────────────────────

    @Test
    fun `a save the process died under is picked back up on the next start`() = test {
        machine().save(videoPost("p1"))
        val asked = store.asked.size

        // A new process: the same index, a fresh machine.
        val next = machine()
        next.start()
        runCurrent()

        assertThat(store.asked.size).isEqualTo(asked + 1)
        assertThat(store.asked.last().key).isEqualTo("p1/video")
        assertThat(next.phase()).isEqualTo(OfflinePhase.SAVING)
        // And nothing was asked of the server again: the grant is in the index.
        assertThat(remote.calls.count { it.startsWith("grant:") }).isEqualTo(1)
    }

    @Test
    fun `a stored copy is found by the next process without fetching anything`() = test {
        stored(machine())
        val asked = store.asked.size

        val next = machine()
        next.start()
        runCurrent()

        assertThat(next.playable("p1")).isNotNull()
        assertThat(store.asked.size).isEqualTo(asked)
    }

    // ── Expiry and the server's word ────────────────────────────────────

    @Test
    fun `a copy past its expiry is deleted with no network at all`() = test {
        val copies = machine()
        stored(copies)
        remote.checkAnswers = null
        now += 31 * DAY_MS

        copies.refresh()

        assertThat(copies.phase()).isNull()
        assertThat(store.stored).isEmpty()
        assertThat(copies.playable("p1")).isNull()
    }

    @Test
    fun `an expired copy cannot be played even before it has been swept`() = test {
        val copies = machine()
        stored(copies)

        now += 31 * DAY_MS

        assertThat(copies.playable("p1")).isNull()
    }

    @Test
    fun `no answer from the server is not a revocation`() = test {
        val copies = machine()
        stored(copies)
        remote.checkAnswers = null
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(remote.calls).contains("check:$DEVICE:p1")
        assertThat(copies.phase()).isEqualTo(OfflinePhase.STORED)
        assertThat(copies.playable("p1")).isNotNull()
        assertThat(store.stored).containsKey("p1/video")
    }

    @Test
    fun `an answer that does not mention the copy is not a revocation either`() = test {
        val copies = machine()
        stored(copies)
        remote.checkAnswers = mapOf("other" to OfflineCheckAnswer.Invalid("deleted"))
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(copies.phase()).isEqualTo(OfflinePhase.STORED)
    }

    @Test
    fun `a copy the server says is no longer allowed is deleted, quietly`() = test {
        val copies = machine()
        val notices = mutableListOf<String>()
        backgroundScope.launch { copies.notices.collect { notices += it } }
        stored(copies)
        remote.checkAnswers = mapOf("p1" to OfflineCheckAnswer.Invalid("not_allowed"))
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(copies.phase()).isNull()
        assertThat(store.stored).isEmpty()
        assertThat(store.files).isEmpty()
        assertThat(notices).containsExactly(offlineRemovedNotice(1))
        assertThat(OfflineIndex({ indexFile }).read().copies).isEmpty()
    }

    @Test
    fun `a confirmed copy is kept, takes the server's expiry and is not asked about again until due`() = test {
        val copies = machine()
        stored(copies)
        val sooner = now + 5 * DAY_MS
        remote.checkAnswers = mapOf("p1" to OfflineCheckAnswer.Valid(sooner))
        now += 3 * DAY_MS

        copies.refresh()
        copies.refresh()

        assertThat(copies.playable("p1")!!.expiresAtMs).isEqualTo(sooner)
        assertThat(remote.calls.count { it.startsWith("check:") }).isEqualTo(1)
    }

    @Test
    fun `a copy the server was just asked about is not asked about again`() = test {
        val copies = machine()
        stored(copies)

        copies.refresh()

        assertThat(remote.calls.none { it.startsWith("check:") }).isTrue()
    }

    @Test
    fun `a forced check asks about every copy`() = test {
        val copies = machine()
        stored(copies, "p1")
        stored(copies, "p2")

        copies.refresh(force = true)

        assertThat(remote.calls).contains("check:$DEVICE:p1,p2")
    }

    @Test
    fun `copies the server still holds that the device no longer has are given back`() = test {
        val copies = machine()
        stored(copies)
        remote.checkAnswers = mapOf("p1" to OfflineCheckAnswer.Valid(null))
        remote.held = listOf("p1", "orphan")
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(remote.removes()).containsExactly("remove:orphan:$DEVICE")
        assertThat(copies.phase()).isEqualTo(OfflinePhase.STORED)
    }

    // ── One viewer ──────────────────────────────────────────────────────

    @Test
    fun `another account on this device finds nothing, and the first account's bytes are deleted`() = test {
        stored(machine())

        viewer = "someone-else"
        val next = machine()
        next.start()
        runCurrent()

        assertThat(next.state.value.copies).isEmpty()
        assertThat(next.playable("p1")).isNull()
        assertThat(store.removedAll).isEqualTo(1)
        assertThat(indexFile.exists()).isFalse()
    }

    @Test
    fun `a signed-out reader of the same process sees no copies`() = test {
        val copies = machine()
        stored(copies)

        viewer = ""
        copies.refresh()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(copies.playable("p1")).isNull()
    }

    // ── Sign-out ────────────────────────────────────────────────────────

    @Test
    fun `sign-out wipes every copy and the index, and tells the server for each`() = test {
        val copies = machine()
        stored(copies, "p1")
        copies.save(videoPost("p2"))

        copies.wipeForSignOut()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(store.removedAll).isEqualTo(1)
        assertThat(store.stored).isEmpty()
        assertThat(indexFile.exists()).isFalse()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE", "remove:p2:$DEVICE")
        assertThat(scheduler.cancelled).isAtLeast(1)
    }

    @Test
    fun `sign-out with no network still wipes the device`() = test {
        val copies = machine()
        stored(copies)
        remote.removeFails = true

        copies.wipeForSignOut()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(store.stored).isEmpty()
        assertThat(indexFile.exists()).isFalse()
    }

    /** The index goes even when the bytes cannot be reached: what the app no longer lists cannot be played. */
    @Test
    fun `sign-out forgets its copies even when the storage cannot be opened`() = test {
        val copies = machine()
        stored(copies)
        store.broken = true

        copies.wipeForSignOut()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(copies.playable("p1")).isNull()
        assertThat(indexFile.exists()).isFalse()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
    }

    @Test
    fun `sign-out wipes a process that never looked at its copies`() = test {
        stored(machine())

        // A new process that goes straight to sign-out.
        machine().wipeForSignOut()

        assertThat(store.removedAll).isEqualTo(1)
        assertThat(indexFile.exists()).isFalse()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
    }
}
