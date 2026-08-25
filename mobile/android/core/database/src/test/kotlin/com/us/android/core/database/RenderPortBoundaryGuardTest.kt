package com.us.android.core.database

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.io.File

/**
 * G-6 at source level: no feature touches the render/export port or its
 * implementation.
 *
 * ## WHY THIS EXISTS ALONGSIDE THE GRADLE GUARD
 *
 * `:feature:post` legitimately depends on both `:core:media` (Slice C upload)
 * and `:core:creator-model` (the PublishTransport adapter must live beside the
 * DTO it freezes), so the Gradle-edge version of G-6 carries a named waiver for
 * it. The waiver must not quietly widen into the hazard the guard was for: a
 * feature calling a RenderExporter implementation it found itself instead of
 * the one app DI bound. This scan closes that door at the import level, waiver
 * or no waiver.
 */
class RenderPortBoundaryGuardTest {

    private val forbidden = listOf(
        "com.us.android.core.creator.model.RenderExporter",
        "com.us.android.core.media.creator.AndroidRenderExporter",
    )

    private fun featureSources(): List<File> {
        var dir = File("").absoluteFile
        while (dir.name != "android" && dir.parentFile != null) dir = dir.parentFile
        check(dir.name == "android") { "could not locate the Android tree" }

        return File(dir, "feature").walkTopDown()
            .onEnter { it.name != "build" }
            .filter { it.isFile && it.extension == "kt" }
            .filter { it.path.replace('\\', '/').contains("/src/main/") }
            .toList()
    }

    @Test
    fun `no feature source imports the render port or its implementation`() {
        val sources = featureSources()
        assertThat(sources.size).isGreaterThan(20)

        val offenders = sources.flatMap { file ->
            val text = file.readText()
            forbidden.filter { it in text }.map { "$it in ${file.name}" }
        }

        assertThat(offenders).isEmpty()
    }
}
