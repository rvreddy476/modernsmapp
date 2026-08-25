package com.us.android.di

import com.us.android.core.creator.model.RenderExporter
import com.us.android.core.media.creator.AndroidRenderExporter
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/**
 * Binds the :core:media render implementation to the :core:creator-model port.
 *
 * DELIBERATELY in :app. The DAG forbids the engine seeing :core:media (G-4)
 * and :core:media seeing the engine (G-5); the app module is the one place
 * allowed to know both sides, which is exactly what a composition root is for.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class CreatorModule {

    @Binds
    abstract fun bindRenderExporter(impl: AndroidRenderExporter): RenderExporter
}
