package com.us.android.core.call.signaling

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

/**
 * The 1:1 call-signaling wire protocol over the session socket.
 *
 * OUTBOUND frames are the flat envelopes ws-gateway relays after verifying
 * the pair holds a live call authorization (callauth; server.go:649-679):
 * `target_user_id` addresses the peer, everything else rides verbatim. The
 * gateway injects `sender_id` server-side — the client NEVER sends it, and
 * inbound parsing trusts only the injected value.
 *
 * PROTOCOL ORDER (chosen so a cold-started callee still works — the offer is
 * never sent to a device that may not be listening yet):
 *
 *   caller: REST create → `call_ring` ........................ ringing
 *   callee: REST accept (callauth → active) → `call_accept`
 *   caller: `call_offer` (SDP)
 *   callee: `call_answer` (SDP)
 *   both:   `ice_candidate` trickle (gateway relays these only once the
 *           callee's accept moved the pair to `active` — the C3 IP-leak gate)
 *   either: `call_end` / `call_decline` / `call_busy`
 */
object CallSignals {

    fun ring(json: Json, targetUserId: String, callId: String, video: Boolean): String =
        json.encodeToString(
            JsonObject.serializer(),
            buildJsonObject {
                put("type", "call_ring")
                put("target_user_id", targetUserId)
                put("call_id", callId)
                put("video", video)
            },
        )

    fun accept(json: Json, targetUserId: String, callId: String): String =
        control(json, "call_accept", targetUserId, callId)

    fun decline(json: Json, targetUserId: String, callId: String): String =
        control(json, "call_decline", targetUserId, callId)

    fun busy(json: Json, targetUserId: String, callId: String): String =
        control(json, "call_busy", targetUserId, callId)

    fun end(json: Json, targetUserId: String, callId: String): String =
        control(json, "call_end", targetUserId, callId)

    fun offer(json: Json, targetUserId: String, callId: String, sdp: String): String =
        sdpFrame(json, "call_offer", targetUserId, callId, sdp)

    fun answer(json: Json, targetUserId: String, callId: String, sdp: String): String =
        sdpFrame(json, "call_answer", targetUserId, callId, sdp)

    fun iceCandidate(
        json: Json,
        targetUserId: String,
        callId: String,
        candidate: String,
        sdpMid: String,
        sdpMLineIndex: Int,
    ): String = json.encodeToString(
        JsonObject.serializer(),
        buildJsonObject {
            put("type", "ice_candidate")
            put("target_user_id", targetUserId)
            put("call_id", callId)
            put("candidate", candidate)
            put("sdp_mid", sdpMid)
            put("sdp_mline_index", sdpMLineIndex)
        },
    )

    private fun control(json: Json, type: String, targetUserId: String, callId: String): String =
        json.encodeToString(
            JsonObject.serializer(),
            buildJsonObject {
                put("type", type)
                put("target_user_id", targetUserId)
                put("call_id", callId)
            },
        )

    private fun sdpFrame(
        json: Json,
        type: String,
        targetUserId: String,
        callId: String,
        sdp: String,
    ): String = json.encodeToString(
        JsonObject.serializer(),
        buildJsonObject {
            put("type", type)
            put("target_user_id", targetUserId)
            put("call_id", callId)
            put("sdp", sdp)
        },
    )
}

/** A validated inbound signal. Anything that fails validation parses to null. */
sealed interface CallSignal {
    val senderId: String
    val callId: String

    data class Ring(
        override val senderId: String,
        override val callId: String,
        val video: Boolean,
    ) : CallSignal

    data class Accept(override val senderId: String, override val callId: String) : CallSignal
    data class Decline(override val senderId: String, override val callId: String) : CallSignal
    data class Busy(override val senderId: String, override val callId: String) : CallSignal
    data class End(override val senderId: String, override val callId: String) : CallSignal

    data class Offer(
        override val senderId: String,
        override val callId: String,
        val sdp: String,
    ) : CallSignal

    data class Answer(
        override val senderId: String,
        override val callId: String,
        val sdp: String,
    ) : CallSignal

    data class Ice(
        override val senderId: String,
        override val callId: String,
        val candidate: String,
        val sdpMid: String,
        val sdpMLineIndex: Int,
    ) : CallSignal
}

/**
 * Parses one relayed frame FAIL-CLOSED (chat directive §5.3, same rule as
 * chat frames): a signal whose `sender_id` or `call_id` is missing, blank or
 * not a UUID-shaped string is REFUSED, never repaired — `sender_id` is the
 * gateway-injected identity every downstream decision keys on. SDP and ICE
 * payload fields must be present and non-blank for their types.
 */
@Suppress("ReturnCount", "CyclomaticComplexMethod")
fun parseCallSignal(type: String, frame: JsonObject): CallSignal? {
    val senderId = frame.str("sender_id") ?: return null
    val callId = frame.str("call_id") ?: return null
    if (!senderId.isUuidShaped() || !callId.isUuidShaped()) return null

    return when (type) {
        "call_ring" -> CallSignal.Ring(
            senderId = senderId,
            callId = callId,
            video = frame["video"]?.jsonPrimitive?.booleanOrNull ?: false,
        )
        "call_accept" -> CallSignal.Accept(senderId, callId)
        "call_decline", "call_reject" -> CallSignal.Decline(senderId, callId)
        "call_busy" -> CallSignal.Busy(senderId, callId)
        "call_end" -> CallSignal.End(senderId, callId)
        "call_offer" -> frame.str("sdp")?.let { CallSignal.Offer(senderId, callId, it) }
        "call_answer" -> frame.str("sdp")?.let { CallSignal.Answer(senderId, callId, it) }
        "ice_candidate" -> {
            val candidate = frame.str("candidate") ?: return null
            val sdpMid = frame.str("sdp_mid") ?: return null
            val index = frame["sdp_mline_index"]?.jsonPrimitive?.intOrNull ?: return null
            CallSignal.Ice(senderId, callId, candidate, sdpMid, index)
        }
        else -> null
    }
}

private fun JsonObject.str(key: String): String? =
    runCatching { this[key]?.jsonPrimitive?.contentOrNull }.getOrNull()?.takeIf { it.isNotBlank() }

private const val UUID_LENGTH = 36
private const val UUID_DASHES = 4

private fun String.isUuidShaped(): Boolean =
    length == UUID_LENGTH && count { it == '-' } == UUID_DASHES
