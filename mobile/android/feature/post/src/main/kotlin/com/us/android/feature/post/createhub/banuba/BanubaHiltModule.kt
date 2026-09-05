package com.us.android.feature.post.createhub.banuba

import com.us.android.core.ui.photoeditor.PhotoEditor
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/**
 * The real SDK behind the gate's seam (tests bind a fake in its place), and
 * the `:core:ui` photo editor PORT bound to Banuba's Photo Editor — the one
 * binding through which `:feature:profile` reaches the editor without a
 * feature edge.
 */
@Module
@InstallIn(SingletonComponent::class)
internal abstract class BanubaHiltModule {
    @Binds
    abstract fun bindBanubaSdk(impl: KoinBanubaSdk): BanubaSdk

    @Binds
    abstract fun bindPhotoEditor(impl: BanubaPhotoEditor): PhotoEditor
}
