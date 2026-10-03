package com.us.android.core.feed.offline

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.PlaybackKind
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/**
 * The index of copies on the device, and a copy becoming what the players
 * take.
 *
 * What this protects: the list surviving a restart exactly as it was
 * written; a file that cannot be read being an empty list rather than a
 * crash on launch; and a stored copy opening with NO network, which means
 * the row the watch screen and the reel's overlay draw, the bytes' key and
 * the reel's sound and mix all come from what was kept.
 */
class OfflineIndexAndMappingTest {

    @get:Rule
    val folder = TemporaryFolder()

    private val file: File get() = File(folder.root, "offline_copies/copies.json")
    private fun index() = OfflineIndex(file = { file })

    private val soundGrant = OfflineGrantSound(
        stream = OfflineGrantStream("https://api.test/v1/audio/s1/serve", "audio/mp4", 77L),
        startMs = 1_500L,
        originalVolume = 0.0,
        overlayVolume = 0.8,
    )

    private fun reelCopy() = grant("p1", nowMs = 5_000L, kind = OfflineKind.REEL, sound = soundGrant)
        .toCopy(videoPost("p1", contentType = "flick", sound = reelSound("s1")), nowMs = 5_000L, ownerId = VIEWER)

    // ── The index ───────────────────────────────────────────────────────

    @Test
    fun `no index file is an empty index`() {
        assertThat(index().read()).isEqualTo(OfflineIndexFile())
    }

    @Test
    fun `what is written is what the next process reads`() {
        val written = OfflineIndexFile(ownerId = VIEWER, deviceId = DEVICE, copies = listOf(reelCopy()))

        assertThat(index().write(written)).isTrue()

        assertThat(index().read()).isEqualTo(written)
        assertThat(file.parentFile!!.list()!!.toList()).containsExactly("copies.json")
    }

    @Test
    fun `a file that cannot be read is an empty index, not a crash`() {
        file.parentFile!!.mkdirs()
        file.writeText("{ this is not json")

        assertThat(index().read()).isEqualTo(OfflineIndexFile())
    }

    @Test
    fun `a newer build's unknown keys are ignored`() {
        file.parentFile!!.mkdirs()
        file.writeText("""{"version":2,"ownerId":"$VIEWER","deviceId":"$DEVICE","copies":[],"added_later":true}""")

        assertThat(index().read().ownerId).isEqualTo(VIEWER)
    }

    /** An index written before 2 Oct 2026's sign-out rule: one owner, no stamp, nothing held. */
    @Test
    fun `a version 1 index reads as one signed-in owner with nothing held`() {
        file.parentFile!!.mkdirs()
        file.writeText("""{"version":1,"ownerId":"$VIEWER","deviceId":"$DEVICE","copies":[]}""")

        val read = index().read()

        assertThat(read.ownerId).isEqualTo(VIEWER)
        assertThat(read.signedOutAtMs).isNull()
        assertThat(read.held).isEmpty()
    }

    @Test
    fun `the sign-out stamp and the copies held for other accounts survive a restart`() {
        val theirs = reelCopy().copy(ownerId = "someone-else")
        val written = OfflineIndexFile(
            ownerId = VIEWER,
            deviceId = DEVICE,
            copies = listOf(reelCopy()),
            signedOutAtMs = 7_000L,
            held = listOf(OfflineHeldSet("someone-else", DEVICE, signedOutAtMs = 3_000L, copies = listOf(theirs))),
        )

        index().write(written)

        assertThat(index().read()).isEqualTo(written)
    }

    @Test
    fun `two accounts' copies of one post never share a key or a folder`() {
        val mine = grant("p1").toCopy(videoPost("p1"), 0L, ownerId = VIEWER)
        val theirs = grant("p1").toCopy(videoPost("p1"), 0L, ownerId = "someone-else")

        assertThat(mine.video.key).isNotEqualTo(theirs.video.key)
        assertThat(mine.folder).isNotEqualTo(theirs.folder)
        // A copy stored before the owner was part of the key keeps the folder it was stored in.
        assertThat(mine.copy(ownerId = "").folder).isEqualTo("p1")
    }

    @Test
    fun `deleting the index leaves nothing to read`() {
        index().write(OfflineIndexFile(ownerId = VIEWER, copies = listOf(reelCopy())))

        index().delete()

        assertThat(file.exists()).isFalse()
        assertThat(index().read().copies).isEmpty()
    }

    // ── A grant becoming a copy ─────────────────────────────────────────

    @Test
    fun `a copy takes the grant's words, the post's where the grant has none, and one key per stream`() {
        val copy = reelCopy()

        assertThat(copy.kind).isEqualTo(OfflineKind.REEL)
        assertThat(copy.title).isEqualTo("Granted p1")
        assertThat(copy.channelName).isEqualTo("Raghu Builds")
        assertThat(copy.stored).isFalse()
        assertThat(copy.grantedAtMs).isEqualTo(5_000L)
        // The grant IS the server's word: the copy is not asked about again until that ages out.
        assertThat(copy.lastCheckedAtMs).isEqualTo(5_000L)
        assertThat(copy.video.key).isEqualTo("$VIEWER/p1/video")
        assertThat(copy.sound!!.stream.key).isEqualTo("$VIEWER/p1/sound")
        assertThat(copy.streams.map { it.key }).containsExactly("$VIEWER/p1/video", "$VIEWER/p1/sound").inOrder()
        assertThat(copy.sound!!.soundId).isEqualTo("s1")
        assertThat(copy.post.authorId).isEqualTo("creator-1")
        assertThat(copy.post.width).isEqualTo(1280)
    }

    @Test
    fun `a grant that names no kind takes the post's`() {
        val granted = grant("p1", kind = null)
        val reel = granted.toCopy(videoPost("p1", contentType = "flick"), nowMs = 0L, ownerId = VIEWER)
        val video = granted.toCopy(videoPost("p1", contentType = "long_video"), nowMs = 0L, ownerId = VIEWER)

        assertThat(reel.kind).isEqualTo(OfflineKind.REEL)
        assertThat(video.kind).isEqualTo(OfflineKind.VIDEO)
    }

    // ── A copy becoming what the players take ───────────────────────────

    @Test
    fun `a stored copy plays by its key from the offline cache`() {
        val playback = reelCopy().playback()

        assertThat(playback.kind).isEqualTo(PlaybackKind.Offline)
        assertThat(playback.cacheKey).isEqualTo("$VIEWER/p1/video")
        assertThat(playback.captions).isEmpty()
    }

    @Test
    fun `only caption tracks that were actually stored are offered`() {
        val copy = reelCopy().copy(
            captions = listOf(
                OfflineCaption("en", "English", "https://api.test/en.vtt", file = "/private/captions_en.vtt"),
                OfflineCaption("hi", "Hindi", "https://api.test/hi.vtt", file = null),
            ),
        )

        val captions = copy.playback().captions

        assertThat(captions.map { it.language }).containsExactly("en")
        assertThat(captions.single().uri).startsWith("file:")
        assertThat(captions.single().label).isEqualTo("English")
    }

    @Test
    fun `the stored sound keeps where it starts and is played from the device`() {
        val track = reelCopy().soundTrack()!!

        assertThat(track.id).isEqualTo("s1")
        assertThat(track.startMs).isEqualTo(1_500L)
        assertThat(track.stored!!.kind).isEqualTo(PlaybackKind.Offline)
        assertThat(track.stored!!.cacheKey).isEqualTo("$VIEWER/p1/sound")
        assertThat(grant("p1").toCopy(videoPost("p1"), 0L, VIEWER).soundTrack()).isNull()
    }

    @Test
    fun `the post is rebuilt from what was kept, with the grant's mix`() {
        val item = reelCopy().toFeedItem()

        assertThat(item.id).isEqualTo("p1")
        assertThat(item.author.id).isEqualTo("creator-1")
        assertThat(item.author.username).isEqualTo("raghu")
        assertThat(item.title).isEqualTo("Granted p1")
        assertThat(item.text).isEqualTo("caption of p1")
        assertThat(item.feedContentType).isEqualTo("flick")
        assertThat(item.media.single().kind).isEqualTo("video")
        assertThat(item.media.single().durationMs).isEqualTo(725_000L)
        assertThat(item.hashtags).containsExactly("build")
        assertThat(item.sound!!.id).isEqualTo("s1")
        assertThat(item.sound!!.startMs).isEqualTo(1_500L)
        // 0 is the creator's mute and survives the round trip.
        assertThat(item.originalVolume).isEqualTo(0.0)
        assertThat(item.overlayVolume).isEqualTo(0.8)
    }

    @Test
    fun `a copy survives the index and still rebuilds the same post`() {
        val copy = reelCopy().copy(stored = true, sizeBytes = 1_077L, posterFile = "/private/poster")
        index().write(OfflineIndexFile(ownerId = VIEWER, deviceId = DEVICE, copies = listOf(copy)))

        val read = index().read().copies.single()

        assertThat(read).isEqualTo(copy)
        assertThat(read.toFeedItem()).isEqualTo(copy.toFeedItem())
        assertThat(read.posterModel()).isEqualTo("file:///private/poster")
    }
}
