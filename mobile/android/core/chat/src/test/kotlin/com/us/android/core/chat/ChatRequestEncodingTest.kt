package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CreateDirectRequest
import com.us.android.core.chat.data.MarkReadRequest
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.chat.data.TypingRequest
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * What this client puts ON the wire.
 *
 * ## WHY THIS FILE EXISTS
 *
 * Every other chat test checks DECODING — the shapes the server sends. Sending
 * was never tested against real bytes: the contracts were captured with curl,
 * which spells every field out by hand, and the controller tests use a fake
 * `ChatApi` that never serialises anything.
 *
 * So this shipped: `SendMessageRequest`'s `type` has a default, and
 * kotlinx.serialization omits a property equal to its default unless told
 * otherwise. The app's `Json` leaves `encodeDefaults` off, so the body was
 * `{"text":"…"}` with no `type`, and message-service binds that field
 * `required,oneof=text media`. Every message the app sent came back 400.
 *
 * It took a real device to find, and the fix is one annotation. These tests use
 * the SAME `Json` configuration the app builds in `NetworkModule` — a test with
 * `encodeDefaults = true` would pass while the app still failed.
 */
class ChatRequestEncodingTest {

    /** Mirrors NetworkModule.provideJson(). Deliberately not `encodeDefaults`. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    @Test
    fun `a sent message carries its type even when it is the default`() {
        val body = json.encodeToString(SendMessageRequest(text = "hello from the device"))

        assertThat(body).contains("\"type\":\"text\"")
        assertThat(body).contains("\"text\":\"hello from the device\"")
    }

    @Test
    fun `an explicitly typed message encodes the same way`() {
        val body = json.encodeToString(SendMessageRequest(type = "text", text = "hi"))

        assertThat(body).contains("\"type\":\"text\"")
    }

    /** `other_user_id`, not `user_id` — the name that cost a 400 during capture. */
    @Test
    fun `direct creation sends other_user_id`() {
        val body = json.encodeToString(CreateDirectRequest("11111111-2222-3333-4444-555555555555"))

        assertThat(body).isEqualTo("""{"other_user_id":"11111111-2222-3333-4444-555555555555"}""")
    }

    /** `message_id`, not `last_read_message_id`. */
    @Test
    fun `mark read sends message_id`() {
        assertThat(json.encodeToString(MarkReadRequest("m1"))).isEqualTo("""{"message_id":"m1"}""")
    }

    @Test
    fun `typing sends its flag`() {
        assertThat(json.encodeToString(TypingRequest(typing = true)))
            .isEqualTo("""{"typing":true}""")
    }
}

// ── Android chat completion pass ────────────────────────────────────────

/**
 * Reactions and deletion address a Scylla row by (bucket, ts, msg_id); the
 * server binds all three `required`, so an omitted field is a 400 the
 * composer cannot explain. `ts` is passed back VERBATIM.
 */
class ChatCompletionRequestEncodingTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    @Test
    fun `a reaction toggle carries emoji bucket and verbatim ts`() {
        val body = json.encodeToString(
            com.us.android.core.chat.data.ToggleReactionRequest(
                emoji = "❤️",
                bucket = "202608",
                ts = "2026-08-25T10:00:00.123Z",
            ),
        )
        assertThat(body).isEqualTo(
            """{"emoji":"❤️","bucket":"202608","ts":"2026-08-25T10:00:00.123Z"}""",
        )
    }

    @Test
    fun `a delete carries bucket and verbatim ts`() {
        val body = json.encodeToString(
            com.us.android.core.chat.data.DeleteMessageRequest(
                bucket = "202608",
                ts = "2026-08-25T10:00:00.123Z",
            ),
        )
        assertThat(body).isEqualTo("""{"bucket":"202608","ts":"2026-08-25T10:00:00.123Z"}""")
    }

    /**
     * Settings are a read-modify-write of two booleans. `false` values MUST
     * be on the wire — an omitted `is_muted` would leave the server value
     * untouched-or-defaulted depending on binding, which is exactly the
     * ambiguity @EncodeDefault removes… except these have no defaults, so
     * both always encode.
     */
    @Test
    fun `conversation settings encode both switches explicitly`() {
        val body = json.encodeToString(
            com.us.android.core.chat.data.ConversationSettingsDto(isMuted = false, isPinned = true),
        )
        assertThat(body).contains("\"is_muted\":false")
        assertThat(body).contains("\"is_pinned\":true")
    }
}
