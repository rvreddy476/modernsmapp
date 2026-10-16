package com.us.android.feature.live.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.feature.live.data.HeartBlock
import com.us.android.feature.live.data.LiveCategoryDto
import com.us.android.feature.live.data.LiveCreatorCardDto
import com.us.android.feature.live.data.LiveStreamDto
import org.junit.Test
import java.io.File
import java.time.ZoneOffset

/**
 * T-30's words: Share's text, the host's name, the blocked-word refusal,
 * the upcoming and past lines — and the founder's rule that live has no
 * gifts, coins or guests anywhere in this feature's code.
 */
class LiveParityWordsTest {

    @Test
    fun `share text is the title, then the web address on its own line`() {
        assertThat(liveShareText("Friday jam", "s1")).isEqualTo("Friday jam\n\nhttps://cleestudio.com/live/s1")
        assertThat(liveShareText("  ", "s1")).isEqualTo("https://cleestudio.com/live/s1")
        assertThat(liveShareText("Friday jam", "")).isNull()
    }

    @Test
    fun `the host's name is the name, else the handle, never the id`() {
        assertThat(creatorName(LiveCreatorCardDto(userId = "u1", name = "Asha", handle = "asha"))).isEqualTo("Asha")
        assertThat(creatorName(LiveCreatorCardDto(userId = "u1", handle = "@asha"))).isEqualTo("@asha")
        assertThat(creatorName(LiveCreatorCardDto(userId = "u1"))).isEqualTo(CREATOR_FALLBACK)
        assertThat(creatorName(null)).isEqualTo(CREATOR_FALLBACK)
    }

    @Test
    fun `the blocked-word refusal says to edit the message`() {
        val copy = chatSendRefusal(AppError.Unknown(code = "CHAT_BLOCKED_WORD", statusCode = 400))
        assertThat(copy).isEqualTo(BLOCKED_WORD_COPY)
        assertThat(copy).contains("Edit it")
    }

    @Test
    fun `a chip falls back to its slug`() {
        assertThat(categoryLabel(LiveCategoryDto(slug = "music", label = "Music"))).isEqualTo("Music")
        assertThat(categoryLabel(LiveCategoryDto(slug = "gaming"))).isEqualTo("gaming")
    }

    @Test
    fun `upcoming, past and recording lines`() {
        assertThat(startsAtLabel("2026-10-14T18:00:00Z", ZoneOffset.UTC)).isEqualTo("Starts 14 Oct, 18:00")
        assertThat(startsAtLabel("", ZoneOffset.UTC)).isNull()
        assertThat(pastStreamLabel(LiveStreamDto(endedAt = "2026-10-12T20:00:00Z", viewerPeak = 140), ZoneOffset.UTC))
            .isEqualTo("12 Oct · Peak 140 watching")
        assertThat(recordingLabel(LiveStreamDto(recordingDurationSeconds = 3_723))).isEqualTo("Recording · 1:02:03")
        assertThat(recordingLabel(LiveStreamDto(recordingDurationSeconds = 65))).isEqualTo("Recording · 1:05")
        assertThat(recordingLabel(LiveStreamDto())).isEqualTo("Recording")
    }

    @Test
    fun `the heart says why it is off`() {
        assertThat(heartLabel(3, null)).contains("Send a heart")
        assertThat(heartLabel(0, HeartBlock.NotLive)).isEqualTo("Hearts open when the stream is live.")
    }

    @Test
    fun `no gift, coin, recharge or guest UI anywhere in live`() {
        val sources = File("src/main").walkTopDown().filter { it.isFile && it.extension in setOf("kt", "xml") }.toList()
        assertThat(sources).isNotEmpty()
        val banned = Regex("""\b(gifts?|coins?|recharge|co-?hosts?|guests?|diamonds?)\b""", RegexOption.IGNORE_CASE)
        val hits = sources.flatMap { file ->
            file.readLines().mapIndexedNotNull { index, line ->
                // The founder's rule may be NAMED in a comment; it may not be built.
                val code = line.substringBefore("//").takeUnless { line.trimStart().startsWith("*") }.orEmpty()
                if (banned.containsMatchIn(code)) "${file.name}:${index + 1}: ${line.trim()}" else null
            }
        }
        assertThat(hits).isEmpty()
    }
}
