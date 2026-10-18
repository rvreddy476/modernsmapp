package com.us.android.core.chat.data

import kotlinx.serialization.Serializable

/*
  Chat parity (schema v9): what the new JSON columns hold. Private to the
  data boundary — a feature never sees a column — and every field defaults,
  so a row written by one build reads in the next.
*/

@Serializable
internal data class CachedReaction(val emoji: String, val userIds: List<String> = emptyList())

@Serializable
internal data class CachedPeer(
    val userId: String,
    val displayName: String = "",
    val username: String = "",
    val avatarUrl: String? = null,
    val isOnline: Boolean? = null,
    val lastActiveAt: String? = null,
) {
    fun toDomain() = ChatPeer(userId, displayName, username, avatarUrl, isOnline, lastActiveAt)
}

@Serializable
internal data class CachedStreak(val days: Int, val atRisk: Boolean = false, val expiresAt: String? = null)

@Serializable
internal data class CachedLastMessage(
    val type: String = "",
    val preview: String = "",
    val senderId: String = "",
    val at: String = "",
    val ownStatus: String? = null,
)

@Serializable
internal data class CachedConversationExtras(
    val peer: CachedPeer? = null,
    val streak: CachedStreak? = null,
    val lastMessage: CachedLastMessage? = null,
    val markedUnread: Boolean = false,
    val sender: CachedSender? = null,
) {
    companion object {
        fun of(conversation: Conversation): CachedConversationExtras? {
            val extras = CachedConversationExtras(
                peer = conversation.peer?.let {
                    CachedPeer(it.userId, it.displayName, it.username, it.avatarUrl, it.isOnline, it.lastActiveAt)
                },
                streak = conversation.streak?.let { CachedStreak(it.days, it.atRisk, it.expiresAt) },
                lastMessage = conversation.lastMessage?.let {
                    CachedLastMessage(it.type, it.preview, it.senderId, it.at, it.ownStatus)
                },
                markedUnread = conversation.markedUnread,
                sender = conversation.sender?.let { CachedSender(it.userId, it.displayName, it.username, it.avatarUrl, it.deleted) },
            )
            return extras.takeIf { it != CachedConversationExtras() }
        }
    }
}

/** Fix pass 2: a request's sender, so the cached requests list keeps its names. */
@Serializable
internal data class CachedSender(
    val userId: String = "",
    val displayName: String = "",
    val username: String = "",
    val avatarUrl: String? = null,
    val deleted: Boolean = false,
) {
    fun toDomain() = RequestSender(userId, displayName, username, avatarUrl, deleted)
}

@Serializable
internal data class CachedMedia(
    val kind: String? = null,
    val width: Int? = null,
    val height: Int? = null,
    val durationMs: Long? = null,
    val sensitive: Boolean? = null,
)

@Serializable
internal data class CachedGif(
    val provider: String = "",
    val id: String = "",
    val url: String = "",
    val previewUrl: String? = null,
    val width: Int = 0,
    val height: Int = 0,
    val stillUrl: String? = null,
    val isSticker: Boolean = false,
)

@Serializable
internal data class CachedSticker(val id: String = "", val url: String = "", val width: Int = 0, val height: Int = 0)

@Serializable
internal data class CachedVoice(val durationMs: Long = 0, val waveform: List<Int> = emptyList())

@Serializable
internal data class CachedMessageExtras(
    val forwarded: Boolean = false,
    val voice: CachedVoice? = null,
    val sticker: CachedSticker? = null,
    val gif: CachedGif? = null,
    val profileUserId: String? = null,
    val soundId: String? = null,
    val streamId: String? = null,
    val replyKind: String? = null,
    val media: CachedMedia? = null,
    val deleted: Boolean = false,
    val edited: Boolean = false,
) {
    fun applyTo(message: Message): Message = message.copy(
        forwarded = forwarded,
        voice = voice?.let { VoiceInfo(it.durationMs, it.waveform) },
        sticker = sticker?.let { StickerInfo(it.id, it.url, it.width, it.height) },
        gif = gif?.let { GifInfo(it.provider, it.id, it.url, it.previewUrl, it.width, it.height, it.stillUrl, it.isSticker) },
        profileUserId = profileUserId,
        soundId = soundId,
        streamId = streamId,
        replyKind = replyKind,
        media = media?.let { MediaInfo(it.kind, it.width, it.height, it.durationMs, it.sensitive) },
        deleted = deleted,
        edited = edited,
    )

    companion object {
        fun of(message: Message): CachedMessageExtras? {
            val extras = CachedMessageExtras(
                forwarded = message.forwarded,
                voice = message.voice?.let { CachedVoice(it.durationMs, it.waveform) },
                sticker = message.sticker?.let { CachedSticker(it.id, it.url, it.width, it.height) },
                gif = message.gif?.let { CachedGif(it.provider, it.id, it.url, it.previewUrl, it.width, it.height, it.stillUrl, it.isSticker) },
                profileUserId = message.profileUserId,
                soundId = message.soundId,
                streamId = message.streamId,
                replyKind = message.replyKind,
                media = message.media?.let { CachedMedia(it.kind, it.width, it.height, it.durationMs, it.sensitive) },
                deleted = message.deleted,
                edited = message.edited,
            )
            return extras.takeIf { it != CachedMessageExtras() }
        }
    }
}

/**
 * A typed send waiting in the outbox (voice, sticker, gif, profile, sound,
 * live): every field the request needs, so a send queued before process
 * death goes out exactly as it was made.
 */
@Serializable
data class TypedSend(
    val type: String,
    val text: String = "",
    val mediaId: String? = null,
    val durationMs: Long? = null,
    val waveform: List<Int>? = null,
    val stickerId: String? = null,
    /** A sticker's url, kept only so the queued bubble can draw it. */
    val stickerUrl: String? = null,
    val gifProvider: String? = null,
    val gifId: String? = null,
    /** The GIF's url EXACTLY as the provider returned it (sent, and drawn by the queued bubble). */
    val gifUrl: String? = null,
    val gifWidth: Int? = null,
    val gifHeight: Int? = null,
    val gifStillUrl: String? = null,
    /** A Giphy sticker (fix pass 2): `gif.is_sticker`; null for a GIF. */
    val gifIsSticker: Boolean? = null,
    val userId: String? = null,
    val soundId: String? = null,
    val streamId: String? = null,
    val replyNoteId: String? = null,
    val replyToId: String? = null,
    val replyToPreview: String? = null,
    val replyToSenderId: String? = null,
) {
    /** The wire request. Only this kind's fields are set; the rest stay null and are omitted. */
    fun toRequest(): SendMessageRequest = SendMessageRequest(
        type = type,
        text = text,
        mediaId = mediaId,
        replyToId = replyToId,
        replyToPreview = replyToPreview,
        replyToSenderId = replyToSenderId,
        durationMs = durationMs,
        waveform = waveform,
        stickerId = stickerId,
        gif = if (gifProvider != null && gifId != null) {
            GifRefDto(
                gifProvider,
                gifId,
                url = gifUrl,
                width = gifWidth,
                height = gifHeight,
                stillUrl = gifStillUrl,
                isSticker = gifIsSticker?.takeIf { it },
            )
        } else {
            null
        },
        userId = userId,
        soundId = soundId,
        streamId = streamId,
        replyNoteId = replyNoteId,
    )

    companion object {
        fun voice(mediaId: String, durationMs: Long, waveform: List<Int>, replyTo: ReplyRef? = null) = TypedSend(
            type = VOICE_MESSAGE,
            mediaId = mediaId,
            durationMs = durationMs.coerceIn(1, MAX_VOICE_MILLIS),
            waveform = waveform.take(MAX_WAVEFORM_POINTS).map { it.coerceIn(0, MAX_WAVEFORM_VALUE) },
            replyToId = replyTo?.messageId,
            replyToPreview = replyTo?.preview,
            replyToSenderId = replyTo?.senderId,
        )

        fun sticker(sticker: StickerInfo) = TypedSend(type = STICKER_MESSAGE, stickerId = sticker.id, stickerUrl = sticker.url)

        fun gif(gif: GifInfo) = TypedSend(
            type = GIF_MESSAGE,
            gifProvider = gif.provider,
            gifId = gif.id,
            gifUrl = gif.url,
            gifWidth = gif.width.takeIf { it > 0 },
            gifHeight = gif.height.takeIf { it > 0 },
            gifStillUrl = gif.stillUrl,
            gifIsSticker = gif.isSticker.takeIf { it },
        )

        fun profile(userId: String) = TypedSend(type = PROFILE_MESSAGE, userId = userId)

        /** A reply to someone's Note: a text message carrying `reply_note_id`. */
        fun noteReply(text: String, noteId: String) = TypedSend(type = TEXT_MESSAGE, text = text, replyNoteId = noteId)
    }
}
