package com.us.android.core.facear.effect

/**
 * Reading an effect bundle's own `config.json` well enough to know whether it
 * can draw anything.
 *
 * ## WHY THIS CHECK EXISTS, AND WHAT IT COST TO LEARN
 *
 * An effect the player LOADS takes over rendering. If its scene declares no
 * content — no assets, no entities, no render list — the player renders that
 * empty scene instead of the camera, and the result on a handset is a **black
 * rectangle**, not a plain camera and not an error. The SDK reports nothing,
 * because from its point of view the effect loaded and activated exactly as
 * asked.
 *
 * That is the worst failure this screen has: it looks like a crash, it happens
 * after everything else has succeeded, and nothing in the logs says so.
 *
 * It is also entirely decidable from bytes this repository ships. A manifest
 * either declares scene content or it does not, so the check is made here,
 * BEFORE any effect is handed to the player, and a bundle that cannot draw is
 * never loaded at all — leaving the plain camera up, the shades tappable, and
 * one honest line on screen.
 *
 * ## WHY NOT A JSON PARSER
 *
 * `:core:facear` has no serialization plugin and does not want one for this:
 * the question is "which keys does the ROOT object have", the answer must not
 * depend on a schema that will change, and a malformed manifest has to degrade
 * to "we cannot tell" rather than throw inside an effect resolver. Thirty
 * lines of depth-aware scanning answer exactly that question and nothing else.
 * Depth-aware and not `contains("\"entities\"")`, because a nested `entities`
 * inside `assets` is not a root declaration and matching it would call a dead
 * bundle alive.
 */

/**
 * The root-object keys of [json], or an empty set when it is not a JSON object.
 *
 * String-aware and escape-aware: a key name appearing inside a VALUE (a
 * shader path, a script, a label) must not be mistaken for a key, and a
 * `\"` inside a string must not end it.
 */
@Suppress("NestedBlockDepth", "CyclomaticComplexMethod")
fun topLevelKeys(json: String): Set<String> {
    val keys = mutableSetOf<String>()
    var depth = 0
    var index = 0
    var pendingKey: String? = null

    while (index < json.length) {
        when (val c = json[index]) {
            '{', '[' -> {
                depth++
                pendingKey = null
                index++
            }

            '}', ']' -> {
                depth--
                pendingKey = null
                index++
            }

            '"' -> {
                val end = endOfString(json, index)
                if (end < 0) return keys
                // Depth 1 is the root object's own members. A string at any
                // deeper level belongs to a value.
                if (depth == 1) pendingKey = json.substring(index + 1, end)
                index = end + 1
            }

            ':' -> {
                // A string is a KEY only when a colon follows it. `["a","b"]`
                // at the root would otherwise contribute phantom keys.
                pendingKey?.let { keys += it }
                pendingKey = null
                index++
            }

            else -> {
                if (!c.isWhitespace() && c != ',') pendingKey = null
                index++
            }
        }
    }
    return keys
}

/** The index of the closing quote of the string starting at [start], or -1. */
private fun endOfString(json: String, start: Int): Int {
    var index = start + 1
    while (index < json.length) {
        when (json[index]) {
            '\\' -> index += 2
            '"' -> return index
            else -> index++
        }
    }
    return -1
}

/**
 * The root keys that mean "this effect puts something in the scene".
 *
 * Read off Banuba's own effects rather than invented. A full effect declares
 * most of them; the SDK's own built-in minimal effect declares only `assets`
 * (one procedural camera texture) plus `scene`, and that is enough to render
 * the camera — so ANY of these is enough to say the bundle draws.
 *
 * `scene` and `script` are deliberately NOT here. `scene` is only the scene's
 * NAME, and `script` is behaviour with nothing to attach to: a manifest
 * carrying those two and nothing else is precisely the empty scene this file
 * exists to catch.
 */
internal val SCENE_CONTENT_KEYS = setOf(
    "assets",
    "components",
    "entities",
    "hierarchy",
    "layers",
    "main_camera",
    "render_list",
    "render_targets",
)

/**
 * Whether [manifest] declares a scene with something in it.
 *
 * False means "loading this would black the screen out". The caller must then
 * not load it — see [BundledTryOnEffects] and the try-on surface.
 */
fun effectDeclaresSceneContent(manifest: String): Boolean =
    topLevelKeys(manifest).any { it in SCENE_CONTENT_KEYS }

/**
 * One effect bundle's `config.json`, as text.
 *
 * A seam rather than an `AssetManager` or a `File`, because the two sources
 * this module has read from completely different places — the APK's assets and
 * a directory in app storage — and because "a manifest that says nothing" has
 * to be provable in a plain JVM test.
 *
 * Null for anything unreadable. Unreadable is NOT "declares nothing": the
 * caller keeps `null` as "we could not tell" and loads the effect anyway,
 * because refusing to load a working bundle because a read failed would be a
 * worse failure than the one this guards.
 */
fun interface EffectManifestReader {
    fun read(path: String): String?
}
