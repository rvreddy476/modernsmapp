package com.us.android.core.call

import com.google.common.truth.Truth.assertThat
import com.us.android.core.call.signaling.CallSignal
import com.us.android.core.call.signaling.CallSignals
import com.us.android.core.call.signaling.parseCallSignal
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Test

/**
 * The signaling wire contract. Outbound frames are pinned to exact bytes —
 * ws-gateway routes on `type` and `target_user_id` verbatim (server.go:649),
 * and the gateway is not ours to adjust from here. Inbound parsing is pinned
 * FAIL-CLOSED: `sender_id` is the gateway-injected identity every routing
 * decision keys on, so a frame without a whole one must be refused.
 */
class CallSignalsTest {

    private val json = Json { ignoreUnknownKeys = true }

    private val caller = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
    private val callId = "11111111-2222-3333-4444-555555555555"

    @Test
    fun `ring frame carries exactly what the gateway relays`() {
        val frame = CallSignals.ring(json, caller, callId, video = true)
        assertThat(frame).isEqualTo(
            """{"type":"call_ring","target_user_id":"$caller","call_id":"$callId","video":true}""",
        )
    }

    @Test
    fun `offer and ice frames carry their payload fields verbatim`() {
        val offer = CallSignals.offer(json, caller, callId, sdp = "v=0 fake-sdp")
        assertThat(offer).isEqualTo(
            """{"type":"call_offer","target_user_id":"$caller","call_id":"$callId","sdp":"v=0 fake-sdp"}""",
        )
        val ice = CallSignals.iceCandidate(json, caller, callId, "candidate:1", "0", 0)
        assertThat(ice).isEqualTo(
            """{"type":"ice_candidate","target_user_id":"$caller","call_id":"$callId",""" +
                """"candidate":"candidate:1","sdp_mid":"0","sdp_mline_index":0}""",
        )
    }

    @Test
    fun `a relayed frame parses with the gateway-injected sender`() {
        val frame = json.parseToJsonElement(
            """{"type":"call_offer","target_user_id":"x","call_id":"$callId",""" +
                """"sdp":"v=0","sender_id":"$caller"}""",
        ).jsonObject

        val signal = parseCallSignal("call_offer", frame)

        assertThat(signal).isEqualTo(CallSignal.Offer(caller, callId, "v=0"))
    }

    @Test
    fun `signals without a whole sender or call id are refused, never repaired`() {
        val cases = listOf(
            // Missing sender entirely (a frame that skipped the gateway).
            """{"type":"call_end","call_id":"$callId"}""",
            // Blank sender.
            """{"type":"call_end","call_id":"$callId","sender_id":""}""",
            // Non-UUID sender — an injected name, not an identity.
            """{"type":"call_end","call_id":"$callId","sender_id":"mallory"}""",
            // Missing call id.
            """{"type":"call_end","sender_id":"$caller"}""",
        )
        for (raw in cases) {
            val frame = json.parseToJsonElement(raw).jsonObject
            assertThat(parseCallSignal("call_end", frame)).isNull()
        }
    }

    @Test
    fun `sdp and ice signals require their payloads`() {
        val noSdp = json.parseToJsonElement(
            """{"type":"call_offer","call_id":"$callId","sender_id":"$caller"}""",
        ).jsonObject
        assertThat(parseCallSignal("call_offer", noSdp)).isNull()

        val noIndex = json.parseToJsonElement(
            """{"type":"ice_candidate","call_id":"$callId","sender_id":"$caller",""" +
                """"candidate":"c","sdp_mid":"0"}""",
        ).jsonObject
        assertThat(parseCallSignal("ice_candidate", noIndex)).isNull()
    }

    @Test
    fun `unmodelled call types parse to null rather than guessing`() {
        val frame = json.parseToJsonElement(
            """{"type":"call_recording_started","call_id":"$callId","sender_id":"$caller"}""",
        ).jsonObject
        assertThat(parseCallSignal("call_recording_started", frame)).isNull()
    }
}
