package com.us.android.core.facear

/**
 * What the try-on ACTUALLY did, in lines a person can read aloud.
 *
 * ## WHY THIS EXISTS AT ALL
 *
 * The handset this ships to is a personal phone with a live SIM. It is not
 * attached to adb, so there is no logcat, no breakpoint and no iteration loop:
 * **one install has to answer the whole question.** A try-on that fails
 * silently — and every way this can fail is silent, because a bare camera is
 * what you see whether the licence is wrong, the bundle is missing, the effect
 * refused to parse, or the shade call went nowhere — costs a round trip per
 * guess.
 *
 * So the screen reports itself. Every field below is something the code
 * OBSERVED: a return value, a status enum, a vendor callback's own string. No
 * field is inferred from another and none is a default standing in for an
 * answer — "not answered" is a distinct, visible state everywhere it is
 * possible, because "we did not ask" and "the answer was no" send you to
 * different bugs.
 *
 * The same lines go through `Log` under [TRY_ON_LOG_TAG], so a future session
 * that DOES have adb gets them for free.
 *
 * Pure data and a pure function, so every state is provable on the JVM.
 */
data class TryOnDiagnostics(
    /** The licence gate's own state machine. */
    val gate: FaceArState = FaceArState.Initialising,

    /**
     * Validity as the SDK reported it, not as this app assumed it.
     *
     * Null until `LicenseManager.isExpired()` has answered. Deliberately
     * separate from [gate]: the gate folds a licence answer together with
     * start-up failures, and when they disagree that is the interesting case.
     */
    val licenceValid: Boolean? = null,

    /** The slug the server's descriptor named. The only join to a file on disk. */
    val effectSlug: String = "",

    /** The bundle, once a source resolved one. */
    val effect: TryOnEffect? = null,

    /** In the source's own words: which source answered, or why none could. */
    val resolution: String? = null,

    /** Whether the bundle's `config.json` was found. Null when nothing looked. */
    val manifestFound: Boolean? = null,

    /** Whether a surface exists. An effect cannot be created before one does. */
    val surfaceReady: Boolean = false,

    /** What `loadEffect` came to. */
    val load: TryOnLoadOutcome = TryOnLoadOutcome.NotAttempted,

    /**
     * Whether the loaded bundle said it can draw (`momentumTryOnReady`), or
     * null when it does not answer that question.
     */
    val drawable: Boolean? = null,

    /** True when a loaded effect was unloaded again because it cannot draw. */
    val effectUnloaded: Boolean = false,

    /**
     * The vendor's own error text, verbatim, from `EffectManager`'s error
     * listener.
     *
     * Verbatim and not summarised, because the one line that solved this
     * screen's first failure was the SDK's own
     * "Malformed config.json: missing the required property 'scene'". A
     * paraphrase of that would have said nothing.
     */
    val sdkError: String? = null,

    /** The vendor's hint channel, same treatment. */
    val sdkHint: String? = null,

    /** The effect URL the player said it activated, when it said so. */
    val activated: String? = null,

    /** The last trip up the apply ladder. */
    val apply: TryOnApplyOutcome? = null,

    /** The shade the viewer has chosen. */
    val variantLabel: String? = null,
    val variantHex: String? = null,

    /** The finish and coverage sent with that shade. */
    val look: TryOnLook = TryOnLook.DEFAULT,
)

/** What `loadEffect` came to, as a closed set rather than a nullable Effect. */
sealed interface TryOnLoadOutcome {
    /** Nothing has been asked yet — usually because no surface exists. */
    data object NotAttempted : TryOnLoadOutcome

    /** An Effect came back. [status] is `Effect.status()` as a name, or null when it would not say. */
    data class Loaded(val via: String, val status: String?) : TryOnLoadOutcome

    /** Every entry point was tried and none returned an Effect. */
    data class Failed(val via: String, val reason: String) : TryOnLoadOutcome
}

/** The one tag every try-on line is logged under, so `adb logcat -s` finds all of it. */
const val TRY_ON_LOG_TAG = "MomentumTryOn"

/**
 * [diagnostics] as the lines the panel renders and the log writes.
 *
 * One fact per line, label first, in the order the steps actually happen — so
 * reading down the panel is reading the pipeline, and the first line that says
 * something unexpected is the failure. Nothing here is conditional on being
 * interesting: a line that disappears when it is fine is a line whose absence
 * has to be interpreted.
 */
@Suppress("CyclomaticComplexMethod")
fun diagnosticLines(diagnostics: TryOnDiagnostics): List<String> = buildList {
    add("Licence gate: ${diagnostics.gate.describe()}")
    add(
        "Licence valid (SDK's answer): " + when (diagnostics.licenceValid) {
            true -> "yes"
            false -> "no"
            null -> "not answered yet"
        },
    )
    add("Effect slug (from the server): ${diagnostics.effectSlug.ifBlank { "(none)" }}")
    add("Effect source: ${diagnostics.resolution ?: "not resolved yet"}")
    add(
        "Manifest config.json: " + when (diagnostics.manifestFound) {
            true -> "found"
            false -> "NOT found"
            null -> "not checked"
        },
    )
    add("Load path: ${diagnostics.effect?.loadPath ?: "(none — no bundle resolved)"}")
    // The line that separates a black screen from a plain camera. An effect
    // whose manifest declares no scene content renders nothing OVER the
    // camera, and the SDK calls that a successful load.
    add(
        "Manifest declares a scene: " + when (diagnostics.effect?.declaresSceneContent) {
            true -> "yes"
            false -> "NO — an empty scene would black the preview out, so it was not loaded"
            null -> "unknown (the manifest could not be read)"
        },
    )
    add("Camera surface: ${if (diagnostics.surfaceReady) "created" else "NOT created yet"}")
    add("loadEffect: ${diagnostics.load.describe()}")
    add(
        "Effect says it can draw: " + when (diagnostics.drawable) {
            true -> "yes"
            false -> "NO — its scene did not give it what it needs"
            null -> "did not say (the bundle implements no readiness function)"
        },
    )
    add(
        "Effect unloaded again: " +
            if (diagnostics.effectUnloaded) "YES — so the plain camera renders" else "no",
    )
    // Verbatim, and always present as a line: "none" is itself the news when
    // an effect failed to load and the SDK said nothing about it.
    add("SDK error (verbatim): ${diagnostics.sdkError ?: "none"}")
    add("SDK hint (verbatim): ${diagnostics.sdkHint ?: "none"}")
    add("Effect activated: ${diagnostics.activated ?: "not reported"}")

    val ack = diagnostics.apply?.ack
    add(
        "Effect script loaded: " + when (ack?.scriptLoaded) {
            true -> "yes"
            false -> "NO — the effect's own script is not running"
            null -> "unknown (the effect did not answer the probe)"
        },
    )
    add("Prefab: ${ack?.prefab ?: "not reported by the effect"}")
    add("Last JS error: ${ack?.error ?: "none reported"}")
    add("Probe answer (verbatim): ${ack?.raw ?: "none"}")
    add("Shade call: ${diagnostics.apply.describeApply()}")
    diagnostics.apply?.failures?.forEach { (path, message) ->
        add("  ${path.label} threw: $message")
    }
    add(
        "Selected shade: " + when {
            diagnostics.variantLabel == null -> "none"
            else -> "${diagnostics.variantLabel} ${diagnostics.variantHex ?: "(no hex)"}"
        },
    )
    add("Look: finish=${diagnostics.look.finish.wire}, coverage=${diagnostics.look.coverage.wire}")
}

private fun FaceArState.describe(): String = when (this) {
    FaceArState.Unlicensed -> "Unlicensed — this build carries no token"
    FaceArState.Initialising -> "Initialising — the SDK has not answered yet"
    FaceArState.Ready -> "Ready"
    FaceArState.Invalid -> "Invalid — the licence is expired or revoked"
    is FaceArState.Failed -> "Failed — $message"
}

private fun TryOnLoadOutcome.describe(): String = when (this) {
    TryOnLoadOutcome.NotAttempted -> "not attempted yet"
    is TryOnLoadOutcome.Loaded -> "Effect returned via $via, status=${status ?: "unavailable"}"
    is TryOnLoadOutcome.Failed -> "NO Effect returned (tried $via) — $reason"
}

private fun TryOnApplyOutcome?.describeApply(): String {
    val outcome = this ?: return "not sent yet"
    val tried = outcome.attempted.joinToString(", ") { it.label }.ifEmpty { "nothing" }
    return when (val path = outcome.acknowledgedBy) {
        null -> "tried $tried — NOT acknowledged (no bundle reported back)"
        else -> "tried $tried — acknowledged by ${path.label}"
    }
}
