package com.us.android.feature.dating.clips

import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.network.CardClipDto
import com.us.android.feature.dating.network.ClipTooLongDetailsDto
import com.us.android.feature.dating.network.PromptAnswerDto
import kotlinx.serialization.json.Json

/*
 * Voice and video prompt answers (mechanic M15, DATING_MEDIA_PROMPTS_ENABLED).
 *
 * A prompt answer may carry one short clip: a voice recording made here
 * (MediaRecorder, AAC in an .m4a) or a video chosen from the system picker.
 * Either is at most 30 seconds — checked here before any upload, and again by
 * the server. It uploads the way a dating photo does (`:core:media`, no lease)
 * and is attached with `PUT /prompts/:promptId/clip {media_id}`. Moderation
 * decides whether others see it: until it is approved only the owner knows it
 * exists, and a card carries only approved clips, as a route that needs the
 * bearer and answers 307 to a short-lived URL.
 *
 * `404 MECHANIC_NOT_ENABLED` from any clip route hides the clip controls for
 * the rest of the session.
 */

/** The two kinds of clip, by their wire words. */
enum class ClipKind(val wire: String) {
    AUDIO("audio"),
    VIDEO("video"),
    ;

    companion object {
        /** Null for a blank or unknown kind: such a clip is not shown at all. */
        fun fromWire(wire: String?): ClipKind? = entries.firstOrNull { it.wire == wire?.trim() }
    }
}

/** Where the owner's clip stands, from `clip_status`. */
enum class ClipStatus {
    /** Approved: others see it. */
    LIVE,

    /** pending or pending_review: not shown to anyone else yet. */
    CHECKING,

    /** rejected: never shown; the owner sees why. */
    REJECTED,
    ;

    companion object {
        /**
         * Fails closed: only the exact word `approved` is live, and `rejected`
         * is rejected; anything else — pending, pending_review, a future
         * word — is still being checked.
         */
        fun fromWire(status: String?): ClipStatus = when (status?.trim()) {
            "approved" -> LIVE
            "rejected" -> REJECTED
            else -> CHECKING
        }
    }
}

/** The owner's own clip on one of their answers. */
data class OwnClipUi(
    val kind: ClipKind,
    val durationMs: Long,
    val status: ClipStatus,
    /** The server's reason for a rejected clip; null otherwise. */
    val reason: String? = null,
) {
    /** What the editor says about it. */
    val statusLine: String
        get() = when (status) {
            ClipStatus.LIVE -> ClipCopy.LIVE
            ClipStatus.CHECKING -> ClipCopy.CHECKING
            ClipStatus.REJECTED -> reason?.trim()?.takeIf { it.isNotEmpty() } ?: ClipCopy.REJECTED
        }
}

/** A card's clip, ready to play: [url] is absolute, on the API origin. */
data class PromptClipUi(
    val kind: ClipKind,
    /** 0 when the server did not say. */
    val durationMs: Long,
    val url: String,
)

/** The rules, in one place. Pure. */
object ClipRules {

    /** The longest clip the server accepts. */
    const val MAX_CLIP_MS = 30_000L

    /** Seconds on the recording countdown. */
    const val MAX_RECORD_SECONDS = (MAX_CLIP_MS / 1_000L).toInt()

    /** The `DatingSession.disableMechanic` key for clips. */
    const val MECHANIC = "prompt_clips"

    private val CLIP_PATH = Regex("^/v1/dating/people/([^/?#]+)/prompts/(\\d+)/clip$")

    /**
     * A card's clip route, or null for anything else. Fail closed: only the
     * exact dating route is ever loaded with the bearer.
     */
    fun clipPath(path: String?): String? = path?.trim()?.takeIf { CLIP_PATH.matches(it) }

    /** True when a picked video may be uploaded: its length is known and within the limit. */
    fun fits(durationMs: Long?): Boolean = durationMs != null && durationMs in 1..MAX_CLIP_MS

    /** The owner's clip from `GET /prompts`, or null when the answer has none (Go omits every clip field then). */
    fun ownClip(dto: PromptAnswerDto): OwnClipUi? {
        val kind = ClipKind.fromWire(dto.clipKind) ?: return null
        if (dto.clipStatus.isNullOrBlank()) return null
        return OwnClipUi(
            kind = kind,
            durationMs = (dto.clipDurationMs ?: 0L).coerceAtLeast(0L),
            status = ClipStatus.fromWire(dto.clipStatus),
            reason = dto.clipReason?.trim()?.takeIf { it.isNotEmpty() },
        )
    }

    /** A card's clip, resolved by [absolute]; null when the kind or the route is not one we know. */
    fun cardClip(dto: CardClipDto?, absolute: (String) -> String): PromptClipUi? {
        dto ?: return null
        val kind = ClipKind.fromWire(dto.kind) ?: return null
        val path = clipPath(dto.url) ?: return null
        return PromptClipUi(kind = kind, durationMs = dto.durationMs.coerceAtLeast(0L), url = absolute(path))
    }

    /** "0:08", "0:30", "1:05". Blank for an unknown length. */
    fun duration(ms: Long): String {
        if (ms <= 0L) return ""
        val total = ((ms + 500L) / 1_000L).coerceAtLeast(1L)
        return "${total / 60}:${(total % 60).toString().padStart(2, '0')}"
    }

    /** The countdown while recording: "0:24 left". */
    fun secondsLeft(seconds: Int): String = "${duration(seconds.coerceAtLeast(0) * 1_000L).ifEmpty { "0:00" }} left"
}

/** The words. Our own. */
object ClipCopy {
    const val RECORD_VOICE = "Record voice"
    const val CHOOSE_VIDEO = "Choose video"
    const val STOP = "Stop"
    const val CANCEL = "Cancel"
    const val REMOVE = "Remove clip"
    const val RECORDING = "Recording"
    const val UPLOADING = "Uploading your clip"
    const val VOICE_ANSWER = "Voice answer"
    const val VIDEO_ANSWER = "Video answer"

    const val LIVE = "Live on your profile"
    const val CHECKING = "Being checked"
    const val REJECTED = "This clip can't be shown on your profile."

    const val ADDED_LIVE = "Clip added. It's live on your profile."
    const val ADDED_CHECKING = "Clip added. We're checking it before others can see it."
    const val REMOVED = "Clip removed."

    const val MIC_TITLE = "Microphone for a voice answer"
    const val MIC_BODY = "Pulse uses the microphone only while you record a voice answer of up to 30 seconds. Nothing is recorded in the background."
    const val MIC_CONTINUE = "Continue"
    const val MIC_DENIED = "Microphone access is off. Allow it in Settings to record a voice answer."

    const val RECORD_FAILED = "The recording didn't save. Try again."
    const val VIDEO_UNREADABLE = "That video couldn't be read. Choose another."
    const val VIDEO_TOO_LONG = "Choose a video of 30 seconds or less."
    const val STILL_PROCESSING = "Your clip is still processing. Try again shortly."
    const val UNSUPPORTED = "Only a voice recording or a video can answer a prompt."
    const val MEDIA_NOT_FOUND = "That upload couldn't be found. Try again."
    const val UNAVAILABLE = "Clips aren't available right now. Try again later."
    const val NOT_ENABLED = "Voice and video answers aren't available right now."

    const val PLAY = "Play"
    const val PAUSE = "Pause"
    const val MUTE = "Mute"
    const val UNMUTE = "Turn sound on"
    const val UNPLAYABLE = "This clip can't be played right now."

    fun tooLong(maxMs: Long): String {
        val seconds = (maxMs / 1_000L).takeIf { it > 0 } ?: (ClipRules.MAX_CLIP_MS / 1_000L)
        return "Keep your clip to $seconds seconds or less."
    }

    fun label(kind: ClipKind): String = if (kind == ClipKind.AUDIO) VOICE_ANSWER else VIDEO_ANSWER

    /** "Voice answer · 0:08". */
    fun summary(kind: ClipKind, durationMs: Long): String =
        listOf(label(kind), ClipRules.duration(durationMs)).filter { it.isNotEmpty() }.joinToString(" · ")
}

/** How a clip refusal reads. [error] codes are the server's; anything else is the module's usual words. */
internal fun clipRefusal(error: DatingError, json: Json): String = when (error.code) {
    CODE_NOT_READY -> ClipCopy.STILL_PROCESSING
    CODE_TOO_LONG -> ClipCopy.tooLong(error.detailsAs(json, ClipTooLongDetailsDto.serializer())?.maxMs ?: 0L)
    CODE_UNSUPPORTED -> ClipCopy.UNSUPPORTED
    CODE_MEDIA_NOT_FOUND -> ClipCopy.MEDIA_NOT_FOUND
    CODE_MEDIA_UNAVAILABLE -> ClipCopy.UNAVAILABLE
    CODE_MECHANIC_NOT_ENABLED -> ClipCopy.NOT_ENABLED
    else -> DatingCopy.forError(error, json)
}

internal const val CODE_NOT_READY = "CLIP_NOT_READY"
internal const val CODE_TOO_LONG = "CLIP_TOO_LONG"
internal const val CODE_UNSUPPORTED = "CLIP_UNSUPPORTED"
internal const val CODE_MEDIA_NOT_FOUND = "CLIP_MEDIA_NOT_FOUND"
internal const val CODE_MEDIA_UNAVAILABLE = "CLIP_MEDIA_UNAVAILABLE"
internal const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
