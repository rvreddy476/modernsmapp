package com.us.android.core.facear.di

import android.content.Context
import com.banuba.sdk.arcloud.data.ArEffectsResourceManager
import com.us.android.core.facear.BanubaFaceArSdk
import com.us.android.core.facear.FaceArSdk
import com.us.android.core.facear.effect.ArCloudTryOnEffects
import com.us.android.core.facear.effect.BanubaArCloudStorage
import com.us.android.core.facear.effect.BundledTryOnEffects
import com.us.android.core.facear.effect.CompositeTryOnEffects
import com.us.android.core.facear.effect.EffectAssetIndex
import com.us.android.core.facear.effect.EffectManifestReader
import com.us.android.core.facear.effect.TryOnEffectSource
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.Dispatchers
import java.io.File
import javax.inject.Singleton

/**
 * The Face AR graph: the real SDK behind the gate's seam, and the effect
 * source a try-on screen resolves through.
 *
 * Hilt only. Koin appears nowhere in this module — the reel studio needs it
 * because the Banuba VIDEO EDITOR's own graph is Koin, and Face AR has no
 * graph of its own to start. Not starting a second Koin context in a process
 * that may already have the vendor's is deliberate, not incidental; see
 * `FaceArGate`'s one-process note.
 */
@Module
@InstallIn(SingletonComponent::class)
internal abstract class FaceArBindings {
    @Binds
    abstract fun bindFaceArSdk(impl: BanubaFaceArSdk): FaceArSdk
}

@Module
@InstallIn(SingletonComponent::class)
internal object FaceArProviders {

    /**
     * Listing one asset directory. `AssetManager.list` answers null for a
     * directory that does not exist, which is every effect slug today.
     */
    @Provides
    @Singleton
    fun provideEffectAssetIndex(@ApplicationContext context: Context): EffectAssetIndex =
        EffectAssetIndex { path -> context.assets.list(path)?.toList().orEmpty() }

    /**
     * The vendor's unpacker, built on first use.
     *
     * A lambda rather than the object: its constructor touches the asset
     * manager and creates directories, and a process that never opens a
     * try-on should not pay for that.
     */
    @Provides
    @Singleton
    fun provideArCloudStorage(@ApplicationContext context: Context): BanubaArCloudStorage =
        BanubaArCloudStorage {
            ArEffectsResourceManager(
                context.assets,
                File(context.filesDir, AR_CLOUD_DIR).apply { mkdirs() },
            )
        }

    /**
     * One bundled effect's `config.json`, as text.
     *
     * Separate from [provideEffectAssetIndex] because it is a different
     * question — "what is in this directory" versus "what does this file say"
     * — and because the answer decides whether an effect may be loaded at all:
     * a manifest declaring no scene content blacks the preview out. See
     * `effectDeclaresSceneContent`.
     */
    @Provides
    @Singleton
    fun provideEffectManifests(@ApplicationContext context: Context): EffectManifestReader =
        EffectManifestReader { path ->
            runCatching {
                context.assets.open(path).use { it.readBytes().decodeToString() }
            }.getOrNull()
        }

    /**
     * Bundled first, cloud second — the order is explained on
     * [CompositeTryOnEffects].
     */
    @Provides
    @Singleton
    fun provideTryOnEffects(
        assets: EffectAssetIndex,
        manifests: EffectManifestReader,
        cloud: BanubaArCloudStorage,
    ): TryOnEffectSource = CompositeTryOnEffects(
        listOf(
            BundledTryOnEffects(assets, manifests),
            ArCloudTryOnEffects(cloud, Dispatchers.IO),
        ),
    )

    /** Where AR Cloud effects are unpacked. Under filesDir, not cache: a download must survive a cache purge. */
    private const val AR_CLOUD_DIR = "bnb-ar-cloud"
}
