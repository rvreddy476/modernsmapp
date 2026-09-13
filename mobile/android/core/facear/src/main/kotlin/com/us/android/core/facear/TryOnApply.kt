package com.us.android.core.facear

/**
 * Getting a shade into a loaded effect, and KNOWING whether it arrived.
 *
 * ## WHY THIS IS A LADDER AND NOT ONE CALL
 *
 * The SDK offers three ways to reach an effect's JavaScript, and which of them
 * a given bundle answers is not knowable from this repository — `callJsMethod`
 * resolves a name against the global object, `evalJs` runs source, and
 * `evalJsSync` runs source and hands back what it evaluated to. A shade that
 * silently does not apply looks exactly like a shade that applied to a colour
 * the shopper cannot tell apart, and the phone this ships to is a personal
 * handset with no adb attached: there is no logcat and no iteration loop.
 *
 * So the shade goes through EVERY path that could work, in order, and the
 * result is a fact rather than a hope. Setting a colour twice is idempotent —
 * the prefab writes four shader parameters — so trying more than one path
 * costs nothing and cannot corrupt anything.
 *
 * ## WHY THE ACKNOWLEDGEMENT IS A PROBE AND NOT A RETURN VALUE
 *
 * `callJsMethod` and `evalJs` return nothing. Only `evalJsSync` returns a
 * String. So "did it arrive" is asked SEPARATELY, by evaluating a probe
 * expression that reads the effect's own state back — see [tryOnProbe].
 *
 * Everything in this file is pure and engine-agnostic: [TryOnJsEngine] is the
 * seam, so the ladder and the parsing are decided on the JVM rather than on a
 * handset nobody can attach a debugger to.
 */

/** Which SDK entry point one attempt went through. */
enum class TryOnCallPath(val label: String) {
    /** `Effect.callJsMethod(name, argumentsJson)`. Only possible for a generated call. */
    CALL_JS_METHOD("callJsMethod"),

    /** `Effect.evalJs(source)`, fire and forget. */
    EVAL_JS("evalJs"),

    /** `Effect.evalJsSync(source)`, which also hands back what it evaluated. */
    EVAL_JS_SYNC("evalJsSync"),
}

/**
 * The three entry points, as the one thing this file needs from the vendor.
 *
 * A seam rather than an `Effect` parameter, because an `Effect` is a native
 * proxy: with this interface the ladder's order, its failure handling and its
 * acknowledgement parsing are all provable in a plain JVM test.
 */
interface TryOnJsEngine {
    fun callJsMethod(method: String, arguments: String)
    fun evalJs(script: String)

    /** The evaluated result, or null when the engine gave nothing back. */
    fun evalJsSync(script: String): String?
}

/**
 * The paths worth trying for [call], in the order they are tried.
 *
 * `callJsMethod` first for a generated call, because it is the documented
 * entry point and the one that does not hand a string to an interpreter. A
 * server-supplied SCRIPT has no method name by definition, so it starts at
 * `evalJs` — there is nothing for `callJsMethod` to resolve.
 *
 * `evalJsSync` is last in both cases: it blocks the caller until the effect's
 * interpreter has run, which is the most expensive of the three and is only
 * worth paying when the cheap ones produced nothing.
 */
fun callPathsFor(call: TryOnJsCall): List<TryOnCallPath> = when (call) {
    is TryOnJsCall.Method -> listOf(
        TryOnCallPath.CALL_JS_METHOD,
        TryOnCallPath.EVAL_JS,
        TryOnCallPath.EVAL_JS_SYNC,
    )

    is TryOnJsCall.Script -> listOf(
        TryOnCallPath.EVAL_JS,
        TryOnCallPath.EVAL_JS_SYNC,
    )
}

/**
 * The expression that asks a loaded effect what it knows about itself.
 *
 * Two questions in one string, because only one round trip through the
 * interpreter is affordable per apply:
 *
 *  * **Has a bundle that reports back been loaded?** If the effect defines a
 *    `momentumTryOnReport()` global it is called and its answer is used
 *    verbatim. Nothing in this build's bundle defines one today — this is the
 *    hook a bundle author gets, and [parseAck] treats its absence as "the
 *    effect did not report", never as failure.
 *  * **Did the effect's script run at all?** `typeof <method>` answers that
 *    from the outside, with no cooperation from the bundle. It is the line
 *    that separates "the shade call went nowhere" from "the effect never
 *    loaded its script", and those are completely different bugs.
 *
 * [method] is the kind's contract method — `setShade` for makeup. Written into
 * the expression rather than quoted, because `typeof` on an undeclared
 * identifier is the one JS operator that does not throw.
 */
fun tryOnProbe(method: String): String =
    "((typeof $REPORT_FUNCTION === 'function')" +
        " ? ('report=' + String($REPORT_FUNCTION()))" +
        " : ('method=' + (typeof $method)" +
        " + '|ready=' + ((typeof $READY_FUNCTION === 'function')" +
        " ? String($READY_FUNCTION()) : 'unknown')))"

/**
 * The optional global a bundle may define to say whether it can actually draw.
 *
 * ## THE CONTRACT, AND WHY IT IS WORTH ONE LINE IN A BUNDLE
 *
 * `momentumTryOnReady()` returns true when the effect's scene gave the bundle
 * everything it needs — for a makeup bundle, that its prefab's material
 * resolved — and false when it did not. Nothing else in the system can answer
 * that: an effect whose material lookup returned null LOADS and ACTIVATES
 * successfully and then draws nothing, and both the SDK and this app report
 * complete success the whole way down.
 *
 * When a bundle answers false the try-on unloads the effect, so the plain
 * camera renders instead of an empty scene, and says one honest line on
 * screen. When a bundle does not define it the answer is "unknown" and nothing
 * is unloaded: a silent bundle must not be punished for a function it never
 * promised.
 */
const val READY_FUNCTION = "momentumTryOnReady"

/** The optional global a bundle may define to report what it did with a shade. */
const val REPORT_FUNCTION = "momentumTryOnReport"

/**
 * What the effect said when [tryOnProbe] asked.
 *
 * Every field is nullable and null means "the effect did not say", never
 * "no". A diagnostic line that guesses is worse than one that admits it does
 * not know — the whole point of this path is that one device run has to tell
 * the truth.
 */
data class TryOnAck(
    /** Exactly what came back, for the diagnostics panel to show verbatim. */
    val raw: String,

    /** True when the effect's script is loaded and its method exists. */
    val scriptLoaded: Boolean?,

    /**
     * True when the bundle said it can draw, false when it said it cannot.
     *
     * False is the one answer that makes the try-on UNLOAD the effect: an
     * effect that loaded but has nothing to draw renders an empty scene over
     * the camera, which on a handset is a black rectangle. Null means the
     * bundle does not implement [READY_FUNCTION] and nothing is unloaded.
     */
    val drawable: Boolean?,

    /** True when a reporting bundle confirmed it applied the shade. */
    val applied: Boolean?,

    /** What the bundle said about its prefab — "constructed", "reused:x", "none". */
    val prefab: String?,

    /** The last error the bundle recorded, if it records one. */
    val error: String?,
)

/**
 * [raw] as an acknowledgement, or null when there was nothing to parse.
 *
 * ## THE TWO SHAPES, AND THE QUOTES
 *
 * `evalJsSync` hands back the evaluated value as text, and an engine that
 * serialises that value returns a JSON string — `"method=function"`, with the
 * quotes. So the quotes are stripped before anything else, and a `\"` inside
 * is unescaped. Getting that wrong would make every probe read as unknown.
 *
 * Then one of two shapes:
 *
 *  * `method=<typeof>` — the fallback probe. `function` means the script ran.
 *  * `report=<k=v|k=v|…>` — a reporting bundle's own answer. Unknown keys are
 *    ignored so a bundle may add fields without this parser being rewritten.
 */
fun parseAck(raw: String?): TryOnAck? {
    val text = raw?.let(::unquote)?.trim().orEmpty()
    if (text.isEmpty() || text == NULL_LITERAL || text == UNDEFINED_LITERAL) return null

    val reported = text.startsWith(REPORT_PREFIX)
    val fields = text.removePrefix(REPORT_PREFIX).split(FIELD_SEPARATOR)
        .mapNotNull { entry ->
            val at = entry.indexOf('=')
            if (at <= 0) null else entry.substring(0, at).trim() to entry.substring(at + 1).trim()
        }
        .toMap()

    return TryOnAck(
        raw = text,
        scriptLoaded = when {
            // A bundle that answered its own report necessarily ran.
            reported -> true
            // `function` is the only answer that means the bundle's method is
            // callable. `undefined` is the interesting failure and the one this
            // whole probe exists to name; anything else is a name holding
            // something that is not callable, which is equally not callable.
            else -> fields[KEY_METHOD]?.let { it == FUNCTION_LITERAL }
        },
        drawable = fields[KEY_READY]?.asFlag(),
        applied = fields[KEY_APPLIED]?.asFlag(),
        prefab = fields[KEY_PREFAB]?.takeIf { it.isNotEmpty() },
        error = fields[KEY_ERROR]?.takeIf { it.isNotEmpty() && it != NULL_LITERAL },
    )
}

/**
 * A field as a boolean, or null when it is neither.
 *
 * `unknown` — what the probe emits for a bundle that does not implement
 * [READY_FUNCTION] — has to come back null rather than false, because false
 * unloads the effect and a silent bundle has not said it cannot draw.
 */
private fun String.asFlag(): Boolean? = when (trim().lowercase()) {
    "true", "yes", "1" -> true
    "false", "no", "0" -> false
    else -> null
}

/** What one trip up the ladder came to. */
data class TryOnApplyOutcome(
    /** Every path that was invoked, in order. */
    val attempted: List<TryOnCallPath>,

    /** The path that was ACKNOWLEDGED, or null when none was. */
    val acknowledgedBy: TryOnCallPath?,

    /** The last acknowledgement read, whichever path produced it. */
    val ack: TryOnAck?,

    /** Paths that threw, and what they threw. */
    val failures: Map<TryOnCallPath, String>,
) {
    val acknowledged: Boolean get() = acknowledgedBy != null
}

/**
 * Sends [call] through [engine], climbing [callPathsFor]'s ladder.
 *
 * Stops at the first path a REPORTING bundle confirms. When no bundle reports
 * — which is every bundle in this build today — it runs every path, because a
 * second identical colour write is free and because "one of these worked" is
 * worth more than a guess about which. [probe], when given, is evaluated after
 * each attempt and its answer is kept for the diagnostics panel either way.
 *
 * A path that throws is recorded and the ladder continues: a native proxy
 * refusing one entry point is precisely the case this exists for.
 */
fun applyThrough(
    call: TryOnJsCall,
    probe: String?,
    engine: TryOnJsEngine,
): TryOnApplyOutcome {
    val attempted = mutableListOf<TryOnCallPath>()
    val failures = linkedMapOf<TryOnCallPath, String>()
    var ack: TryOnAck? = null
    var acknowledgedBy: TryOnCallPath? = null

    for (path in callPathsFor(call)) {
        attempted += path
        val failure = runCatching { engine.send(path, call) }.exceptionOrNull()
        if (failure != null) {
            failures[path] = failure.message ?: failure.javaClass.simpleName
            // A path that could not even be invoked cannot have applied
            // anything, so do not spend a probe round trip on it.
            continue
        }

        val answer = probe?.let { expression ->
            runCatching { engine.evalJsSync(expression) }.getOrNull()
        }
        parseAck(answer)?.let { ack = it }
        if (ack?.applied == true) {
            acknowledgedBy = path
            break
        }
    }

    return TryOnApplyOutcome(
        attempted = attempted,
        acknowledgedBy = acknowledgedBy,
        ack = ack,
        failures = failures,
    )
}

/** One attempt, at the entry point [path] names. */
private fun TryOnJsEngine.send(path: TryOnCallPath, call: TryOnJsCall) = when (path) {
    TryOnCallPath.CALL_JS_METHOD -> when (call) {
        is TryOnJsCall.Method -> callJsMethod(call.method, call.arguments)
        // Unreachable through callPathsFor, and an error rather than a silent
        // no-op if a future caller builds its own order: a script has no
        // method name, and inventing one is how a try-on calls something that
        // does not exist.
        is TryOnJsCall.Script -> error("a server-supplied script has no method name")
    }

    TryOnCallPath.EVAL_JS -> evalJs(call.script)
    TryOnCallPath.EVAL_JS_SYNC -> evalJsSync(call.script)
}

/**
 * [raw] with one layer of JSON string quoting removed, if it has one.
 *
 * Left alone when it is not quoted: an engine that returns the bare value is
 * as likely as one that serialises it, and stripping a first and last
 * character unconditionally would eat real content.
 */
private fun unquote(raw: String): String {
    val text = raw.trim()
    if (text.length < 2 || !text.startsWith('"') || !text.endsWith('"')) return text
    return text.substring(1, text.length - 1)
        .replace("\\\"", "\"")
        .replace("\\\\", "\\")
}

private const val REPORT_PREFIX = "report="
private const val FIELD_SEPARATOR = "|"

// The probe's field names. The expression built above emits
// `method=<typeof>|ready=<bool>` for the fallback probe, and a bundle that
// implements `momentumTryOnReport()` answers with the same `key=value` pairs.
// Parsing splits on FIELD_SEPARATOR and keys the halves, so these are the
// KEY halves — not the `method=` prefix the earlier draft declared, which was
// left over from a parse that read the prefix instead of splitting.
private const val KEY_METHOD = "method"
private const val KEY_READY = "ready"
private const val KEY_APPLIED = "applied"
private const val KEY_PREFAB = "prefab"
private const val KEY_ERROR = "err"
private const val FUNCTION_LITERAL = "function"
private const val UNDEFINED_LITERAL = "undefined"
private const val NULL_LITERAL = "null"
