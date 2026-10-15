package com.us.android.feature.search.di

import com.us.android.feature.search.data.SearchApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Search's endpoints from the app-wide [Retrofit] — no second stack. The
 * recent searches are search-service's now (T-15), not a file on the phone.
 */
@Module
@InstallIn(SingletonComponent::class)
object SearchModule {

    @Provides
    @Singleton
    fun provideSearchApi(retrofit: Retrofit): SearchApi = retrofit.create(SearchApi::class.java)
}
