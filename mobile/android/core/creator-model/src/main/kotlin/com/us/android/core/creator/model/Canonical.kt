package com.us.android.core.creator.model

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.security.MessageDigest
import java.text.Normalizer

/**
 * Canonical bytes and fingerprints for the frozen v1 contract.
 *
 * ## THE RULES, AND WHY EACH ONE IS HERE
 *
 *  1. **UTF-8, non-ASCII emitted RAW.** `छोटे पल` appears as those bytes, never
 *     as `छ...`. Escaping is optional in JSON, so two conforming
 *     serializers would produce different bytes for the same document and the
 *     fingerprint would depend on which one ran.
 *  2. **Object keys sorted by UTF-8 byte**, ordinal — not by locale. A
 *     locale-sensitive sort would make the hash depend on the device's language.
 *  3. **No insignificant whitespace.**
 *  4. **Arrays keep document order.** `pages` and `layers` carry meaning in
 *     their order; sorting them would destroy the carousel.
 *  5. **Integers only.** A non-integer number [throws][IllegalArgumentException]
 *     rather than being rounded. See the class doc on [AndroidCreatorProject].
 *  6. **absent != null != default**, all three distinguishable.
 *  7. **NFC happens at INGESTION, never here.** [normalizeForStorage] is called
 *     when text enters the model. Canonicalization then never transforms a
 *     stored string — which is what makes it safe to hash and re-hash.
 *
 * ## WHAT THIS MUST NEVER TOUCH
 *
 * [AndroidPublishOperation.frozenRequestBase64]. Those bytes belong to the
 * server's idempotency authority. They are carried as an opaque base64 string,
 * so canonicalizing the operation record re-sorts the record's own keys and
 * leaves the encoded payload byte-identical.
 */
object Canonical {

    private val json = Json {
        encodeDefaults = true
        explicitNulls = true
    }

    /** NFC-normalize at the moment text enters the model. Called once, on input. */
    fun normalizeForStorage(input: String): String =
        Normalizer.normalize(input, Normalizer.Form.NFC)

    fun encode(project: AndroidCreatorProject): ByteArray =
        canonicalBytes(json.encodeToJsonElement(AndroidCreatorProject.serializer(), project))

    fun encode(operation: AndroidPublishOperation): ByteArray =
        canonicalBytes(json.encodeToJsonElement(AndroidPublishOperation.serializer(), operation))

    fun fingerprint(project: AndroidCreatorProject): String = sha256Hex(encode(project))

    fun fingerprint(operation: AndroidPublishOperation): String = sha256Hex(encode(operation))

    fun sha256Hex(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(bytes)
            .joinToString("") { byte -> "%02x".format(byte) }

    /** Parse canonical bytes back into a document. Strict: unknown keys throw. */
    fun decodeProject(bytes: ByteArray): AndroidCreatorProject =
        strictJson.decodeFromString(AndroidCreatorProject.serializer(), bytes.decodeToString())

    fun decodeOperation(bytes: ByteArray): AndroidPublishOperation =
        strictJson.decodeFromString(AndroidPublishOperation.serializer(), bytes.decodeToString())

    /**
     * Strict v1 decoder — [Json.ignoreUnknownKeys] is deliberately false.
     *
     * Forward compatibility is via the version envelope only. A decoder that
     * quietly drops a field it does not understand would let a v2 document open
     * as a lossy v1 and then be saved back with the unknown data gone.
     */
    private val strictJson = Json {
        ignoreUnknownKeys = false
        encodeDefaults = true
        explicitNulls = true
    }

    internal fun canonicalBytes(element: JsonElement): ByteArray =
        StringBuilder().also { write(element, it) }.toString().toByteArray(Charsets.UTF_8)

    private fun write(element: JsonElement, out: StringBuilder) {
        when (element) {
            is JsonNull -> out.append("null")
            is JsonObject -> {
                out.append('{')
                // compareTo on Kotlin String is ordinal UTF-16 comparison. For
                // the ASCII key set this contract uses, that is identical to
                // UTF-8 byte order.
                element.entries.sortedBy { it.key }.forEachIndexed { index, (key, value) ->
                    if (index > 0) out.append(',')
                    writeString(key, out)
                    out.append(':')
                    write(value, out)
                }
                out.append('}')
            }
            is JsonArray -> {
                out.append('[')
                element.forEachIndexed { index, item ->
                    if (index > 0) out.append(',')
                    write(item, out)
                }
                out.append(']')
            }
            is JsonPrimitive -> writePrimitive(element, out)
        }
    }

    private fun writePrimitive(primitive: JsonPrimitive, out: StringBuilder) {
        if (primitive.isString) {
            writeString(primitive.content, out)
            return
        }
        val raw = primitive.content
        if (raw == "true" || raw == "false") {
            out.append(raw)
            return
        }
        // Integers only. Rounding here would silently accept a document the
        // contract forbids, and two platforms could round the same midpoint
        // differently — the exact ambiguity integer millionths exist to remove.
        require(raw.toLongOrNull() != null) {
            "canonical form permits integers only; found non-integer number '$raw'"
        }
        out.append(raw)
    }

    private fun writeString(value: String, out: StringBuilder) {
        out.append('"')
        for (char in value) {
            when {
                char == '"' -> out.append("\\\"")
                char == '\\' -> out.append("\\\\")
                char == '\b' -> out.append("\\b")
                char == '' -> out.append("\\f")
                char == '\n' -> out.append("\\n")
                char == '\r' -> out.append("\\r")
                char == '\t' -> out.append("\\t")
                char.code < CONTROL_CHAR_LIMIT -> out.append("\\u%04x".format(char.code))
                // Everything else, including all non-ASCII, is emitted raw.
                else -> out.append(char)
            }
        }
        out.append('"')
    }

    private const val CONTROL_CHAR_LIMIT = 0x20
}

/**
 * The result of reading a document whose version this build may not support.
 *
 * ## WHY PARSING IS TWO-STAGE
 *
 * A strict whole-document decoder cannot be the thing that DISCOVERS a higher
 * version: it fails on the unknown fields before it ever reads the version.
 * Stage one therefore reads only the envelope, permissively; stage two runs the
 * strict decoder, and only when the version says it is safe to.
 */
sealed interface ProjectReadResult {
    data class Supported(val project: AndroidCreatorProject) : ProjectReadResult

    /**
     * The document needs a newer build. [rawBytes] is the file's bytes,
     * UNTOUCHED — not canonicalized, not re-serialized, not partially parsed.
     *
     * Preserving them exactly is the whole point: the user's work must still be
     * there after they update, and a "best effort" parse that drops the fields
     * it did not recognize would quietly destroy it.
     */
    data class UpdateRequired(
        val rawBytes: ByteArray,
        val minReaderVersion: Int,
    ) : ProjectReadResult {
        override fun equals(other: Any?): Boolean =
            other is UpdateRequired &&
                minReaderVersion == other.minReaderVersion &&
                rawBytes.contentEquals(other.rawBytes)

        override fun hashCode(): Int = 31 * rawBytes.contentHashCode() + minReaderVersion
    }
}

object ProjectReader {

    private val envelopeJson = Json { ignoreUnknownKeys = true }

    /**
     * Stage 1 then, if safe, stage 2.
     *
     * @param bytes the file exactly as stored.
     */
    fun read(bytes: ByteArray): ProjectReadResult {
        val envelope = envelopeJson.decodeFromString(
            VersionEnvelope.serializer(),
            bytes.decodeToString(),
        )
        if (envelope.minReaderVersion > AndroidCreatorProject.MIN_READER_VERSION) {
            return ProjectReadResult.UpdateRequired(bytes, envelope.minReaderVersion)
        }
        return ProjectReadResult.Supported(Canonical.decodeProject(bytes))
    }

    @kotlinx.serialization.Serializable
    internal data class VersionEnvelope(val schemaVersion: Int, val minReaderVersion: Int)
}
