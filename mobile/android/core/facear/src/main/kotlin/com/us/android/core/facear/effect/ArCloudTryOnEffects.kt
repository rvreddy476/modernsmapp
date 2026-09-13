package com.us.android.core.facear.effect

import android.net.Uri
import com.us.android.core.facear.TryOnEffect
import com.us.android.core.facear.TryOnEffectOrigin
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Effect bundles downloaded for this licence and unpacked into app storage —
 * Banuba's AR Cloud.
 *
 * ## WHAT IS USED, AND WHAT IS DELIBERATELY NOT
 *
 * Only `ArEffectsResourceManager`, the vendor's unpacker. It is a plain
 * two-argument object (`AssetManager`, storage dir), it owns the directory
 * layout the effect player expects, and `prepareEffect(uri, name)` unpacks a
 * downloaded archive into it.
 *
 * `BackendArEffectsRepository` — which would LIST the effects a licence
 * entitles you to — is not used, and that is a decision rather than an
 * omission. It takes five collaborators (a remote data source, a local one, an
 * assets provider, an assets manager, a dispatcher) that the vendor only
 * assembles inside its own Koin graph, and that graph is started by the reel
 * studio in `:feature:post`. Reaching it from here would mean either a
 * `:core:` → `:feature:` dependency or a second Koin start in one process,
 * and the second is precisely the unverified hazard the licence gate is
 * already written around.
 *
 * So the division of labour is: something that HAS a download URL calls
 * [prepare]; this source answers whether the unpacked bundle is on disk.
 * Nothing queues a download today, which is why every resolution answers
 * [TryOnEffectResolution.Missing] — honestly, and in a sentence.
 *
 * ## THE ADAPTER SEAM
 *
 * [storage] is an interface, not the vendor class, for the same reason the
 * licence gate has a seam: it makes "the cloud has nothing" testable on the
 * JVM. [BanubaArCloudStorage] is the real one.
 */
class ArCloudTryOnEffects(
    private val storage: ArCloudEffectStorage,
    private val io: CoroutineDispatcher,
) : TryOnEffectSource {

    override suspend fun resolve(slug: String): TryOnEffectResolution {
        if (slug.isBlank()) return missing(slug)
        val path = withContext(io) {
            runCatching { storage.unpackedEffect(slug) }.getOrNull()
        } ?: return missing(slug)
        val manifest = withContext(io) { runCatching { storage.manifest(slug) }.getOrNull() }
        return TryOnEffectResolution.Available(
            TryOnEffect(
                slug = slug,
                displayName = slug,
                origin = TryOnEffectOrigin.AR_CLOUD,
                // An ABSOLUTE path, unlike the bundled source's relative one.
                // The effect player accepts either; app storage is not under
                // the SDK's resources base, so there is no relative form.
                loadPath = path,
                // A downloaded bundle gets the same guard a shipped one does.
                // A half-authored effect from the cloud blacks the preview out
                // in exactly the same way, and the cloud is the source we have
                // the LEAST control over.
                declaresSceneContent = manifest?.let(::effectDeclaresSceneContent),
            ),
        )
    }

    /**
     * Unpacks a downloaded archive for [slug]. Whoever obtained the URL calls
     * this; resolution afterwards finds the bundle on disk.
     */
    suspend fun prepare(archive: Uri, slug: String): Boolean = withContext(io) {
        runCatching { storage.prepare(archive, slug) }.isSuccess
    }

    private fun missing(slug: String) = TryOnEffectResolution.Missing(slug, NOT_DOWNLOADED)

    private companion object {
        const val NOT_DOWNLOADED = "This product's try-on effect has not been downloaded yet."
    }
}

/** The vendor's effect storage, as the two operations this module needs. */
interface ArCloudEffectStorage {
    /** The absolute path of the unpacked effect for [slug], or null when it is not there. */
    fun unpackedEffect(slug: String): String?

    /** The unpacked bundle's `config.json` as text, or null when it cannot be read. */
    fun manifest(slug: String): String?

    /** Unpacks [archive] into the effects directory under [slug]. Throws on failure. */
    fun prepare(archive: Uri, slug: String)
}

/**
 * The real thing, over `ArEffectsResourceManager`.
 *
 * Constructed lazily rather than injected: the vendor class touches the asset
 * manager and creates directories in its constructor, and doing that at graph
 * creation would make every process pay for a feature most sessions never
 * open.
 */
class BanubaArCloudStorage(
    private val manager: () -> com.banuba.sdk.arcloud.data.ArEffectsResourceManager,
) : ArCloudEffectStorage {

    override fun unpackedEffect(slug: String): String? {
        val dir = File(manager().storageEffectsDir, slug)
        // The same manifest test the bundled source uses: a directory that
        // exists because a download was interrupted is not an effect.
        return dir.absolutePath.takeIf {
            File(dir, BundledTryOnEffects.MANIFEST).canRead()
        }
    }

    override fun manifest(slug: String): String? = runCatching {
        File(File(manager().storageEffectsDir, slug), BundledTryOnEffects.MANIFEST).readText()
    }.getOrNull()

    override fun prepare(archive: Uri, slug: String) = manager().prepareEffect(archive, slug)
}
