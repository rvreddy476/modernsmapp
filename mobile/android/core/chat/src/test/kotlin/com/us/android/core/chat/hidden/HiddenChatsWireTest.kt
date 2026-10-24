package com.us.android.core.chat.hidden

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.Json
import org.junit.Test

/** Request bodies, frames, paths, the media URL and error mapping, against the pinned contract. */
class HiddenChatsWireTest {

    /** Mirrors NetworkModule.provideJson(): no encodeDefaults. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    // ── Bodies ───────────────────────────────────────────────────────────

    @Test
    fun `settings bodies carry only what is being set`() {
        assertThat(settingsBody(secretKey = "abcd", chatPassword = "secret-1").toString())
            .isEqualTo("""{"secret_key":"abcd","chat_password":"secret-1"}""")
        assertThat(settingsBody(secretKey = "wxyz", currentChatPassword = "secret-1").toString())
            .isEqualTo("""{"secret_key":"wxyz","current_chat_password":"secret-1"}""")
        // An empty string is "not changing it"; false is a real value.
        assertThat(settingsBody(secretKey = "", chatPassword = "", currentChatPassword = "secret-1", strict = false).toString())
            .isEqualTo("""{"current_chat_password":"secret-1","strict":false}""")
        assertThat(settingsBody().toString()).isEqualTo("{}")
        // The web's keyboard shortcut is never touched from Android.
        assertThat(settingsBody(secretKey = "abcd", strict = true).toString()).doesNotContain("shortcut")
    }

    @Test
    fun `reset, reveal, open and step-up bodies`() {
        assertThat(resetBody("abcd", "secret-1").toString()).isEqualTo("""{"secret_key":"abcd","chat_password":"secret-1"}""")
        assertThat(resetBody("abcd", "secret-1", strict = true).toString())
            .isEqualTo("""{"secret_key":"abcd","chat_password":"secret-1","strict":true}""")
        assertThat(revealBody("  abcd12 ").toString()).isEqualTo("""{"secret_key":"abcd12"}""")
        // A password is sent exactly as typed — spaces may be part of it.
        assertThat(openBody(" pass word ").toString()).isEqualTo("""{"chat_password":" pass word "}""")
        assertThat(stepUpBody(" 123456 ").toString()).isEqualTo("""{"otp":"123456"}""")
    }

    @Test
    fun `the hidden subscribe frame carries the open token and no entitlement`() {
        assertThat(hiddenSubscribeFrame(json, "c1", "tok"))
            .isEqualTo("""{"type":"conversation.subscribe","conversation_id":"c1","open_token":"tok"}""")
    }

    // ── Paths and URLs ────────────────────────────────────────────────────

    @Test
    fun `the conversation a path is about`() {
        assertThat(conversationIdOfPath("/v1/chat/conversations/c1/messages")).isEqualTo("c1")
        assertThat(conversationIdOfPath("/v1/chat/conversations/c1")).isEqualTo("c1")
        assertThat(conversationIdOfPath("/v1/chat/conversations/a%20b/read")).isEqualTo("a b")
        assertThat(conversationIdOfPath("/v1/chat/conversations/direct")).isNull()
        assertThat(conversationIdOfPath("/v1/chat/conversations/group")).isNull()
        assertThat(conversationIdOfPath("/v1/chat/conversations")).isNull()
        assertThat(conversationIdOfPath("/v1/chat/hidden/open")).isNull()
        assertThat(conversationIdOfPath("/v1/media/m1/serve")).isNull()
        assertThat(isHiddenRoute("/v1/chat/hidden/reveal")).isTrue()
        assertThat(isHiddenRoute("/v1/chat/conversations/c1")).isFalse()
    }

    @Test
    fun `a hidden chat's media URL gets chat_open, and only with a token`() {
        val url = "https://api.example.com/v1/media/m1/serve"
        assertThat(withChatOpen(url, "tok")).isEqualTo("$url?chat_open=tok")
        assertThat(withChatOpen("$url?w=320", "t/k+")).isEqualTo("$url?w=320&chat_open=t%2Fk%2B")
        // Replaced, never doubled.
        assertThat(withChatOpen("$url?chat_open=old", "new")).isEqualTo("$url?chat_open=new")
        assertThat(withChatOpen(url, null)).isEqualTo(url)
        assertThat(withChatOpen(url, "  ")).isEqualTo(url)
        assertThat(withChatOpen("not a url", "tok")).isEqualTo("not a url")
    }

    @Test
    fun `expires_at in every shape the server might use`() {
        val now = 1_000_000L
        assertThat(expiryMillis("2026-10-24T12:00:00Z", now)).isEqualTo(1_792_843_200_000L)
        assertThat(expiryMillis("1792843200", now)).isEqualTo(1_792_843_200_000L)
        assertThat(expiryMillis("1792843200000", now)).isEqualTo(1_792_843_200_000L)
        assertThat(expiryMillis(null, now)).isEqualTo(now + HIDDEN_TOKEN_TTL_MS)
        assertThat(expiryMillis("garbage", now)).isEqualTo(now + HIDDEN_TOKEN_TTL_MS)
    }

    // ── Errors ────────────────────────────────────────────────────────────

    private fun err(code: String, details: String = "null") =
        """{"error":{"code":"$code","message":"m","details":$details}}"""

    @Test
    fun `a wrong key and not configured are one answer`() {
        assertThat(hiddenErrorOf(404, err("NOT_FOUND"))).isEqualTo(HiddenError.NotFound)
        assertThat(hiddenErrorOf(404, null)).isEqualTo(HiddenError.NotFound)
    }

    @Test
    fun `a wrong chat password says how many tries are left`() {
        assertThat(hiddenErrorOf(401, err("WRONG_PASSWORD", """{"attempts_left":3}""")))
            .isEqualTo(HiddenError.WrongPassword(3))
        assertThat(hiddenErrorOf(401, err("WRONG_PASSWORD", """{"attempts_left":0}""")))
            .isEqualTo(HiddenError.WrongPassword(0))
        assertThat(hiddenErrorOf(401, err("WRONG_PASSWORD"))).isEqualTo(HiddenError.WrongPassword(null))
    }

    @Test
    fun `a lockout says when it ends, from the body or the header`() {
        assertThat(hiddenErrorOf(429, err("LOCKED", """{"retry_after":900}""")))
            .isEqualTo(HiddenError.Locked(900))
        assertThat(hiddenErrorOf(429, """{"error":{"code":"RATE_LIMITED"}}""", retryAfterHeader = "60"))
            .isEqualTo(HiddenError.Locked(60))
        assertThat(hiddenErrorOf(429, null)).isEqualTo(HiddenError.Locked(null))
        // A timestamp, not a duration.
        assertThat(hiddenErrorOf(429, err("LOCKED", """{"retry_after":"2026-10-24T12:15:00Z"}"""), nowMs = 1_792_843_200_000L))
            .isEqualTo(HiddenError.Locked(900))
    }

    @Test
    fun `every pinned code maps to its meaning`() {
        assertThat(hiddenErrorOf(403, err("CHAT_LOCKED"))).isEqualTo(HiddenError.ChatLocked)
        assertThat(hiddenErrorOf(403, err("STEP_UP_REQUIRED"))).isEqualTo(HiddenError.StepUpRequired)
        assertThat(hiddenErrorOf(400, err("SECRETS_MUST_DIFFER"))).isEqualTo(HiddenError.SecretsMustDiffer)
        assertThat(hiddenErrorOf(400, err("INVALID_SECRET_KEY"))).isEqualTo(HiddenError.InvalidSecretKey)
        assertThat(hiddenErrorOf(400, err("INVALID_CHAT_PASSWORD"))).isEqualTo(HiddenError.InvalidChatPassword)
        assertThat(hiddenErrorOf(400, err("SECRETS_REQUIRED"))).isEqualTo(HiddenError.Invalid("m"))
        assertThat(hiddenErrorOf(409, err("HIDDEN_CHATS_NOT_CONFIGURED"))).isEqualTo(HiddenError.NotConfigured)
        assertThat(hiddenErrorOf(503, err("HIDDEN_CHATS_UNAVAILABLE"))).isEqualTo(HiddenError.Unavailable)
        assertThat(hiddenErrorOf(503, err("UNAVAILABLE"))).isEqualTo(HiddenError.Unavailable)
        assertThat(hiddenErrorOf(0, null)).isEqualTo(HiddenError.Network)
        assertThat(hiddenErrorOf(500, err("INTERNAL_ERROR"))).isEqualTo(HiddenError.Unknown(500, "INTERNAL_ERROR"))
    }

    @Test
    fun `step-up refusals keep identity's meaning`() {
        assertThat(stepUpOutcomeOf(401, err("INVALID_OTP"), null, 0)).isEqualTo(StepUpOutcome.WrongCode)
        assertThat(stepUpOutcomeOf(401, err("OTP_REPLAYED"), null, 0)).isEqualTo(StepUpOutcome.Replayed)
        assertThat(stepUpOutcomeOf(403, err("MFA_NOT_ENROLLED"), null, 0)).isEqualTo(StepUpOutcome.NoTwoFactor)
        assertThat(stepUpOutcomeOf(429, err("RATE_LIMITED", """{"retry_after_seconds":300}"""), null, 0))
            .isEqualTo(StepUpOutcome.RateLimited(300))
        assertThat(stepUpOutcomeOf(500, null, null, 0)).isEqualTo(StepUpOutcome.Failed)
    }

    @Test
    fun `the right words for each refusal`() {
        assertThat(settingsErrorMessage(HiddenError.SecretsMustDiffer)).isEqualTo(SECRETS_MUST_DIFFER_TEXT)
        assertThat(settingsErrorMessage(HiddenError.WrongPassword(2))).isEqualTo("That password isn't right. 2 tries left.")
        assertThat(openErrorMessage(HiddenError.Locked(3600))).isEqualTo("Too many wrong tries. Try again in 1 hour.")
        assertThat(openErrorMessage(HiddenError.NotFound)).isEqualTo("Hidden chats locked again. Enter your secret key first.")
    }
}
