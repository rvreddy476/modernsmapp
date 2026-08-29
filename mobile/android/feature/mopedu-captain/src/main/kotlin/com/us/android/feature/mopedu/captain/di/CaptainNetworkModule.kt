package com.us.android.feature.mopedu.captain.di

import com.us.android.feature.mopedu.captain.data.MopeduCaptainApi
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.RealMopeduCaptainRepository
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
abstract class CaptainRepositoryModule {
    @Binds
    @Singleton
    abstract fun bindCaptainRepository(impl: RealMopeduCaptainRepository): MopeduCaptainRepository
}

@Module
@InstallIn(SingletonComponent::class)
object CaptainApiModule {
    @Provides
    @Singleton
    fun provideCaptainApi(retrofit: Retrofit): MopeduCaptainApi =
        retrofit.create(MopeduCaptainApi::class.java)
}
