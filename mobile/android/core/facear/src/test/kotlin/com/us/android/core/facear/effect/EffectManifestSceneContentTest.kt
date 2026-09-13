package com.us.android.core.facear.effect

// The guard that stops a black screen.
//
// An effect the player LOADS takes over rendering. One that declares a scene
// NAME but no scene CONTENT renders an empty scene instead of the camera, and
// the handset shows black. That is strictly worse than failing to load, where
// the SDK falls through to the raw camera — and it is what the founder saw and
// reported as "camera is not working now".
//
// So the manifest is read where the bundle is resolved, and a bundle that
// cannot draw is known before anything reaches the render thread.

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class EffectManifestSceneContentTest {

    @Test
    fun `a manifest with scene content declares it`() {
        assertThat(
            effectDeclaresSceneContent(
                """{"scene":"X","assets":{"images":{}},"render_list":{"default":[]}}""",
            ),
        ).isTrue()
    }

    // Our own bundle's exact shape. `scene` and `script` are a NAME and an
    // entry point; neither puts anything in the scene. `depends` is a vendor
    // prefab key that an effect root does not honour — proven on the device,
    // where the material it should have brought in came back null.
    @Test
    fun `a scene name and a script alone do not count as content`() {
        assertThat(
            effectDeclaresSceneContent(
                """{"scene":"MomentumLipstick","depends":["makeup_lipsshine"],"script":{"entry_point":"scripts/index.js","type":"latest"}}""",
            ),
        ).isFalse()
    }

    @Test
    fun `an empty object declares nothing`() {
        assertThat(effectDeclaresSceneContent("{}")).isFalse()
    }

    // Each content key on its own is enough: a real effect need not carry all
    // of them, and demanding several would reject a legitimate minimal effect.
    @Test
    fun `any single content key is enough`() {
        for (key in SCENE_CONTENT_KEYS) {
            assertThat(effectDeclaresSceneContent("""{"scene":"X","$key":{}}"""))
                .isTrue()
        }
    }
}
