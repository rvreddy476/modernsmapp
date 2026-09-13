package com.us.android.core.facear

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The lines the diagnostics panel shows, for every state that can produce one.
 *
 * ## WHY A TEST PER STATE AND NOT A SPOT CHECK
 *
 * This panel is the entire debugging loop for a screen that runs on a phone
 * with no adb attached. A line that is wrong, missing, or that says "no" when
 * the truth is "nobody asked" costs a build-install-message round trip and
 * sends the reader to the wrong bug. So every state a field can hold has a
 * test, and the two most dangerous confusions — "not answered" vs "answered
 * no", and "not attempted" vs "attempted and failed" — have tests of their
 * own.
 */
class TryOnDiagnosticsTest {

    private fun lines(diagnostics: TryOnDiagnostics) = diagnosticLines(diagnostics)

    private fun line(diagnostics: TryOnDiagnostics, label: String): String =
        lines(diagnostics).first { it.startsWith(label) }

    // ─── the licence ─────────────────────────────────────────────────

    @Test
    fun `every gate state has its own line and names its reason`() {
        val cases = mapOf(
            FaceArState.Unlicensed to "Unlicensed",
            FaceArState.Initialising to "Initialising",
            FaceArState.Ready to "Ready",
            FaceArState.Invalid to "Invalid",
            FaceArState.Failed("libbanuba.so not found") to "libbanuba.so not found",
        )

        cases.forEach { (state, expected) ->
            assertThat(line(TryOnDiagnostics(gate = state), "Licence gate:")).contains(expected)
        }
    }

    @Test
    fun `the sdk's own licence answer is not folded into the gate state`() {
        val notAsked = line(TryOnDiagnostics(licenceValid = null), "Licence valid")
        val no = line(TryOnDiagnostics(licenceValid = false), "Licence valid")
        val yes = line(TryOnDiagnostics(licenceValid = true), "Licence valid")

        assertThat(notAsked).contains("not answered yet")
        assertThat(no).endsWith("no")
        assertThat(yes).endsWith("yes")
    }

    // ─── the bundle ──────────────────────────────────────────────────

    @Test
    fun `an unresolved effect says so rather than showing an empty path`() {
        val lines = lines(TryOnDiagnostics(effectSlug = "momentum_lipstick"))

        assertThat(lines).contains("Effect slug (from the server): momentum_lipstick")
        assertThat(lines).contains("Effect source: not resolved yet")
        assertThat(lines).contains("Manifest config.json: not checked")
        assertThat(lines).contains("Load path: (none — no bundle resolved)")
    }

    @Test
    fun `a missing manifest is loud, and an unchecked one is not the same line`() {
        assertThat(line(TryOnDiagnostics(manifestFound = false), "Manifest"))
            .contains("NOT found")
        assertThat(line(TryOnDiagnostics(manifestFound = true), "Manifest"))
            .endsWith("found")
        assertThat(line(TryOnDiagnostics(manifestFound = null), "Manifest"))
            .contains("not checked")
    }

    @Test
    fun `a resolved bundle shows the path the effect player is actually given`() {
        val diagnostics = TryOnDiagnostics(
            effect = TryOnEffect(
                slug = "momentum_lipstick",
                displayName = "momentum_lipstick",
                origin = TryOnEffectOrigin.BUNDLED,
                loadPath = "effects/momentum_lipstick",
            ),
        )

        assertThat(line(diagnostics, "Load path:")).endsWith("effects/momentum_lipstick")
    }

    // ─── the load ────────────────────────────────────────────────────

    @Test
    fun `the surface comes before the effect, and the panel says which exists`() {
        assertThat(line(TryOnDiagnostics(surfaceReady = false), "Camera surface:"))
            .contains("NOT created yet")
        assertThat(line(TryOnDiagnostics(surfaceReady = true), "Camera surface:"))
            .endsWith("created")
    }

    @Test
    fun `loadEffect has three distinct outcomes and none of them is silence`() {
        assertThat(line(TryOnDiagnostics(), "loadEffect:")).contains("not attempted yet")

        val loaded = TryOnDiagnostics(
            load = TryOnLoadOutcome.Loaded("EffectManager.load(path)", "ACTIVE"),
        )
        assertThat(line(loaded, "loadEffect:"))
            .isEqualTo("loadEffect: Effect returned via EffectManager.load(path), status=ACTIVE")

        val failed = TryOnDiagnostics(
            load = TryOnLoadOutcome.Failed("both", "Malformed config.json"),
        )
        assertThat(line(failed, "loadEffect:"))
            .isEqualTo("loadEffect: NO Effect returned (tried both) — Malformed config.json")
    }

    @Test
    fun `an effect that loaded but would not say its status admits that`() {
        val diagnostics = TryOnDiagnostics(
            load = TryOnLoadOutcome.Loaded("BanubaSdkManager.loadEffect(path, sync)", null),
        )

        assertThat(line(diagnostics, "loadEffect:")).endsWith("status=unavailable")
    }

    // ─── the vendor's own words ──────────────────────────────────────

    @Test
    fun `the sdk error is shown verbatim, because the paraphrase is what cost the last round trip`() {
        val real = "Malformed config.json: missing the required property 'scene'."
        val diagnostics = TryOnDiagnostics(sdkError = real)

        assertThat(line(diagnostics, "SDK error")).endsWith(real)
    }

    @Test
    fun `no sdk error is itself a line, because its absence is news when a load failed`() {
        assertThat(line(TryOnDiagnostics(), "SDK error")).endsWith("none")
        assertThat(line(TryOnDiagnostics(), "SDK hint")).endsWith("none")
        assertThat(line(TryOnDiagnostics(), "Effect activated:")).endsWith("not reported")
    }

    // ─── the shade call ──────────────────────────────────────────────

    @Test
    fun `a shade never sent and a shade sent unacknowledged are different lines`() {
        assertThat(line(TryOnDiagnostics(), "Shade call:")).contains("not sent yet")

        val tried = TryOnDiagnostics(
            apply = TryOnApplyOutcome(
                attempted = listOf(TryOnCallPath.CALL_JS_METHOD, TryOnCallPath.EVAL_JS),
                acknowledgedBy = null,
                ack = null,
                failures = emptyMap(),
            ),
        )
        assertThat(line(tried, "Shade call:"))
            .isEqualTo("Shade call: tried callJsMethod, evalJs — NOT acknowledged (no bundle reported back)")
    }

    @Test
    fun `an acknowledged shade names the path that worked`() {
        val diagnostics = TryOnDiagnostics(
            apply = TryOnApplyOutcome(
                attempted = listOf(TryOnCallPath.CALL_JS_METHOD, TryOnCallPath.EVAL_JS),
                acknowledgedBy = TryOnCallPath.EVAL_JS,
                ack = null,
                failures = emptyMap(),
            ),
        )

        assertThat(line(diagnostics, "Shade call:")).endsWith("acknowledged by evalJs")
    }

    @Test
    fun `a path that threw gets its own indented line with the exception's words`() {
        val diagnostics = TryOnDiagnostics(
            apply = TryOnApplyOutcome(
                attempted = listOf(TryOnCallPath.CALL_JS_METHOD),
                acknowledgedBy = null,
                ack = null,
                failures = mapOf(TryOnCallPath.CALL_JS_METHOD to "no such method"),
            ),
        )

        assertThat(lines(diagnostics)).contains("  callJsMethod threw: no such method")
    }

    @Test
    fun `the script not running is the loudest line on the panel`() {
        val missing = TryOnDiagnostics(
            apply = TryOnApplyOutcome(
                attempted = listOf(TryOnCallPath.CALL_JS_METHOD),
                acknowledgedBy = null,
                ack = parseAck("method=undefined"),
                failures = emptyMap(),
            ),
        )

        assertThat(line(missing, "Effect script loaded:"))
            .contains("NO — the effect's own script is not running")
    }

    @Test
    fun `an unanswered probe is unknown, not no`() {
        assertThat(line(TryOnDiagnostics(), "Effect script loaded:")).contains("unknown")
        assertThat(line(TryOnDiagnostics(), "Probe answer (verbatim):")).endsWith("none")
        assertThat(line(TryOnDiagnostics(), "Prefab:")).contains("not reported by the effect")
        assertThat(line(TryOnDiagnostics(), "Last JS error:")).endsWith("none reported")
    }

    @Test
    fun `a reporting bundle's prefab and error reach the panel`() {
        val diagnostics = TryOnDiagnostics(
            apply = TryOnApplyOutcome(
                attempted = listOf(TryOnCallPath.CALL_JS_METHOD),
                acknowledgedBy = TryOnCallPath.CALL_JS_METHOD,
                ack = parseAck("report=applied=true|prefab=reused:bnb_lipsshine|err=findMaterial null"),
                failures = emptyMap(),
            ),
        )

        assertThat(line(diagnostics, "Prefab:")).endsWith("reused:bnb_lipsshine")
        assertThat(line(diagnostics, "Last JS error:")).endsWith("findMaterial null")
    }

    // ─── the shade itself ────────────────────────────────────────────

    @Test
    fun `the chosen shade is shown with its label and its hex`() {
        val diagnostics = TryOnDiagnostics(variantLabel = "Crimson", variantHex = "#C21F3A")

        assertThat(line(diagnostics, "Selected shade:")).endsWith("Crimson #C21F3A")
    }

    @Test
    fun `a shade with no hex says so rather than showing a blank`() {
        val diagnostics = TryOnDiagnostics(variantLabel = "Medium", variantHex = null)

        assertThat(line(diagnostics, "Selected shade:")).endsWith("Medium (no hex)")
    }

    @Test
    fun `no shade at all is its own answer`() {
        assertThat(line(TryOnDiagnostics(), "Selected shade:")).endsWith("none")
    }

    @Test
    fun `the look is reported in the prefab's vocabulary, not the button's`() {
        val diagnostics = TryOnDiagnostics(
            look = TryOnLook(TryOnFinish.GLITTER, TryOnCoverage.HIGH),
        )

        // "glitter"/"high" is what reached the effect; "Glitter"/"Full" is what
        // the shopper tapped, and printing that would not be checkable against
        // the bundle's schema.
        assertThat(line(diagnostics, "Look:")).isEqualTo("Look: finish=glitter, coverage=high")
    }

    // ─── the panel as a whole ────────────────────────────────────────

    @Test
    fun `every step of the pipeline has a line, in the order the steps happen`() {
        val labels = lines(TryOnDiagnostics()).map { it.substringBefore(':') }

        // Reading down the panel is reading the pipeline, so the first line
        // that surprises the reader is the failure. An out-of-order panel makes
        // that inference wrong.
        assertThat(labels).containsAtLeast(
            "Licence gate",
            "Effect slug (from the server)",
            "Effect source",
            "Manifest config.json",
            "Load path",
            "Camera surface",
            "loadEffect",
            "SDK error (verbatim)",
            "Effect script loaded",
            "Shade call",
            "Selected shade",
        ).inOrder()
    }

    @Test
    fun `the log tag is one stable string, so a future adb session can filter on it`() {
        assertThat(TRY_ON_LOG_TAG).isEqualTo("MomentumTryOn")
    }
}
