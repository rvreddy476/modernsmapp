package com.us.android.feature.echo.di

import com.us.android.feature.echo.data.DefaultEchoMediaUploader
import com.us.android.feature.echo.data.DefaultEchoRepository
import com.us.android.feature.echo.data.EchoApi
import com.us.android.feature.echo.data.EchoMediaUploader
import com.us.android.feature.echo.data.EchoRepository
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import java.time.Clock
import javax.inject.Singleton

/** Echo's endpoints from the app-wide [Retrofit]: no second stack. */
@Module
@InstallIn(SingletonComponent::class)
abstract class EchoModule {

    @Binds
    abstract fun bindEchoRepository(repository: DefaultEchoRepository): EchoRepository

    @Binds
    abstract fun bindEchoMediaUploader(uploader: DefaultEchoMediaUploader): EchoMediaUploader

    companion object {
        @Provides
        @Singleton
        fun provideEchoApi(retrofit: Retrofit): EchoApi = retrofit.create(EchoApi::class.java)

        /** The edit window and the timeline's ages read the time through this, so tests set it. */
        @Provides
        @EchoClock
        fun provideEchoClock(): Clock = Clock.systemUTC()
    }
}

@javax.inject.Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class EchoClock
