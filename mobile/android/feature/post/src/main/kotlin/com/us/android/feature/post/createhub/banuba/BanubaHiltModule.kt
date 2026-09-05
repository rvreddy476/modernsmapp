package com.us.android.feature.post.createhub.banuba

import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/** The real SDK behind the gate's seam. Tests bind a fake in its place. */
@Module
@InstallIn(SingletonComponent::class)
internal abstract class BanubaHiltModule {
    @Binds
    abstract fun bindBanubaSdk(impl: KoinBanubaSdk): BanubaSdk
}
