package com.us.android.core.feed.offline

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.offline.OfflineFetch
import com.us.android.core.media.offline.OfflineFetchState
import com.us.android.core.model.SessionState
import com.us.android.core.ui.UsOfflineAction
import org.junit.Test

/**
 * The rules of an offline copy, one table each.
 *
 * What this protects: who is offered Save offline (the creator's switch,
 * and the owner regardless); Wi-Fi only holding a save on mobile data; a
 * phone left with room to work; a truncated or substituted stream never
 * being kept; and the one decision about keeping a copy, whose middle line
 * is the one that matters most: NO ANSWER IS NOT A REVOCATION.
 */
class OfflineRulesTest {

    // ── Who may save ────────────────────────────────────────────────────

    @Test
    fun `a viewer may save a video the creator allows`() {
        assertThat(videoPost(allowDownload = true).canSaveOffline(isOwn = false)).isTrue()
    }

    @Test
    fun `a viewer may not save a video the creator has not allowed`() {
        assertThat(videoPost(allowDownload = false).canSaveOffline(isOwn = false)).isFalse()
    }

    @Test
    fun `the owner may save their own video whatever the switch says`() {
        assertThat(videoPost(allowDownload = false).canSaveOffline(isOwn = true)).isTrue()
    }

    @Test
    fun `a video still processing, a scheduled one and a post with no video are not offered`() {
        assertThat(videoPost(processing = true).canSaveOffline(isOwn = true)).isFalse()
        assertThat(videoPost(scheduled = true).canSaveOffline(isOwn = true)).isFalse()
        assertThat(videoPost(kind = "image").canSaveOffline(isOwn = true)).isFalse()
    }

    // ── The row ─────────────────────────────────────────────────────────

    private fun entry(phase: OfflinePhase, progress: Float? = null) = OfflineEntry(phase, progress)

    @Test
    fun `the row offers save to a viewer who is allowed and to the owner, and nothing to a viewer who is not`() {
        assertThat(offlineMoreState(videoPost(allowDownload = true), isOwn = false, entry = null).action)
            .isEqualTo(UsOfflineAction.SAVE)
        assertThat(offlineMoreState(videoPost(allowDownload = false), isOwn = true, entry = null).action)
            .isEqualTo(UsOfflineAction.SAVE)
        assertThat(offlineMoreState(videoPost(allowDownload = false), isOwn = false, entry = null).action)
            .isEqualTo(UsOfflineAction.NONE)
    }

    @Test
    fun `a stored copy offers remove, even after the creator switched saving off`() {
        val stored = entry(OfflinePhase.STORED, 1f)

        assertThat(offlineMoreState(videoPost(), isOwn = false, entry = stored).action)
            .isEqualTo(UsOfflineAction.REMOVE)
        assertThat(offlineMoreState(videoPost(allowDownload = false), isOwn = false, entry = stored).action)
            .isEqualTo(UsOfflineAction.REMOVE)
    }

    @Test
    fun `a save in flight offers cancel with its progress, and says when it is held`() {
        val saving = offlineMoreState(videoPost(), isOwn = false, entry = entry(OfflinePhase.SAVING, 0.42f))
        assertThat(saving.action).isEqualTo(UsOfflineAction.CANCEL)
        assertThat(saving.progress).isEqualTo(0.42f)
        assertThat(saving.waiting).isNull()

        val requesting = offlineMoreState(videoPost(), isOwn = false, entry = entry(OfflinePhase.REQUESTING))
        assertThat(requesting.action).isEqualTo(UsOfflineAction.CANCEL)

        val waiting = offlineMoreState(videoPost(), isOwn = false, entry = entry(OfflinePhase.WAITING))
        assertThat(waiting.action).isEqualTo(UsOfflineAction.CANCEL)
        assertThat(waiting.waiting).isEqualTo("Waiting for Wi-Fi")
    }

    /** The sound is stored beside the reel and mixed by the same player, so the reel is offered like any other. */
    @Test
    fun `a reel that plays an added sound is offered like any other`() {
        val reel = videoPost(contentType = "flick", sound = reelSound())

        assertThat(offlineMoreState(reel, isOwn = false, entry = null).action).isEqualTo(UsOfflineAction.SAVE)
    }

    // ── The network ─────────────────────────────────────────────────────

    @Test
    fun `wifi only blocks a save on a metered connection`() {
        assertThat(offlineGate(wifiOnly = true, OfflineConnection.METERED)).isEqualTo(OfflineGate.WAIT_FOR_WIFI)
    }

    @Test
    fun `wifi only lets a save through on an unmetered connection`() {
        assertThat(offlineGate(wifiOnly = true, OfflineConnection.UNMETERED)).isEqualTo(OfflineGate.GO)
    }

    @Test
    fun `with the switch off a metered connection will do`() {
        assertThat(offlineGate(wifiOnly = false, OfflineConnection.METERED)).isEqualTo(OfflineGate.GO)
    }

    @Test
    fun `nothing runs with no connection, whatever the switch says`() {
        assertThat(offlineGate(wifiOnly = true, OfflineConnection.NONE)).isEqualTo(OfflineGate.WAIT_FOR_NETWORK)
        assertThat(offlineGate(wifiOnly = false, OfflineConnection.NONE)).isEqualTo(OfflineGate.WAIT_FOR_NETWORK)
    }

    // ── Room ────────────────────────────────────────────────────────────

    @Test
    fun `a copy fits only if the reserve is still free after it`() {
        val needed = 500L * 1024 * 1024

        assertThat(hasRoomFor(needed, usableBytes = needed + STORAGE_RESERVE_BYTES)).isTrue()
        assertThat(hasRoomFor(needed, usableBytes = needed + STORAGE_RESERVE_BYTES - 1)).isFalse()
        assertThat(hasRoomFor(needed, usableBytes = needed)).isFalse()
    }

    @Test
    fun `a size the server did not declare still has to leave the reserve`() {
        assertThat(hasRoomFor(0L, usableBytes = STORAGE_RESERVE_BYTES)).isTrue()
        assertThat(hasRoomFor(0L, usableBytes = STORAGE_RESERVE_BYTES - 1)).isFalse()
    }

    // ── What arrived ────────────────────────────────────────────────────

    @Test
    fun `stored bytes must be exactly the granted length`() {
        assertThat(sizeMatches(expectedBytes = 1_000L, declaredBytes = -1L, storedBytes = 1_000L)).isTrue()
        assertThat(sizeMatches(expectedBytes = 1_000L, declaredBytes = -1L, storedBytes = 999L)).isFalse()
        assertThat(sizeMatches(expectedBytes = 1_000L, declaredBytes = -1L, storedBytes = 1_001L)).isFalse()
    }

    @Test
    fun `without a granted length the length the storage declared stands in`() {
        assertThat(sizeMatches(expectedBytes = 0L, declaredBytes = 700L, storedBytes = 700L)).isTrue()
        assertThat(sizeMatches(expectedBytes = 0L, declaredBytes = 700L, storedBytes = 500L)).isFalse()
        // The grant wins when both are known.
        assertThat(sizeMatches(expectedBytes = 1_000L, declaredBytes = 700L, storedBytes = 700L)).isFalse()
    }

    @Test
    fun `an empty stream is never a copy, and one of unknown length only has to be there`() {
        assertThat(sizeMatches(expectedBytes = 0L, declaredBytes = -1L, storedBytes = 0L)).isFalse()
        assertThat(sizeMatches(expectedBytes = 1_000L, declaredBytes = -1L, storedBytes = 0L)).isFalse()
        assertThat(sizeMatches(expectedBytes = 0L, declaredBytes = -1L, storedBytes = 300L)).isTrue()
    }

    // ── Keeping a copy ──────────────────────────────────────────────────

    private val copy = OfflineCopy(
        postId = "p1",
        kind = OfflineKind.VIDEO,
        expiresAtMs = 30 * DAY_MS,
        recheckAfterSeconds = 172_800L,
        grantedAtMs = 0L,
        lastCheckedAtMs = 0L,
        stored = true,
        video = OfflineStream("p1/video", "https://api.test/v", "video/mp4", 1_000L),
    )

    @Test
    fun `no answer is not a revocation`() {
        assertThat(offlineVerdict(copy, nowMs = 10 * DAY_MS, answer = null)).isEqualTo(OfflineVerdict.Keep)
    }

    @Test
    fun `not valid deletes the copy, whatever the reason`() {
        for (reason in listOf("deleted", "private", "not_allowed", "expired", "blocked", "revoked", "unknown")) {
            assertThat(offlineVerdict(copy, nowMs = DAY_MS, answer = OfflineCheckAnswer.Invalid(reason)))
                .isEqualTo(OfflineVerdict.Delete(reason))
        }
    }

    @Test
    fun `past its expiry the copy is deleted with or without an answer`() {
        val after = 30 * DAY_MS

        assertThat(offlineVerdict(copy, nowMs = after, answer = null)).isEqualTo(OfflineVerdict.Delete("expired"))
        assertThat(offlineVerdict(copy, nowMs = after, answer = OfflineCheckAnswer.Valid(after + DAY_MS)))
            .isEqualTo(OfflineVerdict.Delete("expired"))
        assertThat(offlineVerdict(copy, nowMs = after - 1, answer = null)).isEqualTo(OfflineVerdict.Keep)
    }

    @Test
    fun `valid keeps the copy and takes the server's expiry, or keeps its own when none is sent`() {
        assertThat(offlineVerdict(copy, nowMs = DAY_MS, answer = OfflineCheckAnswer.Valid(20 * DAY_MS)))
            .isEqualTo(OfflineVerdict.Confirmed(20 * DAY_MS))
        assertThat(offlineVerdict(copy, nowMs = DAY_MS, answer = OfflineCheckAnswer.Valid(null)))
            .isEqualTo(OfflineVerdict.Confirmed(30 * DAY_MS))
        // A "valid" whose expiry has already passed is expired.
        assertThat(offlineVerdict(copy, nowMs = 5 * DAY_MS, answer = OfflineCheckAnswer.Valid(4 * DAY_MS)))
            .isEqualTo(OfflineVerdict.Delete("expired"))
    }

    @Test
    fun `a copy is asked about again once the server's answer has aged out`() {
        assertThat(recheckDue(copy, nowMs = 2 * DAY_MS - 1)).isFalse()
        assertThat(recheckDue(copy, nowMs = 2 * DAY_MS)).isTrue()
    }

    // ── Renewing, and the 48 hours after sign-out ───────────────────────

    @Test
    fun `a renewal is due a day after the grant, and then a day after the last try`() {
        assertThat(renewDue(copy, nowMs = DAY_MS - 1)).isFalse()
        assertThat(renewDue(copy, nowMs = DAY_MS)).isTrue()

        val tried = copy.copy(lastRenewAtMs = 3 * DAY_MS)

        assertThat(renewDue(tried, nowMs = 4 * DAY_MS - 1)).isFalse()
        assertThat(renewDue(tried, nowMs = 4 * DAY_MS)).isTrue()
    }

    @Test
    fun `copies are held for 48 hours after sign-out and not a millisecond less`() {
        assertThat(SIGN_OUT_HOLD_MS).isEqualTo(2 * DAY_MS)
        assertThat(signOutHoldOver(signedOutAtMs = DAY_MS, nowMs = 3 * DAY_MS - 1)).isFalse()
        assertThat(signOutHoldOver(signedOutAtMs = DAY_MS, nowMs = 3 * DAY_MS)).isTrue()
    }

    @Test
    fun `only a real session names a viewer, and a session not read yet names nobody at all`() {
        assertThat(SessionState.Authenticated(userId = "u1", sessionId = "s1").offlineViewerId()).isEqualTo("u1")
        assertThat(SessionState.Unauthenticated.offlineViewerId()).isEmpty()
        assertThat(SessionState.PendingTwoFactor("t").offlineViewerId()).isEmpty()
        assertThat(SessionState.PendingStepUp(listOf("totp")).offlineViewerId()).isEmpty()
        assertThat(SessionState.Unknown.offlineViewerId()).isNull()
    }

    @Test
    fun `only a whole copy inside its expiry can be played`() {
        assertThat(copy.isPlayable(nowMs = DAY_MS)).isTrue()
        assertThat(copy.isPlayable(nowMs = 30 * DAY_MS)).isFalse()
        assertThat(copy.copy(stored = false).isPlayable(nowMs = DAY_MS)).isFalse()
    }

    // ── Progress ────────────────────────────────────────────────────────

    private val video = OfflineStream("p1/video", "v", "video/mp4", 800L)
    private val sound = OfflineStream("p1/sound", "s", "audio/mp4", 200L)

    private fun fetch(state: OfflineFetchState, bytes: Long = 0L, total: Long = -1L) = OfflineFetch(state, bytes, total)

    @Test
    fun `progress is every stream's bytes against every stream's size`() {
        val summary = summarizeFetches(
            listOf(video, sound),
            mapOf(
                "p1/video" to fetch(OfflineFetchState.RUNNING, bytes = 400L),
                "p1/sound" to fetch(OfflineFetchState.DONE, bytes = 200L),
            ),
        )

        assertThat(summary).isEqualTo(OfflineFetchSummary.Running(progress = 0.6f, waiting = false))
    }

    @Test
    fun `a copy is done only when every stream is, and failed when any is`() {
        val done = fetch(OfflineFetchState.DONE)

        assertThat(summarizeFetches(listOf(video, sound), mapOf("p1/video" to done, "p1/sound" to done)))
            .isEqualTo(OfflineFetchSummary.Done)
        assertThat(summarizeFetches(listOf(video, sound), mapOf("p1/video" to done)))
            .isInstanceOf(OfflineFetchSummary.Running::class.java)
        assertThat(
            summarizeFetches(
                listOf(video, sound),
                mapOf("p1/video" to done, "p1/sound" to fetch(OfflineFetchState.FAILED)),
            ),
        ).isEqualTo(OfflineFetchSummary.Failed)
    }

    @Test
    fun `a stream of unknown size has no percentage, and a held one is waiting`() {
        val unsized = OfflineStream("p1/video", "v")

        assertThat(summarizeFetches(listOf(unsized), mapOf("p1/video" to fetch(OfflineFetchState.RUNNING, 10L))))
            .isEqualTo(OfflineFetchSummary.Running(progress = null, waiting = false))
        assertThat(summarizeFetches(listOf(video), mapOf("p1/video" to fetch(OfflineFetchState.WAITING))))
            .isEqualTo(OfflineFetchSummary.Running(progress = 0f, waiting = true))
        // Not asked for yet: nothing has arrived.
        assertThat(summarizeFetches(listOf(video), emptyMap()))
            .isEqualTo(OfflineFetchSummary.Running(progress = 0f, waiting = false))
    }
}
