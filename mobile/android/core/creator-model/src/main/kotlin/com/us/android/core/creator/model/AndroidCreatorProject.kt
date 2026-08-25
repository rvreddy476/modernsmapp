package com.us.android.core.creator.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * `AndroidCreatorProject` v1 — the frozen Creator Studio P0-A document.
 *
 * ## THIS IS A FROZEN CONTRACT
 *
 * The shape, the field names and the canonical byte encoding are frozen. The
 * checked-in fixtures in `src/test/resources/fixtures` assert exact SHA-256
 * values, so changing a field name, a default, or a number's units is a
 * breaking change that will turn those tests red — deliberately.
 *
 * ## ANDROID ONLY
 *
 * Named `AndroidCreatorProject`, not `CreatorProject`, because no iOS
 * compatibility was audited and none is claimed. A later cross-platform schema
 * may require a migration; the rules below (no platform font families, no `sp`,
 * explicit canonicalization) exist to make that migration small, not to pretend
 * it is unnecessary.
 *
 * ## NO FLOATING POINT, ANYWHERE
 *
 * Every fractional quantity is an integer count of millionths, suffixed
 * `Micros`. This is not a style choice. Canonicalizing IEEE-754 doubles is
 * unsound: parsing to a binary double before decimal rounding can put a
 * midpoint on either side, and ordinary serializers emit `0.08` where the
 * contract wants `0.080000`. Integers have exactly one representation, so the
 * fingerprint is stable across platforms and serializer versions.
 *
 * ## THE PUBLISH OPERATION IS NOT IN HERE
 *
 * [AndroidPublishOperation] is a separate record. Putting it in the document
 * would make the document mutate on every publish state change, which would
 * change the document hash the operation is bound to — the binding would
 * invalidate itself.
 */
@Serializable
data class AndroidCreatorProject(
    val schemaVersion: Int = SCHEMA_VERSION,
    val minReaderVersion: Int = MIN_READER_VERSION,
    val profile: String = PROFILE_POST,
    val projectId: String,
    val revision: Int,
    val status: String,
    val createdAtMillis: Long,
    val updatedAtMillis: Long,
    val postText: PostText,
    /**
     * Null for a text-only project.
     *
     * A text post has no visual page, so inventing a canvas for it would be a
     * fabricated fact. [Validators] requires a canvas exactly when pages exist.
     */
    val canvas: Canvas? = null,
    val sourceAssets: List<SourceAsset> = emptyList(),
    val proxies: List<Proxy> = emptyList(),
    /**
     * 0 pages = text-only, 1 = single photo, 2–10 = carousel.
     *
     * Zero is legal. The freeze review accepted this divergence from its own
     * "1–10" wording because its required text-only fixture has no page at all,
     * so `minItems: 1` would have made that fixture invalid by construction.
     */
    val pages: List<Page> = emptyList(),
    val distribution: Distribution = Distribution(),
) {
    companion object {
        /**
         * v2 (P1-B): [Adjustments] gained `saturationMicros` and `warmthMicros`.
         * v1 documents decode (absent channels default to 0); v2 bytes carry the
         * new keys always, so a v1-only reader must defer rather than parse —
         * hence minReader moves with the schema.
         */
        const val SCHEMA_VERSION = 2
        const val MIN_READER_VERSION = 2
        const val PROFILE_POST = "post"
        const val MAX_PAGES = 10

        const val STATUS_EDITING = "editing"
        const val STATUS_PUBLISHING = "publishing"
        const val STATUS_PUBLISHED = "published"
        const val STATUS_FAILED = "failed"
        const val STATUS_QUARANTINED = "quarantined"

        val STATUSES = setOf(
            STATUS_EDITING,
            STATUS_PUBLISHING,
            STATUS_PUBLISHED,
            STATUS_FAILED,
            STATUS_QUARANTINED,
        )
    }
}

@Serializable
data class PostText(val value: String, val language: String)

@Serializable
data class Canvas(
    val widthPx: Int,
    val heightPx: Int,
    val aspect: String,
    val safeZone: SafeZone,
)

@Serializable
data class SafeZone(
    val topMicros: Int,
    val bottomMicros: Int,
    val leftMicros: Int,
    val rightMicros: Int,
)

/**
 * An imported original, copied into the app-private vault.
 *
 * [vaultPath] is always relative. An absolute path or a `content://` URI here
 * would be both a durability bug (the grant can be revoked) and a privacy leak
 * waiting to be serialized into a request or a crash report.
 */
@Serializable
data class SourceAsset(
    val assetId: String,
    val kind: String,
    val vaultPath: String,
    val sha256: String,
    val bytes: Long,
    val mime: String,
    val widthPx: Int,
    val heightPx: Int,
    val origin: String,
)

@Serializable
data class Proxy(val assetId: String, val vaultPath: String, val maxEdgePx: Int)

/**
 * One page of the carousel.
 *
 * Accessibility lives HERE, not on the source asset: the same photo can be
 * cropped differently or reused on two pages, and its honest description can
 * differ between them.
 */
@Serializable
data class Page(
    val pageId: String,
    val accessibility: Accessibility,
    val layers: List<Layer>,
    val renderedOutput: RenderedOutput? = null,
    val confirmedMedia: ConfirmedMedia? = null,
)

@Serializable
data class Accessibility(val altText: String, val decorative: Boolean)

/**
 * A page layer.
 *
 * ## WHY THIS IS SEALED RATHER THAN ONE CLASS WITH NULLABLE HALVES
 *
 * A single class carrying `assetRef`/`crop`/`adjustments` AND `text`/`style`,
 * each nullable, would serialize an image layer with `"style":null,"text":null`
 * — four bytes of nothing that are not in the frozen fixtures and would break
 * every canonical hash. It would also make "an image layer with a font" a
 * representable state that V-4 then has to forbid at runtime.
 *
 * Sealing it makes the wrong shape unconstructible instead of merely invalid.
 */
@Serializable(with = LayerSerializer::class)
sealed interface Layer {
    val layerId: String
    val type: String
    val z: Int
    val transform: Transform

    companion object {
        const val TYPE_IMAGE = "image"
        const val TYPE_TEXT = "text"
    }
}

@Serializable
data class ImageLayer(
    override val layerId: String,
    override val type: String = Layer.TYPE_IMAGE,
    override val z: Int,
    override val transform: Transform,
    val assetRef: String,
    val crop: Crop,
    val adjustments: Adjustments,
) : Layer

@Serializable
data class TextLayer(
    override val layerId: String,
    override val type: String = Layer.TYPE_TEXT,
    override val z: Int,
    override val transform: Transform,
    val text: LayerText,
    val style: TextStyle,
) : Layer

@Serializable
data class Transform(
    val xMicros: Int,
    val yMicros: Int,
    val scaleMicros: Int,
    val rotationDegMicros: Int,
)

@Serializable
data class Crop(val xMicros: Int, val yMicros: Int, val wMicros: Int, val hMicros: Int)

/**
 * The four look channels, in micros (±1_000_000 = ±1.0).
 *
 * Saturation and warmth arrived in schema v2 with default 0, which is exactly
 * "unchanged": a v1 document decodes to the identical look it always had.
 */
@Serializable
data class Adjustments(
    val exposureMicros: Int,
    val contrastMicros: Int,
    val saturationMicros: Int = 0,
    val warmthMicros: Int = 0,
)

@Serializable
data class LayerText(val value: String, val language: String)

/**
 * Fonts are referenced by asset id + version + hash, never by a platform family
 * name. A system fallback would silently change authored pixels between
 * devices, which is exactly what an exported image must not do.
 */
@Serializable
data class TextStyle(
    val fontAssetId: String,
    val fontVersion: String,
    val fontSha256: String,
    val license: String,
    val weight: Int,
    val sizeCanvasMicros: Int,
    val colorArgb: String,
    val align: String,
)

@Serializable
data class RenderedOutput(
    val outputId: String,
    val vaultPath: String,
    val sha256: String,
    val bytes: Long,
    val mime: String,
    val widthPx: Int,
    val heightPx: Int,
)

/**
 * A confirmed server asset bound to this page's rendered output.
 *
 * [processingState] uses the server's own vocabulary verbatim — all six values
 * the `media_assets` CHECK constraint permits — so there is no mapping layer to
 * drift. Widening what can be REPRESENTED never widens what can be PUBLISHED:
 * [Validators] still requires exactly `ready` + `passed` to publish.
 */
@Serializable
data class ConfirmedMedia(
    val mediaId: String,
    val processingState: String,
    val moderationState: String,
    val boundOutputSha256: String,
) {
    companion object {
        val PROCESSING_STATES = setOf(
            "pending_upload",
            "uploaded",
            "processing",
            "ready",
            "failed",
            "rejected",
        )
        val MODERATION_STATES = setOf("pending", "passed", "rejected")
        const val PROCESSING_READY = "ready"
        const val MODERATION_PASSED = "passed"
    }
}

@Serializable
data class Distribution(val audience: String = AUDIENCE_PUBLIC) {
    companion object {
        const val AUDIENCE_PUBLIC = "public"
    }
}

/**
 * The publish operation — a SEPARATE, immutable-cored record.
 *
 * ## WHAT IS IMMUTABLE AND WHAT IS NOT
 *
 * Everything that binds the operation to a specific piece of work never
 * changes: [operationId], [projectId], [boundRevision], [projectDocumentSha256],
 * [orderedOutputSha256], [orderedMediaIds], [creationKey], and all three
 * `frozenRequest*` fields. Only [state], [serverPostId], [lastError],
 * [supersededByOperationId] and [updatedAtMillis] transition.
 *
 * ## WHY THE REQUEST BYTES ARE OPAQUE BASE64
 *
 * [frozenRequestBase64] holds the EXACT bytes the server minted the idempotency
 * key for. They are never NFC-normalized, never re-serialized, never key-sorted
 * — the project canonicalizer must not touch them. Regenerating them and
 * getting one byte different turns a legitimate retry into a
 * `409 IDEMPOTENCY_KEY_REUSED`, or worse, into a second published post.
 *
 * ## EMPTY ARRAYS ARE LEGAL
 *
 * A text-only post has no media and no rendered output, so both ordered arrays
 * are empty. They are either both empty or both non-empty; exact length
 * equality with `pages.length` is [Validators]' job, not the schema's.
 */
@Serializable
data class AndroidPublishOperation(
    val operationId: String,
    val projectId: String,
    val boundRevision: Int,
    val projectDocumentSha256: String,
    val orderedOutputSha256: List<String>,
    val orderedMediaIds: List<String>,
    val creationKey: String,
    val frozenRequestBase64: String,
    val frozenRequestSha256: String,
    val frozenRequestBytes: Int,
    val state: String,
    val serverPostId: String? = null,
    val lastError: OperationError? = null,
    val supersededByOperationId: String? = null,
    val createdAtMillis: Long,
    val updatedAtMillis: Long,
) {
    companion object {
        const val STATE_PUBLISHING = "publishing"
        const val STATE_PUBLISHED = "published"
        const val STATE_FAILED = "failed"
        const val STATE_SUPERSEDED = "superseded"

        val STATES = setOf(STATE_PUBLISHING, STATE_PUBLISHED, STATE_FAILED, STATE_SUPERSEDED)

        /** States that hold the one live slot per project. Terminal states do not. */
        val LIVE_STATES = setOf(STATE_PUBLISHING, STATE_FAILED)
    }
}

@Serializable
data class OperationError(
    val code: String,
    @SerialName("messageKey") val messageKey: String,
    val atMillis: Long,
)
