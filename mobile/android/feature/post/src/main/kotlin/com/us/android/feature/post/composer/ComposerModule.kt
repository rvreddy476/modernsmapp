package com.us.android.feature.post.composer

import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object ComposerModule {

    /**
     * Production creation keys are random UUIDs.
     *
     * Injected rather than called inline so a test can make them deterministic
     * and assert that a retry reuses the SAME key — the property that stops a
     * duplicate post, and the one an inline `UUID.randomUUID()` makes untestable.
     */
    @Provides
    @Singleton
    fun provideCreationKeyFactory(): CreationKeyFactory = RandomCreationKeyFactory()
}
