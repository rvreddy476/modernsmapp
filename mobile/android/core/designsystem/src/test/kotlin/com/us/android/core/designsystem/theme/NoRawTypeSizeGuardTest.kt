package com.us.android.core.designsystem.theme

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test
import java.io.File

/**
 * No production screen writes a text size of its own (founder's rule:
 * colour and font come from ONE place). A size is a [UsTypography] role or
 * a [UsTypeScale] step; a `13.sp` in a screen is neither, and the UI audit
 * of 2026-10-21 found 77 of them, two at 9 sp.
 *
 * The same source scan as [NoRawColourGuardTest]: every main Kotlin source
 * in the Android tree is read, and a line with `<number>.sp` fails unless
 * its file is in the allowlist below, with the reason. A `letterSpacing`
 * is not a size and is left alone; so is a comment.
 */
class NoRawTypeSizeGuardTest {

    /** A file that may hold a raw size: where, why, and (when not the whole file) the only lines that may. */
    private data class Allowed(val path: String, val reason: String, val onlyLinesWith: List<String> = emptyList())

    private val allowlist = listOf(
        Allowed(
            path = "core/designsystem/src/main/kotlin/com/us/android/core/designsystem/theme/Type.kt",
            reason = "the Material roles: the one place a role's size is written",
        ),
        Allowed(
            path = "core/designsystem/src/main/kotlin/com/us/android/core/designsystem/theme/UsTypeScale.kt",
            reason = "the type scale: the one place a step's size is written",
        ),
        Allowed(
            path = "core/designsystem/src/main/kotlin/com/us/android/core/designsystem/component/UsWordmark.kt",
            reason = "the wordmark's four sizes are the brand's, not the type scale's, and are written once here",
        ),
        Allowed(
            path = "core/designsystem/src/main/kotlin/com/us/android/core/designsystem/component/UsOtpField.kt",
            reason = "the real text field is invisible (1 sp, transparent); the boxes beside it are the visual",
            onlyLinesWith = listOf("fontSize = 1.sp"),
        ),
        Allowed(
            path = "feature/rider/src/main/kotlin/com/us/android/feature/rider/offers/OfferScreen.kt",
            reason = "the fare a rider reads from arm's length on a moving bike: larger than any scale step",
            onlyLinesWith = listOf("fontSize = 56.sp"),
        ),
    )

    /** Whole directories that are not the app's own chrome, or are paused and untouched. */
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
    fun `no main source writes a raw text size outside the allowlist`() {
        val sources = productionSources()
        assertThat(sources.size).isGreaterThan(200)

        val offenders = sources
            .filterKeys { path -> allowedDirectories.keys.none { path.startsWith(it) } }
            .flatMap { (path, file) ->
                val allowed = allowlist.firstOrNull { it.path == path }
                rawSizeLines(file.readLines())
                    .filterNot { (_, line) -> allowed.permits(line) }
                    .map { (number, line) -> "$path:$number: ${line.trim()}" }
            }

        assertWithMessage(
            "Raw text sizes in main source. Use a MaterialTheme.typography role or a UsTypeScale step " +
                "(fontSize = UsTypeScale.xs.size); if a step is missing, add it to UsTypeScale.",
        ).that(offenders).isEmpty()
    }

    /** A stale entry is a hole: it would let a new literal into a file that no longer needs one. */
    @Test
    fun `every allowlisted file still exists and still needs its entry`() {
        val sources = productionSources()
        for (allowed in allowlist) {
            val file = sources[allowed.path]
            assertWithMessage("allowlisted file is gone: ${allowed.path}").that(file).isNotNull()
            assertWithMessage("allowlisted file no longer holds a raw size: ${allowed.path}")
                .that(rawSizeLines(file!!.readLines())).isNotEmpty()
            assertWithMessage("no reason given for ${allowed.path}").that(allowed.reason).isNotEmpty()
        }
    }

    @Test
    fun `the scale has no step under 11 sp and climbs the web's ladder`() {
        val sizes = UsTypeScale.steps.map { it.size.value.toInt() }
        assertThat(sizes).isEqualTo(listOf(11, 12, 14, 16, 18, 20, 24, 30, 36))
        for (step in UsTypeScale.steps) {
            assertThat(step.size.isSp).isTrue()
            assertThat(step.lineHeight.value).isAtLeast(step.size.value)
        }
    }

    /** The scanner itself: what it must catch and what it must leave alone. */
    @Test
    fun `the scanner catches a raw size and ignores a letter spacing, a comment and a token`() {
        val caught = listOf(
            "private val BADGE_TEXT = 9.sp",
            "    fontSize = 13.sp,",
            "    Small(32.dp, 13.sp),",
            "    style = MaterialTheme.typography.displaySmall.copy(fontSize = 40.sp),",
            "    lineHeight = 16.5.sp,",
        )
        for (line in caught) {
            assertWithMessage(line).that(rawSizeLines(listOf(line))).hasSize(1)
        }

        val ignored = listOf(
            "    fontSize = UsTypeScale.xs.size,",
            "    style = MaterialTheme.typography.labelSmall,",
            "    letterSpacing = 8.sp,",
            "    // was 9.sp before the type scale",
            "     * The label under each rail glyph: Figtree 11sp semibold.",
            "    val spacing = 8.dp",
            "    fontSize = UsWordmarkSize.Hero,",
        )
        assertThat(rawSizeLines(ignored)).isEmpty()
    }

    private fun Allowed?.permits(line: String): Boolean = when {
        this == null -> false
        onlyLinesWith.isEmpty() -> true
        else -> onlyLinesWith.any { it in line }
    }

    /** The lines (1-based) of [lines] that write a raw size, comments and letter spacings apart. */
    private fun rawSizeLines(lines: List<String>): List<Pair<Int, String>> =
        lines.mapIndexedNotNull { index, raw ->
            val trimmed = raw.trim()
            val isComment = trimmed.startsWith("*") || trimmed.startsWith("/*") || trimmed.startsWith("//")
            if (isComment) return@mapIndexedNotNull null
            val code = raw.substringBefore("//")
            if ("letterSpacing" in code) return@mapIndexedNotNull null
            if (RAW_SIZE.containsMatchIn(code)) index + 1 to raw else null
        }

    private companion object {
        /** `9.sp`, `13.sp`, `16.5.sp` — a number written straight into sp. */
        val RAW_SIZE = Regex("""(?<![A-Za-z_.])\d+(\.\d+)?\.sp\b""")
    }
}
