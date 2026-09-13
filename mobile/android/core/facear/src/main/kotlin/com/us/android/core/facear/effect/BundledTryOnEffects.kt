package com.us.android.core.facear.effect

import com.us.android.core.facear.TryOnEffect
import com.us.android.core.facear.TryOnEffectOrigin

/**
 * Effect bundles shipped inside the APK.
 *
 * ## THE DIRECTORY, AND WHY IT IS THE VENDOR'S
 *
 * A bundle lives at
 *
 * ```
 * core/facear/src/main/assets/bnb-resources/effects/<slug>/
 * ```
 *
 * and is loaded by the path `effects/<slug>`, relative to the SDK's own
 * resources base. `bnb-resources` is Banuba's convention, not one invented
 * here: `BanubaSdkManager.getResourcesBase()` points at it and
 * `BanubaSdkManager.EFFECTS_RESOURCES_PATH` is `/effects`. Choosing our own
 * directory would mean passing extra resource paths to
 * `BanubaSdkManager.initialize`, which is a second thing to keep in step for no
 * gain.
 *
 * Android merges the assets of every module, so a bundle put here is served to
 * the app exactly as if it sat in `:app` — and it sits beside the code that
 * knows what to do with it instead. The full layout is in this module's README.
 *
 * ## WHAT MAKES A BUNDLE PRESENT
 *
 * Exactly one thing: a readable `config.json` in the effect's directory. It is
 * the manifest every Banuba effect has, the effect player fails without it, and
 * checking for the DIRECTORY alone would call a half-copied bundle present.
 *
 * ## WHAT IS ACTUALLY IN THERE
 *
 * `momentum_lipstick`, and for now only that. Two text files — a manifest that
 * depends on the SDK's own `makeup_lipsshine` prefab, and a script that turns
 * Momentum's `setShade` payload into the three settings that prefab requires.
 * `MomentumLipstickBundleTest` holds it to this class's constants, so the
 * bundle and the resolver cannot drift apart.
 */
class BundledTryOnEffects(
    private val assets: EffectAssetIndex,
    private val manifests: EffectManifestReader,
) : TryOnEffectSource {

    override suspend fun resolve(slug: String): TryOnEffectResolution {
        if (slug.isBlank()) return missing(slug)
        val dir = "$EFFECTS_ASSET_DIR/$slug"
        val entries = runCatching { assets.entries(dir) }.getOrDefault(emptyList())
        if (MANIFEST !in entries) return missing(slug)
        // Read HERE, where the bundle is resolved, rather than on the render
        // thread when it is too late: an effect whose manifest declares no
        // scene content blacks the preview out when loaded, and the whole
        // point is to know that before anything is handed to the player.
        val manifest = runCatching { manifests.read("$dir/$MANIFEST") }.getOrNull()
        return TryOnEffectResolution.Available(
            TryOnEffect(
                slug = slug,
                displayName = slug,
                origin = TryOnEffectOrigin.BUNDLED,
                loadPath = "$EFFECTS_LOAD_PREFIX/$slug",
                declaresSceneContent = manifest?.let(::effectDeclaresSceneContent),
            ),
        )
    }

    private fun missing(slug: String) = TryOnEffectResolution.Missing(slug, NO_BUNDLE)

    internal companion object {
        /** Under `src/main/assets`. The vendor's resources base plus its effects path. */
        const val EFFECTS_ASSET_DIR = "bnb-resources/effects"

        /** What `loadEffect` takes: the path RELATIVE to the resources base. */
        const val EFFECTS_LOAD_PREFIX = "effects"

        /** Every Banuba effect has one. Its absence is what "not installed" means. */
        const val MANIFEST = "config.json"

        const val NO_BUNDLE = "This build carries no try-on effect for this product yet."
    }
}

/**
 * Listing one asset directory — the ONE thing [BundledTryOnEffects] needs from
 * Android.
 *
 * A seam rather than an `AssetManager` parameter, so both answers are provable
 * in a plain JVM unit test instead of needing Robolectric to stand up an asset
 * manager. `MomentumLipstickBundleTest` points it at the real shipped files;
 * `TryOnEffectSourceTest` hands it the cases a directory on disk cannot easily
 * be — a half-copied bundle, an index that throws.
 */
fun interface EffectAssetIndex {
    /** The entries directly under [path], or empty when there is no such directory. */
    fun entries(path: String): List<String>
}
