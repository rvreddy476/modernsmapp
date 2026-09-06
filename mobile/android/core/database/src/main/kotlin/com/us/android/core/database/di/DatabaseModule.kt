package com.us.android.core.database.di

import android.content.Context
import androidx.room.Room
import com.us.android.core.database.AnalyticsDao
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ComposerDraftDao
import com.us.android.core.database.RemoteKeyDao
import com.us.android.core.database.UsDatabase
import com.us.android.core.database.UsDatabaseCallbacks
import com.us.android.core.database.UsDatabaseMigrations
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object DatabaseModule {

    @Provides
    @Singleton
    fun provideDatabase(@ApplicationContext context: Context): UsDatabase =
        Room.databaseBuilder(context, UsDatabase::class.java, UsDatabase.NAME)
            // No fallbackToDestructiveMigration. Silently wiping user data on
            // a schema change is the kind of default that is convenient in
            // Phase 1 and catastrophic in production; every version bump
            // writes a real Migration.
            //
            // Slice C makes that concrete: this database now holds unpublished
            // composer drafts, so a destructive fallback would delete the exact
            // thing the table exists to protect.
            .also { builder -> UsDatabaseMigrations.forEach { builder.addMigrations(it) } }
            // Seed the fallback singleton on a FRESH database.
            //
            // A migrating install gets this row from MIGRATION_2_3. A fresh
            // install runs no migration at all, so without this callback the
            // singleton simply would not exist — and code that reads "is the
            // legacy composer still usable?" would get null and have to guess.
            // The two cohorts must answer that question the same way.
            .also { builder -> UsDatabaseCallbacks.all.forEach { builder.addCallback(it) } }
            .build()

    @Provides
    fun provideRemoteKeyDao(database: UsDatabase): RemoteKeyDao = database.remoteKeyDao()

    @Provides
    fun provideComposerDraftDao(database: UsDatabase): ComposerDraftDao =
        database.composerDraftDao()

    @Provides
    fun provideChatDao(database: UsDatabase): ChatDao = database.chatDao()

    @Provides
    fun provideAnalyticsDao(database: UsDatabase): AnalyticsDao = database.analyticsDao()
}
