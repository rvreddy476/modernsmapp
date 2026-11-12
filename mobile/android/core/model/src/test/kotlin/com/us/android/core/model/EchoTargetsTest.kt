package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Echo's ways in (echo-contract.md, 2026-11-12): the push link
 * `momentum://echo/note/{id}`, the web path `/echo/note/{id}`, the
 * `note_reply` / `note_quote` fallback, and the App Link. Each lands on
 * [NotificationTarget.EchoNote], which belongs to [Product.ECHO] — so a
 * closed Echo is never opened from a link.
 */
class EchoTargetsTest {

    private val id = "f1111111-1111-4111-8111-111111111111"

    @Test
    fun `the push link and the web path open the note`() {
        assertThat(NotificationTarget.parse("momentum://echo/note/$id")).isEqualTo(NotificationTarget.EchoNote(id))
        assertThat(NotificationTarget.parse("/echo/note/$id")).isEqualTo(NotificationTarget.EchoNote(id))
        assertThat(
            NotificationTarget.parse("momentum://echo/note/$id?ref=x")
        ).isEqualTo(NotificationTarget.EchoNote(id))
    }

    @Test
    fun `a bad id or another momentum path opens nothing`() {
        assertThat(NotificationTarget.parse("momentum://echo/note/not-a-uuid")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("/echo/note/../../x")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("momentum://echo/note/$id/extra")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("momentum://post/$id")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("https://evil.example/echo/note/$id")).isEqualTo(NotificationTarget.None)
    }

    @Test
    fun `a reply or quote notice without a link falls back to its entity id`() {
        assertThat(NotificationTarget.echoFallback("note_reply", id)).isEqualTo(NotificationTarget.EchoNote(id))
        assertThat(NotificationTarget.echoFallback("note_quote", id)).isEqualTo(NotificationTarget.EchoNote(id))
        assertThat(NotificationTarget.echoFallback("note_quote", "bad")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.echoFallback("comment", id)).isEqualTo(NotificationTarget.None)
    }

    @Test
    fun `a note belongs to Echo, which is closed until the gateway opens it`() {
        assertThat(productOf(NotificationTarget.EchoNote(id))).isEqualTo(Product.ECHO)
        assertThat(productOpen(Product.ECHO, null)).isFalse()
        assertThat(productOpen(Product.ECHO, emptySet())).isFalse()
        assertThat(productOpen(Product.ECHO, setOf(Product.ECHO))).isTrue()
        assertThat(Product.fromWire("echo")).isEqualTo(Product.ECHO)
        assertThat(Product.ECHO.title).isEqualTo("Echo")
    }

    @Test
    fun `the web link round-trips through the App Link parser`() {
        val link = WebLinks.echoNotePermalink(id)
        assertThat(link).isEqualTo("https://cleestudio.com/echo/note/$id")
        assertThat(WebLinks.targetOf(link)).isEqualTo(WebLinkTarget.EchoNote(id))
        assertThat(WebLinks.targetOf("https://cleestudio.com/echo/note/nope")).isNull()
    }
}
