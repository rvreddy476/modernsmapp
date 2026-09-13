package com.us.android.core.facear.effect

import com.google.common.truth.Truth.assertThat
import com.us.android.core.facear.TryOnEffectOrigin
import com.us.android.core.facear.TryOnJsContract
import kotlinx.coroutines.test.runTest
import org.junit.Test
import java.io.File

/**
 * The one effect bundle this build actually ships, held to the four contracts
 * it has to satisfy at once.
 *
 * ## WHAT THIS CAN AND CANNOT PROVE
 *
 * It can prove that the files exist, that they sit in the directory
 * [BundledTryOnEffects] looks in, that the slug is the one the server's
 * descriptor uses, that the manifest depends only on a prefab Banuba ships, and
 * that the script installs the method name [TryOnJsContract] tells every bundle
 * to install. Those are the four ways this bundle has historically been wrong
 * on paper, and they are all decidable from the bytes on disk.
 *
 * It cannot prove that a lipstick appears on a face. There is no JS engine on
 * the JVM test classpath and no device in this repository, so the assertions
 * about `scripts/index.js` are assertions about its TEXT. That is a weaker
 * thing than running it, and it is stated here rather than dressed up: these
 * tests catch a rename, a moved file, a dropped global and an invented
 * dependency. They do not catch a logic error inside `setShade`.
 */
class MomentumLipstickBundleTest {

    /**
     * The slug, written out rather than read from the bundle.
     *
     * This is the whole point of the assertion below: the server descriptor
     * publishes `momentum_lipstick`, and that string is the ONLY join between a
     * catalogue row and a directory in the APK. A test that read the slug from
     * the directory it is checking would agree with any rename.
     */
    private val slug = "momentum_lipstick"

    /** The defaults `scripts/index.js` must supply, because the prefab requires all three. */
    private val defaultFinish = "shine"
    private val defaultCoverage = "mid"

    // ─── where the files are ─────────────────────────────────────────

    /**
     * `:core:facear`'s own directory, found by walking up from wherever the
     * test runner happened to start. Gradle's working directory for Android
     * unit tests is not contractual, and a hard-coded relative path is the
     * kind of thing that passes here and fails in CI.
     */
    private val moduleDir: File =
        generateSequence(File("").absoluteFile) { it.parentFile }
            .firstOrNull { File(it, "$ASSETS/${BundledTryOnEffects.EFFECTS_ASSET_DIR}").isDirectory }
            ?: error("could not find $ASSETS/${BundledTryOnEffects.EFFECTS_ASSET_DIR} above ${File("").absolutePath}")

    /** The effects root, built from the constant the production code resolves with. */
    private val effectsDir = File(moduleDir, "$ASSETS/${BundledTryOnEffects.EFFECTS_ASSET_DIR}")

    private val bundleDir = File(effectsDir, slug)

    private fun read(relative: String) = File(bundleDir, relative).readText()

    /**
     * The shipped assets as an [EffectAssetIndex], answering the way
     * `AssetManager.list` answers.
     *
     * Not a fake with a hand-written map: every other test in this module
     * proves what the source does with an index it was handed, and this one has
     * to prove what it does with the FILES, which is a different claim.
     * `AssetManager.list` returns null for a path that is not a directory, and
     * `File.list` does the same, so the shape matches without pretending.
     */
    private fun shippedAssets() = EffectAssetIndex { path ->
        File(moduleDir, "$ASSETS/$path").list()?.toList().orEmpty()
    }

    /**
     * The manifest reader over the SAME shipped tree, so the resolver is proven
     * against the real files rather than a fixture. A bundle is resolved by
     * reading its config.json, because an effect that declares no scene content
     * blacks the preview out when the player loads it.
     */
    private fun shippedManifests() = EffectManifestReader { path ->
        File(moduleDir, "$ASSETS/$path").takeIf { it.isFile }?.readText()
    }

    // ─── the bundle is installed ─────────────────────────────────────

    @Test
    fun `the bundle sits in the directory the bundled source looks in`() {
        // Not File(…, "bnb-resources/effects/momentum_lipstick") spelled out:
        // the path is composed from the production constant, so moving the
        // bundle without moving the code fails here.
        assertThat(File(bundleDir, BundledTryOnEffects.MANIFEST).isFile).isTrue()
    }

    @Test
    fun `the slug is exactly the one the server descriptor publishes`() {
        assertThat(effectsDir.list()?.toList()).contains(slug)
    }

    @Test
    fun `the bundled source resolves the shipped effect, from the shipped files`() = runTest {
        val answer = BundledTryOnEffects(shippedAssets(), shippedManifests()).resolve(slug)

        assertThat(answer).isInstanceOf(TryOnEffectResolution.Available::class.java)
        val effect = (answer as TryOnEffectResolution.Available).effect
        assertThat(effect.origin).isEqualTo(TryOnEffectOrigin.BUNDLED)
        // What BanubaSdkManager.loadEffect takes: relative to the SDK's own
        // resources base, which is why no path of ours is configured anywhere.
        assertThat(effect.loadPath).isEqualTo("${BundledTryOnEffects.EFFECTS_LOAD_PREFIX}/$slug")
    }

    @Test
    fun `a product whose slug we do not ship is still a missing bundle`() = runTest {
        // The installed path must not have made the missing path unreachable:
        // every other slug the catalogue can name has to keep answering with a
        // sentence rather than opening a camera.
        val answer = BundledTryOnEffects(shippedAssets(), shippedManifests()).resolve("momentum_sunglasses")

        assertThat(answer).isInstanceOf(TryOnEffectResolution.Missing::class.java)
    }

    // ─── the manifest ────────────────────────────────────────────────

    @Test
    fun `the manifest is shaped like an EFFECT, not like a prefab`() {
        val manifest = read(BundledTryOnEffects.MANIFEST)

        // ─── WHAT THE DEVICE TAUGHT US ──────────────────────────────────
        //
        // The first version of this file was modelled on a PREFAB manifest:
        // `apply_order` plus `depends` plus a `script` STRING. The effect
        // player refused it outright:
        //
        //   Malformed config.json: missing the required property 'scene'.
        //   You may be trying to load an effect for SDK v0.x which is not
        //   compatible with the SDK v1.x
        //
        // The real shape was read from a working effect (BeautyBGEffects)
        // pulled off the handset out of Banuba's own demo app, which is the
        // only documentation of this format that exists.
        assertThat(manifest).contains("\"scene\"")
        // `script` is an OBJECT with an entry point, not a path string. A
        // string is the v0.x shape and is what was rejected.
        assertThat(manifest).contains("\"entry_point\"")
        assertThat(File(bundleDir, SCRIPT).isFile).isTrue()
        // `apply_order` is prefab-only — the SDK says so in as many words:
        // "`apply_order` key is mondatory in prefabs". It has no meaning here.
        assertThat(manifest).doesNotContain("\"apply_order\"")
    }

    // The bundle still cannot draw, and the code must know that rather than
    // discover it as a black screen on someone's face.
    //
    // `depends` on a vendor prefab is NOT honoured at an effect root. The
    // effect loads and activates cleanly and then fails one layer deeper:
    // findMaterial("shaders/lipsshine/shiny") returns null, because the
    // prefab's assets were never brought into the scene. Confirmed on the
    // device, and in the SDK binary, where `depends` and `apply_order` are
    // prefab-config keys and no effect-level prefab-include key exists.
    //
    // So this bundle declares a scene NAME and a script and no scene CONTENT.
    // An effect like that takes over rendering and draws nothing. The
    // resolver reads the manifest precisely so the screen can fall back to
    // the plain camera instead.
    @Test
    fun `the shipped bundle is known to declare no scene content`() {
        assertThat(effectDeclaresSceneContent(read(BundledTryOnEffects.MANIFEST))).isFalse()
    }

    @Test
    fun `the manifest's entry point is a script that is actually shipped`() {
        val manifest = read(BundledTryOnEffects.MANIFEST)

        // Relative to the effect's OWN directory, which is how the working
        // effect declares it ("entry_point": "config.js"). The earlier
        // absolute-from-resources-base spelling was the prefab convention.
        assertThat(manifest).contains("\"entry_point\": \"$SCRIPT\"")
        assertThat(File(bundleDir, SCRIPT).isFile).isTrue()
    }

    @Test
    fun `the bundle carries no authored shader, mesh or texture`() {
        val shipped = bundleDir.walkTopDown().filter { it.isFile }
            .map { it.relativeTo(bundleDir).invariantSeparatorsPath }
            .toList()

        // The claim this bundle makes is that a makeup try-on needs NO authored
        // 3D or GLSL content, because Banuba's prefab already has it. Two text
        // files is that claim, checkable. A .frag, .bsm2 or .png appearing here
        // means someone started authoring an effect, and the README's statement
        // about eyewear and jewellery has stopped being the whole story.
        assertThat(shipped).containsExactly(BundledTryOnEffects.MANIFEST, SCRIPT)
    }

    // ─── the script's side of the JS contract ────────────────────────

    @Test
    fun `the script installs the makeup method on the global object`() {
        val script = read(SCRIPT)

        // THE method name, from the same constant the app emits — not the
        // string "setShade". `callJsMethod` resolves its name against the
        // global object, so an `exports` entry alone would not be callable.
        assertThat(script).contains("globalThis.${TryOnJsContract.MAKEUP_METHOD} =")
        assertThat(script).contains("function ${TryOnJsContract.MAKEUP_METHOD}(")
    }

    @Test
    fun `the script accepts the payload in both the forms the two entry points send`() {
        val script = read(SCRIPT)

        // callJsMethod may hand the arguments over as JSON text; evalJs hands
        // over an object literal. A bundle that handles only one of them works
        // for generated calls and breaks for a variant's own `js`, or the
        // reverse — and the failure is silent on a device.
        assertThat(script).contains("JSON.parse(")
        assertThat(script).contains("typeof raw === \"object\"")
    }

    @Test
    fun `the script sends every setting the prefab schema marks required`() {
        val script = read(SCRIPT)

        // makeup_lipsshine/schema.json requires color, finish AND coverage, and
        // Base.setPrefabSettings throws on a key it does not know — so the key
        // spelling is as load-bearing as the presence.
        assertThat(script).contains("color:")
        assertThat(script).contains("finish:")
        assertThat(script).contains("coverage:")
    }

    @Test
    fun `the script defaults the two settings the wire payload cannot carry`() {
        val script = read(SCRIPT)

        // The contract payload is variant/hex/rgb. Nothing in it says shine or
        // mid, and the prefab will not draw without both.
        assertThat(script).contains("DEFAULT_FINISH = \"$defaultFinish\"")
        assertThat(script).contains("DEFAULT_COVERAGE = \"$defaultCoverage\"")
    }

    @Test
    fun `a payload with no colour clears the effect instead of throwing`() {
        val script = read(SCRIPT)

        // A variant may legitimately carry no hex — a lip-liner size, a
        // gift-set option — and `rgbOf` drops a malformed one, so `hex` absent
        // is a normal call and not an error. Bare lips is the answer; a throw
        // out of the interpreter is a dead effect.
        assertThat(script).contains("if (colour === null) {")
        assertThat(script).contains("prefab.clear()")
    }

    @Test
    fun `nothing in the script can throw before the global is installed`() {
        val script = read(SCRIPT)
        val installation = script.indexOf("globalThis.${TryOnJsContract.MAKEUP_METHOD} =")
        assertThat(installation).isGreaterThan(0)

        // makeup_base/scripts/region.js calls bnb.scene.getAssetManager() while
        // it is being LOADED, so requiring the makeup prefab at this file's top
        // level would make installing the global depend on the scene already
        // existing. It is required inside the lazy accessor instead, and the
        // only top-level require is bnb_js/prefabs, which touches no scene.
        val topLevel = script.substring(0, installation)
        assertThat(topLevel).contains("require(\"bnb_js/prefabs\")")
        assertThat(topLevel.lineSequence().filter { it.startsWith("const") && "require(" in it }.toList())
            .containsExactly("const { Base } = require(\"bnb_js/prefabs\")")
    }

    private companion object {
        const val ASSETS = "src/main/assets"
        const val SCRIPT = "scripts/index.js"
    }
}
