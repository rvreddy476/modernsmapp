package com.us.android.core.facear

/**
 * Where the Face AR licence stands for this process.
 *
 * Deliberately the same five states, in the same order, with the same meanings
 * as the reel studio's `BanubaState`: one licence, two product lines, and a
 * reader who has understood one gate should not have to learn a second
 * vocabulary to read the other.
 *
 * Only [Ready] offers a try-on. [Unlicensed] is silent — a build without a
 * token is a deliberate configuration, so the entry point simply is not there
 * — while [Invalid] and [Failed] earn a one-line reason, because a licence WAS
 * expected for a product the seller marked as try-on capable. See
 * [tryOnEligibility], which is where that policy is actually decided.
 */
sealed interface FaceArState {
    /** The build carries no token. Terminal; nothing is ever initialised. */
    data object Unlicensed : FaceArState

    /** A token exists; the SDK has not been asked yet, or its answer is pending. */
    data object Initialising : FaceArState

    /** The licence is active and the effect player may be used. */
    data object Ready : FaceArState

    /** The SDK answered: the licence is expired or revoked. */
    data object Invalid : FaceArState

    /** The token was rejected outright, or the native SDK failed to start. */
    data class Failed(val message: String) : FaceArState
}
