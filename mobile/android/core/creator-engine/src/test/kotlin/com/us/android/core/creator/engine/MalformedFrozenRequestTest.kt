package com.us.android.core.creator.engine

import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.Canonical
import com.us.android.core.database.CreatorLegacyRecoveryEntity
import org.junit.Test

/**
 * CS-A-LB-4 — malformed frozen JSON quarantines; it never throws, and it never
 * reaches the network.
 *
 * ## THE DEFECT THIS CLOSES
 *
 * `verifyFrozenRequest` caught only the top-level parse. Everything after it —
 * `jsonPrimitive`, `jsonArray`, the per-element conversions — throws on JSON
 * that is perfectly valid but shaped unexpectedly. A payload persisted by an
 * older or broken client would therefore blow up the recovery route itself,
 * which is the one code path that exists to rescue that exact user.
 *
 * `media_ids` absent was also silently read as `[]`, so a payload with no media
 * field at all could pass the text-retry branch and be replayed.
 *
 * ## THE SPY
 *
 * Every case below runs through a transport that counts calls. Quarantine is
 * only meaningful if nothing was sent, and "returns Quarantine" does not by
 * itself prove that.
 */
class MalformedFrozenRequestTest {

    /**
     * Stands in for whatever will eventually replay a frozen request.
     *
     * It records calls and nothing else. If a malformed payload ever reaches it,
     * the count is non-zero and the test says so.
     */
    private class SpyTransport {
        var calls = 0
            private set

        fun replay(@Suppress("UNUSED_PARAMETER") body: String) {
            calls++
        }
    }

    private val mediaId = "6f3b1c58-2a41-4e0d-9c77-1b5a0d8e4f21"

    /** Builds a recovery whose stored hash/length genuinely describe [frozen]. */
    private fun recovery(frozen: String, mediaId: String? = null) =
        CreatorLegacyRecoveryEntity(
            recoveryId = "r1",
            kind = CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH,
            text = "a draft",
            language = "en",
            mediaId = mediaId,
            creationKey = "c2e3d4f5-6a7b-4c8d-9e0f-1a2b3c4d5e6f",
            frozenRequestJson = frozen,
            frozenRequestSha = Canonical.sha256Hex(frozen.toByteArray()),
            frozenRequestLen = frozen.toByteArray().size,
            createdAtMillis = 1,
        )

    /**
     * Runs the gate and asserts nothing was transmitted.
     *
     * The transport is only touched on [FrozenRequestVerdict.Retry], which is
     * what makes "zero calls" equivalent to "quarantined before the network".
     */
    private fun verifyWithSpy(
        frozen: String,
        mediaId: String? = null,
    ): Pair<FrozenRequestVerdict, Int> {
        val spy = SpyTransport()
        val verdict = LegacyAdoption.verifyFrozenRequest(recovery(frozen, mediaId))
        if (verdict is FrozenRequestVerdict.Retry) spy.replay(frozen)
        return verdict to spy.calls
    }

    // ------------------------------------------------------------------
    // The table: every shape a persisted payload could actually take
    // ------------------------------------------------------------------

    private val malformed = listOf(
        "not json at all" to "garbage",
        "a JSON array, not an object" to """["post_type","text"]""",
        "a JSON scalar, not an object" to """"just a string"""",
        "post_type absent" to """{"media_ids":[]}""",
        "post_type null" to """{"post_type":null,"media_ids":[]}""",
        "post_type an object" to """{"post_type":{"v":"text"},"media_ids":[]}""",
        "post_type a number" to """{"post_type":7,"media_ids":[]}""",
        "post_type a boolean" to """{"post_type":true,"media_ids":[]}""",
        "media_ids absent" to """{"post_type":"text"}""",
        "media_ids null" to """{"post_type":"text","media_ids":null}""",
        "media_ids an object" to """{"post_type":"text","media_ids":{"0":"x"}}""",
        "media_ids a scalar" to """{"post_type":"text","media_ids":"x"}""",
        "media_ids a number" to """{"post_type":"text","media_ids":3}""",
        "media_ids mixed elements" to """{"post_type":"image","media_ids":["a",7]}""",
        "media_ids nested arrays" to """{"post_type":"image","media_ids":[["a"]]}""",
        "media_ids null element" to """{"post_type":"image","media_ids":[null]}""",
        "media_ids object element" to """{"post_type":"image","media_ids":[{"id":"a"}]}""",
        "empty object" to "{}",
        "empty string" to "",
    )

    @Test
    fun `every malformed shape quarantines without throwing and without transmitting`() {
        malformed.forEach { (label, frozen) ->
            val (verdict, calls) = runCatching { verifyWithSpy(frozen) }
                .getOrElse { thrown ->
                    throw AssertionError("[$label] threw instead of quarantining: $thrown", thrown)
                }

            assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
            assertThat(calls).isEqualTo(0)
        }
    }

    /** The same table, with a media id present, must also never throw. */
    @Test
    fun `every malformed shape quarantines when a media id is present too`() {
        malformed.forEach { (label, frozen) ->
            val (verdict, calls) = runCatching { verifyWithSpy(frozen, mediaId) }
                .getOrElse { thrown ->
                    throw AssertionError("[$label] threw instead of quarantining: $thrown", thrown)
                }

            assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
            assertThat(calls).isEqualTo(0)
        }
    }

    /**
     * ABSENT is not `[]`.
     *
     * This is the case that used to slip through: `media_ids` missing became an
     * empty list, so a payload with no media field passed the text-retry branch
     * and would have been replayed under a live idempotency key.
     */
    @Test
    fun `an absent media_ids is not treated as an empty array`() {
        val (verdict, calls) = verifyWithSpy("""{"post_type":"text"}""")

        assertThat(verdict).isInstanceOf(FrozenRequestVerdict.Quarantine::class.java)
        assertThat(calls).isEqualTo(0)
    }

    // ------------------------------------------------------------------
    // Well-formed payloads must still pass
    // ------------------------------------------------------------------

    /** The real text request from fixture 4c. */
    @Test
    fun `a well-formed text request still retries`() {
        val frozen =
            """{"text":"Notes from a slow morning","visibility":"public","content_type":"post",""" +
                """"post_type":"text","app_origin":"postbook","media_ids":[],"language":"en",""" +
                """"distribution":{"version":1,"main_feed":true,"notify_subscribers":false,""" +
                """"create_reel_preview":false}}"""

        val (verdict, calls) = verifyWithSpy(frozen)

        assertThat(verdict).isEqualTo(FrozenRequestVerdict.Retry)
        assertThat(calls).isEqualTo(1)
    }

    /** The real image request shape. */
    @Test
    fun `a well-formed image request still retries`() {
        val frozen =
            """{"text":"Terrace","visibility":"public","content_type":"post",""" +
                """"post_type":"image","app_origin":"postbook","media_ids":["$mediaId"],""" +
                """"language":"en","distribution":{"version":1,"main_feed":true,""" +
                """"notify_subscribers":false,"create_reel_preview":false}}"""

        val (verdict, calls) = verifyWithSpy(frozen, mediaId)

        assertThat(verdict).isEqualTo(FrozenRequestVerdict.Retry)
        assertThat(calls).isEqualTo(1)
    }

    /**
     * An unknown extra field does NOT quarantine.
     *
     * Forward compatibility is deliberate here: a newer client may add a field,
     * and refusing to replay an otherwise-valid frozen request over an unknown
     * key would strand a publish that already committed.
     */
    @Test
    fun `an unrecognised extra field does not prevent a retry`() {
        val frozen = """{"post_type":"text","media_ids":[],"something_new":42}"""

        val (verdict, _) = verifyWithSpy(frozen)

        assertThat(verdict).isEqualTo(FrozenRequestVerdict.Retry)
    }
}
