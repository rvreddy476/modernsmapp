package com.us.android

import android.content.Context
import coil3.ImageLoader
import coil3.disk.DiskCache
import coil3.disk.directory
import coil3.memory.MemoryCache
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import coil3.request.crossfade
import okhttp3.OkHttpClient

/**
 * The app-wide image loader.
 *
 * BUILT ON OUR OkHttp CLIENT, which is the load-bearing detail. Feed images
 * are delivered through the gateway and are authorized, so a loader with its
 * own HTTP stack would 401 on every one of them. Sharing the client also means
 * images inherit single-flight token refresh — two stacks would each hold a
 * stale token and race the rotation.
 *
 * The bearer is safe to share here for the same reason it is safe for video:
 * `AuthInterceptor` scopes the token to the API origin, so a pre-signed
 * object-store URL is fetched by this same client WITHOUT the credential. See
 * AuthOriginTest.
 *
 * Installed as the process singleton rather than injected, so `:core:ui` can
 * call `AsyncImage` without gaining a Hilt or network dependency — a component
 * that can fetch is one that cannot be previewed.
 */
fun buildImageLoader(context: Context, client: OkHttpClient): ImageLoader =
    ImageLoader.Builder(context)
        .components {
            // Lazy: OkHttpClient construction is not free, and this runs on
            // the cold-start path.
            add(OkHttpNetworkFetcherFactory(callFactory = { client }))
        }
        .memoryCache {
            MemoryCache.Builder()
                .maxSizePercent(context, MEMORY_CACHE_FRACTION)
                .build()
        }
        .diskCache {
            // A separate directory from the Media3 segment cache. Sharing one
            // would put two independent evictors on the same files.
            DiskCache.Builder()
                .directory(context.cacheDir.resolve("images"))
                .maxSizeBytes(DISK_CACHE_BYTES)
                .build()
        }
        // A fade covers the decode, which is what stops a scrolling feed
        // flashing solid blocks as bitmaps land.
        .crossfade(true)
        .build()

/**
 * A fraction of the app's available heap, not a fixed byte count: the budget
 * on a 2GB phone and a flagship are not the same number, and a fixed cache is
 * either wasteful on one or thrashing on the other.
 */
private const val MEMORY_CACHE_FRACTION = 0.20

private const val DISK_CACHE_BYTES = 256L * 1024 * 1024
