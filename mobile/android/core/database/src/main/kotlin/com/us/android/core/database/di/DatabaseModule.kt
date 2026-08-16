package com.us.android.core.database.di

import android.content.Context
import androidx.room.Room
import com.us.android.core.database.RemoteKeyDao
import com.us.android.core.database.UsDatabase
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
            .build()

    @Provides
    fun provideRemoteKeyDao(database: UsDatabase): RemoteKeyDao = database.remoteKeyDao()
}
