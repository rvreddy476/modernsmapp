package com.us.android.core.chat.data

import com.us.android.core.chat.hidden.HiddenOutboxCipher
import com.us.android.core.database.ChatPendingSendEntity
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/*
 * Hidden chats × the durable outbox (24 Oct 2026).
 *
 * A send typed in an open hidden chat is a Room row first. Its BODY — text
 * or caption, media id, quoted reply, the typed-send fields — is sealed with
 * [HiddenOutboxCipher] and the content columns are left empty, so a row that
 * outlives the open session (the chats re-locked before it went) is never
 * plain text on disk. What stays readable is what the drain needs to order
 * and count: the idempotency key, the conversation id, the time, the attempt
 * count and the failed flag.
 *
 * No schema change: the sealed envelope in `extrasJson` IS the marker. A
 * typed send's own JSON starts `{"type":`, never with the envelope's key, and
 * a text row's `extrasJson` is null — so a normal chat's row can never be
 * mistaken for a sealed one, whatever its text says.
 *
 * The ciphertext is bound (AAD) to the row's key and conversation, so a
 * sealed body cannot be moved onto another row or another chat.
 */

/** The decrypted body of a sealed row: exactly the content columns. */
@Serializable
internal data class SealedOutboxBody(
    val text: String = "",
    val mediaId: String? = null,
    val replyToId: String? = null,
    val replyToPreview: String? = null,
    val replyToSenderId: String? = null,
    val type: String? = null,
    val extrasJson: String? = null,
)

@Serializable
internal data class SealedOutboxEnvelope(
    @SerialName(SEALED_ENVELOPE_KEY) val sealed: String,
)

internal const val SEALED_ENVELOPE_KEY = "hidden_outbox_v1"
private const val SEALED_PREFIX = "{\"$SEALED_ENVELOPE_KEY\":"

private val outboxJson = Json { ignoreUnknownKeys = true; encodeDefaults = false }

/** True when this row's body is sealed (a hidden chat's row). */
fun ChatPendingSendEntity.isSealed(): Boolean = extrasJson?.startsWith(SEALED_PREFIX) == true

private fun ChatPendingSendEntity.aad(): ByteArray =
    "hidden-outbox-v1|$idempotencyKey|$conversationId".toByteArray(Charsets.UTF_8)

/**
 * This row with its body sealed, or null when it could not be sealed — the
 * caller then writes NOTHING. An already-sealed row comes back unchanged.
 */
internal fun ChatPendingSendEntity.sealedWith(cipher: HiddenOutboxCipher): ChatPendingSendEntity? {
    if (isSealed()) return this
    val body = SealedOutboxBody(
        text = text,
        mediaId = mediaId,
        replyToId = replyToId,
        replyToPreview = replyToPreview,
        replyToSenderId = replyToSenderId,
        type = type,
        extrasJson = extrasJson,
    )
    val plain = outboxJson.encodeToString(SealedOutboxBody.serializer(), body).toByteArray(Charsets.UTF_8)
    val sealed = cipher.seal(plain, aad()) ?: return null
    plain.fill(0)
    return copy(
        text = "",
        mediaId = null,
        replyToId = null,
        replyToPreview = null,
        replyToSenderId = null,
        type = null,
        extrasJson = outboxJson.encodeToString(SealedOutboxEnvelope.serializer(), SealedOutboxEnvelope(sealed)),
    )
}

/**
 * The row as it was before sealing — in memory, for the one send that needs
 * it; never written back. Null when it cannot be opened (the key is gone, a
 * byte changed). A plain row comes back unchanged.
 */
internal fun ChatPendingSendEntity.openedWith(cipher: HiddenOutboxCipher?): ChatPendingSendEntity? {
    if (!isSealed()) return this
    val envelope = runCatching {
        outboxJson.decodeFromString(SealedOutboxEnvelope.serializer(), extrasJson.orEmpty())
    }.getOrNull() ?: return null
    val plain = cipher?.open(envelope.sealed, aad()) ?: return null
    val body = runCatching {
        outboxJson.decodeFromString(SealedOutboxBody.serializer(), String(plain, Charsets.UTF_8))
    }.getOrNull()
    plain.fill(0)
    return body?.let {
        copy(
            text = it.text,
            mediaId = it.mediaId,
            replyToId = it.replyToId,
            replyToPreview = it.replyToPreview,
            replyToSenderId = it.replyToSenderId,
            type = it.type,
            extrasJson = it.extrasJson,
        )
    }
}

/** What a hidden chat's thread may say about its queued sends: counts only, never a body. */
data class HiddenOutboxStatus(
    /** Queued, going as soon as the chat is open. */
    val waiting: Int = 0,
    /** Parked after a refusal: waits for Retry or Discard. */
    val failed: Int = 0,
) {
    val isEmpty: Boolean get() = waiting == 0 && failed == 0
}

/** The thread's small note: "2 messages waiting — sending now". Null when nothing waits. */
fun hiddenOutboxWaitingNote(status: HiddenOutboxStatus): String? =
    status.waiting.takeIf { it > 0 }?.let { n -> "$n ${if (n == 1) "message" else "messages"} waiting — sending now" }

/** The parked rows' note: "1 message not sent". Null when none failed. */
fun hiddenOutboxFailedNote(status: HiddenOutboxStatus): String? =
    status.failed.takeIf { it > 0 }?.let { n -> "$n ${if (n == 1) "message" else "messages"} not sent" }
