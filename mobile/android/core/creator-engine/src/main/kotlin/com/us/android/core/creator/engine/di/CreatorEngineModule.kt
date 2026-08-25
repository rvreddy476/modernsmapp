package com.us.android.core.creator.engine.di

import android.content.Context
import com.us.android.core.creator.engine.SourceVault
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.Dispatchers
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object CreatorEngineModule {

    /**
     * The production vault: real IO dispatcher, real quota, real free-space
     * probe. The constructor's other parameters exist only so tests can prove
     * refusal deterministically, and are deliberately NOT injectable — there is
     * exactly one production configuration, and this is it.
     */
    @Provides
    @Singleton
    fun provideSourceVault(@ApplicationContext context: Context): SourceVault =
        SourceVault(context, Dispatchers.IO)
}
