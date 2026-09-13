package com.us.android.core.facear

/**
 * Whether a product screen shows a "Try on" action, and what it says when it
 * cannot.
 *
 * ## THE DECISION, STATED ONCE
 *
 * A licence has five states and a product may or may not be try-on capable,
 * which is ten combinations and therefore exactly the kind of thing that gets
 * decided differently on each screen that asks. It is decided here, by a pure
 * function, and every screen renders the answer.
 *
 * The rule, and the reasoning:
 *
 *  * **Not capable → [Hidden].** Nothing is drawn. Most products are not
 *    try-on-able and a greyed-out control on all of them is noise.
 *  * **Capable + [FaceArState.Ready] → [Offer].** The action opens.
 *  * **Capable + [FaceArState.Unlicensed] → [Hidden].** A build with no token
 *    is a deliberate configuration, not a fault the viewer can act on. This is
 *    the same silence the reel studio keeps for the same state.
 *  * **Capable + [FaceArState.Initialising] → [Hidden].** Transient and
 *    usually sub-second; a control that appears a moment after the page reads
 *    as a glitch, and one that says "starting…" invites a tap that does
 *    nothing.
 *  * **Capable + [FaceArState.Invalid] or [FaceArState.Failed] → [Unavailable]
 *    with a one-line reason.** A try-on WAS expected for this product, so
 *    silence would look like a missing feature rather than a licence problem.
 *    The line names the licence, not the vendor and not the exception.
 */
sealed interface TryOnEligibility {
    /** Draw the action. */
    data object Offer : TryOnEligibility

    /** Draw nothing at all. */
    data object Hidden : TryOnEligibility

    /** Draw one muted line saying why, and no tappable control. */
    data class Unavailable(val reason: String) : TryOnEligibility
}

/**
 * The decision. [descriptor] is null for a product whose payload carried no
 * `try_on` object at all — an older server, which means exactly "not capable".
 */
fun tryOnEligibility(descriptor: TryOnDescriptor?, state: FaceArState): TryOnEligibility {
    if (descriptor?.isUsable != true) return TryOnEligibility.Hidden
    return when (state) {
        FaceArState.Ready -> TryOnEligibility.Offer
        FaceArState.Unlicensed, FaceArState.Initialising -> TryOnEligibility.Hidden
        FaceArState.Invalid -> TryOnEligibility.Unavailable(LICENCE_EXPIRED)
        is FaceArState.Failed -> TryOnEligibility.Unavailable(TRY_ON_UNAVAILABLE)
    }
}

/**
 * Whether the effect can be loaded at all, and what to say when it cannot.
 *
 * ## THE FAILURE THIS EXISTS FOR: A BLACK SCREEN, NOT AN ERROR
 *
 * An effect the player loads **takes over rendering**. A bundle whose scene
 * has nothing in it therefore renders nothing — not the camera, not an error,
 * a black rectangle — while the SDK reports a successful load and a successful
 * activation, and every layer of this app agrees that everything worked. It is
 * the worst failure mode the screen has, because it looks like a crash and
 * arrives after all the good news.
 *
 * Two independent facts can catch it, and both are used:
 *
 *  * **before loading** — the bundle's own manifest declares no scene content
 *    ([TryOnEffect.declaresSceneContent] is false). Decidable from bytes in
 *    the APK, so the effect is never handed to the player in the first place;
 *  * **after loading** — the bundle answered `momentumTryOnReady()` with false,
 *    meaning its scene did not give it what it needs (for a makeup bundle,
 *    that its prefab's material was not in the scene). The effect is then
 *    unloaded, which puts the plain camera back.
 *
 * Either way the camera keeps working, the shade carousel keeps working, and
 * Add to bag keeps working — none of them depends on the effect. The shopper
 * gets one line saying the preview is not there, and that is the honest
 * partial. A black rectangle is not.
 *
 * Null when there is nothing to say: no effect resolved at all (the surface
 * already shows a full empty state for that), or a bundle that has not said it
 * cannot draw.
 */
fun tryOnPreviewNotice(effect: TryOnEffect?, drawable: Boolean?): String? {
    val resolved = effect ?: return null
    // `false`, explicitly — never `!= true`. Null means "we could not read the
    // manifest" or "the bundle does not implement the readiness function", and
    // treating either as a refusal would disable a working try-on.
    if (resolved.declaresSceneContent == false || drawable == false) return PREVIEW_UNAVAILABLE
    return null
}

/**
 * The lines a viewer can be shown.
 *
 * None names Banuba, a token, a prefab or an exception message: the vendor is
 * not the viewer's business, and an SDK message ("libbanuba.so not found",
 * "cannot read property 'findParameter' of null") tells them nothing they can
 * act on. That text belongs in the debug diagnostics panel, verbatim, where
 * the person who can act on it will read it.
 */
internal const val LICENCE_EXPIRED = "Try-on is unavailable — the licence has expired."
internal const val TRY_ON_UNAVAILABLE = "Try-on could not start on this device."

/**
 * Said when the camera is live but nothing can be drawn on the face.
 *
 * It claims only what is true — the preview is missing, everything else works
 * — and it does not promise a fix or blame the device, because the cause is
 * neither: the bundle this build ships cannot draw, and that is ours.
 */
internal const val PREVIEW_UNAVAILABLE =
    "Shade preview isn't ready in this build yet — the camera, the shades and your bag all still work."
