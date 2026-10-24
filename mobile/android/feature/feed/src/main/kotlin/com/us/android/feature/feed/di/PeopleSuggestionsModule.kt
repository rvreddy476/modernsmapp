package com.us.android.feature.feed.di

import com.us.android.feature.feed.ui.DefaultPeopleSuggestionsSource
import com.us.android.feature.feed.ui.PeopleSuggestionsSource
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.components.ViewModelComponent

/** People you may know on Home (2026-10-24): the engine and the names behind it. */
@Module
@InstallIn(ViewModelComponent::class)
abstract class PeopleSuggestionsModule {
    @Binds
    abstract fun bindPeopleSuggestionsSource(source: DefaultPeopleSuggestionsSource): PeopleSuggestionsSource
}
