package com.us.android.core.facear

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The apply ladder: which entry points are tried, in what order, what happens
 * when one of them throws, and how the effect's answer is read.
 *
 * ## WHY THESE TESTS ARE THE POINT
 *
 * None of this is provable on the device it ships to. The handset has no adb
 * attached, and every failure of a shade call looks identical from the front —
 * a face with nothing on it. So the ORDER, the fallbacks and the parsing are
 * settled here, on the JVM, with a fake engine that can be made to throw on
 * demand; the device is then asked one question it can actually answer, which
 * is what the diagnostics panel prints.
 */
class TryOnApplyTest {

    /**
     * An engine that records what it was asked and can be told to refuse.
     *
     * `answers` is a queue of what `evalJsSync` returns, so a test can say "the
     * first probe found nothing, the second found a report" — which is exactly
     * the shape of a bundle that only becomes ready on its second call.
     */
    private class FakeEngine(
        private val refuse: Set<TryOnCallPath> = emptySet(),
        answers: List<String?> = emptyList(),
    ) : TryOnJsEngine {
        val calls = mutableListOf<String>()
        private val queue = ArrayDeque(answers)

        override fun callJsMethod(method: String, arguments: String) {
            calls += "callJsMethod:$method:$arguments"
            if (TryOnCallPath.CALL_JS_METHOD in refuse) error("no such method")
        }

        override fun evalJs(script: String) {
            calls += "evalJs:$script"
            if (TryOnCallPath.EVAL_JS in refuse) error("eval refused")
        }

        override fun evalJsSync(script: String): String? {
            calls += "evalJsSync:$script"
            if (script.startsWith(PROBE_MARK) || script.contains("momentumTryOnReport")) {
                return if (queue.isEmpty()) null else queue.removeFirst()
            }
            if (TryOnCallPath.EVAL_JS_SYNC in refuse) error("sync eval refused")
            return null
        }
    }

    private val method = TryOnJsCall.Method("setShade", """{"variant":"v-1","hex":"#C21F3A"}""")
    private val script = TryOnJsCall.Script("setGradient({top:'#C21F3A'})")

    // ─── the order ───────────────────────────────────────────────────

    @Test
    fun `a generated call starts at the documented entry point and ends at the blocking one`() {
        assertThat(callPathsFor(method)).containsExactly(
            TryOnCallPath.CALL_JS_METHOD,
            TryOnCallPath.EVAL_JS,
            TryOnCallPath.EVAL_JS_SYNC,
        ).inOrder()
    }

    @Test
    fun `a server-supplied script never reaches callJsMethod, because it has no method name`() {
        assertThat(callPathsFor(script)).containsExactly(
            TryOnCallPath.EVAL_JS,
            TryOnCallPath.EVAL_JS_SYNC,
        ).inOrder()
    }

    // ─── climbing it ─────────────────────────────────────────────────

    @Test
    fun `with no bundle reporting back, every path is tried`() {
        val engine = FakeEngine()

        val outcome = applyThrough(method, tryOnProbe("setShade"), engine)

        // Idempotent: the prefab writes four shader parameters, so the same
        // colour twice is the same colour. Trying all of them is what turns
        // "one of these works on a real device" into a fact instead of a bet.
        assertThat(outcome.attempted).containsExactly(
            TryOnCallPath.CALL_JS_METHOD,
            TryOnCallPath.EVAL_JS,
            TryOnCallPath.EVAL_JS_SYNC,
        ).inOrder()
        assertThat(outcome.acknowledged).isFalse()
        assertThat(outcome.failures).isEmpty()
    }

    @Test
    fun `a reporting bundle stops the ladder at the first path it confirms`() {
        val engine = FakeEngine(answers = listOf("report=applied=true|prefab=constructed"))

        val outcome = applyThrough(method, tryOnProbe("setShade"), engine)

        assertThat(outcome.attempted).containsExactly(TryOnCallPath.CALL_JS_METHOD)
        assertThat(outcome.acknowledgedBy).isEqualTo(TryOnCallPath.CALL_JS_METHOD)
        assertThat(outcome.ack?.applied).isTrue()
        assertThat(outcome.ack?.prefab).isEqualTo("constructed")
    }

    @Test
    fun `a path that throws is recorded and the next one is still tried`() {
        val engine = FakeEngine(
            refuse = setOf(TryOnCallPath.CALL_JS_METHOD),
            // The first probe never runs — a refused call cannot have applied
            // anything — so this answer belongs to the evalJs attempt.
            answers = listOf("report=applied=true"),
        )

        val outcome = applyThrough(method, tryOnProbe("setShade"), engine)

        assertThat(outcome.failures[TryOnCallPath.CALL_JS_METHOD]).isEqualTo("no such method")
        assertThat(outcome.acknowledgedBy).isEqualTo(TryOnCallPath.EVAL_JS)
    }

    @Test
    fun `a refused path costs no probe round trip`() {
        val engine = FakeEngine(refuse = setOf(TryOnCallPath.CALL_JS_METHOD))

        applyThrough(method, tryOnProbe("setShade"), engine)

        // The probe after callJsMethod must NOT have run: asking an effect
        // whether it applied something it was never told about is a wasted
        // blocking call on the render thread.
        assertThat(engine.calls.first()).startsWith("callJsMethod:")
        assertThat(engine.calls[1]).startsWith("evalJs:")
    }

    @Test
    fun `every path throwing is an outcome, not an exception`() {
        val engine = FakeEngine(
            refuse = setOf(
                TryOnCallPath.CALL_JS_METHOD,
                TryOnCallPath.EVAL_JS,
                TryOnCallPath.EVAL_JS_SYNC,
            ),
        )

        val outcome = applyThrough(method, tryOnProbe("setShade"), engine)

        // A try-on that cannot apply a shade must leave the shopper on a
        // camera with a diagnostic line, never take the process down.
        assertThat(outcome.failures.keys).containsExactly(
            TryOnCallPath.CALL_JS_METHOD,
            TryOnCallPath.EVAL_JS,
            TryOnCallPath.EVAL_JS_SYNC,
        )
        assertThat(outcome.acknowledged).isFalse()
    }

    @Test
    fun `the generated call reaches callJsMethod as a name and arguments, never as source`() {
        val engine = FakeEngine()

        applyThrough(method, probe = null, engine = engine)

        assertThat(engine.calls).contains(
            """callJsMethod:setShade:{"variant":"v-1","hex":"#C21F3A"}""",
        )
        // ...and the same call as SOURCE for the eval paths, which is what
        // TryOnJsCall.Method.script exists for.
        assertThat(engine.calls).contains(
            """evalJs:setShade({"variant":"v-1","hex":"#C21F3A"})""",
        )
    }

    @Test
    fun `a server-supplied script is sent verbatim`() {
        val engine = FakeEngine()

        applyThrough(script, probe = null, engine = engine)

        assertThat(engine.calls).contains("evalJs:setGradient({top:'#C21F3A'})")
        assertThat(engine.calls.none { it.startsWith("callJsMethod") }).isTrue()
    }

    @Test
    fun `no probe means no probe`() {
        val engine = FakeEngine()

        val outcome = applyThrough(method, probe = null, engine = engine)

        assertThat(engine.calls.none { it.contains("momentumTryOnReport") }).isTrue()
        assertThat(outcome.ack).isNull()
    }

    // ─── reading the answer ──────────────────────────────────────────

    @Test
    fun `the probe asks both questions in one round trip`() {
        val probe = tryOnProbe("setShade")

        // A reporting bundle's answer if it has one...
        assertThat(probe).contains("momentumTryOnReport")
        // ...and otherwise whether the effect's own script even ran, which
        // needs no cooperation from the bundle at all.
        assertThat(probe).contains("typeof setShade")
    }

    @Test
    fun `the fallback probe separates a missing script from a loaded one`() {
        assertThat(parseAck("method=function")?.scriptLoaded).isTrue()
        assertThat(parseAck("method=undefined")?.scriptLoaded).isFalse()
        // Anything else is a bundle that put something other than a function
        // on that name, which is not callable either.
        assertThat(parseAck("method=object")?.scriptLoaded).isFalse()
    }

    @Test
    fun `an answer the engine serialised as a JSON string is unquoted first`() {
        // evalJsSync hands back the evaluated value as text, and an engine that
        // serialises it returns the quotes too. Getting this wrong would make
        // every probe on every device read as "unknown".
        assertThat(parseAck("\"method=function\"")?.scriptLoaded).isTrue()
        assertThat(parseAck("\"report=applied=true\"")?.applied).isTrue()
    }

    @Test
    fun `a report is read field by field and unknown fields are ignored`() {
        val ack = parseAck("report=applied=true|prefab=reused:bnb_makeup_lipsshine|err=|mood=good")

        assertThat(ack?.applied).isTrue()
        assertThat(ack?.prefab).isEqualTo("reused:bnb_makeup_lipsshine")
        // An empty `err` is no error, not an error whose text is "".
        assertThat(ack?.error).isNull()
        assertThat(ack?.scriptLoaded).isTrue()
    }

    @Test
    fun `a report that names an error keeps it verbatim`() {
        val ack = parseAck("report=applied=false|err=findMaterial returned null")

        assertThat(ack?.applied).isFalse()
        assertThat(ack?.error).isEqualTo("findMaterial returned null")
    }

    @Test
    fun `nothing back is null, never a false answer`() {
        // The distinction the whole panel rests on: "the effect said no" and
        // "the effect said nothing" send you to different bugs.
        listOf(null, "", "   ", "undefined", "null").forEach { nothing ->
            assertThat(parseAck(nothing)).isNull()
        }
    }

    @Test
    fun `an answer in no known shape is kept raw rather than guessed at`() {
        val ack = parseAck("something the interpreter printed")

        assertThat(ack?.raw).isEqualTo("something the interpreter printed")
        assertThat(ack?.scriptLoaded).isNull()
        assertThat(ack?.applied).isNull()
    }

    private companion object {
        const val PROBE_MARK = "(typeof momentumTryOnReport"
    }
}
