package com.us.android.feature.search.di

import com.us.android.feature.search.data.DataStoreRecentSearches
import com.us.android.feature.search.data.RecentSearchStore
import com.us.android.feature.search.data.SearchApi
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Search's endpoints from the app-wide [Retrofit] — no second stack — and the recent list's store. */
@Module
@InstallIn(SingletonComponent::class)
abstract class SearchModule {

    @Binds
    abstract fun bindRecentSearches(store: DataStoreRecentSearches): RecentSearchStore

    companion object {
        @Provides
        @Singleton
        fun provideSearchApi(retrofit: Retrofit): SearchApi = retrofit.create(SearchApi::class.java)
    }
}
