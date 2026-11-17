package com.us.android.feature.post.data

import com.us.android.core.feed.data.PostEditApi
import com.us.android.core.feed.data.PostEditPatch
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import kotlinx.coroutines.CancellationException
import javax.inject.Inject

/**
 * 18+ after a create whose answer did not confirm it (lv-9, 2026-11-17).
 *
 * The create carries `age_restricted`; a server that stored it echoes
 * `true` and nothing here runs. Otherwise — a server from before the field —
 * the gate is set by `PATCH /v1/posts/:id {age_restricted:true}` (tried
 * twice), and if that still cannot be confirmed the video is made PRIVATE
 * rather than left open without the gate, and the creator is told. This is
 * the web studio's `secureAgeRestriction`, failing closed the same way.
 */
fun interface AgeRestrictionGuard {
    suspend fun secure(postId: String, visibility: String): AgeGateOutcome
}

sealed interface AgeGateOutcome {
    /** The gate is on. */
    data object Confirmed : AgeGateOutcome

    /** Not confirmed; the video was already private, or was made private. */
    data class Private(val message: String) : AgeGateOutcome

    /** Not confirmed and it could not be made private: the creator must act now. */
    data class Exposed(val message: String) : AgeGateOutcome

    /** What the publish tile says, or null when there is nothing to say. */
    val notice: String?
        get() = when (this) {
            Confirmed -> null
            is Private -> message
            is Exposed -> message
        }

    companion object {
        const val ALREADY_PRIVATE =
            "The 18+ age restriction could not be saved. Your video is private, so no one else can watch it. " +
                "Turn on 18+ in Creator Hub before you make it public."
        const val MADE_PRIVATE =
            "The 18+ age restriction could not be saved, so we made your video private to keep it from " +
                "under-18 viewers. Turn on 18+ in Creator Hub, then make it public again."
        const val EXPOSED =
            "The 18+ age restriction could not be saved and the video could not be made private. It may be " +
                "visible without the age gate. Open Creator Hub now and turn on 18+, or make the video private."
    }
}

class PatchAgeRestrictionGuard @Inject constructor(
    private val api: PostEditApi,
) : AgeRestrictionGuard {

    override suspend fun secure(postId: String, visibility: String): AgeGateOutcome {
        if (patchAgeRestricted(postId) == true) return AgeGateOutcome.Confirmed
        if (visibility == VISIBILITY_PRIVATE) return AgeGateOutcome.Private(AgeGateOutcome.ALREADY_PRIVATE)
        val hidden = attempt { api.update(postId, PostEditPatch(visibility = VISIBILITY_PRIVATE)) } != null
        return if (hidden) {
            AgeGateOutcome.Private(AgeGateOutcome.MADE_PRIVATE)
        } else {
            AgeGateOutcome.Exposed(AgeGateOutcome.EXPOSED)
        }
    }

    /**
     * The server's stored flag after the PATCH, or null when every attempt
     * failed. A failed call is retried; an answer that says false is a server
     * that did not apply it, and a second try will not help.
     */
    private suspend fun patchAgeRestricted(postId: String): Boolean? {
        repeat(PATCH_ATTEMPTS) {
            attempt { api.update(postId, PostEditPatch(ageRestricted = true)).data }
                ?.let { return it.ageRestricted }
        }
        return null
    }

    /** The call's answer, or null when it failed. Cancellation still propagates. */
    // Any failure of the call is "not confirmed"; the outcome, not the exception, reaches the creator.
    @Suppress("TooGenericExceptionCaught", "SwallowedException")
    private suspend fun <T> attempt(call: suspend () -> T): T? = try {
        call()
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        null
    }

    private companion object {
        const val PATCH_ATTEMPTS = 2
    }
}
