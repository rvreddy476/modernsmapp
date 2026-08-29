package com.us.android.feature.mopedu.rider.di

import com.us.android.feature.mopedu.rider.data.MopeduRiderApi
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import com.us.android.feature.mopedu.rider.data.RealMopeduRiderRepository
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
abstract class RiderRepositoryModule {
    @Binds
    @Singleton
    abstract fun bindRiderRepository(impl: RealMopeduRiderRepository): MopeduRiderRepository
}

@Module
@InstallIn(SingletonComponent::class)
object RiderApiModule {
    @Provides
    @Singleton
    fun provideRiderApi(retrofit: Retrofit): MopeduRiderApi =
        retrofit.create(MopeduRiderApi::class.java)
}
