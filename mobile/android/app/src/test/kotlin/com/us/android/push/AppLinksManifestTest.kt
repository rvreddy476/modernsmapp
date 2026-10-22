package com.us.android.push

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * The manifest side of the web share routes (2026-10-22): every route
 * `WebLinks.targetOf` parses must also be claimed by a verified
 * (`autoVerify`) VIEW filter on both web hosts, or Android hands the tap
 * to the browser and the parser never sees it. `/shop/` stays unclaimed
 * while commerce is paused.
 */
class AppLinksManifestTest {

    private val manifest: File by lazy {
        listOf(File("src/main/AndroidManifest.xml"), File("app/src/main/AndroidManifest.xml"))
            .first { it.isFile }
    }

    /** The `https` web filter: autoVerify, on the two web hosts. Its path prefixes. */
    private fun webPrefixes(): Set<String> {
        val doc = DocumentBuilderFactory.newInstance()
            .apply { isNamespaceAware = true }
            .newDocumentBuilder()
            .parse(manifest)
        val filters = doc.getElementsByTagName("intent-filter")
        val prefixes = mutableSetOf<String>()
        for (i in 0 until filters.length) {
            val filter = filters.item(i) as Element
            if (filter.getAttributeNS(ANDROID_NS, "autoVerify") != "true") continue
            val data = filter.getElementsByTagName("data")
            val hosts = (0 until data.length).map { (data.item(it) as Element).getAttributeNS(ANDROID_NS, "host") }
            if (!hosts.containsAll(WEB_HOSTS)) continue
            (0 until data.length)
                .map { (data.item(it) as Element).getAttributeNS(ANDROID_NS, "pathPrefix") }
                .filter { it.isNotEmpty() }
                .forEach(prefixes::add)
        }
        return prefixes
    }

    @Test
    fun `every share route the app opens is claimed on both web hosts with autoVerify`() {
        val prefixes = webPrefixes()
        for (route in ROUTES) {
            assertWithMessage("no verified App Link filter claims $route").that(prefixes).contains(route)
        }
    }

    @Test
    fun `the shop stays with the browser while commerce is paused`() {
        assertThat(webPrefixes().none { it.startsWith("/shop") }).isTrue()
    }

    private companion object {
        const val ANDROID_NS = "http://schemas.android.com/apk/res/android"
        val WEB_HOSTS = listOf("cleestudio.com", "app.cleestudio.com")

        /** `/reels/` also covers `/reels/sound/<id>`. */
        val ROUTES = listOf("/post/", "/reels/", "/posttube/watch/", "/posttube/channel/", "/channel/", "/u/", "/hashtag/")
    }
}
