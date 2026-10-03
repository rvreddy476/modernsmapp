package com.us.android.feature.tube.ui.offline

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.offline.OfflineCopy
import com.us.android.core.feed.offline.OfflineEntry
import com.us.android.core.feed.offline.OfflineKind
import com.us.android.core.feed.offline.OfflinePhase
import com.us.android.core.feed.offline.OfflineState
import com.us.android.core.feed.offline.OfflineStream
import com.us.android.feature.tube.ui.watch.WatchOfflineStatus
import com.us.android.feature.tube.ui.watch.captionChoice
import com.us.android.feature.tube.ui.watch.watchOfflineStatus
import org.junit.Test

/**
 * The Offline page's list and its words, and what the watch screen says
 * about a copy.
 *
 * What this protects: copies listed under Videos and Reels, newest first,
 * by TITLE and never by a file or a path; a size and an expiry a person can
 * read, the expiry never promising more than the server granted; a save in
 * flight reading as a save; "Offline copy" shown only when the device's
 * copy is what is playing; and captions never turning themselves on.
 */
class OfflinePageTest {

    private val day = 24L * 60 * 60 * 1000
    private val now = 100 * day

    private fun copy(
        postId: String,
        kind: OfflineKind = OfflineKind.VIDEO,
        grantedAt: Long = now,
        size: Long = 184_320_000L,
        expiresAt: Long = now + 12 * day,
        title: String = "Title $postId",
    ) = OfflineCopy(
        postId = postId,
        kind = kind,
        title = title,
        channelName = "Raghu Builds",
        expiresAtMs = expiresAt,
        recheckAfterSeconds = 172_800L,
        grantedAtMs = grantedAt,
        lastCheckedAtMs = grantedAt,
        stored = true,
        video = OfflineStream("$postId/video", "https://api.test/v1/media/m/serve/720p", "video/mp4", size),
        posterFile = "/data/user/0/app/no_backup/offline_copies/files/$postId/poster",
        sizeBytes = size,
    )

    private fun stored(copy: OfflineCopy) = copy.postId to OfflineEntry(OfflinePhase.STORED, 1f, copy)

    // ── The sections ────────────────────────────────────────────────────

    @Test
    fun `copies are listed under videos and reels, newest first`() {
        val state = OfflineState(
            copies = mapOf(
                stored(copy("old-video", grantedAt = now - 5 * day)),
                stored(copy("reel", kind = OfflineKind.REEL, grantedAt = now - day)),
                stored(copy("new-video", grantedAt = now)),
            ),
            loaded = true,
        )

        val sections = offlineSections(state, now)

        assertThat(sections.videos.map { it.postId }).containsExactly("new-video", "old-video").inOrder()
        assertThat(sections.reels.map { it.postId }).containsExactly("reel")
        assertThat(sections.count).isEqualTo(3)
        assertThat(sections.isEmpty).isFalse()
    }

    @Test
    fun `a stored copy reads as its channel, its size and its expiry`() {
        val row = offlineSections(OfflineState(mapOf(stored(copy("p1")))), now).videos.single()

        assertThat(row.title).isEqualTo("Title p1")
        assertThat(row.meta).isEqualTo("Raghu Builds · 175 MB · Expires in 12 days")
        assertThat(row.stored).isTrue()
        assertThat(row.progress).isNull()
    }

    /** founder, 2026-10-02: "Nobody can see download location." */
    @Test
    fun `no word a row shows is a path, a file name or an address`() {
        val row = offlineSections(OfflineState(mapOf(stored(copy("p1")))), now).videos.single()

        for (text in listOf(row.title, row.meta, row.channelName)) {
            assertThat(text).doesNotContain("/")
            assertThat(text).doesNotContain("no_backup")
            assertThat(text).doesNotContain("http")
        }
    }

    @Test
    fun `a save in flight reads as a save, and a held one says what it waits for`() {
        val saving = copy("p1").copy(stored = false)
        val state = OfflineState(
            mapOf(
                "p1" to OfflineEntry(OfflinePhase.SAVING, 0.42f, saving),
                "p2" to OfflineEntry(OfflinePhase.WAITING, null, copy("p2").copy(stored = false)),
                // The grant is still on the wire: nothing to list yet.
                "p3" to OfflineEntry(OfflinePhase.REQUESTING),
            ),
        )

        val rows = offlineSections(state, now).videos.associateBy { it.postId }

        assertThat(rows.keys).containsExactly("p1", "p2")
        assertThat(rows.getValue("p1").meta).isEqualTo("Raghu Builds · Saving 42%")
        assertThat(rows.getValue("p1").stored).isFalse()
        assertThat(rows.getValue("p1").progress).isEqualTo(0.42f)
        assertThat(rows.getValue("p2").meta).isEqualTo("Raghu Builds · Waiting for Wi-Fi")
    }

    @Test
    fun `a copy with no title is still named`() {
        val state = OfflineState(
            mapOf(
                stored(copy("v", title = "")),
                stored(copy("r", kind = OfflineKind.REEL, title = "")),
            ),
        )

        val sections = offlineSections(state, now)

        assertThat(sections.videos.single().title).isEqualTo("Video")
        assertThat(sections.reels.single().title).isEqualTo("Reel")
    }

    @Test
    fun `nothing saved is an empty page`() {
        assertThat(offlineSections(OfflineState(loaded = true), now).isEmpty).isTrue()
    }

    // ── The words ───────────────────────────────────────────────────────

    @Test
    fun `sizes are said in the unit a person would use`() {
        assertThat(offlineSizeLabel(0L)).isEqualTo("0 B")
        assertThat(offlineSizeLabel(900L)).isEqualTo("900 B")
        assertThat(offlineSizeLabel(6_144_000L)).isEqualTo("5 MB")
        assertThat(offlineSizeLabel(184_320_000L)).isEqualTo("175 MB")
        assertThat(offlineSizeLabel(1_288_490_189L)).isEqualTo("1.2 GB")
        assertThat(offlineSizeLabel(2048L)).isEqualTo("2 KB")
        assertThat(offlineSizeLabel(-5L)).isEqualTo("0 B")
    }

    /** Whole days, rounded DOWN: the label never promises more than the server granted. */
    @Test
    fun `the expiry never promises more than is left`() {
        assertThat(offlineExpiryLabel(now + 12 * day, now)).isEqualTo("Expires in 12 days")
        assertThat(offlineExpiryLabel(now + 2 * day - 1, now)).isEqualTo("Expires tomorrow")
        assertThat(offlineExpiryLabel(now + day, now)).isEqualTo("Expires tomorrow")
        assertThat(offlineExpiryLabel(now + day - 1, now)).isEqualTo("Expires today")
        assertThat(offlineExpiryLabel(now - day, now)).isEqualTo("Expires today")
    }

    @Test
    fun `the storage line counts copies and says what they take`() {
        assertThat(offlineStorageLabel(0, 0L)).isEqualTo("0 copies · 0 B on this device")
        assertThat(offlineStorageLabel(1, 6_144_000L)).isEqualTo("1 copy · 5 MB on this device")
        assertThat(offlineStorageLabel(3, 184_320_000L)).isEqualTo("3 copies · 175 MB on this device")
    }

    @Test
    fun `a save says its percentage once the length is known`() {
        assertThat(offlineSavingLabel(null)).isEqualTo("Saving")
        assertThat(offlineSavingLabel(0f)).isEqualTo("Saving 0%")
        assertThat(offlineSavingLabel(0.999f)).isEqualTo("Saving 99%")
        assertThat(offlineSavingLabel(1f)).isEqualTo("Saving 100%")
    }

    // ── The watch screen ────────────────────────────────────────────────

    @Test
    fun `the watch screen says offline copy only when the device's copy is what plays`() {
        val storedEntry = OfflineEntry(OfflinePhase.STORED, 1f, copy("p1"))

        assertThat(watchOfflineStatus(offlineCopy = true, entry = storedEntry)).isEqualTo(WatchOfflineStatus.Playing)
        // Saved after this video started: what is on screen is still the network's stream.
        assertThat(watchOfflineStatus(offlineCopy = false, entry = storedEntry)).isNull()
        assertThat(watchOfflineStatus(offlineCopy = false, entry = null)).isNull()
    }

    @Test
    fun `the watch screen shows the ring while a copy is being saved`() {
        assertThat(watchOfflineStatus(false, OfflineEntry(OfflinePhase.SAVING, 0.3f)))
            .isEqualTo(WatchOfflineStatus.Saving(0.3f, waiting = null))
        assertThat(watchOfflineStatus(false, OfflineEntry(OfflinePhase.REQUESTING)))
            .isEqualTo(WatchOfflineStatus.Saving(null, waiting = null))
        assertThat(watchOfflineStatus(false, OfflineEntry(OfflinePhase.WAITING, 0.1f)))
            .isEqualTo(WatchOfflineStatus.Saving(0.1f, waiting = "Waiting for Wi-Fi"))
    }

    @Test
    fun `captions never turn themselves on, and a choice is kept only where the next video has it`() {
        assertThat(captionChoice(current = null, offered = listOf("en", "hi"))).isNull()
        assertThat(captionChoice(current = "en", offered = listOf("en", "hi"))).isEqualTo("en")
        assertThat(captionChoice(current = "en", offered = listOf("hi"))).isNull()
        assertThat(captionChoice(current = "en", offered = emptyList())).isNull()
    }
}
