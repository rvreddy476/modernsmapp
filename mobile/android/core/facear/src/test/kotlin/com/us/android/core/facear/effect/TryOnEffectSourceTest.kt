package com.us.android.core.facear.effect

import android.net.Uri
import com.google.common.truth.Truth.assertThat
import com.us.android.core.facear.TryOnEffectOrigin
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * Effect resolution, over indexes this test hands in — the cases a real
 * directory cannot easily be: nothing installed, a half-copied bundle, an asset
 * manager that throws, a cloud copy alongside a bundled one.
 *
 * The REAL shipped bundle is the subject of `MomentumLipstickBundleTest`
 * instead. Both matter: one bundle exists now, so "missing" is no longer the
 * only answer, but it is still the answer most of the catalogue gets and it has
 * to keep arriving as a sentence rather than as a camera with nothing on it.
 */
class TryOnEffectSourceTest {

    private val io = UnconfinedTestDispatcher()

    /** Assets as a map of directory to entries — what an APK's assets really are. */
    private fun assets(vararg dirs: Pair<String, List<String>>): EffectAssetIndex {
        val index = dirs.toMap()
        return EffectAssetIndex { path -> index[path].orEmpty() }
    }

    /**
     * A manifest reader over a map of path to text. The production code reads
     * the bundle`s config.json to learn its scene name, so a test that only
     * supplied an asset index no longer compiles - which is the point: a
     * bundle without a readable manifest is a half-copied bundle.
     */
    private fun manifests(vararg files: Pair<String, String>): EffectManifestReader {
        val index = files.toMap()
        return EffectManifestReader { path -> index[path] }
    }

    private class FakeStorage(private val unpacked: Map<String, String> = emptyMap()) :
        ArCloudEffectStorage {
        var prepared: Pair<Uri, String>? = null

        override fun unpackedEffect(slug: String): String? = unpacked[slug]
        override fun manifest(slug: String): String? = unpacked[slug]?.let { VALID_MANIFEST }

        override fun prepare(archive: Uri, slug: String) {
            prepared = archive to slug
        }
    }

    // ─── bundled ─────────────────────────────────────────────────────

    @Test
    fun `the bundled source answers missing, in plain language, when no bundle exists`() = runTest {
        val source = BundledTryOnEffects(assets(), manifests())

        val answer = source.resolve("momentum-eyewear-v1")

        assertThat(answer).isInstanceOf(TryOnEffectResolution.Missing::class.java)
        val reason = (answer as TryOnEffectResolution.Missing).reason
        assertThat(reason).isEqualTo(BundledTryOnEffects.NO_BUNDLE)
        // Plain language: a sentence a viewer can read, not a path or a code.
        assertThat(reason).doesNotContain("/")
        assertThat(reason).doesNotContain("config.json")
    }

    @Test
    fun `a directory without the manifest is a half-copied bundle, not an effect`() = runTest {
        val source = BundledTryOnEffects(
            assets("bnb-resources/effects/slug" to listOf("preview.png", "effect.js")),
            manifests(),
        )

        assertThat(source.resolve("slug")).isInstanceOf(TryOnEffectResolution.Missing::class.java)
    }

    @Test
    fun `a bundle with a manifest resolves to the vendor's relative load path`() = runTest {
        val source = BundledTryOnEffects(
            assets("bnb-resources/effects/slug" to listOf("config.json", "effect.js")),
            manifests("bnb-resources/effects/slug/config.json" to VALID_MANIFEST),
        )

        val answer = source.resolve("slug")

        assertThat(answer).isInstanceOf(TryOnEffectResolution.Available::class.java)
        val effect = (answer as TryOnEffectResolution.Available).effect
        assertThat(effect.loadPath).isEqualTo("effects/slug")
        assertThat(effect.origin).isEqualTo(TryOnEffectOrigin.BUNDLED)
    }

    @Test
    fun `a blank slug never touches the asset index`() = runTest {
        var asked = 0
        val source = BundledTryOnEffects(
            EffectAssetIndex {
                asked++
                listOf("config.json")
            },
            manifests(),
        )

        assertThat(source.resolve("  ")).isInstanceOf(TryOnEffectResolution.Missing::class.java)
        assertThat(asked).isEqualTo(0)
    }

    @Test
    fun `an asset index that throws is a missing bundle, not a crashed screen`() = runTest {
        val source = BundledTryOnEffects(EffectAssetIndex { error("asset manager is gone") }, manifests())

        assertThat(source.resolve("slug")).isInstanceOf(TryOnEffectResolution.Missing::class.java)
    }

    // ─── AR Cloud ────────────────────────────────────────────────────

    @Test
    fun `the cloud source says the effect has not been downloaded when nothing is unpacked`() =
        runTest {
            val source = ArCloudTryOnEffects(FakeStorage(), io)

            val answer = source.resolve("slug")

            assertThat(answer).isInstanceOf(TryOnEffectResolution.Missing::class.java)
        }

    @Test
    fun `an unpacked cloud effect resolves to its absolute path`() = runTest {
        val source = ArCloudTryOnEffects(
            FakeStorage(mapOf("slug" to "/data/user/0/app/files/bnb-ar-cloud/effects/slug")),
            io,
        )

        val answer = source.resolve("slug")

        val effect = (answer as TryOnEffectResolution.Available).effect
        assertThat(effect.loadPath).startsWith("/data/")
        assertThat(effect.origin).isEqualTo(TryOnEffectOrigin.AR_CLOUD)
    }

    @Test
    fun `storage that throws while looking is a missing effect`() = runTest {
        val source = ArCloudTryOnEffects(
            object : ArCloudEffectStorage {
                override fun unpackedEffect(slug: String): String? = error("no filesystem")
                override fun manifest(slug: String): String? = null
                override fun prepare(archive: Uri, slug: String) = Unit
            },
            io,
        )

        assertThat(source.resolve("slug")).isInstanceOf(TryOnEffectResolution.Missing::class.java)
    }

    // ─── composite ───────────────────────────────────────────────────

    @Test
    fun `both sources missing reports the BUNDLED reason, which is the actionable truth today`() =
        runTest {
            val composite = CompositeTryOnEffects(
                listOf(
                    BundledTryOnEffects(assets(), manifests()),
                    ArCloudTryOnEffects(FakeStorage(), io),
                ),
            )

            val answer = composite.resolve("slug")

            assertThat((answer as TryOnEffectResolution.Missing).reason)
                .isEqualTo(BundledTryOnEffects.NO_BUNDLE)
        }

    @Test
    fun `a bundled effect wins over a downloaded one`() = runTest {
        val composite = CompositeTryOnEffects(
            listOf(
                BundledTryOnEffects(assets("bnb-resources/effects/slug" to listOf("config.json")), manifests()),
                ArCloudTryOnEffects(FakeStorage(mapOf("slug" to "/data/cloud/slug")), io),
            ),
        )

        val effect = (composite.resolve("slug") as TryOnEffectResolution.Available).effect

        assertThat(effect.origin).isEqualTo(TryOnEffectOrigin.BUNDLED)
    }

    @Test
    fun `the cloud is reached when the build carries no bundle`() = runTest {
        val composite = CompositeTryOnEffects(
            listOf(
                BundledTryOnEffects(assets(), manifests()),
                ArCloudTryOnEffects(FakeStorage(mapOf("slug" to "/data/cloud/slug")), io),
            ),
        )

        val effect = (composite.resolve("slug") as TryOnEffectResolution.Available).effect

        assertThat(effect.origin).isEqualTo(TryOnEffectOrigin.AR_CLOUD)
    }

    @Test
    fun `no sources at all is still an answer the screen can render`() = runTest {
        val answer = CompositeTryOnEffects(emptyList()).resolve("slug")

        assertThat(answer).isInstanceOf(TryOnEffectResolution.Missing::class.java)
    }
}

// A manifest that declares real scene content, and one that does not.
//
// The second is the shape our own `momentum_lipstick` bundle currently has:
// a scene NAME, a script, and a dependency on a vendor component that an
// effect root does not honour. It loads cleanly and then draws nothing, which
// on a handset is a black screen — the founder reported exactly that as "the
// camera is not working". `declaresSceneContent` exists to catch it before the
// player is handed anything.
private const val VALID_MANIFEST =
    """{"scene":"Something","assets":{"images":{}},"render_list":{"default":[]}}"""

private const val EMPTY_SCENE_MANIFEST =
    """{"scene":"MomentumLipstick","depends":["makeup_lipsshine"],"script":{"entry_point":"scripts/index.js","type":"latest"}}"""
