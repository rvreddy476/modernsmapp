package com.us.android.feature.echo

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.feature.echo.data.EchoErrorCodes
import com.us.android.feature.echo.data.EchoRules
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.MEDIA_LIMIT_MESSAGE
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.StagedMedia
import com.us.android.feature.echo.data.acceptedPicks
import com.us.android.feature.echo.data.canEditText
import com.us.android.feature.echo.data.compactCount
import com.us.android.feature.echo.data.echoErrorMessage
import com.us.android.feature.echo.data.entryProblem
import com.us.android.feature.echo.data.isAuthor
import com.us.android.feature.echo.data.mediaProblem
import com.us.android.feature.echo.data.rateLimitedMessage
import com.us.android.feature.echo.data.shortAge
import org.junit.Test
import java.time.Instant

/** The limits the server enforces, checked first on the phone, and the words every refusal says. */
class EchoRulesTest {

    private fun img(key: String, ready: Boolean = true) = StagedMedia(
        key,
        isGif = false,
        mediaId = if (ready) "m-$key" else null
    )
    private fun gif(key: String) = StagedMedia(key, isGif = true, mediaId = "m-$key")

    @Test
    fun `text is 1 to 500 characters, counted as the server counts runes`() {
        assertThat(entryProblem("a".repeat(500), emptyList())).isNull()
        assertThat(entryProblem("a".repeat(501), emptyList())).isEqualTo("Notes can be up to 500 characters.")
        // An emoji outside the BMP is one rune, two UTF-16 units.
        assertThat(EchoRules.length("😀".repeat(500))).isEqualTo(500)
        assertThat(entryProblem("😀".repeat(500), emptyList())).isNull()
        assertThat(entryProblem("   ", emptyList())).isEqualTo("Write something or add a photo.")
        assertThat(entryProblem("", listOf(img("a")))).isNull()
        assertThat(EchoRules.remaining("  hello  ")).isEqualTo(495)
    }

    @Test
    fun `up to four photos, or one GIF alone`() {
        assertThat(mediaProblem(List(4) { img("$it") })).isNull()
        assertThat(mediaProblem(List(5) { img("$it") })).isEqualTo(MEDIA_LIMIT_MESSAGE)
        assertThat(mediaProblem(listOf(gif("g")))).isNull()
        assertThat(mediaProblem(listOf(gif("g"), img("a")))).isEqualTo(MEDIA_LIMIT_MESSAGE)
        assertThat(mediaProblem(listOf(gif("g"), gif("h")))).isEqualTo(MEDIA_LIMIT_MESSAGE)
    }

    @Test
    fun `picks past the limit are left out`() {
        assertThat(acceptedPicks(emptyList(), List(6) { false })).isEqualTo(4)
        assertThat(acceptedPicks(listOf(img("a"), img("b"), img("c")), listOf(false, false))).isEqualTo(1)
        assertThat(acceptedPicks(emptyList(), listOf(true))).isEqualTo(1)
        assertThat(acceptedPicks(emptyList(), listOf(true, false))).isEqualTo(1)
        assertThat(acceptedPicks(listOf(img("a")), listOf(true))).isEqualTo(0)
        assertThat(acceptedPicks(listOf(gif("g")), listOf(false))).isEqualTo(0)
    }

    @Test
    fun `a photo still uploading or failed holds Post`() {
        assertThat(entryProblem("hi", listOf(img("a", ready = false)))).isEqualTo("Your photos are still uploading.")
        assertThat(entryProblem("hi", listOf(StagedMedia("a", false, failed = true))))
            .isEqualTo("A photo didn't upload. Remove it and try again.")
    }

    private fun note(author: String = "me", created: String = "2026-11-12T09:30:00Z") = Note(
        id = "n1", authorId = author, author = null, text = "t", media = emptyList(), createdAt = created,
        editedAt = null,
        replyToId = null, conversationId = "n1", quoteOfId = null, replyPolicy = ReplyPolicy.EVERYONE, replyCount = 0,
        likeCount = 0, repostCount = 0, liked = false, reposted = false, viewerCanReply = true, quoted = null,
    )

    @Test
    fun `the author edits for 30 minutes, nobody else ever`() {
        val created = Instant.parse("2026-11-12T09:30:00Z")
        assertThat(canEditText(note(), "me", created.plusSeconds(29 * 60 + 59))).isTrue()
        assertThat(canEditText(note(), "me", created.plusSeconds(30 * 60))).isFalse()
        assertThat(canEditText(note(), "someone", created.plusSeconds(60))).isFalse()
        assertThat(canEditText(note(), "", created.plusSeconds(60))).isFalse()
        assertThat(canEditText(note(created = "not a time"), "me", created)).isFalse()
        assertThat(isAuthor(note(), "me")).isTrue()
        assertThat(isAuthor(note(), "you")).isFalse()
        assertThat(isAuthor(note(author = ""), "")).isFalse()
    }

    @Test
    fun `every contract code has its own sentence`() {
        val codes = mapOf(
            EchoErrorCodes.PREMIUM_REQUIRED to "Posting on Echo needs Momentum Premium",
            EchoErrorCodes.ACCOUNT_TOO_NEW to "Accounts can reply once they are 7 days old.",
            EchoErrorCodes.REPLY_NOT_ALLOWED to EchoWords.REPLY_NOT_ALLOWED,
            EchoErrorCodes.NOTE_TOO_LONG to "Notes can be up to 500 characters.",
            EchoErrorCodes.EMPTY_NOTE to "Write something or add a photo.",
            EchoErrorCodes.TOO_MANY_MEDIA to MEDIA_LIMIT_MESSAGE,
            EchoErrorCodes.MEDIA_NOT_ALLOWED to "Echo takes photos and GIFs only.",
            EchoErrorCodes.NOTE_NOT_FOUND to EchoWords.NOTE_NOT_FOUND,
            EchoErrorCodes.EDIT_WINDOW_CLOSED to "Notes can only be edited for 30 minutes after posting.",
            EchoErrorCodes.NOT_AUTHOR to "Only the author can change this note.",
            EchoErrorCodes.ECHO_NOT_ENABLED to "Echo isn't available right now.",
            EchoErrorCodes.PREMIUM_CHECK_UNAVAILABLE to
                "We couldn't check your Premium membership. Try again in a moment.",
            EchoErrorCodes.ACCOUNT_CHECK_UNAVAILABLE to "We couldn't check your account. Try again in a moment.",
            EchoErrorCodes.INVALID_THREAD to "A thread needs 2 to 25 notes, each within the limits.",
            EchoErrorCodes.MEDIA_NOT_OWNED to "One of these photos isn't yours to post.",
            EchoErrorCodes.MEDIA_NOT_FOUND to "One of these photos is missing. Remove it and add it again.",
            EchoErrorCodes.MEDIA_NOT_READY to "Your photos are still processing. Try again in a moment.",
        )
        for ((code, words) in codes) {
            assertThat(echoErrorMessage(AppError.Unknown(code = code, statusCode = 400))).isEqualTo(words)
        }
        // The status that carries each code keeps it: 403, 404, 503.
        assertThat(
            echoErrorMessage(AppError.Forbidden(EchoErrorCodes.PREMIUM_REQUIRED))
        ).isEqualTo(EchoWords.PREMIUM_REQUIRED)
        assertThat(
            echoErrorMessage(AppError.NotFound(code = EchoErrorCodes.NOTE_NOT_FOUND))
        ).isEqualTo(EchoWords.NOTE_NOT_FOUND)
        assertThat(
            echoErrorMessage(AppError.Server(503, EchoErrorCodes.ECHO_NOT_ENABLED))
        ).isEqualTo("Echo isn't available right now.")
        assertThat(echoErrorMessage(AppError.InvalidRequest("bad")))
            .isEqualTo("Something in this note isn't right. Check it and try again.")
        assertThat(echoErrorMessage(AppError.NoNetwork())).isEqualTo(EchoWords.NO_NETWORK)
        assertThat(echoErrorMessage(AppError.Server(500, "INTERNAL_ERROR"))).isEqualTo(EchoWords.GENERIC)
    }

    @Test
    fun `rate limited says when to try again from Retry-After`() {
        assertThat(
            echoErrorMessage(AppError.RateLimited(retryAfterSeconds = 120))
        ).isEqualTo("You're posting too fast. Try again in 2 minutes.")
        assertThat(rateLimitedMessage(61)).isEqualTo("You're posting too fast. Try again in 2 minutes.")
        assertThat(rateLimitedMessage(1)).isEqualTo("You're posting too fast. Try again in 1 second.")
        assertThat(rateLimitedMessage(45)).isEqualTo("You're posting too fast. Try again in 45 seconds.")
        assertThat(rateLimitedMessage(null)).isEqualTo("You're posting too fast. Try again later.")
    }

    @Test
    fun `ages and counts are short`() {
        val created = "2026-11-12T09:30:00Z"
        val t = Instant.parse(created)
        assertThat(shortAge(created, t.plusSeconds(10))).isEqualTo("now")
        assertThat(shortAge(created, t.plusSeconds(5 * 60))).isEqualTo("5m")
        assertThat(shortAge(created, t.plusSeconds(3 * 3600))).isEqualTo("3h")
        assertThat(shortAge(created, t.plusSeconds(2 * 86400))).isEqualTo("2d")
        assertThat(shortAge(created, t.plusSeconds(10 * 86400))).isEqualTo("2026-11-12")
        assertThat(compactCount(0)).isEmpty()
        assertThat(compactCount(999)).isEqualTo("999")
        assertThat(compactCount(1_200)).isEqualTo("1.2K")
        assertThat(compactCount(3_000_000)).isEqualTo("3M")
    }
}
