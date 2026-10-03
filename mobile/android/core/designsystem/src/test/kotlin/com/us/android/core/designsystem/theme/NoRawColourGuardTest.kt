package com.us.android.core.designsystem.theme

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test
import java.io.File

/**
 * No production screen writes a colour of its own (founder's rule: colour
 * comes from ONE place, `UsColorTokens`).
 *
 * ## WHY A SOURCE SCAN
 *
 * Until 2026-10-02 the app was only ever seen dark, so a `Color.White` label
 * or a `Color(0xFF18181D)` card looked right. On a light device the first is
 * white on white and the second is a dark slab under dark ink. Nothing in the
 * compiler tells a theme token from a literal, so the rule is enforced the way
 * `RawDaoBoundaryGuardTest` enforces its boundary: every main Kotlin source in
 * the Android tree is read, and a line that builds or names a raw colour
 * fails, unless its file is in the short allowlist below, with the reason.
 *
 * A colour that is the same in both themes ON PURPOSE (what is drawn over a
 * photo, the story ring, a tile's gradient, chat's own bubble, the text pill
 * burned into a video) is still a named token, not a literal: it lives in
 * `UsColorTokens` and says why it does not invert.
 */
class NoRawColourGuardTest {

    /** A file that may hold raw colours: where, why, and (when not the whole file) the only lines that may. */
    private data class Allowed(val path: String, val reason: String, val onlyLinesWith: List<String> = emptyList())

    private val allowlist = listOf(
        Allowed(
            path = "core/designsystem/src/main/kotlin/com/us/android/core/designsystem/theme/UsColorTokens.kt",
            reason = "the one place a colour is written",
        ),
        Allowed(
            path = "core/designsystem/src/main/kotlin/com/us/android/core/designsystem/icon/UsIcons.kt",
            reason = "a vector path needs SOME stroke or fill to exist; every icon is tinted where it is drawn",
            onlyLinesWith = listOf("SolidColor(Color.Black)"),
        ),
        Allowed(
            path = "feature/post/src/main/kotlin/com/us/android/feature/post/studio/StudioScreen.kt",
            reason = "the creator's own text colour, stored in the document as a string and parsed to be shown",
            onlyLinesWith = listOf("android.graphics.Color.parseColor(argb)"),
        ),
        Allowed(
            path = "core/media/src/main/kotlin/com/us/android/core/media/creator/AndroidRenderExporter.kt",
            reason = "paints the exported image file (its ground, the creator's text colour), not a screen",
            onlyLinesWith = listOf("canvas.drawColor(Color.BLACK)", "Color.parseColor(layer.style.colorArgb)"),
        ),
        Allowed(
            path = "core/chat/src/main/kotlin/com/us/android/core/chat/lock/PinKdf.kt",
            reason = "0x00002000 is a window flag, not a colour",
            onlyLinesWith = listOf("WINDOW_FLAG_SECURE"),
        ),
    )

    /** Whole directories that are not the app's own chrome. */
    private val allowedDirectories = mapOf(
        "core/facear/" to "the third-party Face AR try-on surface, skinned to the SDK's own look",
    )

    private val root: File by lazy {
        var dir = File("").absoluteFile
        while (dir.name != "android" && dir.parentFile != null) dir = dir.parentFile
        check(dir.name == "android") { "could not locate the Android tree from ${File("").absolutePath}" }
        dir
    }

    /** Every main Kotlin source in the tree, by its path from the Android root. */
    private fun productionSources(): Map<String, File> =
        root.walkTopDown()
            .onEnter { it.name != "build" && it.name != ".gradle" }
            .filter { it.isFile && it.extension == "kt" }
            .map { it.relativeTo(root).path.replace('\\', '/') to it }
            .filter { (path, _) -> "/src/main/" in path }
            .toMap()

    @Test
    fun `no main source builds or names a raw colour outside the allowlist`() {
        val sources = productionSources()
        // The scan must actually be scanning something, or it guards nothing.
        assertThat(sources.size).isGreaterThan(200)

        val offenders = sources
            .filterKeys { path -> allowedDirectories.keys.none { path.startsWith(it) } }
            .flatMap { (path, file) ->
                val allowed = allowlist.firstOrNull { it.path == path }
                rawColourLines(file.readLines())
                    .filterNot { (_, line) -> allowed.permits(line) }
                    .map { (number, line) -> "$path:$number: ${line.trim()}" }
            }

        assertWithMessage(
            "Raw colours in main source. Use MaterialTheme.colorScheme or UsTheme.extended; " +
                "if a role is missing, add a token to UsColorTokens.",
        ).that(offenders).isEmpty()
    }

    /** A stale entry is a hole: it would let a new literal into a file that no longer needs one. */
    @Test
    fun `every allowlisted file still exists and still needs its entry`() {
        val sources = productionSources()
        for (allowed in allowlist) {
            val file = sources[allowed.path]
            assertWithMessage("allowlisted file is gone: ${allowed.path}").that(file).isNotNull()
            assertWithMessage("allowlisted file no longer holds a raw colour: ${allowed.path}")
                .that(rawColourLines(file!!.readLines())).isNotEmpty()
            assertWithMessage("no reason given for ${allowed.path}").that(allowed.reason).isNotEmpty()
        }
        for (directory in allowedDirectories.keys) {
            assertWithMessage("allowlisted directory is gone: $directory")
                .that(sources.keys.any { it.startsWith(directory) }).isTrue()
        }
    }

    /** The scanner itself: what it must catch and what it must leave alone. */
    @Test
    fun `the scanner catches each way of writing a colour and ignores what is not one`() {
        val caught = listOf(
            "    tint = Color.White,",
            "    .background(Color.Black.copy(alpha = 0.5f))",
            "    color = Color.Gray,",
            "private val SCRIM = Color(0x99000000)",
            "private val BACKDROP = Color(BACKDROP_ARGB)",
            "private const val DANGER = 0xFFB3261E",
            "private const val NAVY = 0xFF0F3460.toInt()",
            "    paint.color = android.graphics.Color.WHITE",
            "    val c = Color.parseColor(hex)",
            "    val c = hex.toColorInt()",
            "    val c = Color.argb(255, 0, 0, 0)",
            "    val c = Color.hsl(210f, 0.5f, 0.5f)",
        )
        for (line in caught) {
            assertWithMessage(line).that(rawColourLines(listOf(line))).hasSize(1)
        }

        val ignored = listOf(
            "    tint = UsTheme.extended.onMedia,",
            "    color = MaterialTheme.colorScheme.primary,",
            "    if (checked) accent else Color.Transparent",
            "    var ground: Color = Color.Unspecified",
            "    statusBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT),",
            "    // White over the photo: Color.White would vanish on a light frame.",
            "     * It used to be Color(0xFF18181D).",
            "    val scrim = UsTheme.extended.stage.copy(alpha = 0.8f) // not Color.Black",
            "@Preview(name = \"Login\", showBackground = true, backgroundColor = 0xFF000000)",
            "    internal fun avatarColor(seed: String): Color {",
            "    stroke = SolidColor(UsTheme.extended.accent),",
            "    val mask = flags and 0xFF",
        )
        assertThat(rawColourLines(ignored)).isEmpty()
    }

    private fun Allowed?.permits(line: String): Boolean = when {
        this == null -> false
        onlyLinesWith.isEmpty() -> true
        else -> onlyLinesWith.any { it in line }
    }

    /** The lines (1-based) of [lines] that build or name a raw colour, comments and `@Preview` grounds apart. */
    private fun rawColourLines(lines: List<String>): List<Pair<Int, String>> =
        lines.mapIndexedNotNull { index, raw ->
            val trimmed = raw.trim()
            val isComment = trimmed.startsWith("*") || trimmed.startsWith("/*") || trimmed.startsWith("//")
            // A preview's ground is tooling: it is never drawn in the app.
            if (isComment || trimmed.startsWith("@Preview")) return@mapIndexedNotNull null
            val code = raw.substringBefore("//")
            if (RAW_COLOUR.any { it.containsMatchIn(code) }) index + 1 to raw else null
        }

    private companion object {
        val RAW_COLOUR = listOf(
            // Compose's and android.graphics' named colours. Transparent and Unspecified are not colours.
            Regex("""\bColor\.(White|Black|Gray|DarkGray|LightGray|Red|Green|Blue|Yellow|Cyan|Magenta)\b"""),
            Regex("""\bColor\.(WHITE|BLACK|GRAY|DKGRAY|LTGRAY|RED|GREEN|BLUE|YELLOW|CYAN|MAGENTA)\b"""),
            // Any colour built in place: Color(0x…), Color(SOME_ARGB), Color(red = …).
            Regex("""(?<![A-Za-z.])Color\("""),
            // An ARGB or RGB literal, whatever it is assigned to.
            Regex("""\b0x[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?\b"""),
            // A colour parsed or computed from numbers.
            Regex("""\bparseColor\(|\.toColorInt\(|\bColor\.(rgb|argb|hsv|hsl)\("""),
        )
    }
}
