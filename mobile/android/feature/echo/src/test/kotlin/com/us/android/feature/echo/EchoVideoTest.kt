package com.us.android.feature.echo

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.echo.data.EchoVideoRules
import com.us.android.feature.echo.data.NoteDto
import com.us.android.feature.echo.data.StagedMedia
import com.us.android.feature.echo.data.VIDEO_ALONE_MESSAGE
import com.us.android.feature.echo.data.entryProblem
import com.us.android.feature.echo.data.mediaProblem
import com.us.android.feature.echo.data.toNote
import com.us.android.feature.echo.data.videoDurationLabel
import com.us.android.feature.echo.data.videoLimitFor
import com.us.android.feature.echo.data.videoTooLongNotice
import com.us.android.feature.echo.ui.CARD_ACTIONS
import com.us.android.feature.echo.ui.CardAction
import com.us.android.feature.echo.ui.FOCUS_PILL_ROWS
import com.us.android.feature.echo.ui.FocusPill
import com.us.android.feature.echo.ui.compactCountWords
import com.us.android.feature.echo.ui.echoItLabel
import com.us.android.feature.echo.ui.stagedVideoStatus
import com.us.android.feature.echo.ui.viewsWords
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Video in notes (echo-v2-contract.md "Video in notes", 15 Nov 2026): the
 * poster's limits, the read side, the composer's media rule; and the card's
 * action row and the note page's pill grid with the views pill.
 */
class EchoVideoTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    // ── limits ──

    @Test
    fun `without Premium 140 s and 512 MB, with Premium 10 minutes and no size cap`() {
        val short = videoLimitFor(premium = false)
        assertThat(short.maxMs).isEqualTo(140_000L)
        assertThat(short.maxBytes).isEqualTo(512L * 1024 * 1024)
        assertThat(short.check(140_000L, 512L * 1024 * 1024)).isNull()
        assertThat(short.check(140_001L, 1L)).isEqualTo("Videos can be up to 2 min 20 s.")
        assertThat(short.check(1_000L, 512L * 1024 * 1024 + 1)).isEqualTo("Videos can be up to 512 MB.")

        val premium = videoLimitFor(premium = true)
        assertThat(premium.maxMs).isEqualTo(EchoVideoRules.PREMIUM_MAX_MS)
        assertThat(premium.maxBytes).isNull()
        assertThat(premium.check(600_000L, 5L * 1024 * 1024 * 1024)).isNull()
        assertThat(premium.check(600_001L, 1L)).isEqualTo("Videos can be up to 10 minutes.")
        // A length the file does not say is the server's to judge.
        assertThat(short.check(0L, 1L)).isNull()
    }

    @Test
    fun `the too-long notice names the limit the length points to`() {
        assertThat(videoTooLongNotice(150_000L))
            .isEqualTo("This video is longer than 2 min 20 s and is only visible to you.")
        assertThat(videoTooLongNotice(700_000L))
            .isEqualTo("This video is longer than 10 minutes and is only visible to you.")
    }

    @Test
    fun `the length chip reads m ss`() {
        assertThat(videoDurationLabel(0)).isEmpty()
        assertThat(videoDurationLabel(7_400)).isEqualTo("0:07")
        assertThat(videoDurationLabel(140_000)).isEqualTo("2:20")
        assertThat(videoDurationLabel(600_000)).isEqualTo("10:00")
        assertThat(videoDurationLabel(3_723_000)).isEqualTo("1:02:03")
    }

    // ── reading ──

    @Test
    fun `a video note reads its video, length, playlist and the author-only flags`() {
        val body = """
            {"data":{"id":"n1","author_id":"me","text":"clip","post_type":"video","is_processing":true,
              "video_too_long":true,
              "media":[{"media_id":"v1","kind":"video","position":0,"duration_ms":150000,
                "hls_url":"/v1/media/v1/hls/master.m3u8","processing_status":"ready"}]}}
        """.trimIndent()
        val dto = json.decodeFromString<ApiEnvelope<NoteDto>>(body).data!!
        assertThat(dto.postType).isEqualTo("video")
        val note = dto.toNote()
        assertThat(note.isProcessing).isTrue()
        assertThat(note.videoTooLong).isTrue()
        val video = note.video!!
        assertThat(video.mediaId).isEqualTo("v1")
        assertThat(video.isGif).isFalse()
        assertThat(video.durationMs).isEqualTo(150_000L)
        assertThat(video.hlsUrl).isEqualTo("/v1/media/v1/hls/master.m3u8")
    }

    @Test
    fun `video_too_long is false when omitted, and a picture is no video`() {
        val body = """
            {"data":{"id":"n2","author_id":"a","text":"pic",
              "media":[{"media_id":"i1","kind":"image","position":0}]}}
        """.trimIndent()
        val note = json.decodeFromString<ApiEnvelope<NoteDto>>(body).data!!.toNote()
        assertThat(note.videoTooLong).isFalse()
        assertThat(note.isProcessing).isFalse()
        assertThat(note.video).isNull()
        assertThat(note.media.single().hlsUrl).isNull()
    }

    // ── the composer's media rule ──

    private fun video(ready: Boolean = true, failed: Boolean = false) = StagedMedia(
        localKey = "v",
        isGif = false,
        isVideo = true,
        mediaId = if (ready) "m-v" else null,
        failed = failed,
    )

    @Test
    fun `a video goes on its own`() {
        assertThat(mediaProblem(listOf(video()))).isNull()
        assertThat(mediaProblem(listOf(video(), StagedMedia("p", isGif = false, mediaId = "m-p"))))
            .isEqualTo(VIDEO_ALONE_MESSAGE)
        assertThat(entryProblem("", listOf(video()))).isNull()
        assertThat(entryProblem("hi", listOf(video(ready = false)))).isEqualTo("Your video is still uploading.")
        assertThat(entryProblem("hi", listOf(video(ready = false, failed = true))))
            .isEqualTo("The video didn't upload. Remove it and try again.")
    }

    @Test
    fun `the upload tile counts up`() {
        assertThat(stagedVideoStatus(0f)).isEqualTo("Uploading video…")
        assertThat(stagedVideoStatus(0.42f)).isEqualTo("Uploading video… 42%")
    }

    // ── the action row and the pill grid ──

    @Test
    fun `the card's row is Reply, Echo it, heart, views, More`() {
        assertThat(CARD_ACTIONS).containsExactly(
            CardAction.REPLY,
            CardAction.ECHO,
            CardAction.HEART,
            CardAction.VIEWS,
            CardAction.MORE,
        ).inOrder()
        assertThat(echoItLabel(0)).isEqualTo("Echo it")
        assertThat(echoItLabel(3)).isEqualTo("Echo it 3")
        assertThat(echoItLabel(1_200)).isEqualTo("Echo it 1.2K")
        assertThat(viewsWords(1)).isEqualTo("1 view")
        assertThat(viewsWords(42)).isEqualTo("42 views")
        assertThat(viewsWords(0)).isEqualTo("0 views")
    }

    @Test
    fun `the note page's pills are 3 by 2, views in the first row and no quotes pill`() {
        assertThat(FOCUS_PILL_ROWS).containsExactly(
            listOf(FocusPill.HEARTS, FocusPill.ECHOES, FocusPill.VIEWS),
            listOf(FocusPill.SAVE, FocusPill.SHARE, FocusPill.MORE),
        ).inOrder()
        assertThat(compactCountWords(42, "heart", "hearts")).isEqualTo("42 hearts")
        assertThat(compactCountWords(1, "echo", "echoes")).isEqualTo("1 echo")
        assertThat(compactCountWords(0, "echo", "echoes")).isEqualTo("0 echoes")
        assertThat(compactCountWords(12_345, "view", "views")).isEqualTo("12.3K views")
    }
}
