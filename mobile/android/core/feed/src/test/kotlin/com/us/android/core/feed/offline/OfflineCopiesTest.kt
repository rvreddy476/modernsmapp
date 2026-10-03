package com.us.android.core.feed.offline

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

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
 * seeing another's copies; sign-out keeping the copies for 48 hours, shown
 * to nobody, and deleting them after; and a copy being renewed while the
 * device is online, once a day at most and never in a burst.
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

    /** Who is signed in: an id, blank for nobody, null while the session is not known yet. */
    private val session = MutableStateFlow<String?>(VIEWER)
    private var viewer: String
        get() = session.value.orEmpty()
        set(value) {
            session.value = value
        }
    private var connection = OfflineConnection.UNMETERED

    private val indexFile: File get() = File(folder.root, "copies.json")

    /** The running "process": a new [machine] ends the one before it, as a restart would. */
    private var process: Job? = null

    private fun TestScope.machine(): OfflineCopies {
        process?.cancel()
        val job = Job(backgroundScope.coroutineContext.job).also { process = it }
        return OfflineCopies(
            remote = remote,
            store = store,
            index = OfflineIndex(file = { indexFile }),
            prefs = prefs,
            viewerId = { session.value },
            viewerChanges = { session },
            connection = { connection },
            now = { now },
            scheduler = scheduler,
            scope = CoroutineScope(backgroundScope.coroutineContext + job),
            io = UnconfinedTestDispatcher(testScheduler),
        )
    }

    /** The process dies: nothing reacts to what happens next until a new [machine] starts. */
    private fun kill() {
        process?.cancel()
    }

    private fun index() = OfflineIndex({ indexFile }).read()

    private fun test(block: suspend TestScope.() -> Unit) = runTest(UnconfinedTestDispatcher(), testBody = block)

    private fun OfflineCopies.phase(postId: String = "p1"): OfflinePhase? = state.value.phaseOf(postId)

    /** p1 saved and whole on the device. */
    private suspend fun TestScope.stored(copies: OfflineCopies, postId: String = "p1", bytes: Long = 1_000L) {
        copies.save(videoPost(postId))
        store.done("$viewer/$postId/video", bytes)
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
            .containsExactly("$VIEWER/p1/video" to "https://api.test/v1/media/m-p1/serve/720p")
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

        store.running("$VIEWER/p1/video", bytes = 250L, total = 1_000L)
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

        store.done("$VIEWER/p1/video", 1_000L)
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

        store.done("$VIEWER/p1/video", 1_000L)
        runCurrent()
        assertThat(copies.phase()).isEqualTo(OfflinePhase.SAVING)

        store.done("$VIEWER/p1/sound", 300L)
        runCurrent()

        assertThat(store.asked.map { it.key }).containsExactly("$VIEWER/p1/video", "$VIEWER/p1/sound")
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

        assertThat(store.files).containsExactly("$VIEWER-p1/poster", "$VIEWER-p1/captions_en.vtt")
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

        store.waiting("$VIEWER/p1/video")
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
        store.done("$VIEWER/p1/video", 400L)
        runCurrent()

        assertThat(copies.phase()).isNull()
        assertThat(copies.playable("p1")).isNull()
        assertThat(store.removed).contains("$VIEWER/p1/video")
        assertThat(store.stored).isEmpty()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
        assertThat(notices).containsExactly(COULD_NOT_SAVE_OFFLINE)
        assertThat(OfflineIndex({ indexFile }).read().copies).isEmpty()
    }

    @Test
    fun `a fetch that was given up on is discarded and the server is told`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))

        store.failed("$VIEWER/p1/video")
        runCurrent()

        assertThat(copies.phase()).isNull()
        assertThat(store.removed).contains("$VIEWER/p1/video")
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
    }

    // ── Cancel and remove ───────────────────────────────────────────────

    @Test
    fun `cancelling a save takes the partial bytes and tells the server`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))
        store.running("$VIEWER/p1/video", 250L, 1_000L)
        runCurrent()

        copies.remove("p1")

        assertThat(copies.phase()).isNull()
        assertThat(store.removed).containsExactly("$VIEWER/p1/video")
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
        assertThat(store.asked.last().key).isEqualTo("$VIEWER/p1/video")
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
        assertThat(store.stored).containsKey("$VIEWER/p1/video")
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
        // Not renewable, so the expiry the check gave is the one that stands.
        remote.checkAnswers = mapOf("p1" to OfflineCheckAnswer.Valid(sooner, renewable = false))
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
    fun `another account on this device never sees, plays or counts the first account's copies`() = test {
        stored(machine())

        viewer = OTHER
        val next = machine()
        next.start()
        runCurrent()

        assertThat(next.state.value.copies).isEmpty()
        assertThat(next.state.value.usedBytes).isEqualTo(0L)
        assertThat(next.playable("p1")).isNull()
        // Nothing of the first account's was asked about under the second one's session.
        assertThat(remote.calls.none { it.startsWith("check:") || it.startsWith("remove:") }).isTrue()
        // Held, not deleted: the first account has 48 hours to come back.
        assertThat(store.stored).containsKey("$VIEWER/p1/video")
        assertThat(index().ownerId).isEqualTo(OTHER)
        assertThat(index().held.single().ownerId).isEqualTo(VIEWER)
        assertThat(index().held.single().signedOutAtMs).isEqualTo(now)
    }

    @Test
    fun `a second account's copy of the same post has its own bytes, and removing it leaves the first account's`() =
        test {
            stored(machine())
            machine().holdForSignOut()
            viewer = OTHER
            val theirs = machine()

            stored(theirs)

            // A grant of its own and a fetch of its own: the bytes already on the device were not handed over.
            assertThat(remote.calls.count { it == "grant:p1:$DEVICE" }).isEqualTo(2)
            assertThat(store.asked.map { it.key }).containsExactly("$VIEWER/p1/video", "$OTHER/p1/video")
            assertThat(theirs.state.value.copies.keys).containsExactly("p1")
            assertThat(theirs.state.value.usedBytes).isEqualTo(1_000L)

            theirs.remove("p1")

            assertThat(store.stored.keys).containsExactly("$VIEWER/p1/video")
            assertThat(index().held.single().copies.single().postId).isEqualTo("p1")
        }

    /** The Offline page reads the list before anything has settled whose copies are in front. */
    @Test
    fun `a different account sees nothing even before the index has been settled`() = test {
        stored(machine())
        kill()

        viewer = OTHER
        val next = machine()
        next.ensureLoaded()

        assertThat(next.state.value.copies).isEmpty()
        assertThat(next.state.value.usedBytes).isEqualTo(0L)
        assertThat(next.playable("p1")).isNull()
    }

    @Test
    fun `the server's answers about one account's copies never touch another's`() = test {
        // Both accounts hold a copy of p1 on this device; the first one is signed in.
        stored(machine())
        machine().holdForSignOut()
        viewer = OTHER
        stored(machine())
        viewer = ""
        runCurrent()
        viewer = VIEWER
        val copies = machine()
        copies.start()
        runCurrent()
        // While the first account's check is on the wire, the second account takes the phone.
        remote.checkAnswers = mapOf("p1" to OfflineCheckAnswer.Invalid("private"))
        remote.onCheck = {
            viewer = OTHER
            testScheduler.runCurrent()
        }

        copies.refresh(force = true)

        // "p1 is private" was said to the first account. The second account's p1 is still there.
        assertThat(copies.playable("p1")).isNotNull()
        assertThat(store.stored.keys).containsExactly("$VIEWER/p1/video", "$OTHER/p1/video")
    }

    @Test
    fun `a grant that arrives after sign-out is given back, not kept`() = test {
        val copies = machine()
        remote.onGrant = { copies.holdForSignOut() }

        val result = copies.save(videoPost("p1"))

        assertThat(result).isEqualTo(OfflineSaveResult.Refused(COULD_NOT_SAVE_OFFLINE))
        assertThat(store.asked).isEmpty()
        assertThat(index().copies).isEmpty()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE")
    }

    @Test
    fun `remove all takes only the signed-in account's copies`() = test {
        stored(machine())
        machine().holdForSignOut()
        viewer = OTHER
        val theirs = machine()
        stored(theirs, "p2")

        theirs.removeAll()

        assertThat(store.removedAll).isEqualTo(0)
        assertThat(store.stored.keys).containsExactly("$VIEWER/p1/video")
        assertThat(remote.removes()).containsExactly("remove:p2:$DEVICE")
        assertThat(index().held.single().copies).hasSize(1)
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

    //
    // founder, 2026-10-02: sign-out no longer wipes. The copies stay for 48 hours, shown to nobody; the
    // same account coming back inside that finds them; after it they are deleted. The four tests that
    // asserted an empty device after sign-out are replaced by the ones below.

    @Test
    fun `sign-out keeps the stored copies on the device, stamped and hidden, and tells the server nothing`() = test {
        val copies = machine()
        stored(copies, "p1")

        copies.holdForSignOut()

        // Hidden at once, while the session is still the owner's.
        assertThat(copies.state.value.copies).isEmpty()
        assertThat(copies.state.value.usedBytes).isEqualTo(0L)
        assertThat(copies.playable("p1")).isNull()
        // Kept: the bytes, the row, and when the 48 hours began.
        assertThat(store.removedAll).isEqualTo(0)
        assertThat(store.stored).containsKey("$VIEWER/p1/video")
        assertThat(index().copies.single().postId).isEqualTo("p1")
        assertThat(index().ownerId).isEqualTo(VIEWER)
        assertThat(index().signedOutAtMs).isEqualTo(now)
        assertThat(remote.removes()).isEmpty()
        // The background check stays: it is what deletes the copies when the 48 hours are up.
        assertThat(scheduler.cancelled).isEqualTo(0)
    }

    @Test
    fun `sign-out cancels a save still in flight and tells the server for it alone`() = test {
        val copies = machine()
        stored(copies, "p1")
        copies.save(videoPost("p2"))

        copies.holdForSignOut()

        assertThat(store.removed).containsExactly("$VIEWER/p2/video")
        assertThat(remote.removes()).containsExactly("remove:p2:$DEVICE")
        assertThat(index().copies.map { it.postId }).containsExactly("p1")
    }

    @Test
    fun `sign-out with nothing stored leaves nothing to wake up for`() = test {
        val copies = machine()
        copies.save(videoPost("p1"))

        copies.holdForSignOut()

        assertThat(index().copies).isEmpty()
        assertThat(index().signedOutAtMs).isNull()
        assertThat(scheduler.cancelled).isEqualTo(1)
    }

    @Test
    fun `sign-out hides the copies even when the storage cannot be opened`() = test {
        val copies = machine()
        stored(copies)
        store.broken = true

        copies.holdForSignOut()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(copies.playable("p1")).isNull()
        assertThat(index().signedOutAtMs).isEqualTo(now)
    }

    @Test
    fun `sign-out stamps a process that never looked at its copies`() = test {
        stored(machine())

        // A new process that goes straight to sign-out.
        machine().holdForSignOut()

        assertThat(store.stored).containsKey("$VIEWER/p1/video")
        assertThat(index().signedOutAtMs).isEqualTo(now)
    }

    @Test
    fun `the same account signing back in within 48 hours finds its copies again`() = test {
        val copies = machine()
        stored(copies)
        now += DAY_MS
        copies.holdForSignOut()
        viewer = ""
        runCurrent()
        assertThat(copies.state.value.copies).isEmpty()

        now += SIGN_OUT_HOLD_MS - 1
        viewer = VIEWER
        runCurrent()

        assertThat(copies.phase()).isEqualTo(OfflinePhase.STORED)
        assertThat(copies.playable("p1")).isNotNull()
        assertThat(index().signedOutAtMs).isNull()
        assertThat(remote.removes()).isEmpty()
        // Still subject to the server's word: its last answer aged out meanwhile, so it was asked at once.
        assertThat(remote.calls).contains("check:$DEVICE:p1")
    }

    @Test
    fun `the same account back within 48 hours in a new process finds its copies again`() = test {
        stored(machine())
        machine().holdForSignOut()

        now += DAY_MS
        val next = machine()
        next.start()
        runCurrent()

        assertThat(next.playable("p1")).isNotNull()
        assertThat(index().signedOutAtMs).isNull()
    }

    @Test
    fun `a copy that expires while it is held is deleted then, not kept for its 48 hours`() = test {
        stored(machine())
        machine().holdForSignOut()
        viewer = OTHER
        machine().refresh()
        kill()
        // Held behind the second account, and its thirty days run out a day into the 48 hours.
        val heldSet = index().held.single()
        OfflineIndex({ indexFile }).write(
            index().copy(held = listOf(heldSet.copy(copies = heldSet.copies.map { it.copy(expiresAtMs = now + 1) }))),
        )

        // The second account is still the one signed in; being held never outlasts the grant.
        now += DAY_MS
        machine().refresh()

        assertThat(store.stored).isEmpty()
        assertThat(index().held).isEmpty()

        viewer = VIEWER
        val next = machine()
        next.start()
        runCurrent()

        assertThat(next.state.value.copies).isEmpty()
    }

    @Test
    fun `48 hours after sign-out the copies are deleted on the next start, bytes and rows`() = test {
        stored(machine())
        machine().holdForSignOut()
        viewer = ""

        now += SIGN_OUT_HOLD_MS
        machine().start()
        runCurrent()

        assertThat(store.stored).isEmpty()
        assertThat(store.files).isEmpty()
        assertThat(index().copies).isEmpty()
        assertThat(index().held).isEmpty()
        // Nobody is signed in: there is no session to tell the server with, and its rows age out.
        assertThat(remote.removes()).isEmpty()
        assertThat(scheduler.cancelled).isAtLeast(1)
    }

    @Test
    fun `one millisecond short of 48 hours nothing is deleted`() = test {
        stored(machine())
        machine().holdForSignOut()
        viewer = ""

        now += SIGN_OUT_HOLD_MS - 1
        machine().start()
        runCurrent()

        assertThat(store.stored).containsKey("$VIEWER/p1/video")
        assertThat(index().copies).hasSize(1)
    }

    @Test
    fun `the background check deletes copies held past 48 hours with nobody signed in`() = test {
        stored(machine())
        machine().holdForSignOut()
        viewer = ""
        val worker = machine()

        now += SIGN_OUT_HOLD_MS
        // What OfflineCheckWorker runs.
        worker.refresh()

        assertThat(store.stored).isEmpty()
        assertThat(index().copies).isEmpty()
    }

    @Test
    fun `the same account back after 48 hours finds nothing, and the server is told for each copy`() = test {
        stored(machine(), "p1")
        stored(machine(), "p2")
        machine().holdForSignOut()
        viewer = ""
        val copies = machine()
        copies.start()
        runCurrent()

        now += SIGN_OUT_HOLD_MS
        viewer = VIEWER
        runCurrent()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(store.stored).isEmpty()
        assertThat(remote.removes()).containsExactly("remove:p1:$DEVICE", "remove:p2:$DEVICE")
    }

    @Test
    fun `a different account signing in after 48 hours deletes the copies and never sees them`() = test {
        stored(machine())
        machine().holdForSignOut()
        viewer = ""
        val copies = machine()
        copies.start()
        runCurrent()

        now += SIGN_OUT_HOLD_MS
        viewer = OTHER
        runCurrent()

        assertThat(copies.state.value.copies).isEmpty()
        assertThat(store.stored).isEmpty()
        assertThat(index().held).isEmpty()
        // A DELETE is the caller's own: under another account's session it is not sent at all.
        assertThat(remote.removes()).isEmpty()
    }

    @Test
    fun `a different account inside the 48 hours never sees the copies, and their owner still gets them back`() =
        test {
            stored(machine())
            machine().holdForSignOut()
            viewer = ""
            val copies = machine()
            copies.start()
            runCurrent()

            now += DAY_MS / 2
            viewer = OTHER
            runCurrent()
            copies.refresh(force = true)

            assertThat(copies.state.value.copies).isEmpty()
            assertThat(copies.state.value.usedBytes).isEqualTo(0L)
            assertThat(copies.playable("p1")).isNull()
            assertThat(remote.calls.none { it.startsWith("check:") }).isTrue()

            now += DAY_MS / 2
            viewer = ""
            runCurrent()
            viewer = VIEWER
            runCurrent()

            assertThat(copies.playable("p1")).isNotNull()
            // The 48 hours ran from the sign-out, not from the second account's visit.
            assertThat(index().held).isEmpty()
        }

    @Test
    fun `the 48 hours run from the sign-out, not from another account's visit`() = test {
        stored(machine())
        machine().holdForSignOut()
        val signedOutAt = now

        kill()
        now += DAY_MS
        viewer = OTHER
        machine().refresh()

        assertThat(index().held.single().signedOutAtMs).isEqualTo(signedOutAt)
    }

    /** A session that ended without the sign-out path: a token that expired, storage that was cleared. */
    @Test
    fun `copies with an owner and nobody signed in are stamped on start`() = test {
        stored(machine())

        kill()
        viewer = ""
        now += 5 * DAY_MS
        val next = machine()
        next.start()
        runCurrent()

        // Stamped now, not deleted: the 48 hours begin when the app noticed.
        assertThat(index().signedOutAtMs).isEqualTo(now)
        assertThat(store.stored).containsKey("$VIEWER/p1/video")
        assertThat(next.state.value.copies).isEmpty()

        now += SIGN_OUT_HOLD_MS
        machine().start()
        runCurrent()

        assertThat(store.stored).isEmpty()
    }

    @Test
    fun `a session that ends while the app is running stamps the copies at once`() = test {
        val copies = machine()
        stored(copies)

        now += DAY_MS
        viewer = ""
        runCurrent()

        assertThat(index().signedOutAtMs).isEqualTo(now)
        assertThat(copies.state.value.copies).isEmpty()
    }

    @Test
    fun `nothing is stamped or deleted while the session is not known yet`() = test {
        stored(machine())

        kill()
        session.value = null
        now += 5 * DAY_MS
        machine().start()
        runCurrent()

        assertThat(index().signedOutAtMs).isNull()
        assertThat(store.stored).containsKey("$VIEWER/p1/video")
    }

    // ── Renewal ─────────────────────────────────────────────────────────
    //
    // founder, 2026-10-02: a copy lasts thirty days from its grant, and the grant is repeated while the
    // device is online, so it only runs out on a phone that stayed offline.

    /** A check the server answered "valid" for every stored copy. */
    private fun confirmAll(vararg postIds: String, renewable: Boolean? = null) {
        remote.checkAnswers = postIds.associateWith {
            if (renewable == null) OfflineCheckAnswer.Valid(null) else OfflineCheckAnswer.Valid(null, renewable)
        }
    }

    private fun grants(): List<String> = remote.calls.filter { it.startsWith("grant:") }

    @Test
    fun `after a check the server answered, a confirmed copy is renewed and takes the new expiry`() = test {
        val copies = machine()
        stored(copies)
        val firstExpiry = copies.playable("p1")!!.expiresAtMs
        confirmAll("p1")
        now += 3 * DAY_MS

        copies.refresh()

        // The grant, repeated for this device, after the check.
        assertThat(remote.calls.takeLast(2)).containsExactly("held:$DEVICE", "grant:p1:$DEVICE").inOrder()
        assertThat(remote.calls.indexOf("check:$DEVICE:p1")).isLessThan(remote.calls.lastIndexOf("grant:p1:$DEVICE"))
        assertThat(copies.playable("p1")!!.expiresAtMs).isEqualTo(now + 30 * DAY_MS)
        assertThat(copies.playable("p1")!!.expiresAtMs).isGreaterThan(firstExpiry)
        // And it is in the index: the next process has the new expiry too.
        assertThat(index().copies.single().expiresAtMs).isEqualTo(now + 30 * DAY_MS)
        // No video bytes were asked for again.
        assertThat(store.asked).hasSize(1)
    }

    @Test
    fun `a copy is renewed at most once in 24 hours`() = test {
        val copies = machine()
        stored(copies)
        confirmAll("p1")
        now += 3 * DAY_MS

        copies.refresh(force = true)
        now += RENEW_INTERVAL_MS - 1
        copies.refresh(force = true)

        assertThat(grants()).hasSize(2) // The save, and one renewal.

        now += 1
        copies.refresh(force = true)

        assertThat(grants()).hasSize(3)
    }

    @Test
    fun `a copy saved less than 24 hours ago is not renewed`() = test {
        val copies = machine()
        stored(copies)
        confirmAll("p1")
        now += RENEW_INTERVAL_MS - 1

        copies.refresh(force = true)

        assertThat(grants()).hasSize(1)
    }

    @Test
    fun `a copy the server says is not renewable is not renewed`() = test {
        val copies = machine()
        stored(copies)
        val expiry = copies.playable("p1")!!.expiresAtMs
        confirmAll("p1", renewable = false)
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(grants()).hasSize(1)
        assertThat(copies.playable("p1")!!.expiresAtMs).isEqualTo(expiry)
    }

    @Test
    fun `a check that could not be made renews nothing`() = test {
        val copies = machine()
        stored(copies)
        remote.checkAnswers = null
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(grants()).hasSize(1)
    }

    @Test
    fun `a refused renewal does not delete the copy, and is not asked again that day`() = test {
        val copies = machine()
        stored(copies)
        val expiry = copies.playable("p1")!!.expiresAtMs
        confirmAll("p1")
        now += 3 * DAY_MS

        for (refusal in listOf(
            AppError.Forbidden(code = "OFFLINE_NOT_ALLOWED"),
            AppError.NotFound(),
            AppError.Unknown(statusCode = 409, code = "OFFLINE_LIMIT"),
            AppError.NoNetwork(),
        )) {
            remote.grants["p1"] = AppResult.Failure(refusal)
            copies.refresh(force = true)

            assertThat(copies.phase()).isEqualTo(OfflinePhase.STORED)
            assertThat(copies.playable("p1")!!.expiresAtMs).isEqualTo(expiry)
            assertThat(store.stored).containsKey("$VIEWER/p1/video")
            now += RENEW_INTERVAL_MS
        }

        assertThat(remote.removes()).isEmpty()
        // One try a day, whatever it came to.
        assertThat(grants()).hasSize(5)
    }

    @Test
    fun `renewals go one at a time with a gap, never as a burst`() = test {
        val copies = machine()
        stored(copies, "p1")
        stored(copies, "p2")
        stored(copies, "p3")
        confirmAll("p1", "p2", "p3")
        now += 3 * DAY_MS
        val before = grants().size

        val refresh = backgroundScope.launch { copies.refresh() }
        runCurrent()

        assertThat(grants().size - before).isEqualTo(1)
        advanceTimeBy(999)
        runCurrent()
        assertThat(grants().size - before).isEqualTo(1)
        advanceTimeBy(1)
        runCurrent()
        assertThat(grants().size - before).isEqualTo(2)
        advanceTimeBy(1_000)
        runCurrent()

        assertThat(grants().drop(before)).containsExactly("grant:p1:$DEVICE", "grant:p2:$DEVICE", "grant:p3:$DEVICE")
        assertThat(refresh.isCompleted).isTrue()
    }

    @Test
    fun `renewal does not wait for wifi`() = test {
        val copies = machine()
        stored(copies)
        confirmAll("p1")
        connection = OfflineConnection.METERED
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(prefs.wifiOnly.value).isTrue()
        assertThat(grants()).hasSize(2)
    }

    @Test
    fun `a copy the check deleted is not renewed`() = test {
        val copies = machine()
        stored(copies)
        remote.checkAnswers = mapOf("p1" to OfflineCheckAnswer.Invalid("private"))
        now += 3 * DAY_MS

        copies.refresh()

        assertThat(grants()).hasSize(1)
        assertThat(copies.phase()).isNull()
    }
}

private const val OTHER = "someone-else"
