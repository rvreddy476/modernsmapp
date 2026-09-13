package com.us.android.core.facear

/**
 * The try-on domain: what a product says it can be tried on as, and which
 * shade or colourway the viewer has chosen.
 *
 * Pure Kotlin, no Android and no Banuba type. That is what lets `:core:commerce`
 * declare a descriptor on a `Product` without gaining the ability to call the
 * SDK, and what lets every rule below be tested on the JVM.
 */

/**
 * What kind of thing is being tried on.
 *
 * The vocabulary is the SERVER's (`try_on.kind`), turned into a closed type
 * here so a screen branches on a set the compiler knows rather than
 * string-matching. [UNKNOWN] is the honest landing place for a kind this build
 * has not seen: a server that adds "footwear" must make try-on unavailable,
 * never crash and never silently render the wrong effect on someone's face.
 */
enum class TryOnKind(val wire: String, val label: String) {
    EYEWEAR("eyewear", "Glasses"),
    MAKEUP("makeup", "Makeup"),
    JEWELLERY("jewellery", "Jewellery"),
    WATCH("watch", "Watch"),
    UNKNOWN("", "Try on"),
    ;

    companion object {
        fun from(raw: String?): TryOnKind {
            val wire = raw?.trim()?.lowercase().orEmpty()
            // "jewelry" as well as "jewellery": the server contract says the
            // British spelling, and a US spelling slipping through a seller
            // importer should still put a necklace on a neck.
            if (wire == "jewelry") return JEWELLERY
            return entries.firstOrNull { it != UNKNOWN && it.wire == wire } ?: UNKNOWN
        }
    }
}

/**
 * One shade or colourway.
 *
 * [hex] is optional because not every variant is a colour — a frame SIZE is a
 * variant with no colour to send — and because a malformed hex from the wire
 * must degrade to "no colour" rather than to a broken effect. Validation lives
 * in the JS-call builder, not in the constructor: a descriptor is wire data,
 * and refusing to construct one would lose the label the viewer can still
 * usefully see.
 *
 * [js] is the escape hatch the server contract provides for a look a colour
 * cannot drive — a gradient lens, a two-tone strap, a matte finish. When it is
 * present it REPLACES the generated call: the seller has stated the exact
 * script the effect needs and Momentum has nothing to add. It is effect-author
 * content, not buyer input; see [tryOnJsCall] for what that does and does not
 * imply about trusting it.
 */
data class TryOnVariant(
    val id: String,
    val label: String,
    val hex: String? = null,
    val js: String? = null,
)

/**
 * What the server says about a product's try-on.
 *
 * [capable] is stated by the server rather than inferred from the other fields
 * being present, because "the seller has not finished setting this up" and "the
 * seller switched it off" are different answers that both have to mean no. The
 * client's own sanity checks are in [isUsable], which is what the eligibility
 * decision actually reads.
 */
data class TryOnDescriptor(
    val capable: Boolean,
    val kind: TryOnKind,
    val effectSlug: String,
    val variants: List<TryOnVariant> = emptyList(),
) {
    /**
     * Whether this descriptor describes a try-on that could actually run.
     *
     * A `capable: true` with no effect slug is a half-configured product, and
     * an unrecognised kind is an effect this build does not know how to
     * parameterise. Both are "no try-on" — offering the action would open a
     * camera that can never put anything on a face.
     */
    val isUsable: Boolean
        get() = capable && effectSlug.isNotBlank() && kind != TryOnKind.UNKNOWN

    /** The variant the product screen's own selection maps onto, or null. */
    fun variantById(id: String?): TryOnVariant? =
        id?.let { wanted -> variants.firstOrNull { it.id == wanted } }

    companion object {
        /** What an absent `try_on` object means. */
        val NOT_CAPABLE = TryOnDescriptor(
            capable = false,
            kind = TryOnKind.UNKNOWN,
            effectSlug = "",
        )
    }
}

/** Where an effect bundle came from. */
enum class TryOnEffectOrigin(val label: String) {
    /** Shipped inside the APK, under the module's assets. */
    BUNDLED("bundled"),

    /** Downloaded for this licence and unpacked into app storage. */
    AR_CLOUD("AR Cloud"),
}

/**
 * A resolved effect: a bundle that exists, and the path the effect player
 * loads it by.
 *
 * [loadPath] is what goes to `BanubaSdkManager.loadEffect`. For a bundled
 * effect it is relative to the SDK's resources base (`effects/<slug>`); for a
 * cloud effect it is an absolute path in app storage. The effect player accepts
 * both, which is the only reason the two sources can share one type.
 */
data class TryOnEffect(
    val slug: String,
    val displayName: String,
    val origin: TryOnEffectOrigin,
    val loadPath: String,
    /**
     * Whether the bundle's manifest declares a scene with anything in it.
     *
     * **False means this effect must NOT be loaded.** An effect the player
     * loads takes over rendering, so an empty scene renders instead of the
     * camera and the handset shows a black rectangle — no error, no log line,
     * and everything upstream reporting success. See `effectDeclaresSceneContent`.
     *
     * Null is "the manifest could not be read", which is NOT the same as no:
     * refusing to load a working bundle because a file read failed would be a
     * worse failure than the one this guards against.
     */
    val declaresSceneContent: Boolean? = null,
)

/**
 * The FINISH a makeup try-on is drawn with.
 *
 * Not invented here: these are exactly the two values
 * `bnb_prefabs/makeup_lipsshine/schema.json` enumerates for its required
 * `finish` property, and `MakeupBase.apply` looks the name up in the prefab's
 * own settings table — an unrecognised one logs a warning and draws NOTHING.
 * So the closed set is the contract, and [wire] is the only string that may
 * reach the effect.
 */
enum class TryOnFinish(val wire: String, val label: String) {
    /** The default. A shopper comparing two reds wants the shade, not sparkle. */
    SHINE("shine", "Shine"),
    GLITTER("glitter", "Glitter"),
    ;

    companion object {
        val DEFAULT = SHINE

        fun from(raw: String?): TryOnFinish =
            entries.firstOrNull { it.wire == raw?.trim()?.lowercase() } ?: DEFAULT
    }
}

/**
 * How heavily the shade is laid on.
 *
 * The same story as [TryOnFinish]: `low`/`mid`/`high` are the prefab schema's
 * own enum, and `MakeupBase.apply` turns the chosen one into the blend weight
 * `K`. The LABELS are ours, because "mid" is not a word a shopper picks a
 * lipstick with.
 */
enum class TryOnCoverage(val wire: String, val label: String) {
    LOW("low", "Sheer"),

    /**
     * The default. Full coverage on a segmentation mask reads as paint rather
     * than lipstick on anyone whose lips the mask edges slightly over-report.
     */
    MID("mid", "Medium"),
    HIGH("high", "Full"),
    ;

    companion object {
        val DEFAULT = MID

        fun from(raw: String?): TryOnCoverage =
            entries.firstOrNull { it.wire == raw?.trim()?.lowercase() } ?: DEFAULT
    }
}

/**
 * Everything about a try-on except the colour: the two prefab settings the
 * wire payload cannot carry, chosen by the shopper instead of defaulted in JS.
 *
 * They are free — the prefab already implements both — and they are the
 * difference between a colour picker and a try-on.
 */
data class TryOnLook(
    val finish: TryOnFinish = TryOnFinish.DEFAULT,
    val coverage: TryOnCoverage = TryOnCoverage.DEFAULT,
) {
    companion object {
        val DEFAULT = TryOnLook()
    }
}
