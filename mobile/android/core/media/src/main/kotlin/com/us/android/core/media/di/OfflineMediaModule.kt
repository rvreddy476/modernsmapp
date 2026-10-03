package com.us.android.core.media.di

import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.database.DatabaseProvider
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.cache.Cache
import androidx.media3.datasource.cache.CacheDataSource
import androidx.media3.datasource.cache.NoOpCacheEvictor
import androidx.media3.datasource.cache.SimpleCache
import com.us.android.core.media.offline.Media3OfflineMediaStore
import com.us.android.core.media.offline.OfflineMediaStore
import com.us.android.core.media.offline.OfflineStorage
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import java.io.File
import javax.inject.Qualifier
import javax.inject.Singleton

/** The offline copies' own cache and its index: never the streaming cache in `cacheDir`. */
@Qualifier
@Retention(AnnotationRetention.RUNTIME)
annotation class OfflineCache

/** The data source a stored copy is PLAYED through: the offline cache and local files, and no network. */
@Qualifier
@Retention(AnnotationRetention.RUNTIME)
annotation class OfflineRead

/**
 * Offline copies' storage (2026-10-02). Everything here opens under
 * [OfflineStorage.root], the app's no-backup directory; see that class for
 * why it is nowhere else.
 */
@Module
@InstallIn(SingletonComponent::class)
@OptIn(UnstableApi::class)
object OfflineMediaModule {

    /**
     * The index of what the cache holds, in a database file BESIDE the
     * bytes. Media3's stock provider keeps it in the app's `databases`
     * directory, which a device-to-device transfer copies: an index that
     * arrived on a new phone without its bytes would describe copies that
     * are not there.
     */
    @Provides
    @Singleton
    @OfflineCache
    fun provideOfflineDatabase(
        @ApplicationContext context: Context,
        storage: OfflineStorage,
    ): DatabaseProvider = OfflineDatabaseProvider(context, storage.databaseFile)

    /**
     * One cache, singleton for the reason the streaming one is: `SimpleCache`
     * locks its directory. No evictor: a copy leaves when it expires, is
     * revoked or is removed by the viewer, never to make room.
     */
    @Provides
    @Singleton
    @OfflineCache
    fun provideOfflineCache(
        storage: OfflineStorage,
        @OfflineCache database: DatabaseProvider,
    ): Cache = SimpleCache(storage.mediaDir, NoOpCacheEvictor(), database)

    /**
     * Reads a stored copy. The cache has NO upstream: a byte that is not on
     * the device is an error the player shows, never a request, so playing a
     * copy cannot touch the network. Caption files are plain files, which
     * `DefaultDataSource` opens itself.
     */
    @Provides
    @Singleton
    @OfflineRead
    fun provideOfflineReadFactory(
        @ApplicationContext context: Context,
        @OfflineCache cache: Cache,
    ): DataSource.Factory = DefaultDataSource.Factory(
        context,
        CacheDataSource.Factory()
            .setCache(cache)
            .setUpstreamDataSourceFactory(null)
            .setCacheWriteDataSinkFactory(null),
    )
}

@Module
@InstallIn(SingletonComponent::class)
abstract class OfflineMediaBindings {
    @Binds
    abstract fun bindOfflineMediaStore(impl: Media3OfflineMediaStore): OfflineMediaStore
}

/** Media3's `StandaloneDatabaseProvider`, at a path of our choosing. It owns no schema: the tables are Media3's. */
@OptIn(UnstableApi::class)
private class OfflineDatabaseProvider(context: Context, file: File) :
    SQLiteOpenHelper(context, file.absolutePath, null, DATABASE_VERSION),
    DatabaseProvider {

    override fun onCreate(db: SQLiteDatabase) = Unit

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit

    private companion object {
        const val DATABASE_VERSION = 1
    }
}
