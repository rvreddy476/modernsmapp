package com.us.android.feature.post.createhub.banuba

/**
 * Where the advanced (Banuba) reel editor stands for this process.
 *
 * Only [Ready] routes a reel through the SDK; every other state falls
 * through to the Media3 studio. [Unlicensed] is silent (a build without a
 * token is a deliberate configuration), while [Invalid] and [Failed] are
 * worth a muted line on the studio because a licence WAS expected.
 */
sealed interface BanubaState {
    /** The build carries no token. Terminal; nothing is ever initialised. */
    data object Unlicensed : BanubaState

    /** A token exists; the SDK has not been asked yet, or its answer is pending. */
    data object Initialising : BanubaState

    /** The licence is active. */
    data object Ready : BanubaState

    /** The SDK answered: the licence is expired or revoked. */
    data object Invalid : BanubaState

    /** The token was rejected outright (empty or truncated) or the SDK failed to start. */
    data class Failed(val message: String) : BanubaState
}
