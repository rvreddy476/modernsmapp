/**
 * Momentum's lipstick try-on.
 *
 * NOTHING HERE IS AN EFFECT OF OUR OWN. Every pixel is drawn by Banuba's
 * shipped `makeup_lipsshine` prefab — its shaders, its glitter texture and its
 * `lips` / `lips_shining` segmentation networks, all of which arrive inside the
 * `makeup` and `lips` artifacts. This file is the twenty lines of glue that
 * turn Momentum's wire payload into the three settings that prefab requires.
 *
 * ## THE TWO CALLERS, AND WHY BOTH ARE HANDLED
 *
 * 1. `Effect.callJsMethod("setShade", '{"variant":…,"hex":"#RRGGBB",…}')`, which
 *    is what `FaceArSession.apply` emits for a generated call. The SDK resolves
 *    the method name against the global object — the same resolution that makes
 *    Banuba's own documented `callJsMethod("Eyes.color", …)` work — so `setShade`
 *    is assigned to `globalThis` below, explicitly, rather than only exported.
 * 2. `Effect.evalJs('setShade({…})')`, which is the same call written as source
 *    and is what a variant's server-supplied `js` string would reach.
 *
 * The two differ in ONE way that matters: (1) may hand the payload over as a
 * JSON *string* and (2) hands over a JS *object*. [payloadOf] accepts either,
 * so neither entry point is a special case.
 *
 * ## WHY THE PREFAB IS REQUIRED AND BUILT LAZILY
 *
 * Both the require and the construction reach into the live scene.
 * `bnb_prefabs/makeup_base/scripts/region.js` calls
 * `bnb.scene.getAssetManager()` at its own top level, and
 * `MakeupLipsshine`'s constructor then looks up
 * `findMaterial("shaders/lipsshine/shiny")` — so neither can succeed before the
 * effect's assets exist. A shade can be asked for before that (the app applies
 * the selected variant immediately after `loadEffect`), and this file's top
 * level therefore does nothing that can throw: **installing `setShade` on the
 * global object must not depend on the scene being ready**, or an early failure
 * would leave the app calling a method that does not exist. Everything scene-
 * shaped happens on the first call that needs it, and is retried on the next
 * call if it failed.
 */

// `bnb_js/prefabs` is safe to require at load: it defines `Base` and
// `parseColor` and touches no scene object. The MAKEUP prefab is not, and is
// required inside [region] instead — see the note there.
const { Base } = require("bnb_js/prefabs")

/**
 * `makeup_lipsshine/schema.json` marks `color`, `finish` AND `coverage` as
 * required, and `MakeupBase.apply` reads the finish out of the prefab's own
 * settings table — an unknown finish logs a warning and draws nothing at all.
 * Momentum's payload carries only a colour, so the other two have to come from
 * here.
 *
 * `shine` over `glitter`: a glitter finish is a look, and a shopper comparing
 * two reds needs the shade, not the sparkle. `mid` over `high`: `K` is the
 * blend weight, and full coverage on a segmentation mask reads as paint rather
 * than lipstick on anyone whose lips the mask edges slightly over-report.
 */
const DEFAULT_FINISH = "shine"
const DEFAULT_COVERAGE = "mid"

/** Exactly what the prefab's schema enumerates. Anything else is not a finish. */
const FINISHES = ["shine", "glitter"]
const COVERAGES = ["low", "mid", "high"]

const TAG = "[momentum_lipstick] "

/** The prefab instance, or null until the scene can build one. */
let lips = null

/**
 * The prefab, or null when the scene is not ready for it yet.
 *
 * Built once and cached. A failure is not remembered as a failure: the next
 * call tries again, because "the effect had not finished activating" is a
 * transient state and a shade tapped a second later must still work.
 */
function region() {
    if (lips !== null) return lips
    try {
        // Required HERE and not at the top of the file: this module's chain of
        // requires ends in `makeup_base/scripts/region.js`, which calls
        // `bnb.scene.getAssetManager()` while it is being loaded.
        const { MakeupLipsshine } = require("bnb_prefabs/makeup_lipsshine/scripts/index.js")
        lips = new MakeupLipsshine()
    } catch (error) {
        bnb.log(TAG + "the lips prefab is not ready yet: " + error)
        lips = null
    }
    return lips
}

/**
 * The payload as an object, or null when there is nothing usable in it.
 *
 * A string arrives from `callJsMethod`; an object from `evalJs`. Malformed JSON
 * is null rather than an exception — a broken shade must leave the face as it
 * is, not tear down the effect.
 */
function payloadOf(raw) {
    if (raw === null || raw === undefined) return null
    if (typeof raw === "object") return raw
    if (typeof raw !== "string") return null
    const text = raw.trim()
    if (text.length === 0) return null
    try {
        const parsed = JSON.parse(text)
        return parsed !== null && typeof parsed === "object" ? parsed : null
    } catch (error) {
        bnb.log(TAG + "unreadable payload: " + error)
        return null
    }
}

/**
 * `#RRGGBB` for a six-digit hex, or null for anything else.
 *
 * Written as a loop and not a regular expression on purpose: this file runs in
 * the effect player's own interpreter, and the fewer of its optional language
 * features this leans on, the fewer ways it can fail on a device.
 *
 * `parseColor` in `bnb_js/prefabs` takes `"#RRGGBB"` directly, so a validated
 * hex is passed straight through with no conversion of our own — the `rgb`
 * floats the contract also sends are not needed and are ignored.
 */
function colourOf(value) {
    if (typeof value !== "string") return null
    let digits = value.trim()
    if (digits.charAt(0) === "#") digits = digits.substring(1)
    if (digits.length !== 6) return null
    for (let i = 0; i < digits.length; i++) {
        const c = digits.charAt(i).toUpperCase()
        const decimal = c >= "0" && c <= "9"
        const letter = c >= "A" && c <= "F"
        if (!decimal && !letter) return null
    }
    return "#" + digits.toUpperCase()
}

/** [value] when the prefab's schema allows it, else [fallback]. */
function oneOf(value, allowed, fallback) {
    if (typeof value !== "string") return fallback
    const wanted = value.trim().toLowerCase()
    return allowed.indexOf(wanted) === -1 ? fallback : wanted
}

/**
 * Puts [raw]'s shade on the lips, or takes the lipstick off.
 *
 * Off — `clear()` — for every case where there is no colour to draw: no
 * payload, no `hex` (a variant that is a size rather than a shade), or a `hex`
 * the contract should never have sent. The alternative is throwing, and a
 * throw here is a dead effect rather than a bare face.
 */
function setShade(raw) {
    const prefab = region()
    if (prefab === null) return

    try {
        const payload = payloadOf(raw)
        const colour = payload === null ? null : colourOf(payload.hex)
        if (colour === null) {
            prefab.clear()
            return
        }
        prefab.setPrefabSettings({
            color: colour,
            // Accepted from the payload as well as defaulted, so a variant's
            // own `js` can ask for a glitter or a heavier coverage without
            // needing a second effect bundle.
            finish: oneOf(payload.finish, FINISHES, DEFAULT_FINISH),
            coverage: oneOf(payload.coverage, COVERAGES, DEFAULT_COVERAGE),
        })
    } catch (error) {
        bnb.log(TAG + "could not apply the shade: " + error)
    }
}

/** Bare lips. Exposed so a host can undo a shade without picking another. */
function clearShade() {
    const prefab = region()
    if (prefab === null) return
    try {
        prefab.clear()
    } catch (error) {
        bnb.log(TAG + "could not clear the shade: " + error)
    }
}

/**
 * The effect as a prefab class, for the one wiring where the effect player
 * instantiates the script itself.
 *
 * The player builds a prefab's script with
 * `new (require('<script>').<PascalCaseName>)(…)` and then drives it through
 * `setPrefabSettings`. Whether it does that for an EFFECT's own script as well
 * as for a prefab's is not something this repository can prove without a
 * device, so the class is provided and named for `momentum_lipstick`: if the
 * player looks for it, it finds it and behaves exactly like the global; if it
 * does not, nothing has been lost. The constructor deliberately touches no
 * scene object, so it cannot fail.
 */
class MomentumLipstick extends Base {
    setPrefabSettings(state) {
        setShade(state)
    }

    clear() {
        clearShade()
    }
}

// The global is the contract. `callJsMethod` resolves its method name against
// the global object, so an export alone would not be callable.
globalThis.setShade = setShade
globalThis.clearShade = clearShade

exports = {
    MomentumLipstick,
    setShade,
    clearShade,
}
