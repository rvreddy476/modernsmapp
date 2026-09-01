package com.us.android.core.creator.model

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.util.Base64

/**
 * The cross-object invariants JSON Schema cannot express — V-1 … V-19.
 *
 * ## WHY THESE ARE NOT IN THE SCHEMA
 *
 * JSON Schema can say "this is a 64-character hex string". It cannot say "this
 * hash equals the hash on that page", or "these two arrays are the same length
 * as a third array in a different document". Those are the invariants that
 * actually prevent a wrong carousel or a duplicate post, so they get a named,
 * individually testable rule each rather than being folded into prose.
 *
 * Every violation is returned, not thrown, so a recovery screen can show the
 * user what is wrong instead of crashing on the first problem.
 */
object Validators {

    private val ULID = Regex("^[0-9A-HJKMNP-TV-Z]{26}$")
    private val UUID = Regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
    private val SHA256 = Regex("^[0-9a-f]{64}$")
    private val VAULT_PATH = Regex("^(sources|proxies|outputs)/[A-Za-z0-9_-]{1,64}\\.bin$")

    /** Local-only prefixes that must never reach a request, a log or telemetry. */
    private val LEAKY_PREFIXES = listOf("content:", "file:", "/data/", "/storage/")

    fun validate(project: AndroidCreatorProject): List<String> = buildList {
        addAll(validateIdentity(project))
        addAll(validateCardinality(project))
        addAll(validateReferences(project))
        addAll(validatePages(project))
        addAll(validatePublishReadiness(project))
        addAll(validateNoLeakage(project))
    }

    private fun validateIdentity(project: AndroidCreatorProject) = buildList {
        if (!ULID.matches(project.projectId)) add("projectId is not a Crockford ULID")
        if (project.schemaVersion != AndroidCreatorProject.SCHEMA_VERSION) {
            add("V-0: schemaVersion must be ${AndroidCreatorProject.SCHEMA_VERSION}")
        }
        if (project.status !in AndroidCreatorProject.STATUSES) {
            add("status '${project.status}' is not a known status")
        }

        // V-1: ids unique within the document.
        val assetIds = project.sourceAssets.map { it.assetId }
        if (assetIds.distinct().size != assetIds.size) add("V-1: duplicate assetId")
        val pageIds = project.pages.map { it.pageId }
        if (pageIds.distinct().size != pageIds.size) add("V-1: duplicate pageId")
        val layerIds = project.pages.flatMap { page -> page.layers.map { it.layerId } }
        if (layerIds.distinct().size != layerIds.size) add("V-1: duplicate layerId")
        val outputIds = project.pages.mapNotNull { it.renderedOutput?.outputId }
        if (outputIds.distinct().size != outputIds.size) add("V-1: duplicate outputId")

        // V-14: one vault path cannot be claimed twice.
        val vaultPaths = project.sourceAssets.map { it.vaultPath } +
            project.proxies.map { it.vaultPath } +
            project.pages.mapNotNull { it.renderedOutput?.vaultPath }
        if (vaultPaths.distinct().size != vaultPaths.size) add("V-14: duplicate vaultPath")
        vaultPaths.filterNot { VAULT_PATH.matches(it) }.forEach {
            add("V-14: vaultPath '$it' is not a relative app-private path")
        }
    }

    private fun validateCardinality(project: AndroidCreatorProject) = buildList {
        // V-13: 0 pages = text-only and needs real text; 1..10 otherwise.
        if (project.pages.size > AndroidCreatorProject.MAX_PAGES) {
            add("V-13: ${project.pages.size} pages exceeds ${AndroidCreatorProject.MAX_PAGES}")
        }
        if (project.pages.isEmpty() && project.postText.value.isBlank()) {
            add("V-13: a project with no pages must carry non-empty post text")
        }
        // A page-bearing project must declare a canvas; a text-only one must not
        // invent one.
        if (project.pages.isNotEmpty() && project.canvas == null) {
            add("a project with pages must declare a canvas")
        }
        project.canvas?.let { addAll(validateAspect(it)) }
    }

    /**
     * V-7 — the declared aspect must match the declared pixels.
     *
     * A canvas claiming 4:5 while measuring 1080x1080 would export at a
     * different shape than the editor previewed, and the mismatch would only
     * show up in the published post.
     */
    private fun validateAspect(canvas: Canvas): List<String> = buildList {
        val ratio = ASPECT_RATIOS[canvas.aspect]
        if (ratio == null) {
            add("V-7: unknown aspect '${canvas.aspect}'")
            return@buildList
        }
        val (wide, tall) = ratio
        val expected = canvas.widthPx * tall / wide
        // One pixel of slack: an odd width cannot divide evenly into every ratio.
        if (kotlin.math.abs(expected - canvas.heightPx) > ASPECT_PIXEL_SLACK) {
            add("V-7: aspect ${canvas.aspect} implies height $expected, got ${canvas.heightPx}")
        }
    }

    private fun validateReferences(project: AndroidCreatorProject) = buildList {
        val declared = project.sourceAssets.map { it.assetId }.toSet()
        project.pages.forEach { page ->
            page.layers.forEach { layer ->
                val ref = (layer as? ImageLayer)?.assetRef
                // V-2
                if (ref != null && ref !in declared) {
                    add("V-2: layer ${layer.layerId} references undeclared asset '$ref'")
                }
            }
        }
        // V-3
        project.proxies.filter { it.assetId !in declared }.forEach {
            add("V-3: proxy references undeclared asset '${it.assetId}'")
        }
        project.sourceAssets.filterNot { SHA256.matches(it.sha256) }.forEach {
            add("asset ${it.assetId} has a malformed sha256")
        }
    }

    private fun validatePages(project: AndroidCreatorProject) = buildList {
        project.pages.forEach { page ->
            addAll(validateLayers(page))
            // V-12, the unconditional half: decorative AND described is a
            // contradiction in any state. The "no decision yet" half lives in
            // publish readiness — an EDITING page the author has not decided on
            // is an ordinary in-progress state, not a defect.
            val access = page.accessibility
            if (access.decorative && access.altText.isNotEmpty()) {
                add("V-12: page ${page.pageId} is marked decorative but carries alt text")
            }
            // V-10
            val output = page.renderedOutput
            val confirmed = page.confirmedMedia
            if (output != null && confirmed != null &&
                confirmed.boundOutputSha256 != output.sha256
            ) {
                add("V-10: page ${page.pageId} confirmed media is bound to a different output")
            }
            if (confirmed != null) {
                if (!UUID.matches(confirmed.mediaId)) {
                    add("page ${page.pageId} mediaId is not a UUID")
                }
                if (confirmed.processingState !in ConfirmedMedia.PROCESSING_STATES) {
                    add("page ${page.pageId} unknown processingState '${confirmed.processingState}'")
                }
                if (confirmed.moderationState !in ConfirmedMedia.MODERATION_STATES) {
                    add("page ${page.pageId} unknown moderationState '${confirmed.moderationState}'")
                }
            }
        }
    }

    /**
     * V-4, V-5 and V-6 — the layer stack of one page.
     *
     * V-4 is what keeps a page a PAGE: exactly one base image at the bottom,
     * text above it. Two image layers would be a composition the exporter has no
     * defined behaviour for, and zero would be a page with nothing to render.
     */
    private fun validateLayers(page: Page): List<String> = buildList {
        val images = page.layers.filter { it.type == Layer.TYPE_IMAGE }
        if (images.size != 1) {
            add("V-4: page ${page.pageId} has ${images.size} image layers, expected exactly 1")
        }
        images.filter { it.z != 0 }.forEach {
            add("V-4: page ${page.pageId} image layer ${it.layerId} must be at z=0")
        }
        page.layers.filter { it.type == Layer.TYPE_TEXT && it.z < 1 }.forEach {
            add("V-4: page ${page.pageId} text layer ${it.layerId} must be above the image")
        }
        // V-5: z is the array index, so the document order IS the paint order.
        page.layers.forEachIndexed { index, layer ->
            if (layer.z != index) {
                add("V-5: page ${page.pageId} layer ${layer.layerId} z=${layer.z} != index $index")
            }
        }
        // V-6: a crop that runs off the canvas would sample pixels that do not exist.
        page.layers.filterIsInstance<ImageLayer>().map { it.crop }.forEach { crop ->
            if (crop.xMicros + crop.wMicros > MICROS) {
                add("V-6: page ${page.pageId} crop exceeds the canvas horizontally")
            }
            if (crop.yMicros + crop.hMicros > MICROS) {
                add("V-6: page ${page.pageId} crop exceeds the canvas vertically")
            }
        }
    }

    private fun validatePublishReadiness(project: AndroidCreatorProject) = buildList {
        val publishing = project.status == AndroidCreatorProject.STATUS_PUBLISHING ||
            project.status == AndroidCreatorProject.STATUS_PUBLISHED
        if (!publishing) return@buildList

        project.pages.forEach { page ->
            // V-9
            if (page.renderedOutput == null) {
                add("V-9: page ${page.pageId} is publishing with no rendered output")
            }
            if (page.confirmedMedia == null) {
                add("V-9: page ${page.pageId} is publishing with no confirmed media")
            }
            // V-12's publish half (every page needs a decision before leaving
            // the device) was retired 2026-09-01 by founder call: alt text is
            // a nudge, never a publish blocker. Only the unconditional
            // contradiction half (decorative AND described) remains, above.
        }
        if (project.status != AndroidCreatorProject.STATUS_PUBLISHED) return@buildList

        // V-11. Widening what can be REPRESENTED never widens what can be
        // PUBLISHED: exactly ready + passed, no other pair, ever.
        project.pages.mapNotNull { page -> page.confirmedMedia?.let { page.pageId to it } }
            .forEach { (pageId, media) ->
                if (media.processingState != ConfirmedMedia.PROCESSING_READY ||
                    media.moderationState != ConfirmedMedia.MODERATION_PASSED
                ) {
                    add(
                        "V-11: page $pageId published with " +
                            "${media.processingState}/${media.moderationState}, " +
                            "only ready/passed may publish",
                    )
                }
            }
    }

    /**
     * V-15 — local-only facts must not be serializable into anything outbound.
     *
     * Deliberately does NOT inspect [PostText.value] or text-layer content. A
     * user may legitimately post a URL, and a blanket ban on `://` would reject
     * ordinary writing. Only the fields that carry paths are checked.
     */
    private fun validateNoLeakage(project: AndroidCreatorProject) = buildList {
        val pathBearing = project.sourceAssets.map { it.vaultPath } +
            project.proxies.map { it.vaultPath } +
            project.pages.mapNotNull { it.renderedOutput?.vaultPath }
        pathBearing.forEach { path ->
            LEAKY_PREFIXES.firstOrNull { path.startsWith(it) }?.let {
                add("V-15: '$path' leaks a local location (prefix '$it')")
            }
        }
    }

    /**
     * V-16 … V-19 — the operation against the project it claims to publish.
     */
    fun validateOperation(
        operation: AndroidPublishOperation,
        project: AndroidCreatorProject,
    ): List<String> = buildList {
        if (!ULID.matches(operation.operationId)) add("operationId is not a Crockford ULID")
        if (operation.state !in AndroidPublishOperation.STATES) {
            add("unknown operation state '${operation.state}'")
        }
        if (!SHA256.matches(operation.frozenRequestSha256)) {
            add("frozenRequestSha256 is malformed")
        }

        // Schema-level parity: both empty or both non-empty. Exact equality is
        // this function's job, which is the division the reviewer required.
        val outputs = operation.orderedOutputSha256
        val mediaIds = operation.orderedMediaIds
        if (outputs.isEmpty() != mediaIds.isEmpty()) {
            add("the two ordered arrays must be both empty or both non-empty")
        }

        // V-16
        if (outputs.size != mediaIds.size) {
            add("V-16: ${outputs.size} output hashes but ${mediaIds.size} media ids")
        }
        if (outputs.size != project.pages.size) {
            add("V-16: operation covers ${outputs.size} items, project has ${project.pages.size} pages")
        }
        project.pages.forEachIndexed { index, page ->
            val output = page.renderedOutput
            if (output != null && index < outputs.size && outputs[index] != output.sha256) {
                add("V-16: output hash at $index does not match page ${page.pageId}")
            }
            val confirmed = page.confirmedMedia
            if (confirmed != null && index < mediaIds.size && mediaIds[index] != confirmed.mediaId) {
                add("V-16: media id at $index does not match page ${page.pageId}")
            }
        }

        addAll(validateFrozenRequest(operation, mediaIds))
    }

    /**
     * V-17 and V-18 — the opaque request bytes.
     *
     * V-18 runs FIRST and returns on failure. Reading a request whose hash or
     * length is already wrong would be interpreting bytes that are not the ones
     * the server minted the idempotency key for, and any conclusion drawn from
     * them would be about a request that was never sent.
     *
     * The request is parsed but NEVER re-serialized. Re-encoding it to inspect
     * it is exactly how the bytes drift from what the server hashed.
     */
    private fun validateFrozenRequest(
        operation: AndroidPublishOperation,
        mediaIds: List<String>,
    ): List<String> = buildList {
        val decoded = runCatching { Base64.getDecoder().decode(operation.frozenRequestBase64) }
            .getOrNull()
        if (decoded == null) {
            add("V-18: frozenRequestBase64 is not valid base64")
            return@buildList
        }
        if (decoded.size != operation.frozenRequestBytes) {
            add("V-18: frozenRequestBytes=${operation.frozenRequestBytes} but decoded ${decoded.size}")
        }
        if (Canonical.sha256Hex(decoded) != operation.frozenRequestSha256) {
            add("V-18: frozenRequestSha256 does not match the decoded bytes")
        }

        // The entire structural read is fallible, not just the parse.
        //
        // `jsonPrimitive` and `jsonArray` THROW on valid JSON of an unexpected
        // shape, so catching only the parse left a persisted payload from an old
        // or broken client able to blow up validation itself. A validator that
        // can crash is worse than no validator: it turns "this document is wrong"
        // into "this app does not open".
        val fields = readFrozenFields(decoded)
        if (fields == null) {
            add("V-17: the frozen request is not a readable v1 request")
            return@buildList
        }
        val (postType, requestMediaIds) = fields

        if (requestMediaIds != mediaIds) {
            add("V-17: the frozen request's media_ids do not match orderedMediaIds")
        }
        if (mediaIds.isEmpty() && postType != POST_TYPE_TEXT) {
            add("V-17: an operation with no media must carry post_type=text, got '$postType'")
        }
        if (mediaIds.isNotEmpty() && postType != POST_TYPE_IMAGE) {
            add("V-17: an operation with media must carry post_type=image, got '$postType'")
        }
    }

    /**
     * Total extraction of `post_type` and `media_ids`, or null.
     *
     * `media_ids` must be PRESENT and an array. Absent is not the same as `[]`:
     * the real `CreatePostRequest` always emits the field, so its absence means
     * these bytes came from something this build cannot reason about — and a
     * document whose provenance is unknown must not validate as a text post.
     */
    private fun readFrozenFields(decoded: ByteArray): Pair<String, List<String>>? {
        val root = runCatching { Json.parseToJsonElement(decoded.decodeToString()) }
            .getOrNull() as? JsonObject ?: return null

        val postType = (root["post_type"] as? JsonPrimitive)
            ?.takeIf { it.isString }?.content ?: return null

        val array = root["media_ids"] as? JsonArray ?: return null

        return postType to array.map { element ->
            (element as? JsonPrimitive)?.takeIf { it.isString }?.content ?: return null
        }
    }

    // MagicNumber: these ARE the aspect definitions. "4:5" to (4 to 5) is more
    // readable than a pair of named constants restating the string beside them.
    @Suppress("MagicNumber")
    private val ASPECT_RATIOS = mapOf(
        "1:1" to (1 to 1),
        "4:5" to (4 to 5),
        "3:4" to (3 to 4),
        "9:16" to (9 to 16),
    )
    private const val ASPECT_PIXEL_SLACK = 1

    const val POST_TYPE_TEXT = "text"
    const val POST_TYPE_IMAGE = "image"
    private const val MICROS = 1_000_000
}
