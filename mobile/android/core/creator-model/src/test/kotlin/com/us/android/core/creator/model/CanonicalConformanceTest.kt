package com.us.android.core.creator.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * CS-FZ-1 — the frozen contract, proven against checked-in bytes.
 *
 * ## WHAT THIS TEST IS FOR
 *
 * Every fixture in `src/test/resources/fixtures` is a byte-exact artifact with a
 * SHA-256 recorded in `expected.txt`. This test parses each one into the Kotlin
 * model and re-serializes it, and requires the result to be byte-identical.
 *
 * That is a stronger claim than "it round-trips". It means the Kotlin model, the
 * canonicalizer, and the frozen specification all agree on exactly one byte
 * sequence per document. Rename a field, change a default, reorder a key, escape
 * a Devanagari character, or admit a float, and these hashes change — which is
 * the entire point of freezing them.
 *
 * If one of these fails, the fixture is not wrong. The change is.
 */
class CanonicalConformanceTest {

    private fun fixture(name: String): ByteArray =
        checkNotNull(javaClass.getResourceAsStream("/fixtures/$name.json")) {
            "missing fixture $name"
        }.use { it.readBytes() }

    private fun readLedger(): ByteArray =
        checkNotNull(javaClass.getResourceAsStream("/fixtures/expected.txt")) {
            "missing fixtures/expected.txt"
        }.use { it.readBytes() }

    private fun expected(): Map<String, Pair<Int, String>> =
        readLedger().decodeToString().trim().lines().associate { line ->
            val (name, bytes, sha) = line.split(" ")
            name to (bytes.toInt() to sha)
        }

    private val projectFixtures = listOf(
        "01-legacy-text-only",
        "01b-text-only-publishing",
        "02-legacy-single-photo",
        "03-carousel-cab-editing",
        "04-carousel-cab-publishing",
    )

    private val operationFixtures = listOf(
        "04b-publish-operation",
        "04c-publish-operation-text",
    )

    @Test
    fun `every project fixture reserializes to its exact frozen bytes`() {
        val ledger = expected()
        projectFixtures.forEach { name ->
            val original = fixture(name)
            val (expectedBytes, expectedSha) = ledger.getValue(name)

            assertThat(original.size).isEqualTo(expectedBytes)
            assertThat(Canonical.sha256Hex(original)).isEqualTo(expectedSha)

            val reserialized = Canonical.encode(Canonical.decodeProject(original))

            assertThat(reserialized.decodeToString()).isEqualTo(original.decodeToString())
            assertThat(Canonical.sha256Hex(reserialized)).isEqualTo(expectedSha)
        }
    }

    @Test
    fun `every operation fixture reserializes to its exact frozen bytes`() {
        val ledger = expected()
        operationFixtures.forEach { name ->
            val original = fixture(name)
            val (expectedBytes, expectedSha) = ledger.getValue(name)

            assertThat(original.size).isEqualTo(expectedBytes)

            val reserialized = Canonical.encode(Canonical.decodeOperation(original))

            assertThat(reserialized.decodeToString()).isEqualTo(original.decodeToString())
            assertThat(Canonical.sha256Hex(reserialized)).isEqualTo(expectedSha)
        }
    }

    /**
     * Non-ASCII is emitted RAW.
     *
     * Escaping is optional in JSON, so if this were not pinned, two conforming
     * serializers would produce different bytes for the same document and the
     * fingerprint would depend on which one happened to run.
     */
    @Test
    fun `Devanagari and Tamil survive canonicalization unescaped`() {
        val bytes = Canonical.encode(
            Canonical.decodeProject(fixture("03-carousel-cab-editing")),
        )
        val text = bytes.decodeToString()

        assertThat(text).contains("छोटे पल")
        assertThat(text).contains("மாலை நேரம்")
        assertThat(text).doesNotContain("\\u0091")
    }

    /**
     * The higher-version envelope is preserved byte-for-byte.
     *
     * A v1 reader must not canonicalize it, must not drop `timeline` or
     * `audioTracks`, and must not "best effort" parse it. The user's work has to
     * still be there after they update the app.
     */
    @Test
    fun `a higher version document is preserved untouched and reports update required`() {
        val name = "05-higher-version-envelope"
        val raw = fixture(name)
        val (expectedBytes, expectedSha) = expected().getValue(name)

        assertThat(raw.size).isEqualTo(expectedBytes)

        val result = ProjectReader.read(raw)

        assertThat(result).isInstanceOf(ProjectReadResult.UpdateRequired::class.java)
        val updateRequired = result as ProjectReadResult.UpdateRequired
        // The fixture moved to 3 when this build's reader became 2 (P1-B): it
        // must always model a version HIGHER than the build under test.
        assertThat(updateRequired.minReaderVersion).isEqualTo(3)
        assertThat(Canonical.sha256Hex(updateRequired.rawBytes)).isEqualTo(expectedSha)
    }

    @Test
    fun `a supported version is decoded rather than deferred`() {
        val result = ProjectReader.read(fixture("02-legacy-single-photo"))

        assertThat(result).isInstanceOf(ProjectReadResult.Supported::class.java)
        assertThat((result as ProjectReadResult.Supported).project.pages).hasSize(1)
    }
}
