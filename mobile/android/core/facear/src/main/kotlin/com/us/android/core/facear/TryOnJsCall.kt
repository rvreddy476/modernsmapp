package com.us.android.core.facear

/**
 * Turning "this product, that shade" into the effect's JS call.
 *
 * ## WHY THIS IS A PURE FUNCTION AND NOT THREE LINES IN THE SURFACE
 *
 * A Banuba effect is parameterised by calling a JavaScript method inside it —
 * `Effect.callJsMethod(name, argumentsJson)`, or `Effect.evalJs(script)` for
 * the same call written as source. Assembling that string in a composable puts
 * an untestable contract between Momentum and every effect bundle inside a
 * recomposition: a mis-cased method name or an unquoted hex is a lipstick that
 * silently does not change colour, on a device, with nothing to read.
 *
 * So the call is built here, once, by a function that takes data and returns
 * data — and the wire contract every bundle must implement is in
 * [TryOnJsContract] and in this module's README, where a bundle author can
 * read it.
 */

/**
 * The invocation, in whichever of the SDK's two forms applies.
 *
 * A closed type rather than a method/arguments pair with a nullable name,
 * because the two cases reach DIFFERENT SDK entry points — `callJsMethod` and
 * `evalJs` — and a caller that has to check for null before deciding which is
 * a caller that can get it wrong.
 */
sealed interface TryOnJsCall {
    /** The call as source, which is the `evalJs` form of either case. */
    val script: String

    /**
     * The generated call: a named method with JSON arguments, for
     * `callJsMethod`. The normal path, and the one the contract in
     * [TryOnJsContract] describes.
     */
    data class Method(val method: String, val arguments: String) : TryOnJsCall {
        override val script: String get() = "$method($arguments)"
    }

    /**
     * A script the SERVER supplied on the variant (`try_on.variants[].js`),
     * for `evalJs`. Used verbatim: it exists precisely because the generated
     * call could not express the look, so rewriting it would defeat it.
     */
    data class Script(override val script: String) : TryOnJsCall
}

/**
 * The method name each kind's effect bundle must expose, and the argument
 * shape it receives.
 *
 * Momentum's contract, not Banuba's: the SDK only knows how to call a named JS
 * method, and something has to decide what that name is. Stated as constants so
 * the README, the tests and the call builder cannot drift apart.
 */
object TryOnJsContract {
    const val EYEWEAR_METHOD = "setFrameColour"
    const val MAKEUP_METHOD = "setShade"
    const val JEWELLERY_METHOD = "setMetal"
    const val WATCH_METHOD = "setStrap"

    /** What a kind's bundle must expose, or null for a kind with no contract. */
    fun methodFor(kind: TryOnKind): String? = when (kind) {
        TryOnKind.EYEWEAR -> EYEWEAR_METHOD
        TryOnKind.MAKEUP -> MAKEUP_METHOD
        TryOnKind.JEWELLERY -> JEWELLERY_METHOD
        TryOnKind.WATCH -> WATCH_METHOD
        // An unrecognised kind has no agreed method, and guessing one is how a
        // try-on ends up calling something that does not exist.
        TryOnKind.UNKNOWN -> null
    }
}

/**
 * The call that applies [variant] to [descriptor]'s effect, or null when there
 * is nothing to apply.
 *
 * Null — meaning "leave the effect at its own default" — for every case where
 * a call would be a guess:
 *
 *  * the descriptor is not a usable try-on ([TryOnDescriptor.isUsable]);
 *  * no variant is chosen, so the bundle's own default shade stands;
 *  * the variant is not one of the descriptor's own, which is a caller bug
 *    rather than something to forward to the effect.
 *
 * ## THE SERVER-SUPPLIED SCRIPT WINS
 *
 * A variant may carry its own `js` — the contract's escape hatch for a look no
 * colour can drive. When it does, that script is returned verbatim as
 * [TryOnJsCall.Script] and nothing is generated. It is the seller's and the
 * effect author's statement of what the effect needs, and a generated call
 * layered on top would either contradict it or be ignored.
 *
 * That script reaches `evalJs`, so it is worth being plain about the trust
 * boundary: it is authored content from the catalogue, not buyer input, and it
 * runs inside the effect's own sandboxed interpreter against a local camera
 * frame — no app API, no network, no filesystem. What it cannot be allowed to
 * become is a path for one user's text to run in another's session, which is
 * why it is a seller-set catalogue field and not, say, a review or a search
 * term.
 *
 * ## THE GENERATED CALL
 *
 * The arguments are hand-built JSON rather than serialized from a class on
 * purpose: the shape is three fields wide, it is a CONTRACT with bundles
 * written by other people, and reading the exact bytes that will reach an
 * effect matters more here than avoiding six lines of string building. Every
 * value that can come from the wire is escaped — see [escapeJson].
 */
fun tryOnJsCall(
    descriptor: TryOnDescriptor,
    variant: TryOnVariant?,
    look: TryOnLook = TryOnLook.DEFAULT,
    bare: Boolean = false,
): TryOnJsCall? {
    if (!descriptor.isUsable) return null
    val chosen = variant ?: return null
    if (descriptor.variants.none { it.id == chosen.id }) return null
    chosen.js?.trim()?.takeIf { it.isNotEmpty() }?.let {
        // A BARE call is "the same look with the colour taken off", and there
        // is no such thing for a script the seller wrote: it exists precisely
        // because the generated call could not express the look, so there is
        // nothing here that knows which part of it was the colour. The compare
        // control is disabled for such a variant rather than sending something
        // that would change the look in an unknown way.
        return if (bare) null else TryOnJsCall.Script(it)
    }
    val method = TryOnJsContract.methodFor(descriptor.kind) ?: return null

    // Bare is the contract's own "no colour" payload — the case a variant that
    // is a SIZE rather than a shade already produces — so the compare control
    // needs no new method, no new bundle vocabulary and no second code path in
    // any effect. `momentum_lipstick` answers an absent `hex` by clearing.
    val rgb = if (bare) null else rgbOf(chosen.hex)
    val fields = buildList {
        add("\"variant\":\"${escapeJson(chosen.id)}\"")
        // The hex is echoed back in the normalised `#RRGGBB` form the contract
        // states, never the raw wire string: a bundle that parses the hex
        // itself must not have to cope with `0xAABBCC` or a missing hash.
        rgb?.let { add("\"hex\":\"${it.hex}\"") }
        // ...and the same colour pre-divided into the 0..1 floats a shader
        // actually wants, so no bundle has to implement hex parsing in JS.
        rgb?.let { add("\"rgb\":[${it.r},${it.g},${it.b}]") }
        // MAKEUP ONLY, and on purpose. `finish` and `coverage` are
        // `makeup_lipsshine/schema.json`'s own required properties; they mean
        // nothing to an eyewear or watch bundle, and `Base.setPrefabSettings`
        // THROWS on a state key its class does not implement. Sending them to
        // every kind would turn a shade change into a dead effect on three of
        // the four.
        if (descriptor.kind == TryOnKind.MAKEUP) {
            add("\"finish\":\"${look.finish.wire}\"")
            add("\"coverage\":\"${look.coverage.wire}\"")
        }
    }
    return TryOnJsCall.Method(method = method, arguments = "{${fields.joinToString(",")}}")
}

/** A colour, normalised for the contract: `#RRGGBB` plus 0..1 floats. */
internal data class TryOnRgb(val hex: String, val r: String, val g: String, val b: String)

/**
 * [hex] as a contract colour, or null when it is not one.
 *
 * Accepts `#RRGGBB` and bare `RRGGBB`, case-insensitively, because both turn
 * up in seller-entered data. Everything else — a colour name, an eight-digit
 * hex with alpha, a truncated value, an empty string — is null, and the caller
 * then sends the variant without a colour rather than sending a colour the
 * effect will misread.
 */
internal fun rgbOf(hex: String?): TryOnRgb? {
    val digits = hex?.trim()?.removePrefix("#")?.uppercase() ?: return null
    if (digits.length != HEX_DIGITS || digits.any { it !in HEX_ALPHABET }) return null
    val channels = (0 until HEX_DIGITS step 2).map { i ->
        digits.substring(i, i + 2).toInt(HEX_RADIX)
    }
    return TryOnRgb(
        hex = "#$digits",
        r = channels[0].asUnitFloat(),
        g = channels[1].asUnitFloat(),
        b = channels[2].asUnitFloat(),
    )
}

/**
 * A 0..255 channel as a 0..1 literal with three decimals.
 *
 * Formatted by integer arithmetic rather than `String.format`, so the output
 * does not depend on the device's locale — a French locale would emit "0,502",
 * which is not JSON and which a JS bundle would read as two arguments.
 */
private fun Int.asUnitFloat(): String {
    val thousandths = (this * UNIT_SCALE + CHANNEL_MAX / 2) / CHANNEL_MAX
    val fraction = (thousandths % UNIT_SCALE).toString().padStart(UNIT_DECIMALS, '0')
    return "${thousandths / UNIT_SCALE}.$fraction"
}

/**
 * JSON string escaping for values that came off the wire.
 *
 * A variant id is server data, and server data reaching a JS `eval` unescaped
 * is both a broken effect and a script-injection seam. Only the characters JSON
 * requires: quote, backslash, and the control characters that cannot appear
 * literally in a JSON string.
 */
internal fun escapeJson(raw: String): String = buildString(raw.length) {
    raw.forEach { c ->
        when {
            c == '"' -> append("\\\"")
            c == '\\' -> append("\\\\")
            c == '\n' -> append("\\n")
            c == '\r' -> append("\\r")
            c == '\t' -> append("\\t")
            c < ' ' -> append("\\u%04x".format(c.code))
            else -> append(c)
        }
    }
}

private const val HEX_DIGITS = 6
private const val HEX_RADIX = 16
private const val CHANNEL_MAX = 255
private const val UNIT_SCALE = 1000

/** Three, to match [UNIT_SCALE] — "0.502", never "0.52". */
private const val UNIT_DECIMALS = 3
private const val HEX_ALPHABET = "0123456789ABCDEF"
