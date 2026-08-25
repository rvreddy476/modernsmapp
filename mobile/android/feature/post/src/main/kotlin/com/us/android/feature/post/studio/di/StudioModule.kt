package com.us.android.feature.post.studio.di

import com.us.android.core.creator.model.PublishTransport
import com.us.android.feature.post.studio.StudioPublishTransport
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/**
 * Binds the Slice C-backed transport to the engine's publish port.
 *
 * The RenderExporter binding lives in :app (which sees :core:media); this one
 * lives here because the adapter and the DTO it freezes are both in this module.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class StudioModule {

    @Binds
    abstract fun bindPublishTransport(impl: StudioPublishTransport): PublishTransport
}
