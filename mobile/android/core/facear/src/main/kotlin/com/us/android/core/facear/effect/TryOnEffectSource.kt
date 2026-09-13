package com.us.android.core.facear.effect

import com.us.android.core.facear.TryOnEffect

/**
 * Where a try-on effect bundle comes from.
 *
 * Two implementations exist — [BundledTryOnEffects] (inside the APK) and
 * [ArCloudTryOnEffects] (downloaded for this licence) — and a screen asks
 * neither of them directly: it asks [CompositeTryOnEffects], which prefers the
 * bundled copy and falls back to the cloud.
 *
 * ## WHAT SHIPS TODAY
 *
 * One bundle: `momentum_lipstick`, in this module's assets, composed from the
 * `makeup_lipsshine` prefab that the SDK's own `makeup` artifact ships
 * complete. So [BundledTryOnEffects] answers [TryOnEffectResolution.Available]
 * for that slug and [TryOnEffectResolution.Missing] for every other one — which
 * is still most of the catalogue, and is why this interface exists rather than a
 * direct `loadEffect` call: "no bundle for THIS product" has to be an answer the
 * screen can render as a sentence, not a camera that opens and tracks a face
 * with nothing on it.
 *
 * A second makeup look is another two files (see the README). A try-on with no
 * prefab behind it — eyewear, jewellery, a watch — is not, and the README says
 * what it would actually need.
 */
interface TryOnEffectSource {
    /** Resolves the effect for [slug], or says why it cannot. */
    suspend fun resolve(slug: String): TryOnEffectResolution
}

/** The answer. */
sealed interface TryOnEffectResolution {
    /** A bundle exists; [TryOnEffect.loadPath] is what the effect player takes. */
    data class Available(val effect: TryOnEffect) : TryOnEffectResolution

    /**
     * No bundle for this slug. [reason] is plain language for the viewer, not a
     * log line: it is what most of the catalogue still gets, because one
     * bundle ships and it is a lipstick.
     */
    data class Missing(val slug: String, val reason: String) : TryOnEffectResolution
}

/**
 * Bundled first, cloud second.
 *
 * That order and not the other way round: a bundled effect is already on the
 * device, costs no network and cannot half-download, so when a build ships one
 * it should win. The cloud is what makes a new shade possible without a store
 * release.
 *
 * When both are missing the viewer sees the FIRST source's reason, because
 * "this build carries no try-on effect" is the actionable truth today and "the
 * download has not arrived" would be misleading when nothing was ever queued.
 */
class CompositeTryOnEffects(
    private val sources: List<TryOnEffectSource>,
) : TryOnEffectSource {

    override suspend fun resolve(slug: String): TryOnEffectResolution {
        var first: TryOnEffectResolution.Missing? = null
        sources.forEach { source ->
            when (val answer = source.resolve(slug)) {
                is TryOnEffectResolution.Available -> return answer
                is TryOnEffectResolution.Missing -> if (first == null) first = answer
            }
        }
        return first ?: TryOnEffectResolution.Missing(slug, NO_SOURCES)
    }

    private companion object {
        const val NO_SOURCES = "Try-on effects are not set up in this build."
    }
}
