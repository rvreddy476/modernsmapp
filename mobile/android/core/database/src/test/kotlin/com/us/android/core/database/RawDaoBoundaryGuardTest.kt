package com.us.android.core.database

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.io.File

/**
 * CS-A-LB-1 — the raw DAO primitives are unreachable from production code.
 *
 * ## WHY A SOURCE SCAN
 *
 * Room requires DAO methods to be public — KSP generates public overrides — so
 * the compiler cannot make `rawInsertOperation` and friends private to the
 * guarded transactions. Without enforcement, "the guarded methods are the only
 * way in" is a comment, and a future caller can insert an operation that skips
 * [CreatorInvariants] entirely: an unknown state outside every liveness rule, a
 * published row with no server post id, a claimed slot with no operation.
 *
 * So the boundary is enforced the same way the backend's projection guard works:
 * scan every production Kotlin source in the Android tree and fail if anything
 * other than the DAO file itself mentions a raw mutation.
 */
class RawDaoBoundaryGuardTest {

    /** The one file allowed to call raw mutations: the DAO that guards them. */
    private val owner = "CreatorStudio.kt"

    private val rawMutations = listOf(
        "rawInsertOperation",
        "rawClaimLiveSlot",
        "rawReleaseLiveSlot",
        "rawUpdateLifecycle",
        "rawDeleteOperationsForProject",
    )

    /** Walks up from the module dir to the Android root, then scans src/main. */
    private fun productionSources(): List<File> {
        var dir = File("").absoluteFile
        while (dir.name != "android" && dir.parentFile != null) dir = dir.parentFile
        check(dir.name == "android") { "could not locate the Android tree from ${File("").absolutePath}" }

        return dir.walkTopDown()
            .onEnter { it.name != "build" && it.name != ".gradle" }
            .filter { it.isFile && it.extension == "kt" }
            .filter { it.path.replace('\\', '/').contains("/src/main/") }
            .toList()
    }

    @Test
    fun `no production file outside the guarded DAO touches a raw mutation`() {
        val sources = productionSources()
        // The scan must actually be scanning something, or it guards nothing.
        assertThat(sources.size).isGreaterThan(50)

        val offenders = sources
            .filter { it.name != owner }
            .flatMap { file ->
                val text = file.readText()
                rawMutations.filter { it in text }.map { "$it in ${file.name}" }
            }

        assertThat(offenders).isEmpty()
    }

    /** The owner file really does contain them — the guard is scanning the right names. */
    @Test
    fun `the guarded DAO itself declares every raw mutation`() {
        val dao = productionSources().single { it.name == owner }.readText()

        rawMutations.forEach { name -> assertThat(dao).contains(name) }
    }
}
